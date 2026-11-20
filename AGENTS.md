<critical_behavior>
# Critical Behavior
- **IMPORTANT**: Gent is in development. Change APIs freely; no backward compatibility is needed.
- **ALWAYS fully finish your tasks** when executing anything. Never stop to ask "would you like to continue?" or anything similar.
  You are given tasks, fully complete them, don't waste our time. Exception: Destructive actions with irreversible consequences.
- **ALWAYS verify your work and assumptions**, don't just read the code, actually test what you're doing.
  Write tests, write scripts to test behaviors, run playwright to check console, take screenshots, run the commands.
- **NEVER blame without evidence**, don't say something like "X fail due to Y", find evidence!
  **Always investigate to find root cause**, if unable to find evidence, state why and clarify it's a hypothesis.
- **ALWAYS write proper documentation**, write *why* it was done this way, and *how* only if it's not obvious.
  Write for a human/yourself when revisiting this code in the future, what's important so futureself work faster, fewer mistakes, less tokens?
- **NEVER execute staging, unstaging, or stashing git changes**, this will cause **loss of verification work** in our collaboration!
- **NEVER remove or edit changes that are not yours**, you are not the only one working in the repository.
  When you absolutely require to touch changes that are not yours, ASK permission.
- **NEVER delete valid comments**, contextual comments are important for maintainability.
  Removing comments that contains business context (the "why") will cause **loss of context**.
  Only remove comments if they are no longer valid.
- **ALWAYS** use ASD-STE100 Simplified Technical English (STE) in ALL your outputs, e.g. when communicating with me, writing documentations, code comments, variable names, function names, entity names, tests, comments.
- **ALWAYS prioritize readability, maintainability, simplicity, elegance** of your code.
  Aim for low-cyclomatic complexity in your code, exit early whenever you can, it's okay to repeat lines if we reduce cyclomatic
  complexity or prevent interleaving conditionals.
- **DO NOT excessively nil check**. Treat pointer/interface parameters/struct fields as required unless explicitly documented optional.
  If caller sends nil, then nil pointer panic is fine. Fail fast, unless we truly need the recovery.
- **Code simplicity**: Remove vestigial code and unused interfaces when found.
- **ALWAYS make main branch a single trunk line**. No merge commits, squash and rebase when needed.
</critical_behavior>

<gent_design_principles>
# Gent design principles

- Prefer explicit interfaces. Result structs and typed enums must show possible states and fields to
  check. This keeps expected control flow visible in the interface.
- Define each enum with its own named string type. Declare each allowed value as a typed const.
  Add a Go documentation comment immediately above each enum constant. Start the comment with the
  constant name and explain what the value means and when it applies.
- Use explicit result types for expected failures and other conditions that callers must handle.
- Do not use error types or error constructors to represent expected states.
- Use `error` for an unexpected operation failure with no accepted result. Use explicit result
  states for expected conditions that the caller must handle.
- **Propagate, Fix, Restore, Continue**: propagate unexpected execution errors from Hooks,
  Loop.Next, output processing, and request construction/validation to the application. This
  includes invalid requests or unusable native context caused by selected code/configuration.
  NewTurn/Continue return zero T, nil ExecutionResult, and non-nil error. End the attempt and
  release its owned resources; do not make a terminal error save or accept broken provisional state.
  Preserve the unfinished accepted turn, earlier accepted batches, and last completed result. The
  application can log the error, fix its code/configuration, restore full batches or
  RunStateSnapshot plus the accepted tail, then call Continue. Do not resubmit an accepted task or
  automatically retry unchanged failing code. Include as much relevant diagnostic detail as
  available: the failing stage, component, operation, run/turn/context/call identities, and the
  failed requirement with known actual/required values. Wrap the original cause so
  errors.Is/errors.As still work. Preserve available source/stack detail; do not invent it. Cleanup
  or diagnostic failures must not mask the main error. Keep raw diagnostics in application
  errors/private records, not model steering. The separate handled-tool, Subscriber, cancellation,
  storage, and limit contracts still apply. See D103 in [req-multi-turn.md](req-multi-turn.md).
