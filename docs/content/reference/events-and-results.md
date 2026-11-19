Use named types and constants when branching. Do not infer framework state from text in a model
answer, an error message, or an event timestamp.

## Execution results

| Status | Meaning |
| --- | --- |
| Finished | The turn completed; inspect TerminationReason |
| Paused | The accepted task remains unfinished |
| Busy | Another attempt owns the context; no task was admitted |
| NoActiveTurn | Continue has no unfinished task |

## Tool and recovery results

| Result | Meaning |
| --- | --- |
| ToolExecuted | A normal domain response; not a business-success flag |
| ToolBlocked | A required condition prevented the action |
| ToolInvalidArguments | Input failed a domain or schema rule |
| ToolRecoveryCompleted | Inspection found the completed result |
| ToolRecoveryNotStarted | Evidence proves the original action did not start |
| ToolRecoveryUnresolved | Inspection cannot establish the outcome |

## Content and delivery

AssistantContent classifies unspecified content, commentary, exposed reasoning, intermediary text,
final-answer candidates, and accepted final answers. `accepted_answer` publishes complete saved
output. `model_stream_chunk` carries provisional provider progress.

RecordedEvent includes run, turn, attempt, context, model-call, and tool-call identities where
applicable. EventSequenceNumber determines order. Application event kinds remain open, so consumers
must not assume every event has a built-in payload type.

## Counters and gauges

Counter keys use the named `StatKey` constants. GetCounter is cumulative. GetTurnCounter subtracts
the accepted baseline. Gauges measure current-turn conditions. See the [gent
API](/reference/api/gent/) for all keys, termination reasons, delivery stops, save phases, and
replay/load outcomes.

The [Subscribers](/observe/subscribers/) and [Configuration](/run/configuration/) pages show use.

## Common payloads

| Payload | Use |
| --- | --- |
| BeforeExecutionEvent, AfterExecutionEvent | Attempt lifecycle |
| BeforeIterationEvent, AfterIterationEvent | Loop iteration boundaries |
| BeforeModelCallEvent, AfterModelCallEvent | Effective request and model response |
| BeforeToolCallEvent, AfterToolCallEvent | Effective tool input and managed result |
| StreamChunk | Provisional ordered content |
| StateChangeEvent, HistoryChange | Appended history and selected context |
| LimitExceededEvent | The matched key, usage, and allowance |
| CommonEvent | Application-defined activity |

Check RecordedEvent.Type and the concrete payload type. Structural markers can have no payload.
See the [typed chunk example](/observe/streaming/#read-a-model-chunk) for a checked type assertion.

## Termination reasons

{{values:TerminationReason}}

## Save phases

{{values:ExecutionPhase}}

## Replay and load states

{{values:ApplyStateStatus}}

{{values:RunStateLoadStatus}}

{{values:RestoreStatus}}

## Delivery stops

{{values:SubscriberStopReason}}

## Built-in statistics keys

These declarations include counters, gauges, and prefixes. Their comments state the measurement.
Applications can add their own keys; do not treat this list as a closed set.

{{values:StatKey}}
