package gent

import (
	"context"
	"errors"
	"fmt"

	"sync"
	"time"
)

// ExecutionContext owns the mutable state of one conversation. Runner creates or restores it.
// NewTurn and Continue run separate attempts with fresh cancellation and delivery resources.
// Stats, managed model/tool calls, and history changes use the same accepted-save contract.
// Full archived history belongs in RunView; this context retains state needed for execution.
//
// Methods synchronize framework state. Data returns application LoopData, whose author must
// synchronize custom fields when managed operations can access them concurrently. Borrowed
// configuration, saved messages, results, and subscriber values are read-only.
type ExecutionContext struct {
	// ownerCtx survives accepted turns. attempt contains only the current worker resources.
	ownerCtx        context.Context
	config          *Config
	runner          any
	attempt         *executionAttempt
	released        bool
	unusable        bool
	turnId          string
	turnOpen        bool
	instructions    []*ModelMessage
	lastOutcome     *SavedOutcome
	resumePhase     ExecutionPhase
	limitDetails    *LimitExceeded
	prepareRecovery func(*ExecutionContext) (bool, error)

	toolSearches       []*ToolSearchTrace
	steeringMessages   SteeringMessages
	finalContentSource *ContentSource

	mu                 sync.RWMutex
	run                *executionRun
	savedTask          EncodedValue
	active             []EntryId
	draftRevision      uint64
	state              map[string]EncodedValue
	phase              ExecutionPhase
	completedIteration int
	restored           bool
	currentToolPlan    *ToolPlan
	boundToolPlans     map[string]*ToolPlan

	// Go context for cancellation (created via context.WithCancelCause)
	goCtx  context.Context
	cancel context.CancelCauseFunc
	// deadlineCancel is set only when restore owns an added deadline context.

	// Limits that trigger execution termination
	limits        []Limit
	exceededLimit *Limit // set when a limit is exceeded

	// Execution result (populated on termination)
	result *ExecutionResult

	// User's custom loop data (retained as interface for extensibility)
	data LoopData

	// Execution name (e.g., "main", "compaction", "tool:search")
	name string

	// Opaque context identity for reliable trace correlation.
	contextId string

	// Per-context call sequences for generated model/tool call IDs.
	nextModelCallSeq uint64
	nextToolCallSeq  uint64
	modelReplay      modelReplay

	// Current position (auto-tracked)
	iteration int

	// All events (append-only log)

	// Aggregates (auto-updated when certain events are published)
	stats *ExecutionStats

	// Nesting support

	// Timing
	startTime time.Time
	endTime   time.Time

	// Termination
	terminationReason TerminationReason
	finalResult       []ContentPart
	acceptedValue     *EncodedValue
	refusal           *ModelRefusal
	err               error
	// terminalEvent is frozen by PublishAfterExecution and queued only at the terminal save.
	// run.mu protects it so acceptance cannot save success without the matching outcome.
	terminalEvent *RecordedEvent

	// Event publisher for dispatching events to subscribers (set by Executor)

	// Streaming support

	// Compaction configuration (optional)
}

// -----------------------------------------------------------------------------
// Data Access
// -----------------------------------------------------------------------------

// Data returns the user's LoopData.
func (execCtx *ExecutionContext) Data() LoopData {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.data
}

// Name returns the name of this execution context.
func (execCtx *ExecutionContext) Name() string {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.name
}

// ContextId returns the opaque identity of this execution context.
func (execCtx *ExecutionContext) ContextId() string {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.contextId
}

// -----------------------------------------------------------------------------
// Context and Limits
// -----------------------------------------------------------------------------

var errExecutionReleased = errors.New("gent: execution resources released")

// Context returns the underlying context.Context for this execution.
// Use this when calling external APIs that require context.Context.
//
// The context is cancelled when:
//   - A configured limit is exceeded
//   - The parent context is cancelled
//   - Stop requests a pause, or Release disposes of the handle
//   - The execution terminates
func (execCtx *ExecutionContext) Context() context.Context {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.goCtx
}

// Limits returns the configured limits.
func (execCtx *ExecutionContext) Limits() []Limit {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.limits
}

// ExceededLimit returns the limit that was exceeded, or nil if no limit was exceeded.
func (execCtx *ExecutionContext) ExceededLimit() *Limit {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.exceededLimit
}

// Result returns the attempt's result after NewTurn or Continue returns. The value and every
// reachable field are read-only. Use Runner.GetLastTurnResult to read the last completed turn
// while a newer turn is unfinished. Unexpected execution failures return no accepted result.
func (execCtx *ExecutionContext) Result() *ExecutionResult {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.result
}