- A `Tool.Call` or `Tool.Recover` error ends that operation, not the agent run. Send safe steering
  to the model. Keep raw errors and rejected returns in private diagnostic records.
- Cancellation, storage failures, and run limits retain their separate run-level contracts.
- Do not clone for defensive safety. Use ownership transfer and read-only getters. Copy only when
  a feature needs independent mutable values or a stable saved value. Document that reason.
- Support recovery and continuation with updated agent code and configuration wherever saved state
  remains usable. Do not require a saved run to use a matching Runner version. Preserve accepted
  evidence and counters. Allow changes to tools, limits, and steering messages. Require migration
  or report an explicit result when saved data or provider state cannot be used.
- Keep loop-specific models, prompts, retries, and checks on the Loop. Put common services and
  execution policies in gent.Config. Ask the owner before moving a setting with unclear scope.
- Store recoverable custom LoopData in checkpointed SaveBatch values. Treat RunStateSnapshots as
  optional replay acceleration. Batch-only replay must restore the same accepted custom data.
- Use RunStateSnapshot for a captured serializable RunState or its encoded form. Use this term in
  discussions, documentation, and related identifiers. It does not introduce another public state
  type. Keep quoted current-code and third-party names accurate when explaining older APIs.
- Use EventSequenceNumber, PreviousEventSequenceNumber, and LastAppliedEventSequenceNumber for
  the corresponding ordered event positions. The approved rename must cover code, wire names,
  trace cursors, tests, documentation, in-code documentation, and Quick Start material together.
  Keep exact historical and current-code names only when clearly identified as evidence.
- User recovery guarantee: when an application correctly stores complete SaveBatch values through
  Recorder.Save and follows the documented LoopData and managed-execution contracts, Gent must
  restore the same logical agent state at every accepted savepoint. The application can then use
  Continue, or NewTurn after a completed turn. Full batch replay and a RunStateSnapshot plus its
  accepted tail must produce the same state, including custom data, framework continuation, history,
  counters, accepted output, and tool/recovery evidence. Reuse accepted results and completed
  recovery preparation. Treat this as an implementation and test invariant. This guarantees saved
  logical state and continuation; it does not promise identical future model output or undo external
  effects. Use the managed recovery contract for unfinished external operations.
- Subagent recovery and ease of use: Gent must give each child agent loop the same recovery
  guarantees and ease of use as the root loop. A custom AgentLoop must have a managed way to
  create and run subagent Runners. The application must save and recover that loop through the
  same root process used for a single loop: Recorder.Save, full batch replay or RunStateSnapshot
  plus its accepted tail, then Continue or NewTurn as permitted by the root turn state. Gent must
  restore the subagents, their accepted tasks and results, custom data, and managed recovery
  evidence. Gent owns their saved relationships, result reuse, and recovery order. Do not require
  the application to list, replay, or restart subagents separately. Apply the existing LoopData,
  managed-operation, archive, and external-effect contracts. Prove this with tests that use the
  same root save/load code for loops with and without subagents. Remove the old child-context
  feature in the current refactor; defer its replacement until this full contract is supported.
  See D116 in [req-multi-turn.md](req-multi-turn.md).
- User archive guarantee: when an application correctly stores complete SaveBatch values through
  Recorder.Save at approved savepoints and retains the archive, Gent must reconstruct the complete
  accepted run history in RunView, from the first batch through the last accepted savepoint, without
  rerunning models, tools, or output processing. Keep readable content, native evidence, original
  prompts, effective requests, responses, tool results, validation decisions, and compaction changes
  so the application can inspect what happened. Compaction must not delete archived originals.
  A compact RunStateSnapshot plus its tail restores execution but cannot replace the earlier archive
  for full troubleshooting. Unsaved live output and unexposed provider reasoning are outside this
  guarantee. Treat archive reconstruction as an implementation and test invariant.
- Keep the recovery invariant in Go documentation on Recorder and on RunState capture/replay APIs
  when implemented. Custom Next restarts at its entry point on restored data; its author owns safe
  repeated local work and new operations. Gent does not require custom step enums. Preserve accepted
  managed results and recovery preparation for the same operation.
