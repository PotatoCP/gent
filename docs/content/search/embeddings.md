Local embeddings are optional. Basic agent execution and provider calls do not need ONNX libraries.
Native embedding support needs CGO, the tokenizer library, and a compatible ONNX Runtime library.

## Prepare the runtime

From the checkout, use the CLI to inspect supported model configurations:

```bash
go run ./cmd/gent model list
```

The CLI also provides `setup onnx` and `model download <name>`. These commands install libraries or
download model files. Read the command output for the exact paths and configuration. A CGO-disabled
build reports local ONNX support as unavailable instead of silently using another embedder.

## Create the embedder

`config` is the selected `common.ModelConfig`. `modelPath` and `tokenizerPath` point to its local
files.

{{example:embedder-create}}

## Embed text and queries

{{example:embedder-use}}

The caller must close the embedder after all queries and indexes have stopped using it.
MaxConcurrency bounds active inference. NumThreads sets the ONNX session's thread use. TokenCounter
and MarkdownChunker support token-aware document chunks.

## Use another embedding service

Implement Embedder's query, text, batch, dimension, token-count, and Close methods. Implementations
must support concurrent calls. Query preparation can differ from document preparation. The tests use
a deterministic embedder for ranking contracts and configured local fixtures for ONNX.