// -----------------------------------------------------------------------------
// Limit Checking (internal)
// -----------------------------------------------------------------------------

// limitExceededInfo contains information about a limit that was
// exceeded.
type limitExceededInfo struct {
	limit        *Limit
	currentValue float64
	matchedKey   StatKey
}

// checkLimits checks if any limits are exceeded on this context.
// This is called by ExecutionStats when counters/gauges are updated.
// The current turn baseline selects turn usage; run scope uses cumulative counters.
func (execCtx *ExecutionContext) checkLimits() {
	var info *limitExceededInfo

	execCtx.updateContextState(func() {
		// Already exceeded, don't check again
		if execCtx.exceededLimit != nil {
			return
		}

		// Check all limits
		info = execCtx.evaluateLimitsLocked()
		if info == nil {
			return
		}

		execCtx.exceededLimit = info.limit
		execCtx.limitDetails = &LimitExceeded{Limit: info.limit, MatchedKey: info.matchedKey,
			CurrentValue: info.currentValue}
	})

	if info == nil {
		return
	}

	// Publish event outside lock to avoid deadlock
	execCtx.PublishLimitExceeded(*info.limit, info.currentValue, info.matchedKey)

	// Cancel context after publishing event
	execCtx.cancel(fmt.Errorf("limit exceeded: %s > %v", info.limit.Key, info.limit.MaxValue))
}

// EnforceIterationLimit checks exact iteration limits using executor semantics.
//
// Iteration limits are a hard cap on completed loop iterations, so they use >= after
// AfterIteration instead of the generic stat limit's > comparison on stat update. Scope selects
// the current turn's iterations or cumulative run iterations.
func (execCtx *ExecutionContext) EnforceIterationLimit() bool {
	return execCtx.enforceLocalIterationLimit()
}

func (execCtx *ExecutionContext) enforceLocalIterationLimit() bool {
	var info *limitExceededInfo

	execCtx.updateContextState(func() {
		if execCtx.exceededLimit != nil {
			return
		}
		info = execCtx.evaluateIterationLimitsLocked()
		if info == nil {
			return
		}
		execCtx.exceededLimit = info.limit
		execCtx.limitDetails = &LimitExceeded{Limit: info.limit, MatchedKey: info.matchedKey,
			CurrentValue: info.currentValue}
	})

	if info == nil {
		return false
	}
	execCtx.PublishLimitExceeded(*info.limit, info.currentValue, info.matchedKey)
	execCtx.cancel(fmt.Errorf(
		"limit exceeded: %s >= %v", info.limit.Key, info.limit.MaxValue,
	))
	return true
}

func (execCtx *ExecutionContext) evaluateIterationLimitsLocked() *limitExceededInfo {
	for i := range execCtx.limits {
		limit := &execCtx.limits[i]
		if !isIterationLimitKey(limit.Key) || limit.Type != LimitExactKey {
			continue
		}
		val := execCtx.stats.limitCounter(limit.Key, limit.Scope)
		if float64(val) >= limit.MaxValue {
			return &limitExceededInfo{
				limit:        limit,
				currentValue: float64(val),
				matchedKey:   limit.Key,
			}
		}
	}
	return nil
}

// evaluateLimitsLocked evaluates all limits against current stats.
// Returns info about the first exceeded limit, or nil if all limits are within bounds.
// Must be called with lock held.
func (execCtx *ExecutionContext) evaluateLimitsLocked() *limitExceededInfo {
	for i := range execCtx.limits {
		limit := &execCtx.limits[i]
		if info := execCtx.checkLimitLocked(limit); info != nil {
			return info
		}
	}
	return nil
}

// checkLimitLocked checks if a single limit is exceeded.
// Returns info about the exceeded limit, or nil if not exceeded.
// Must be called with lock held.
func (execCtx *ExecutionContext) checkLimitLocked(limit *Limit) *limitExceededInfo {
	switch limit.Type {
	case LimitExactKey:
		return execCtx.checkExactKeyLimit(limit)
	case LimitKeyPrefix:
		return execCtx.checkPrefixLimit(limit)
	default:
		return nil
	}
}