- Teach the documentation site with short examples that show inputs, calls, and results. Keep setup
  and unrelated application code outside each example. Complete Go files are optional references.
- Keep public interfaces simple, intuitive, elegant, and quick to understand.

Keys to controlling complexity:

1. The number of methods/interface you have. We always strive for the smallest amount of methods and
   interfaces that still achieves our goals. If we can simplify, simplify. If we can consolidate,
   consolidate. Reducing the interface surface makes it easier and more intuitive, and it will look
   more elegant. Doing this while still having correct semantics is the real challenge. Don't
   sacrifice one for another.
2. The number of ways to do things. We always strive to only have 1 way to do the exact same thing.
   There shouldn't be multiple ways of achieving the same exact goal. If we have multiple ways of
   doing things, that is usually a part that can be simplified. Having multiple ways of doing things
   mean we have to continuously support them, and it's not maintainable.
3. Start with good defaults, allow customization of those defaults. When we want to have simple and
   advanced ways of doing the exact same thing, have single unified interface with good defaults.
   Have the user customize through the same unified interface. This achieves #2 without sacrificing
   customizability.
</gent_design_principles>

<critical_constraints>
# Critical Constraints

Apply these constraints in priority order:

1. Ensure reliable repeated use. Prevent race conditions, data races, and memory leaks.
2. Minimize CPU and memory use without reducing reliability or performance.
3. Prevent unnecessary work and delay. Maintain good performance.
</critical_constraints>

<codebase_architecture>
# Gent - Go LLM Agent Framework

Gent provides explicit interfaces for custom Go agent loops and standard implementations. One
ExecutionContext represents one conversation. NewTurn adds user input to the same RunId.

## File organization

- Root `.go` files expose public interfaces, type aliases, and constructors.
- `internal/core/` implements shared state and runtime services behind those public aliases.
- `executor/` exposes the typed Runner wrapper without exposing the internal runner type.
- `agents/react/`, `models/`, `toolchain/`, `output/`, and `compaction/` implement the interfaces.
- `internal/tt/` supplies test helpers; `internal/multiturntest/` proves recovery across savepoints.

## Runner and Loop

- `Loop[T].Next` returns LAContinue or LATerminate; `Output()` defines the accepted type.
- `executor.NewRunner(loop, config)` binds common services. Models and loop policy stay on Loop.
- `NewExecutionContext(ctx, name)` creates a fresh conversation; `Restore(ctx, state)` restores
  local state.
- NewTurn and Continue return `(T, *ExecutionResult, error)` and use fresh attempt resources.
- Status is Finished, Paused, Busy, or NoActiveTurn. Stop and limits pause unfinished tasks.
- Stop requests a recoverable pause. Go context cancellation interrupts the worker. Release disposes
  of the handle and joins workers; it does not complete the task or make a terminal save.
- After clean completion or pause, reuse the handle. After interruption or error, restore accepted
  state. GetLastTurnResult returns the last completed result without execution or validation.
- Custom Next restarts at entry on restored LoopData. Its author owns safe repeated local work and
  new operation identities. Gent reuses accepted managed results for the same operation.
- ReAct saves its model response and tool-plan progress internally; no custom step enum is required.

## Task, custom data, history, and state

- Task.Instructions supplies system prefix messages. Nil keeps the prefix; a non-nil slice replaces
  it, including an empty slice that clears it. Task.Messages holds conversation input with at least
  one user message. NewTask(parts...) creates one user message.
- Task messages enter compactable history, including the original prompt. Instructions stay outside
  compaction. Hooks cannot change the saved task; they can change effective request input.
- LoopData embeds CheckpointState and TurnResetter. BasicLoopData is an empty default
  implementation.
- Config.NewLoopData creates an empty instance. Restore fills its saved fields; NewTurn keeps it and
  resets only declared turn fields. ResetTurn must perform local assignment only.
- History keeps stable absolute identities and saved origin. UpdateHistory proposes appended records
  and a complete ordered selection. Approved saves accept the state and evidence together.
