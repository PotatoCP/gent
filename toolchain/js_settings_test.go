package toolchain

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type integerToolInput struct {
	ID uint64 `json:"id"`
}

func TestProgramExecutionSettingsAndTypedNumbers(t *testing.T) {
	type input struct {
		mode gent.PTCIntegerMode
		code string
	}
	type expected struct {
		status gent.ToolStatus
		id     uint64
	}
	type testCase struct {
		name     string
		input    input
		expected expected
	}
	chain := New().RegisterTool(gent.DefineTool("fetch", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		parts, err := gent.NewParts().Data(integerToolInput{9007199254740993}).
			PDF([]byte("%PDF-fixture")).Build()
		return &gent.ToolExecutionResponse{Status: gent.ToolExecuted, Parts: parts,
			Instructions: "Required guidance."}, err
	}).MustBuild())
	chain.RegisterTool(gent.DefineTool("consume", func(_ context.Context, value integerToolInput) (
		*gent.ToolExecutionResponse, error,
	) {
		return capturedToolResponse(value, nil)
	}).MustBuild())
	const code = `const reply = tool.call({tool:"fetch",args:{}});
tool.forward(reply.parts.filter(p=>p.kind==="bytes"));
tool.call({tool:"consume",args:{id:reply.parts[0].data.id}});`
	cases := []testCase{
		{"number", input{gent.PTCIntegerNumber, code},
			expected{gent.ToolExecuted, 9007199254740992}},
		{"bigint", input{gent.PTCIntegerBigInt, code},
			expected{gent.ToolExecuted, 9007199254740993}},
		{"outside uint64", input{gent.PTCIntegerBigInt,
			`tool.call({tool:"consume",args:{id:18446744073709551616n}});`},
			expected{gent.ToolInvalidArguments, 0}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			steering := gent.SteeringMessages{PTCBytesOmitted: "Custom byte guidance.",
				ToolError: "Custom operation guidance."}
			wrapper := NewJsToolChainWrapper(chain).WithIntegerMode(test.input.mode)
			var records []*gent.ToolCallRecord
			loop := &toolTestLoop{next: func(execCtx *gent.ExecutionContext) (*gent.AgentLoopResult, error) {
				var err error
				records, err = executeTestCode(wrapper, execCtx, test.input.code, testFormat())
				if err != nil {
					return nil, err
				}
				return toolTestDone(execCtx)
			}}
			runner, ctx, store := newToolTestRun(t, context.Background(), loop, &gent.Config{
				ToolChain: wrapper, SteeringMessages: &steering})
			_, _, err := runner.NewTurn(ctx, gent.NewTask(gent.TextPart("Process typed numbers.")))
			if !assert.NoError(t, err) {
				return
			}
			var consumed *gent.ToolCallRecord
			for _, event := range toolEvents(t, ctx) {
				if after, ok := event.Payload.(*gent.AfterToolCallEvent); ok && after.ToolName == "consume" {
					consumed = after.Output.(*gent.ToolCallRecord)
				}
			}
			if !assert.NotNil(t, consumed, "outer reply: %s", recordText(records[0])) {
				return
			}
			actual := expected{status: consumed.Execution().Status}
			if actual.status == gent.ToolExecuted {
				value, err := ctx.Codecs().Decode(*consumed.Input())
				assert.NoError(t, err)
				actual.id = value.(integerToolInput).ID
			}
			assert.Equal(t, test.expected, actual)
			if actual.status == gent.ToolExecuted {
				assert.Equal(t, gent.PDFPart([]byte("%PDF-fixture")), records[0].Response().Parts()[2])
			}
			// Completed records need neither the old wrapper nor its typed input registrations.
			// Current settings belong to the new Runner; they are not restored configuration.
			_, fresh := restoreToolTest(t, &toolTestLoop{}, &gent.Config{
				Codecs: gent.NewCodecRegistry(), SteeringMessages: &steering}, store.runStateSnapshot)
			require.Equal(t, steering.Resolved(), fresh.SteeringMessages())
			plan, found, err := fresh.SavedToolPlan()
			assert.NoError(t, err)
			assert.True(t, found)
			assert.Equal(t, records, plan.Results)
		})
	}
}