// checkExactKeyLimit checks if an exact key limit is exceeded.
// Returns info about the exceeded limit, or nil if not exceeded.
func (execCtx *ExecutionContext) checkExactKeyLimit(
	limit *Limit,
) *limitExceededInfo {
	if isIterationLimitKey(limit.Key) {
		return nil
	}
	// Check counters first
	if val := execCtx.stats.limitCounter(limit.Key, limit.Scope); val > 0 {
		if float64(val) > limit.MaxValue {
			return &limitExceededInfo{
				limit:        limit,
				currentValue: float64(val),
				matchedKey:   limit.Key,
			}
		}
	}
	// Check gauges
	if val := execCtx.stats.GetGauge(limit.Key); val > 0 {
		if val > limit.MaxValue {
			return &limitExceededInfo{
				limit:        limit,
				currentValue: val,
				matchedKey:   limit.Key,
			}
		}
	}
	return nil
}

func isIterationLimitKey(key StatKey) bool {
	return key == SCIterations
}

// checkPrefixLimit checks if any key with the given prefix exceeds
// the limit.
// Returns info about the exceeded limit, or nil if not exceeded.
func (execCtx *ExecutionContext) checkPrefixLimit(
	limit *Limit,
) *limitExceededInfo {
	prefix := string(limit.Key)
	// Check all counters with matching prefix
	for key := range execCtx.stats.Counters() {
		val := execCtx.stats.limitCounter(StatKey(key), limit.Scope)
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			if float64(val) > limit.MaxValue {
				return &limitExceededInfo{
					limit:        limit,
					currentValue: float64(val),
					matchedKey:   StatKey(key),
				}
			}
		}
	}
	// Check all gauges with matching prefix
	for key, val := range execCtx.stats.Gauges() {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			if val > limit.MaxValue {
				return &limitExceededInfo{
					limit:        limit,
					currentValue: val,
					matchedKey:   StatKey(key),
				}
			}
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Iteration Management
// -----------------------------------------------------------------------------

// Iteration returns the current iteration number (1-indexed).
// Returns 0 if no iteration has started.
func (execCtx *ExecutionContext) Iteration() int {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.iteration
}

// IncrementIteration increments the iteration counter.
// Called by the Executor at the start of each iteration before publishing BeforeIterationEvent.
func (execCtx *ExecutionContext) IncrementIteration() {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	execCtx.iteration++
}

// -----------------------------------------------------------------------------
// Event Publishing
// -----------------------------------------------------------------------------

// updateContextState executes the given function while holding the mutex lock.
// The lock is always released via defer, ensuring safe cleanup even on panic.
func (execCtx *ExecutionContext) updateContextState(fn func()) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	fn()
}

// publish is the internal implementation for all event publishing.
// It records the event, updates stats, checks limits, and dispatches to subscribers.
func (execCtx *ExecutionContext) publish(event Event) {
	execCtx.publishWithEventIteration(event, nil)
}

// publishWithEventIteration publishes an event and optionally pins AfterModelCallEvent to the
// iteration where its asynchronous model call started.
func (execCtx *ExecutionContext) publishWithEventIteration(event Event, iteration *int) {
	execCtx.publishConfigured(event, iteration, nil)
}

// Publish records a custom event, updates stats, checks limits, and dispatches to subscribers.
// Use this for user-defined Event types. For framework events, use the typed PublishXXX methods.
func (execCtx *ExecutionContext) Publish(event Event) error {
	execCtx.publish(event)
	return execCtx.RecordingError()
}

// populateBaseEvent fills in BaseEvent fields.
// Must be called with lock held.
func (execCtx *ExecutionContext) populateBaseEvent(event Event, source string) {
	base := eventBase(event)
	if base == nil {
		return
	}
	base.Timestamp = time.Now()
	base.Iteration = execCtx.iteration
	if base.Source == "" {
		base.Source = source
	}
	if base.ContextId == "" {
		base.ContextId = execCtx.contextId
	}
}

func eventBase(event Event) *BaseEvent {
	switch e := event.(type) {
	case *BeforeExecutionEvent:
		return &e.BaseEvent
	case *AfterExecutionEvent:
		return &e.BaseEvent
	case *BeforeIterationEvent:
		return &e.BaseEvent
	case *AfterIterationEvent:
		return &e.BaseEvent
	case *BeforeModelCallEvent:
		return &e.BaseEvent
	case *AfterModelCallEvent:
		return &e.BaseEvent
	case *BeforeToolCallEvent:
		return &e.BaseEvent
	case *AfterToolCallEvent:
		return &e.BaseEvent
	case *ParseErrorEvent:
		return &e.BaseEvent
	case *OutputValidationEvent:
		return &e.BaseEvent
	case *ErrorEvent:
		return &e.BaseEvent
	case *CommonEvent:
		return &e.BaseEvent
	case *StateChangeEvent:
		return &e.BaseEvent
	case *LimitExceededEvent:
		return &e.BaseEvent
	case *CompactionEvent:
		return &e.BaseEvent
	default:
		if withBase, ok := event.(interface{ EventBase() *BaseEvent }); ok {
			return withBase.EventBase()
		}
		return nil
	}
}

