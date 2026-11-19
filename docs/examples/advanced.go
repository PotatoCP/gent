package examples

import (
	"context"
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/compaction"
	"github.com/rickchristie/gent/policy"
	"github.com/rickchristie/gent/search"
	"github.com/rickchristie/gent/toolchain"
	"github.com/rickchristie/gent/trace"
)

func toolGuidance(service *orderService) gent.Tool[*lookupInput] {
	// docs:start tools-policy
	tool := gent.DefineTool("lookup_order", service.Lookup).
		Policy("Use only an order ID supplied in this conversation.").MustBuild()
	// The handler must still enforce account access before reading an order.
	// docs:end tools-policy
	return tool
}

func discoverableTool(service *orderService) gent.Tool[*lookupInput] {
	// docs:start tools-metadata
	tool := gent.DefineTool("lookup_order", service.Lookup).
		Description("Read an order by its ID.").
		Domain("orders").Categories("status").Keywords("shipment", "delivery").
		SyntheticQueries("Where is my order?").MustBuild()
	// docs:end tools-metadata
	return tool
}

func pinTool(chain *toolchain.SearchChain) {
	// docs:start tools-pin
	chain.Pin("lookup_order")
	// Pin before Initialize. The selected definition stays in NativeTools.
	// docs:end tools-pin
}

func semanticTools(embedder search.Embedder) gent.ToolSearcher {
	// docs:start tools-custom-search
	index := search.NewFlatIndex(&toolchain.ToolChunkAdapter{}, embedder)
	engine := toolchain.NewIndexToolSearcher("semantic", index).
		WithSearchGuidance("Describe the operation you need.")
	// Any SearchIndex[gent.IndexableTool] can supply this custom engine.
	// docs:end tools-custom-search
	return engine
}

func hybridTools(embedder search.Embedder) gent.ToolSearcher {
	// docs:start tools-fused-search
	engine := toolchain.NewFusedToolSearcher(embedder)
	// Add the engine to SearchChain before Initialize.
	// docs:end tools-fused-search
	return engine
}

func boundedConfig() *gent.Config {
	// docs:start config-guidance
	config := &gent.Config{SteeringMessages: &gent.SteeringMessages{
		ToolBlocked: "A required application condition is absent. Ask how to proceed.",
	}}
	// Empty guidance fields keep their defaults.
	// docs:end config-guidance
	// docs:start recording-bounds
	config.Recording = &gent.RecordingConfig{
		MaxPendingEvents: 8_192,
		MaxPendingBytes:  16 << 20,
		SaveTimeout:      20 * time.Second,
	}
	// Bounds stop unbounded pending evidence. They do not create new savepoints.
	// docs:end recording-bounds
	// docs:start codec-bounds
	config.Codecs = gent.NewCodecRegistry(gent.WithCodecLimits(gent.CodecLimits{
		MaxEncodedBytes: 32 << 20,
		MaxBinaryBytes:  8 << 20,
	}))
	// Allow for encoded overhead. Custom codecs own their decoded allocation limits.
	// docs:end codec-bounds
	// docs:start config-prefix-limit
	config.Limits = []gent.Limit{{
		Type: gent.LimitKeyPrefix, Key: "app:checked_files:", MaxValue: 40,
	}}
	// Each matching counter has its own allowance; this is not a sum.
	// docs:end config-prefix-limit
	return config
}

// docs:start compaction-custom
type RecentContext struct {
	Keep int
}

func (strategy *RecentContext) Compact(execCtx *gent.ExecutionContext,
	input *gent.CompactionInput,
) (*gent.HistoryUpdate, error) {
	// Reuse the standard selector's pin handling. Keep must be positive.
	return compaction.NewSlidingWindow(strategy.Keep).Compact(execCtx, input)
}

// docs:end compaction-custom

func managedModel(execCtx *gent.ExecutionContext, model gent.Model,
	messages []*gent.ModelMessage,
) (*gent.ModelCallResponse, error) {
	// docs:start loop-model
	request := gent.NewModelCallRequest(messages)
	stream, err := execCtx.GenerateContentStream(model, gent.ModelCall{Source: "review"}, request)
	if err != nil {
		return nil, err
	}
	// Response waits for stream cleanup. It includes all candidate parts.
	return stream.Response()
	// docs:end loop-model
}

func applicationEvent(execCtx *gent.ExecutionContext) error {
	// docs:start loop-event
	return execCtx.Publish(&gent.CommonEvent{
		BaseEvent:   gent.BaseEvent{EventName: "app:file_checked"},
		Description: "File checks completed.",
		Data:        "README.md",
	})
	// The next approved save accepts this event. Register codecs for custom data types.
	// docs:end loop-event
}

func replaceDocuments(ctx context.Context, index search.SearchIndex[*policy.Policy]) error {
	// docs:start search-update
	return index.Swap(ctx, map[string]*policy.Policy{
		"refund": {Id: "refund", FullContent: "Refunds are available within 14 days."},
	})
	// Swap replaces the complete catalog. Add replaces one ID; Remove deletes one ID.
	// docs:end search-update
}

func privateTrace(runId string) (*trace.Sequencer, error) {
	// docs:start trace-redact
	return trace.NewSequencer(runId, trace.Config{Redactor: trace.RedactorFuncs{
		ToolArgs:   func(any) any { return "[private tool arguments]" },
		ToolOutput: func(any) any { return "[private tool result]" },
	}})
	// This covers these two display fields only. Set other callbacks for your data policy.
	// Redaction does not remove the original accepted evidence from SaveBatch storage.
	// docs:end trace-redact
}

func notStartedResult() *gent.ToolRecoveryResponse {
	// docs:start recovery-not-started
	return &gent.ToolRecoveryResponse{
		Status:       gent.ToolRecoveryNotStarted,
		Instructions: "The service confirms that the original request did not start.",
	}
	// Use this only when inspection proves absence of the original effect.
	// docs:end recovery-not-started
}

func deprecatedTool(chain *toolchain.Registry, tool any) {
	// docs:start recovery-binding
	chain.RegisterDeprecatedTool(tool)
	// The old name remains available for recovery, but is not advertised for new calls.
	// docs:end recovery-binding
}
