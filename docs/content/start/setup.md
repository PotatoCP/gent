Gent is a Go library. Your application owns its process, model credentials, and storage. These pages
describe the development checkout, which requires Go 1.24.10 or later. Use matching source until a
release contains these APIs.

## Use the current checkout

Run the complete example from the repository root:

```bash
export OPENAI_API_KEY='your-api-key'
export GENT_MODEL='your-model-id'
go run ./docs/examples/hello
```

Replace the example values with your own settings. Do not put real keys in source files. The command
makes a real provider call and can incur a charge. The test suite uses local fixtures.

## Use Gent in another project

From your application's Go module, point Gent at this checkout:

```bash
go mod edit -replace=github.com/rickchristie/gent=/absolute/path/to/gent
go get github.com/rickchristie/gent
```

Replace the path with your checkout path. When you use a published version, remove the local
replacement and select that version. Check the version shown in these pages before copying code.

## Imports

The first example uses these imports. Its complete source includes a function and a command entry
point. The teaching example omits those wrappers so the Gent calls are easy to see.

{{example:imports}}

No database, web server, or native embedding library is required. Go contexts control cancellation.
See [Local embeddings](/search/embeddings/) for optional native search dependencies.