// updateStatsForEvent updates stats based on event type.
// Must be called with lock held.
func (execCtx *ExecutionContext) updateStatsForEvent(event Event) {
	switch e := event.(type) {
	// Increment BEFORE events (for prevention/limits)
	case *BeforeIterationEvent:
		execCtx.stats.incrCounterDirect(SCIterations, 1)
		// Reset per-iteration token gauges
		execCtx.stats.ResetGauge(SGInputTokensLastIteration)
		execCtx.stats.ResetGauge(SGOutputTokensLastIteration)
		execCtx.stats.ResetGauge(SGTotalTokensLastIteration)
		execCtx.stats.resetGaugesByPrefix(
			SGInputTokensLastIterationFor,
		)
		execCtx.stats.resetGaugesByPrefix(
			SGOutputTokensLastIterationFor,
		)
		execCtx.stats.resetGaugesByPrefix(
			SGTotalTokensLastIterationFor,
		)

	case *BeforeToolCallEvent:
		execCtx.stats.incrCounterDirect(SCToolCalls, 1)
		if e.ToolName != "" {
			execCtx.stats.incrCounterDirect(
				SCToolCallsFor+StatKey(e.ToolName), 1,
			)
		}

	// Increment AFTER events (for recording)
	case *AfterModelCallEvent:
		totalTokens := int64(e.InputTokens) + int64(e.OutputTokens)
		execCtx.stats.incrCounterDirect(
			SCInputTokens, int64(e.InputTokens),
		)
		execCtx.stats.incrCounterDirect(
			SCOutputTokens, int64(e.OutputTokens),
		)
		execCtx.stats.incrCounterDirect(
			SCTotalTokens, totalTokens,
		)
		if e.Model != "" {
			execCtx.stats.incrCounterDirect(
				SCInputTokensFor+StatKey(e.Model),
				int64(e.InputTokens),
			)
			execCtx.stats.incrCounterDirect(
				SCOutputTokensFor+StatKey(e.Model),
				int64(e.OutputTokens),
			)
			execCtx.stats.incrCounterDirect(
				SCTotalTokensFor+StatKey(e.Model),
				totalTokens,
			)
		}

		// Per-iteration gauges describe the active iteration. Late model completions
		// from a closed stream keep their original event iteration and must not update
		// gauges that may have already been reset for a newer iteration.
		if e.Iteration == execCtx.Iteration() {
			execCtx.stats.incrGaugeInternal(
				SGInputTokensLastIteration,
				float64(e.InputTokens),
			)
			execCtx.stats.incrGaugeInternal(
				SGOutputTokensLastIteration,
				float64(e.OutputTokens),
			)
			execCtx.stats.incrGaugeInternal(
				SGTotalTokensLastIteration,
				float64(totalTokens),
			)
			if e.Model != "" {
				execCtx.stats.incrGaugeInternal(
					SGInputTokensLastIterationFor+
						StatKey(e.Model),
					float64(e.InputTokens),
				)
				execCtx.stats.incrGaugeInternal(
					SGOutputTokensLastIterationFor+
						StatKey(e.Model),
					float64(e.OutputTokens),
				)
				execCtx.stats.incrGaugeInternal(
					SGTotalTokensLastIterationFor+
						StatKey(e.Model),
					float64(totalTokens),
				)
			}
		}

	case *AfterToolCallEvent:
		if record, ok := e.Output.(*ToolCallRecord); ok {
			execCtx.updateToolRecordStats(record)
		} else if e.Error != nil {
			execCtx.addToolError(e.ToolName)
		}

	case *ParseErrorEvent:
		switch e.ErrorType {
		case ParseErrorTypeToolchain:
			execCtx.stats.incrCounterDirect(
				SCToolchainParseErrorTotal, 1,
			)
			execCtx.stats.incrGaugeInternal(
				SGToolchainParseErrorConsecutive, 1,
			)
		}

	case *OutputValidationEvent:
		execCtx.updateOutputStats(e)
	case *CompactionEvent:
		execCtx.stats.incrCounterDirect(SCCompactions, 1)
	}
}

type modelCallEventOptions struct {
	modelCallId   string
	streamId      string
	streamTopicId string
	source        string
	provider      string
}

// ModelCallEventOption configures model call event metadata.
type ModelCallEventOption func(*modelCallEventOptions)

// WithModelCallId associates before/after model events with a specific model call.
func WithModelCallId(id string) ModelCallEventOption {
	return func(opts *modelCallEventOptions) {
		opts.modelCallId = id
	}
}

