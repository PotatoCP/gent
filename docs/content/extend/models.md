A model adapter maps Gent's request to its provider and preserves the returned evidence. It receives
a Go context and a resolved request. The managed runtime owns call identities and saves.

## Prepare the request

This deterministic adapter is useful for tests:

{{example:custom-model-prepare}}

## Return a complete candidate

{{example:custom-model-response}}

MessageId connects chunks and complete content. A provider tool request is a separate ordered part.
Retain native completion, refusals, tool IDs, phases, signatures, and continuation data. Do not
label a model candidate as an accepted final answer.

## Complete a producer

This small producer returns a completed response. A real adapter can send chunks before completion.

{{example:custom-model-stream}}

Response waits for producer completion. Complete must run after cleanup, including cancellation. Use
stable chunk payloads before reusing producer buffers. Do not leave an unjoined goroutine or an
unread response body behind when the caller closes the stream.
