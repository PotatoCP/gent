Use Live events for previews and accepted history as the durable basis. Keep provider reasoning,
opaque native data, candidate answers, and accepted answers distinct.

## Identify delivery

{{example:stream-keys}}

An interrupted attempt can reuse unsaved event positions. Never use EventSequenceNumber alone as the
identity of a Live preview. Accepted delivery can confirm a preview; confirmation is not a second
display item.

## Read a model chunk

{{example:stream-payload}}

Reasoning is only what the provider exposes. Do not infer hidden reasoning from opaque continuation
items. Tool events contain call identities that connect requests and results.

## Reconnect in order

1. Start buffering new delivery.
2. Load accepted history to a fixed accepted head.
3. Replace the accepted display with that history.
4. Discard buffered events already covered by the head.
5. Apply remaining events in order. Detect gaps and load again when needed.
6. Drop previews from an interrupted attempt when accepted state replaces them.

A live preview is not a trusted replay cursor. A failed callback or a slow connection can cause
repeated delivery. Use the keys above to prevent duplicate text or tool rows.

## Publish the accepted answer

`model_stream_chunk` is provisional provider output. `accepted_answer` carries the complete saved
answer, with content classification `final_answer`. Use SourceMessageIds to replace its candidate
previews. Do not append both as separate answers. The accepted output can differ from the original
candidate after processing.

The [chat example](/examples/#streaming-chat) implements the complete transport and browser flow.
Its tests cover reconnect, duplicate delivery, changed attempts, and accepted-answer replacement.