- Private RunState holds compact execution state. RunView reconstructs the complete accepted
  archive.
  A compact RunStateSnapshot cannot replace earlier batches for archive reconstruction.
- RunState capture is optional replay acceleration. Every SaveBatch carries complete custom data and
  control state. The Recorder capture callback returns a stable value for that exact batch.
- CodecRegistry validates format, integrity, and typed data. Register codecs before decoding. Apply,
  UnmarshalRunState, and Restore report expected outcomes with typed results.
- See `docs/content/storage/history.md` for archive contracts and links to savepoints, recovery,
  retention, and ownership.

## Model and assistant content

- Model receives standard context.Context and resolved ModelCallRequest; native adapters are in
  `models/`. Managed calls go through ExecutionContext.GenerateContentStream.
- OpenAI uses Responses. Each adapter owns its native request and response mapping.
- Use native controls where supported. Unsupported media uses configured guidance; no transcoding.
- Preserve native completion, refusal, tool IDs, phases, signatures, and opaque continuation items.
- ModelCallResponse.Parts preserves ordered content and native tool requests. AssistantContent
  classifies each logical item; known candidate output is FinalAnswerCandidate until accepted.
- StreamChunk.Content uses the same item representation. Live chunks are frozen but provisional.
- Model calls do not add independent request/response savepoints. The next approved save accepts
  their evidence; interruption can repeat unsaved model work and lose unsaved usage accounting.
- Stream producers call Complete only after cleanup, including cancellation. Response waits for it.
- accepted_answer publishes complete FinalAnswer content only after the terminal save. Source IDs
  link processed output to the original model candidates; the archive retains both.
- See `docs/content/agent/models.md`, `docs/content/agent/tools.md`, and
  `docs/content/agent/outputs.md` for provider and content details.

## ToolChain and Output

- Registry, SearchChain, and JsToolChainWrapper implement native ToolChain in `toolchain/`.
- NativeTools selects definitions; PrepareCalls proposes a plan; ExecuteNext runs a managed step.
- Save effective validated input before a tool starts; accept result, prepared reply, and plan
  progress together afterward. Fresh Executed/Blocked reset call-error gauges; invalid input and
  recovery do not. Publishing call events updates the corresponding statistics.
- Tool.Recover reports Completed, NotStarted, or Unresolved. It inspects the original effect; it
  does not repeat it. The model decides later actions. Accepted recovery preparation is reused.
- SharedLocalSearch advertises search, shared execution, and pins. Current catalogs can change.
- PTC exposes run_code and saves inner effects separately. Interrupted scripts return partial
  evidence without restarting the script. Parallel plans require the chain's explicit support.
- TextOutput, ImageOutput, JsonOutput[T], and SchemaOutput[T] implement Output[T]. Instructions()
  supplies optional caller-authored guidance. Configure sets native controls; Process checks values.
- Always run local validation, then the optional custom validator. OutputResult carries Accepted,
  Value, Content, and Steering. Unexpected errors follow the Propagate, Fix, Restore, Continue rule.
- Keep a rejected candidate and correction selected through the next main model call. Corrections
  use normal limits. Terminal restoration reads accepted output without processing it again.
- TextFormat renders tool-result sections; it does not parse answers or create agent prompts.

## Statistics, limits, subscribers, and hooks

- GetCounter is cumulative; GetTurnCounter subtracts the accepted turn baseline. Counters increase
  only. NewTurn resets all gauges and rebuilds context-size gauges from retained state.
- Limit.Scope defaults to LimitTurn. LimitRun measures cumulative counters; gauges remain
  turn-scoped.
  Exact iteration limits use >= at completed iteration boundaries; other rules use > per update.
- Config.Limits has no hidden default. DefaultLimits returns an optional caller-selected preset.
  Check saved usage before dependent work; Continue does not reset an exhausted allowance.
- Child contexts, hierarchy propagation, and self-prefixed counters are removed under D116. A future
  subagent feature must meet the root recovery and ease-of-use contract above.
- Config.Subscribers registers bounded, ordered Accepted or explicit Live delivery. Payloads are
  concrete read-only values. A stopped subscriber reports its cursor; it does not fail execution.
