A model adapter maps Gent's request to its provider and preserves the returned evidence. It receives
a Go context and a resolved request. The managed runtime owns call identities and saves.

## Prepare the request

This deterministic adapter is useful for tests:

{{example:custom-model-prepare}}

## Return a complete candidate

{{example:custom-model-response}}

`MessageId` connects chunks and complete content. When you return `ModelStatusAnswer` with
final-answer candidates, select the items for output in `CandidateSource.MessageIds`. For example,
if an earlier item was a draft, select only the final candidate. An empty selection in that
response is an adapter error. A response with only commentary has no answer candidate.

A provider tool request is a separate ordered part. Retain native completion, refusals, tool IDs,
phases, signatures, and continuation data. Only the loop can accept a candidate as a final answer.

## Complete a producer

Use this producer pattern inside GenerateContentStream instead of returning NewCompletedStream.
This small producer returns a completed response. A real adapter can send chunks before completion.

{{example:custom-model-stream}}

Response waits for producer completion. Complete must run after cleanup, including cancellation. Use
stable chunk payloads before reusing producer buffers. Do not leave an unjoined goroutine or an
unread response body behind when the caller closes the stream.

## Use the model in an agent

{{example:custom-model-runner}}

Create an execution context and call NewTurn as shown in [Hello World](/start/hello-world/). ReAct
uses the adapter through the managed model path and accepts `Ready.` through its output contract.