// WithModelStream associates model events with a stream ID and topic ID.
func WithModelStream(streamId string, streamTopicId string) ModelCallEventOption {
	return func(opts *modelCallEventOptions) {
		opts.streamId = streamId
		opts.streamTopicId = streamTopicId
	}
}

// WithModelCallSource pins model event source when completion happens asynchronously.
func WithModelCallSource(source string) ModelCallEventOption {
	return func(opts *modelCallEventOptions) {
		opts.source = source
	}
}

// WithModelProvider records the provider name when the model wrapper knows it.
func WithModelProvider(provider string) ModelCallEventOption {
	return func(opts *modelCallEventOptions) {
		opts.provider = provider
	}
}

type toolCallEventOptions struct {
	toolCallId    string
	source        string
	attemptId     string
	validateInput func(any) error
}

// WithToolCallInputValidation validates the effective typed input after BeforeToolCall returns,
// before publishing or saving the call. Tool chains use this for schema and Go type checks.
// A failure is an application error, not correction guidance for model-supplied arguments.
func WithToolCallInputValidation(validate func(any) error) ToolCallEventOption {
	return func(options *toolCallEventOptions) { options.validateInput = validate }
}

// WithToolCallAttempt identifies the physical attempt reconciled by a recovery adapter.
func WithToolCallAttempt(id string) ToolCallEventOption {
	return func(options *toolCallEventOptions) { options.attemptId = id }
}

// ToolCallEventOption configures tool call identity, source, or effective input validation.
type ToolCallEventOption func(*toolCallEventOptions)

// WithToolCallId associates before/after tool events with a specific tool call.
func WithToolCallId(id string) ToolCallEventOption {
	return func(opts *toolCallEventOptions) {
		opts.toolCallId = id
	}
}

// WithToolCallSource pins tool event source when completion happens asynchronously.
func WithToolCallSource(source string) ToolCallEventOption {
	return func(opts *toolCallEventOptions) {
		opts.source = source
	}
}

func applyModelCallEventOptions(opts []ModelCallEventOption) modelCallEventOptions {
	var result modelCallEventOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&result)
		}
	}
	return result
}

func applyToolCallEventOptions(opts []ToolCallEventOption) toolCallEventOptions {
	var result toolCallEventOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&result)
		}
	}
	return result
}

func (execCtx *ExecutionContext) nextModelCallId() (string, error) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	return execCtx.allocateCallId("model", &execCtx.nextModelCallSeq)
}

func (execCtx *ExecutionContext) nextToolCallId() (string, error) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	return execCtx.allocateCallId("tool", &execCtx.nextToolCallSeq)
}

// The caller holds mu. Allocation must never wrap, because accepted identities remain valid
// after their source records leave the compact execution state.
func (execCtx *ExecutionContext) allocateCallId(kind string, sequence *uint64) (string, error) {
	if *sequence == ^uint64(0) {
		return "", fmt.Errorf("gent: %s call sequence exhausted: run=%s turn=%s context=%s",
			kind, execCtx.run.id, execCtx.turnId, execCtx.contextId)
	}
	*sequence++
	return fmt.Sprintf("%s:%s:%d", execCtx.contextId, kind, *sequence), nil
}

// -----------------------------------------------------------------------------
// PublishXXX Convenience Methods
// -----------------------------------------------------------------------------

// PublishBeforeExecution publishes a BeforeExecutionEvent.
func (execCtx *ExecutionContext) PublishBeforeExecution() *BeforeExecutionEvent {
	event := &BeforeExecutionEvent{
		BaseEvent: BaseEvent{EventName: EventNameExecutionBefore},
		Name:      execCtx.Name(),
	}
	execCtx.publish(event)
	return event
}

// PublishAfterExecution runs the completion hooks and freezes an AfterExecutionEvent.
// CommitCheckpoint(PhaseTerminal) saves it with the outcome before live consumers see success.
func (execCtx *ExecutionContext) PublishAfterExecution(
	reason TerminationReason,
	err error,
) *AfterExecutionEvent {
	event := &AfterExecutionEvent{
		BaseEvent:         BaseEvent{EventName: EventNameExecutionAfter},
		TerminationReason: reason,
		Error:             err,
		Output:            execCtx.FinalResult(),
		Refusal:           execCtx.Refusal(),
		Duration:          execCtx.Duration(),
	}
	execCtx.publish(event)
	return event
}

