Implement `Loop[T]` when ReAct does not match your workflow. Next performs an iteration. Output
defines the accepted type. Return LAContinue for more work or a prepared terminal result.

## Accept a local answer

This loop needs no model or tool call:

{{example:loop-custom}}

Process a complete candidate before AcceptOutput. A rejection needs useful correction content; your
loop decides how to use it on the next call. Unexpected errors propagate to the application.

## Give the custom loop to the Runner

{{example:loop-runner}}

Use the same NewExecutionContext and NewTurn calls as [Hello World](/start/hello-world/). This loop
returns `Ready.` through normal output acceptance and persistence.

## Add history

Within Next, propose an appended entry and a complete active selection:

{{example:loop-history}}

The draft revision detects stale proposals. Use NewHistoryEntry for stable identities. Do not change
accepted entries in place. Active selection is different from the complete archive.

## Use managed operations

Call `execCtx.GenerateContentStream` for model work. Supply a stable ModelCall identity when order
alone is not enough, such as concurrent calls. PrepareCalls proposes a tool plan; ExecuteNext runs
its managed next step. Direct Tool.Call or unrelated external writes bypass this recovery contract.

After restore, custom Next starts at its entry point on restored LoopData. The author owns safe
repeated local work and identities for new operations. Gent reuses accepted managed results and
recovery preparation for the same operation. It does not restore the Go stack or require a step
enum. Managed subagent loops are not available in this version.

## Call a model

Within Next, `messages` is your selected request input, including the effective system prefix:

{{example:loop-model}}

## Publish application progress

Within Next:

{{example:loop-event}}
