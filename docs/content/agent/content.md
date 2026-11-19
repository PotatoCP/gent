`ContentPart` values carry message input and tool results. Order matters. Keep structured data and
media as parts instead of flattening them into text.

## Combine text and data

{{example:content-parts}}

`TextPart` keeps exact authored text. JSON-looking text remains text. `Data` captures a Go value and
reports construction errors through `Build`.

## Supply media bytes

Use the preceding `parts` and valid file bytes named `png`, `pdf`, `audio`, and `video`:

{{example:content-media}}

Gent saves supplied bytes for recovery. Media helpers do not transcode files. Provider support is
checked when preparing delivery. Unsupported media follows configured guidance.

## Supply an image URL

This uses an example URL. Replace it with an accessible image before making a provider call.

{{example:content-url}}

Use bytes when the exact file must remain available after URL expiry or a content change.

## Request cache handling

{{example:content-cache}}

A stable leading request can help cache reuse, but provider rules control the result. Compaction and
instruction replacement can change that request. Cache configuration does not alter archive duties.