// PublishBeforeIteration publishes a BeforeIterationEvent.
// Stats updated: Iterations counter is incremented.
func (execCtx *ExecutionContext) PublishBeforeIteration() *BeforeIterationEvent {
	event := &BeforeIterationEvent{
		BaseEvent: BaseEvent{EventName: EventNameIterationBefore},
	}
	execCtx.publish(event)
	return event
}

// PublishAfterIteration publishes an AfterIterationEvent.
func (execCtx *ExecutionContext) PublishAfterIteration(
	result *AgentLoopResult,
	duration time.Duration,
	iterationErrors ...error,
) *AfterIterationEvent {
	event := &AfterIterationEvent{
		BaseEvent: BaseEvent{EventName: EventNameIterationAfter},
		Result:    result,
		Duration:  duration,
	}
	if len(iterationErrors) > 0 {
		event.Error = iterationErrors[0]
	}
	execCtx.publish(event)
	return event
}

// PublishBeforeModelCall publishes a BeforeModelCallEvent.
// Returns the event so callers can use the (potentially modified) Request field.
func (execCtx *ExecutionContext) PublishBeforeModelCall(
	model string,
	request *ModelCallRequest,
	opts ...ModelCallEventOption,
) *BeforeModelCallEvent {
	options := applyModelCallEventOptions(opts)
	if options.modelCallId == "" {
		id, err := execCtx.nextModelCallId()
		if err != nil {
			execCtx.stopRecording(err)
			return &BeforeModelCallEvent{Model: model, Request: request}
		}
		options.modelCallId = id
	}
	event := &BeforeModelCallEvent{
		ToolSearches: execCtx.takeToolSearches(),
		BaseEvent: BaseEvent{
			EventName: EventNameModelCallBefore,
			Source:    options.source,
		},
		Model:         model,
		Provider:      options.provider,
		Request:       request,
		ModelCallId:   options.modelCallId,
		StreamId:      options.streamId,
		StreamTopicId: options.streamTopicId,
	}
	execCtx.publish(event)
	return event
}

// PublishAfterModelCall publishes an AfterModelCallEvent.
// Stats updated: InputTokens, OutputTokens (and per-model variants).
func (execCtx *ExecutionContext) PublishAfterModelCall(
	model string,
	request *ModelCallRequest,
	response *ModelCallResponse,
	duration time.Duration,
	err error,
	opts ...ModelCallEventOption,
) *AfterModelCallEvent {
	options := applyModelCallEventOptions(opts)
	event := newAfterModelCallEvent(model, request, response, duration, err, options)
	execCtx.publish(event)
	return event
}

// PublishAfterModelCallForIteration publishes an AfterModelCallEvent for the iteration where
// the model call started. Use this for asynchronous model calls that may complete after the
// execution context has advanced to a later iteration.
func (execCtx *ExecutionContext) PublishAfterModelCallForIteration(
	iteration int,
	model string,
	request *ModelCallRequest,
	response *ModelCallResponse,
	duration time.Duration,
	err error,
	opts ...ModelCallEventOption,
) *AfterModelCallEvent {
	options := applyModelCallEventOptions(opts)
	event := newAfterModelCallEvent(model, request, response, duration, err, options)
	execCtx.publishWithEventIteration(event, &iteration)
	return event
}

func newAfterModelCallEvent(
	model string,
	request *ModelCallRequest,
	response *ModelCallResponse,
	duration time.Duration,
	err error,
	options modelCallEventOptions,
) *AfterModelCallEvent {
	event := &AfterModelCallEvent{
		BaseEvent: BaseEvent{
			EventName: EventNameModelCallAfter,
			Source:    options.source,
		},
		Model:         model,
		Provider:      options.provider,
		Request:       request,
		Response:      response,
		Duration:      duration,
		Error:         err,
		ModelCallId:   options.modelCallId,
		StreamId:      options.streamId,
		StreamTopicId: options.streamTopicId,
	}
	if response != nil && response.Info != nil {
		event.InputTokens = response.Info.InputTokens
		event.OutputTokens = response.Info.OutputTokens
	}
	return event
}

