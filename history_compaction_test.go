package gent_test

import (
	"context"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/toolchain"
	"github.com/stretchr/testify/assert"
)

type historySequenceModel struct {
	choices  []*gent.ContentChoice
	texts    []string
	requests []*gent.ModelCallRequest
}

func (m *historySequenceModel) GenerateContentStream(
	_ context.Context,
	request *gent.ModelCallRequest,
) (
	gent.Stream, error,
) {
	choice := &gent.ContentChoice{Completion: gent.ModelCompletion{Status: gent.ModelStatusAnswer}}
	if len(m.choices) > 0 {
		choice = m.choices[len(m.requests)]
	} else {
		choice = recoveryAnswerChoice(m.texts[len(m.requests)])
	}
	m.requests = append(m.requests, request)
	return gent.NewCompletedStream(&gent.ModelCallResponse{
		Choices: []*gent.ContentChoice{choice},
		Info:    &gent.GenerationInfo{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
	}, nil), nil
}

func TestHistoryCompaction_PreservesBothSummariesAndResumes(t *testing.T) {
	worker, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &recoveryStore{
		codecs: gent.NewCodecRegistry(),
		cancel: cancel,
		phase:  gent.PhaseCompactionCompleted,
		after:  2,
	}
	model := &historySequenceModel{choices: []*gent.ContentChoice{recoveryChoice(), recoveryChoice()}}
	summaries := &historySequenceModel{texts: []string{
		"Policy P10 permits refunds within 30 days.", "Two policy lookups were completed.",
	}}
	newAgent := func(model gent.Model, summary gent.Model) (*react.Agent[string], *gent.Config) {
		chain := toolchain.New().RegisterTool(
			gent.DefineTool(
				"policy",
				func(
					context.Context,
					recoveryInput,
				) (
					*gent.ToolExecutionResponse,
					error,
				) {
					return capturedToolResponse("Policy P10 permits refunds within 30 days.", nil)
				},
			).MustBuild(),
		)
		return react.NewAgent(model), &gent.Config{ToolChain: chain,
			CompactionTrigger:  compaction.NewStatThresholdTrigger().OnCounter(gent.SCIterations, 1),
			CompactionStrategy: compaction.NewSummarization(summary, format.NewXML()).WithKeepRecent(0)}
	}
	agent, config := newAgent(model, summaries)
	config.Codecs, config.Recorder = store.codecs, store
	runner, execCtx := newTestExecution(t, worker, agent, config)
	_, _, err := runner.NewTurn(execCtx, gent.NewTask(gent.TextPart("Check the policy twice.")))
	assert.ErrorIs(t, err, context.Canceled)
	bundle := store.view
	assert.Equal(t, gent.PhaseCompactionCompleted, bundle.Checkpoint.Phase)
	var summaryEntries []*gent.HistoryEntry
	var markers []*gent.CompactionRecord
	var originals []*gent.HistoryEntry
	var users []*gent.HistoryEntry
	var retained []*gent.HistoryEntry
	for _, entry := range bundle.History.Entries {
		switch entry.Kind {
		case gent.HistorySummary:
			summaryEntries = append(summaryEntries, entry)
		case gent.HistoryCompaction:
			markers = append(markers, entry.Compaction)
		case gent.HistoryMessages:
			if entry.Origin == gent.RecordUserInput {
				users = append(users, entry)
			} else if entry.Origin == gent.RecordCompactedModified {
				retained = append(retained, entry)
			} else {
				originals = append(originals, entry)
			}
		}
	}
	if !assert.Len(t, summaryEntries, 2) || !assert.Len(t, markers, 2) ||
		!assert.Len(t, originals, 2) {
		return
	}
	assert.Len(t, users, 1)
	assert.Len(t, retained, 2)
	assert.Equal(t, []gent.EntryId{users[0].Id, originals[0].Id}, markers[0].InputIds)
	assert.Equal(
		t,
		[]gent.EntryId{
			retained[0].Id,
			summaryEntries[0].Id,
			originals[1].Id,
		},
		markers[1].InputIds,
	)
	assert.Equal(t, []gent.EntryId{retained[1].Id, summaryEntries[1].Id}, markers[1].Active)
	assert.Len(t, markers[0].ModelCallIds, 1)
	assert.Len(t, markers[1].ModelCallIds, 1)
	// The first summary remains fully decodable even though the second summary lost the fact.
	first, second := summaryEntries[0], summaryEntries[1]
	assert.Equal(t, `<prior_work_summary>
Policy P10 permits refunds within 30 days.
</prior_work_summary>`, first.Messages[0].Parts[0].(interface{ String() string }).String())
	assert.Equal(t, `<prior_work_summary>
Two policy lookups were completed.
</prior_work_summary>`, second.Messages[0].Parts[0].(interface{ String() string }).String())
	assert.NotEqual(t, summaries.requests[0].Messages, summaries.requests[1].Messages)
	freshModel := &historySequenceModel{texts: []string{"Done."}}
	freshSummary := &historySequenceModel{}
	freshAgent, freshConfig := newAgent(freshModel, freshSummary)
	fresh, _, err := resumeRecovery(t, freshAgent, store.runStateSnapshot, freshConfig)
	if !assert.NoError(t, err) {
		return
	}
	assert.Empty(t, freshSummary.requests)
	assert.Len(t, freshModel.requests, 1)
	assert.Equal(t, int64(2), fresh.Stats().GetCounter(gent.SCCompactions))
	assert.Equal(t, 3, fresh.Iteration())
}
func (m *historySequenceModel) PrepareRequest(execCtx *gent.ExecutionContext,
	request *gent.ModelCallRequest, chain gent.ToolChain,
) (*gent.ModelCallRequest, error) {
	return gent.PrepareModelRequest(execCtx, request, chain)
}
