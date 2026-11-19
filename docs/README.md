# Gent documentation site

This directory contains the public documentation site. Start with Hello World, then add features
through short examples. The site replaces the former Quick Start and interface-review pages.

## Build and open

Run these commands from the repository root with Node.js 20 or later and the Go version in `go.mod`:

```bash
npm ci --prefix docs
npm --prefix docs run build
```

Open `docs/dist/index.html` in a browser. Styles, fonts, navigation, copying, and search work with
local files. Relative links name each `index.html` explicitly. Search loads a local script
instead of using fetch, which browsers restrict for local files.

For an HTTP preview:

```bash
npm --prefix docs run serve
```

Open <http://127.0.0.1:4173>. The preview server binds to localhost. The published site only needs a
static file host. Node.js and Go are used for the build and local checks.

`docs/dist` contains the complete static site. The build uses no CDN, remote model, or hosted search
service. Fonts and their licenses are in `assets`. Build output, dependencies, and screenshots are
ignored by Git.

## Verify

The example package uses real public Runner calls with deterministic models and local HTTP fixtures.
It checks typed outputs, tools, scripts, events, custom data, and both replay paths. The native
embedding check uses the repository's small ONNX fixture. A portable build checks the explicit
unavailable result.

```bash
go test ./... -count=1 -timeout 300s
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s
go test -race ./... -count=1 -timeout 300s
go test ./docs/examples/... -count=1 -timeout 300s -coverprofile=/tmp/gent-examples.cover
npm --prefix docs run check:examples -- /tmp/gent-examples.cover
npm --prefix docs test
cd docs
npx playwright install chromium
npm run browser
```

Browser checks require Node.js 20 or later and a local listening socket. `CHROMIUM_PATH` can select
an existing Chromium executable. The check opens every page through local files and HTTP. It also
tests a nested host path and a moved folder with spaces. It checks styles, fonts, exact copied code,
search, mobile navigation, and browser errors. Screenshots are saved in `docs/screenshots`.

Conversation Lab is a separate Go module. Run its Go and browser checks from `examples/chat` as its
README specifies. Provider construction and fixture tests do not prove current remote model support.
Use the configured live checks before a release that makes provider capability claims.

## Publish static files

To select an output directory:

```bash
npm --prefix docs run build -- --output /tmp/gent-site
```

Publish that directory at the website root or a path such as `/gent/`. The same files work at either
location; no base-path setting or rebuild is needed. Configure the host to redirect missing routes
to `404.html` in the published directory. Select your host separately. The build does not publish
automatically. Only a marked build directory can be replaced.

## Maintain one source

- `site.json` defines page titles, descriptions, and navigation order.
- `content/` holds page explanations in ASD-STE100 Simplified Technical English.
- `examples/` holds complete Go source and the JavaScript script used by the code blocks.
- `tools/api.go` reads exported Go declarations and resolves public aliases.
- `build.mjs` creates pages, the search index, the API reference, and exact source links.
- `serve.mjs` supplies the optional local HTTP preview.
- `tests/` checks links, copied code, routes, browser behavior, and build safety.

Add a named `docs:start` / `docs:end` region to tested source. Insert `{{example:name}}` in exactly
one teaching page. The builder rejects missing, duplicate, or unused regions. It removes only common
indentation and outer blank lines. The page check compares all displayed code with these regions.
Imports and function wrappers remain in the complete source link.

Code colors are set in `assets/site.css`. The builder extends the Go grammar to mark calls and
exported names in short fragments. This gives them distinct colors without a browser highlighter
or a complete Go file. Highlighting changes HTML only; displayed and copied code must match the
source. The print palette stays readable when the browser omits background colors.

Code uses Consolas when it is installed, then Inconsolata, Cascadia Mono, or Cascadia Code. The
bundled IBM Plex Mono font is the fallback. This order needs no external font service.

Explain required inputs before a fragment. Explain its result and relevant contract after it. Keep
comments for ownership, guarantees, timing, and failure behavior. Do not repeat application setup on
each page. Keep one canonical example for each behavior and link related pages to it.

The API reference and its source pages come from the same checkout as the examples. The header marks
the site as development documentation. Internal review and implementation reports belong in
`development/`, outside the published site.