// PublishBeforeToolCall publishes a BeforeToolCallEvent.
// Returns the event so callers can use the (potentially modified) Args field.
// Stats updated: ToolCalls (and per-tool variants).
// WithToolCallInputValidation checks Hook changes before the event and tool-ready save are accepted.
// Check RecordingError before starting the tool; a validation failure must reach the application.
func (execCtx *ExecutionContext) PublishBeforeToolCall(
	toolName string,
	args any,
	opts ...ToolCallEventOption,
) *BeforeToolCallEvent {
	options := applyToolCallEventOptions(opts)
	if options.toolCallId == "" {
		options.toolCallId = execCtx.selectedToolCallId()
	}
	if options.toolCallId == "" {
		id, err := execCtx.nextToolCallId()
		if err != nil {
			execCtx.stopRecording(err)
			return &BeforeToolCallEvent{ToolName: toolName, Args: args}
		}
		options.toolCallId = id
	}
	event := &BeforeToolCallEvent{
		BaseEvent: BaseEvent{
			EventName: EventNameToolCallBefore,
			Source:    options.source,
		},
		ToolName:   toolName,
		Args:       args,
		ToolCallId: options.toolCallId,
	}
	execCtx.publishConfigured(event, nil, func() error {
		if options.validateInput == nil {
			return nil
		}
		if err := options.validateInput(event.Args); err != nil {
			return fmt.Errorf("gent: effective input for tool %q in run %q turn %q context %q call %q: %w",
				toolName, execCtx.RunId(), execCtx.TurnId(), execCtx.ContextId(), event.ToolCallId, err)
		}
		return nil
	})
	if execCtx.RecordingError() != nil {
		return event
	}
	if err := execCtx.saveToolReady(event); err != nil {
		execCtx.stopRecording(err)
	}
	return event
}

// PublishAfterToolCall publishes an AfterToolCallEvent.
// Stats updated: ToolCallsErrorTotal, ToolCallsErrorConsecutive (on error).
func (execCtx *ExecutionContext) PublishAfterToolCall(
	toolName string,
	args any,
	output any,
	duration time.Duration,
	err error,
	opts ...ToolCallEventOption,
) *AfterToolCallEvent {
	options := applyToolCallEventOptions(opts)
	event := &AfterToolCallEvent{
		BaseEvent: BaseEvent{
			EventName: EventNameToolCallAfter,
			Source:    options.source,
			AttemptId: options.attemptId,
		},
		ToolName:   toolName,
		Args:       args,
		Output:     output,
		Duration:   duration,
		Error:      err,
		ToolCallId: options.toolCallId,
	}
	execCtx.publish(event)
	return event
}

// PublishParseError publishes a ParseErrorEvent.
// Stats updated: Based on errorType - format, toolchain, termination, or section errors.
func (execCtx *ExecutionContext) PublishParseError(
	errorType ParseErrorType,
	rawContent string,
	err error,
) *ParseErrorEvent {
	event := &ParseErrorEvent{
		BaseEvent:  BaseEvent{EventName: EventNameParseError},
		ErrorType:  errorType,
		RawContent: rawContent,
		Error:      err,
	}
	execCtx.publish(event)
	return event
}

// PublishError publishes an ErrorEvent.
func (execCtx *ExecutionContext) PublishError(err error) *ErrorEvent {
	event := &ErrorEvent{
		BaseEvent: BaseEvent{EventName: EventNameError},
		Error:     err,
	}
	execCtx.publish(event)
	return event
}

// PublishLimitExceeded publishes a LimitExceededEvent.
// This is called automatically when a limit is exceeded during
// checkLimits().
func (execCtx *ExecutionContext) PublishLimitExceeded(
	limit Limit,
	currentValue float64,
	matchedKey StatKey,
) *LimitExceededEvent {
	event := &LimitExceededEvent{
		BaseEvent: BaseEvent{
			EventName: EventNameLimitExceeded,
		},
		Limit:        limit,
		CurrentValue: currentValue,
		MatchedKey:   matchedKey,
	}
	execCtx.publish(event)
	return event
}

// PublishCompaction publishes a CompactionEvent.
// Stats updated: SCCompactions counter is incremented.
func (execCtx *ExecutionContext) PublishCompaction(
	lengthBefore int,
	lengthAfter int,
	duration time.Duration,
) *CompactionEvent {
	event := &CompactionEvent{
		BaseEvent: BaseEvent{
			EventName: EventNameCompaction,
		},
		ScratchpadLengthBefore: lengthBefore,
		ScratchpadLengthAfter:  lengthAfter,
		Duration:               duration,
	}
	execCtx.publish(event)
	return event
}

// PublishCommonEvent publishes a CommonEvent for user-defined events.
// The eventName should use the format "namespace:event_name" (e.g., "myapp:cache_hit").
func (execCtx *ExecutionContext) PublishCommonEvent(
	eventName string,
	description string,
	data any,
) *CommonEvent {
	event := &CommonEvent{
		BaseEvent:   BaseEvent{EventName: eventName},
		Description: description,
		Data:        data,
	}
	execCtx.publish(event)
	return event
}

