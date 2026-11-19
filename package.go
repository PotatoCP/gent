// Package gent provides typed contracts for agent loops, models, tools, output, and saved runs.
// Applications own provider clients, prompts, storage, and deployment. The agents/react package
// supplies a standard Loop; executor.Runner uses the same lifecycle for custom loops.
//
// # One conversation, multiple turns
//
// Configure shared services once, create an ExecutionContext, and call NewTurn with each task.
// The RunId stays fixed. Each accepted task has its own TurnId. NewTurn preserves earlier input,
// assistant messages, native continuation, summaries, and custom run data. It resets declared
// turn fields and gauges and records counter baselines. Continue keeps the unfinished task.
//
//	runner := executor.NewRunner(react.NewAgent(model), &gent.Config{
//	    ToolChain: tools,
//	    Recorder: recorder,
//	    Limits: gent.DefaultLimits(),
//	})
//	execCtx, err := runner.NewExecutionContext(ctx, "support")
//	if err != nil { return err }
//	defer execCtx.Release()
//	answer, result, err := runner.NewTurn(execCtx, &gent.Task{
//	    Instructions: []*gent.ModelMessage{
//	        gent.TextParts(gent.ChatMessageTypeSystem, "Use verified order facts."),
//	    },
//	    Messages: gent.NewTask(gent.TextPart("Where is order ORD-123?")).Messages,
//	})
//	if err != nil { return err }
//	if result.Status == gent.ExecutionFinished &&
//	    result.TerminationReason == gent.TerminationSuccess {
//	    fmt.Println(answer)
//	}
//
// NewTurn and Continue return (T, *ExecutionResult, error). Busy and NoActiveTurn are explicit
// statuses. A limit or Stop returns Paused with the unfinished turn. Success and refusal finish
// the turn. Unexpected operation failures return zero T, nil result, and an error. Fix the cause,
// restore accepted state, and Continue instead of resubmitting an accepted task.
//
// Stop cancels the active attempt and saves a pause when possible; it does not wait. Release joins
// owned resources and permanently disposes of the handle. The application routes Stop by current
// RunId, TurnId, and AttemptId so an old browser command cannot stop a later attempt.
//
// # Prompts and output
//
// Task.Instructions supplies the effective system prefix. Nil inherits it, a non-nil slice replaces
// it, and an empty slice clears it. Task.Messages contains conversation messages with at least one
// user message and no system message. NewTask builds one user message from ordered ContentPart
// values.
// Task admission freezes its evidence. Generated corrections do not become user-authored input.
//
// The caller decides whether to include Output.Instructions. Configure applies native controls;
// local validation still runs. Output.Process receives selected answer-candidate content. Expected
// rejection returns steering; an error stops the attempt. ReAct retains the native candidate and
// correction until the next main request. Corrections use the ordinary configured limits.
//
// An accepted typed value and complete processed content are saved before the caller receives T
// or a Subscriber receives accepted_answer. Its source IDs link transformed output to model
// previews.
// Restore and GetLastTurnResult do not process or publish a completed answer again.
//
// # Persistence and recovery
//
// Recorder.Save stores complete SaveBatch values. Gent supplies a lazy capture callback for an
// optional stable RunStateSnapshot. CodecRegistry.MarshalRunState and UnmarshalRunState preserve
// the typed state and integrity checks. A captured RunStateSnapshot can be encoded after Save ends;
// the capture callback itself is valid only during that Save call.
//
// Full batch replay and a RunStateSnapshot plus its accepted tail restore the same logical state
// at the same EventSequenceNumber. This includes LoopData, history selection, counters, accepted
// results, operation identities, and managed recovery evidence. It does not promise identical
// future model output or undo external effects. A custom Next restarts at its entry point.
//
// RunState.Apply is compact execution replay. RunView.Apply reconstructs the full accepted archive
// for inspection without models or tools. Compaction never deletes archived originals. Storage
// owns transactions, exact-retry receipts, cursor checks, and writer fencing. A lost acknowledgment
// must be reconciled before another writer continues. See Recorder and the history guide at
// docs/content/storage/history.md.
//
// A Tool.Recover handler reports Completed, NotStarted, or Unresolved for an unfinished action.
// Gent returns evidence through the original native call identity; it does not automatically
// repeat the original action. Accepted reports and prepared responses are reused after restoration.
// Managed JavaScript recovery returns inner-call evidence without restarting the script or its VM.
//
// # Observation and mutation
//
// Config.Subscribers registers typed, read-only event callbacks. Accepted delivery is the default;
// Live delivery includes unsaved previews. AttemptId distinguishes provisional event positions that
// can be reused after restoration. A failed or slow Subscriber stops independently of execution.
// OnStop reports the delivery outcome. Browser transport and acknowledgments belong to the app.
//
// Config.Hooks supplies synchronous BeforeModelCall and BeforeToolCall callbacks. These can change
// effective inputs before Gent freezes them. They cannot change saved input or operation identity.
// An error blocks the dependent action. Hooks can repeat after interruption; use managed tools for
// external actions that need recovery. trace.Sequencer implements Subscriber for display
// projection.
//
// StreamChunk.Content and ContentChoice.Parts hold ordered assistant groups and tool-call parts.
// AssistantContent classifies exposed reasoning, commentary, and answer candidates. Only Gent can
// publish final_answer after acceptance. Opaque provider continuation stays separate from readable
// content. A classification is not a portable provider wire field.
//
// # Configuration and limits
//
// An empty Config has no execution limits. DefaultLimits returns an explicit optional preset.
// Supplying Limits replaces the rule set. LimitTurn uses the accepted turn baseline; LimitRun uses
// cumulative counters. Continue and Restore preserve both. Recording and Subscriber bounds remain
// active even when there are no execution-limit rules.
//
// Loop-specific prompts, models, and retry checks stay on the Loop. Shared tools, storage, Hooks,
// Subscribers, limits, and compaction policy belong in Config. Use a fresh Runner and Restore to
// change configuration. Compatible evolved code can continue saved runs without an old Runner
// version. BasicLoopData is empty; custom data must capture and restore every added field.
//
// # Ownership
//
// Transferred values and borrowed getters are read-only. Copy only for independent mutable data or
// a stable captured value. Successful Apply takes ownership of retained batch values; Ready Restore
// transfers RunState to ExecutionContext. Rejected replay and failed Restore transfer no ownership.
// See docs/content/operate/ownership.md, docs/content/agent/tools.md, and
// docs/content/agent/outputs.md for component-specific contracts.
package gent