- Config.Hooks supports only BeforeModelCall and BeforeToolCall. They run synchronously before input
  is frozen. A failure prevents the dependent operation and propagates to the application.
- Trace is a display projection, not recovery storage. Reconnect against the accepted archive and
  reconcile provisional Live positions. See `docs/content/observe/subscribers.md` for delivery failure
  handling.
</codebase_architecture>

<testing_standards>
**General Standards**
- Always use table-driven tests for functions with multiple scenarios.
- For table driven tests, create explicit `input`, `mocks` (if needed), and `expected` structs.
- The expected struct should contain all expected outputs, including errors.
- Expected output must be FULL matches, for example:
  - For strings, compare entire string, not substrings.
  - For structs, compare entire struct, not just individual fields.
  - For fields that contains timestamps, assert >= expected time instead of exact match, DO NOT use "not zero" assertion for timestamps!
- The process being tested MUST be close to real-world as possible.
  Avoid changing internals to simulate scenarios, i.e. call public methods instead of changing private fields.
- No vanity tests. Tests must validate real functionality/feature that user expects.
- Use testify's `assert` package for assertions.
- Define `input`, `expected`, etc. as named types inside the test function — never use inline anonymous structs in table-driven tests.

**String Testing**
- For multi-line string inputs and expected outputs, ALWAYS use backticks (raw string literals) with real line breaks. NEVER use `"" + ""` concatenation or `\n` escapes for multi-line strings.
- Assert exact string matches. Do NOT use `Contains`, `HasPrefix`, or other partial match shortcuts. If the full expected output is known, assert it exactly.

**Running Tests**
- For Markdown-only design changes, check the documents. Do not run Go suites. Use focused tests
  only when needed to verify a design assumption.
- After code changes, run full test suites over `./...`, including native, CGO-disabled,
  and race configurations. Targeted checks never replace a full suite.
- Run tests without `-v` and without piping to `grep`. Go test already prints full output for failing packages — no filtering needed.
- Use 300s timeout to accommodate ONNX embedder index builds in policy/search tests.
- Example: `go test ./... -count=1 -timeout 300s`
- Portable suite: `CGO_ENABLED=0 go test ./... -count=1 -timeout 300s`
- Race suite: `go test -race ./... -count=1 -timeout 300s`
- Repeat these suites in each module: root, `docs`, `examples`, `examples/chat`, and `scripts`.
  Nested module boundaries exclude teaching material and release tools from the library ZIP.
  Root `./...` does not test those modules. See `development/releasing.md` for the release gate.

**Release Verification**
- Before releasing a version, run all required tests, including live tests for configured providers.
- Load the configured API keys so live tests make real provider calls and use tokens or credits.
  Fixture tests and skipped live tests do not verify provider behavior.
- Investigate each failure and fix its root cause. Do not weaken assertions or skip failing tests
  to obtain a pass. Run the affected checks again, then complete the required full suites.
- Release only after all checks in the agreed release scope pass. Report missing configuration or
  unavailable providers before treating the release gate as complete.

**Testing Stats & Limits**
- Whenever we create new stats (`*ExecutionStats`), we need to create tests on:
  - Stat field is incremented correctly.
  - If consecutive, stat field is reset correctly.
  - New limit test at `agent/agent_executor_test.go` with mocks.
- Limit tests must cover:
  - Limit is exceeded in first iteration.
  - Limit is exceeded in Nth iteration.
- If the stat is based on prefix, then the limit test must:
  - Have multiple prefixed stats, e.g. `gent:tool_calls:tool1`, `gent:tool_calls:tool2`.
  - Test must verify that when both limits are reached, only the correct one triggers the limit error.
</testing_standards>

<standards_convention>
- MAX line length: 100 characters, DO NOT break line before 100 chars.
- NEVER swallow errors. Errors must be either returned, or logged.
- Use ctx for context.Context and execCtx for gent.ExecutionContext in code and examples. Use
  role-specific names when a scope has multiple contexts. Apply this rule across the repository
  when implementing the approved API changes.
</standards_convention>

<critical_rules>
**Never commit and push without asking permission!**
</critical_rules>
