SearchIndex supports Search, Add, Remove, and Swap. Adding an existing ID replaces that document.
Swap replaces the complete catalog atomically. Each adapter owns the document-to-index mapping.

## Search keywords

This example uses the existing policy document adapter:

{{example:search-keywords}}

## Search vectors

Use an existing embedder whose owner controls its lifetime:

{{example:search-vector}}

The chunk adapter splits documents within the embedder's token allowance. Search results retain
source snippets. Vectors are not a replacement for original readable document content.

## Adapt your document type

Implement ChunkAdapter for your own documents. This adapter uses the model's token counter when it
splits Markdown or plain text:

{{example:search-adapter}}

Give the adapter and an existing embedder to FlatIndex:

{{example:search-article}}

The result contains the document ID, score, and matching text. The application owns the shared
embedder. Close it after all indexes and queries finish.

## Combine results

`first` and `second` are indexes of the same document type:

{{example:search-fused}}

Add documents to `index`, then call `index.Search(ctx, query, topK)` as in the earlier examples.
The fused index applies writes to its source indexes and combines their search results.

Set weights and normalization to match the score semantics of the sources. Top-K controls result
count. The search API also provides knee truncation and source-specific confidence settings.

## Supply another index

Implement `SearchIndex[Doc]` for another store. Implement ChunkAdapter for vector preparation or
BleveAdapter for keyword fields and queries. A custom Fuser combines named source results. Keep
concurrent updates and reads safe. Close owned resources after their users finish.

## Replace the catalog

Use the existing `ctx` and `index`:

{{example:search-update}}
