package examples

import (
	"context"
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

//go:embed media.js
var mediaScript string

func TestMediaScript(t *testing.T) {
	bytes := []byte("image fixture")
	tool := gent.DefineTool("receipt", func(context.Context, struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.PNGPart(bytes)}}, nil
	}).MustBuild()
	chain := programmatic(toolchain.New().RegisterTool(tool))
	runCheck(t, &gent.Config{ToolChain: chain}, func(execCtx *gent.ExecutionContext) error {
		args, err := json.Marshal(map[string]string{"code": mediaScript})
		if err != nil {
			return err
		}
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{
			toolCall("run_code", string(args))})
		if err != nil {
			return err
		}
		record, err := chain.ExecuteNext(execCtx, plan, format.NewXML())
		if err != nil {
			return err
		}
		assert.Equal(t, gent.ToolExecuted, record.Execution().Status)
		assert.Equal(t, []gent.ContentPart{
			gent.TextPart("Tool receipt (call " + execCtx.ContextId() + ":tool:2):"),
			gent.PNGPart(bytes), gent.TextPart(`<output>
Receipt ready.
</output>`),
		}, record.Response().Parts())
		return nil
	})
}

type fixtureLedger struct{ calls []string }

func (ledger *fixtureLedger) Inspect(ctx context.Context, id string) (
	*gent.ToolRecoveryResponse, error,
) {
	ledger.calls = append(ledger.calls, id)
	return recoveredResult(), ctx.Err()
}

func TestRecoveryExamples(t *testing.T) {
	ledger := &fixtureLedger{}
	tool := recoveringTool(&orderService{}, ledger)
	identity := gent.ToolIdentity{ToolCallId: "original"}
	request := gent.NewToolRecoveryRequest(identity, "lookup_order",
		map[string]any{"id": "ABC"}, &lookupInput{ID: "ABC"})
	for i := 0; i < 2; i++ {
		response, err := tool.Recover(context.Background(), request)
		assert.NoError(t, err)
		assert.Equal(t, recoveredResult(), response)
		assert.NoError(t, gent.ValidateToolRecovery(response))
	}
	assert.Equal(t, []string{"original", "original"}, ledger.calls)
	assert.NoError(t, gent.ValidateToolRecovery(unresolvedResult()))
	assert.NoError(t, gent.ValidateToolRecovery(notStartedResult()))
	var callId string
	identified := gent.DefineTool("identify", func(ctx context.Context, input struct{}) (
		*gent.ToolExecutionResponse, error,
	) {
		id, err := toolIdentity(ctx)
		if err != nil {
			return nil, err
		}
		callId = id
		return &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.TextPart(id)}}, nil
	}).MustBuild()
	chain := toolchain.New()
	deprecatedTool(chain, tool)
	chain.RegisterTool(identified)
	runCheck(t, &gent.Config{ToolChain: chain}, func(execCtx *gent.ExecutionContext) error {
		definitions, err := chain.NativeTools(execCtx)
		if err != nil {
			return err
		}
		assert.Len(t, definitions, 1)
		assert.Equal(t, "identify", definitions[0].Function.Name)
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{
			toolCall("identify", `{}`)})
		if err != nil {
			return err
		}
		record, err := chain.ExecuteNext(execCtx, plan, format.NewXML())
		if err != nil {
			return err
		}
		assert.Equal(t, &gent.ToolExecutionResponse{Status: gent.ToolExecuted,
			Parts: []gent.ContentPart{gent.TextPart(callId)}}, record.Execution())
		assert.NotEmpty(t, callId)
		return nil
	})
}

func TestAdvancedExamples(t *testing.T) {
	service := &orderService{}
	tool := discoverableTool(service)
	assert.Equal(t, "Use only an order ID supplied in this conversation.",
		toolGuidance(service).Policy())
	for _, engine := range []gent.ToolSearcher{
		semanticTools(fixtureEmbedder{}), hybridTools(fixtureEmbedder{}),
	} {
		chain, err := searchWithEngine(tool, engine)
		if !assert.NoError(t, err) {
			return
		}
		runCheck(t, &gent.Config{ToolChain: chain}, func(execCtx *gent.ExecutionContext) error {
			result, err := chain.Search(execCtx, &gent.ToolSearchRequest{
				QueryType: engine.Id(), Query: "order",
			})
			assert.NoError(t, err)
			assert.Equal(t, &gent.ToolSearchResult{Status: gent.ToolExecuted,
				Names: []string{"lookup_order"}, Instructions: "Page 1; 1 matching tools."}, result)
			return nil
		})
	}
	chain := toolchain.NewSearch(toolchain.SearchHintDomainCategories).
		RegisterEngine(toolchain.NewRegexToolSearcher())
	chain.RegisterTool(tool)
	pinTool(chain)
	assert.NoError(t, chain.Initialize())
	seq, err := privateTrace("private")
	assert.NoError(t, err)
	seq.Close()
	log := &EventLog{}
	config := boundedConfig()
	config.Subscribers = []*gent.SubscriberRegistration{{Subscriber: log}}
	runCheck(t, config, func(execCtx *gent.ExecutionContext) error {
		assert.Equal(t, "A required application condition is absent. Ask how to proceed.",
			execCtx.SteeringMessages().ToolBlocked)
		assert.Equal(t, gent.DefaultSteeringMessages().ToolError,
			execCtx.SteeringMessages().ToolError)
		definitions, err := chain.NativeTools(execCtx)
		if err != nil {
			return err
		}
		var names []string
		for _, definition := range definitions {
			names = append(names, definition.Function.Name)
		}
		assert.Equal(t, []string{"tool_registry_search", "execute_tool", "lookup_order"}, names)
		response, err := managedModel(execCtx, &FixedModel{Text: "Ready."},
			[]*gent.ModelMessage{gent.TextParts(gent.ChatMessageTypeHuman, "Start.")})
		if err != nil {
			return err
		}
		assert.Equal(t, []gent.ContentPart{gent.TextPart("Ready.")},
			response.Choices[0].CandidateContent())
		return applicationEvent(execCtx)
	})
	var appEvents []*gent.CommonEvent
	for _, event := range log.Events {
		if event.EventName == "app:file_checked" {
			appEvents = append(appEvents, event.Payload.(*gent.CommonEvent))
		}
	}
	assert.Len(t, appEvents, 1)
	if len(appEvents) == 1 {
		assert.Equal(t, "README.md", appEvents[0].Data)
	}
	input := &gent.CompactionInput{Revision: 5, Entries: []*gent.HistoryEntry{
		{Id: "one"}, {Id: "two"}, {Id: "three"},
	}}
	proposal, err := (&RecentContext{Keep: 2}).Compact(nil, input)
	assert.NoError(t, err)
	assert.Equal(t, &gent.HistoryUpdate{ExpectedDraftRevision: 5,
		Active: []gent.EntryId{"two", "three"}}, proposal)
}