// Stats returns the execution stats for this context.
//
// Thread Safety:
//   - Safe to call from any goroutine
//   - All ExecutionStats methods handle their own locking
//   - Safe to read during subscriber execution (subscribers run synchronously)
func (execCtx *ExecutionContext) Stats() *ExecutionStats {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.stats
}

// -----------------------------------------------------------------------------
// Nesting
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Termination
// -----------------------------------------------------------------------------

// SetTermination sets the termination reason and final result.
// Called by the Executor when execution ends.
//
// This also populates the Result() field with an ExecutionResult containing
// all termination information.
func (execCtx *ExecutionContext) SetTermination(
	reason TerminationReason, result []ContentPart, err error,
) {
	execCtx.setTermination(reason, result, nil, err)
}

// SetRefusal records a native refusal outcome without accepting content. The executor commits
// this outcome before returning it, just as it commits a successful typed result.
func (execCtx *ExecutionContext) SetRefusal(refusal *ModelRefusal) {
	execCtx.setTermination(TerminationRefused, nil, refusal, nil)
}

func (execCtx *ExecutionContext) setTermination(
	reason TerminationReason, result []ContentPart, refusal *ModelRefusal, err error,
) {
	execCtx.mu.Lock()
	defer execCtx.mu.Unlock()
	execCtx.terminationReason = reason
	if reason != TerminationSuccess {
		execCtx.acceptedValue = nil
	}
	execCtx.finalResult = CloneContentParts(result)
	execCtx.refusal = cloneRefusal(refusal)
	execCtx.err = err
	execCtx.endTime = time.Now()
	status := ExecutionFinished
	if reason == TerminationStopped || reason == TerminationLimitExceeded {
		status = ExecutionPaused
	}
	execCtx.result = &ExecutionResult{
		Status:            status,
		TurnId:            execCtx.turnId,
		TerminationReason: reason,
		Output:            execCtx.finalResult,
		Refusal:           execCtx.refusal,
		ExceededLimit:     execCtx.limitDetails,
	}
	if reason == TerminationCanceled {
		execCtx.result.CancellationCause = SaveError(err)
	}
}

func (execCtx *ExecutionContext) Refusal() *ModelRefusal {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return cloneRefusal(execCtx.refusal)
}

// TerminationReason returns why execution terminated.
func (execCtx *ExecutionContext) TerminationReason() TerminationReason {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.terminationReason
}

// FinalResult returns the final result (if terminated successfully).
func (execCtx *ExecutionContext) FinalResult() []ContentPart {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return CloneContentParts(execCtx.finalResult)
}

// Error returns the error (if terminated with error).
func (execCtx *ExecutionContext) Error() error {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.err
}

// StartTime returns when execution began.
func (execCtx *ExecutionContext) StartTime() time.Time {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.startTime
}

// EndTime returns when execution completed.
func (execCtx *ExecutionContext) EndTime() time.Time {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return execCtx.endTime
}

// Duration returns the total execution duration.
// If execution is still in progress, returns duration since start.
func (execCtx *ExecutionContext) Duration() time.Duration {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	if execCtx.endTime.IsZero() {
		return time.Since(execCtx.startTime)
	}
	return execCtx.endTime.Sub(execCtx.startTime)
}

// -----------------------------------------------------------------------------
// Streaming Support
// -----------------------------------------------------------------------------

// EmitChunk records progress under this context. Model adapters use the managed model boundary
// to associate progress with its call. Accepted answers are emitted only by the terminal save.
func (execCtx *ExecutionContext) EmitChunk(chunk StreamChunk) error {
	return execCtx.emitChunk(&chunk, nil)
}

func (execCtx *ExecutionContext) emitChunk(chunk *StreamChunk, call *BeforeModelCallEvent) error {
	if len(chunk.SourceMessageIds) != 0 {
		return fmt.Errorf("gent: only accepted answers can set source message identities")
	}
	if err := validateAssistantParts(chunk.Content, true); err != nil {
		return err
	}
	return execCtx.recordChunk(chunk, call)
}

// BuildSourcePath identifies the context and current iteration for display.
func (execCtx *ExecutionContext) BuildSourcePath() string {
	execCtx.mu.RLock()
	defer execCtx.mu.RUnlock()
	return fmt.Sprintf("%s/%d", execCtx.name, execCtx.iteration)
}

// IsWorkerInterrupted reports owner cancellation or Stop before a managed operation completes.
// The pending operation stays recoverable; no failed-tool result replaces its unknown outcome.
func (execCtx *ExecutionContext) IsWorkerInterrupted() bool {
	return execCtx.Context().Err() != nil && execCtx.ExceededLimit() == nil &&
		execCtx.RecordingError() == nil
}
