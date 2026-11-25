# Release Gent

Release from a clean `main` commit that is already pushed. Keep the branch linear. Get the owner's
permission to commit, push, and publish before those actions.

```sh
# Preview only. This command does not change Git state or call providers.
bash scripts/release.sh v0.8.0 --notes /tmp/gent-release-notes.md

# Verify, tag, check the published packages, then create the GitHub release.
bash scripts/release.sh v0.8.0 --notes /tmp/gent-release-notes.md --execute
```

Choose the next version from the current tags. Public API changes can require a new minor version
while Gent remains below v1. Supply release notes that describe the new behavior and API changes.
There is no default version and no option to bypass checks during publication.

## Package boundaries

Docs and examples remain in the Git repository. They are not part of the Go module ZIP or GitHub
source archive. Root README and license files stay in the library package.

Two rules are necessary. `.gitattributes` applies `export-ignore` to `docs/` and `examples/` for Git
source archives. Separate `go.mod` files at those roots exclude the same trees from Go module ZIPs.
The chat application also has its own module. Release tools have a separate `scripts` module so
their dependencies do not enter the library module. Local `replace` directives point to the root.

Do not use `.gitattributes` alone to control the Go download. Do not move example assets into a
library package or remove their module boundaries. A root `go.work` is not necessary.

```sh
go -C scripts run ./releasecheck -root .. -out /tmp/gent-module.zip
```

This command passes the current tracked and non-ignored files to Go's module ZIP writer. It checks
that the input includes the teaching files and that the output excludes them. It also checks a real
Git archive with current attributes. No staging or commit is necessary. Publication checks the
GitHub source archive and the downloaded module too, so a local build cannot hide a packaging fault.

## Release checks

```sh
# Run the same checks without creating a release.
bash scripts/verify-release.sh
```

The gate runs native, CGO-disabled, and race suites in all five modules. It checks documentation
examples, site builds, npm advisories, browsers, package contents, and the CLI. Install Node
20 or later and Chromium. Use `CHROMIUM_PATH` for the docs browser and `CHROME_PATH` for the chat
browser to select a Chromium path. `RELEASE_REPORT_DIR` selects the log folder.

The root suites read the fixed persistence corpus in
[`internal/multiturntest/testdata/storage`](../internal/multiturntest/testdata/storage/README.md).
Keep every released corpus. Add files for a new storage representation or a changed run schema.
Verify old receipts, archive content, and continuation with the current writer before release.
An encoder and decoder built from the same revision cannot establish upgrade compatibility alone.

Go test modes run in sequence with one package at a time. Concurrent modes can make the large
recovery suites exceed their 300-second package deadline through CPU contention. The gate keeps
that deadline and all scenarios, collects each module's result, then reports failures. Each mode
has its own process environment so CGO-disabled settings cannot enter the race suite.

The gate reads `.env` as data and honors existing environment settings. It does not execute shell
expressions or print credentials. Fixture suites receive no live-provider settings. Each configured
provider then runs against its real endpoint. These calls use tokens or credits. xAI also runs the
legacy streaming checks and the airline and e-commerce scenarios.

Each live feature has its own 300-second test deadline. One shared package deadline can expire
before later features get their full request allowance. This does not change their assertions or
the shorter request and scenario timeouts.

Discovery includes `TestNativeLive` as well as the `TestLive` groups. `TestNativeLive` checks
programmatic tool calls and search with programmatic calls for each configured provider. A failed
group fails the gate. Fixture tests do not replace these calls to real providers.
The programmatic-call check requires a complete program that uses the lookup result for the read.
It also checks every actual inner call against saved evidence. A corrected script can follow a
partial script failure; the earlier calls remain visible and must still match their full results.
Direct calls, split programs without a complete program, and missing operations do not pass.

The chat browser also runs with the first configured supported provider: OpenAI, Anthropic,
Gemini, or xAI. Configure at least one of these providers. This verifies real streaming and recovery
through the server and UI, in addition to the deterministic browser tests.

Live browser checks compare complete displayed answers and tool outcomes with accepted archive
evidence. They check original prompts in later effective requests and compare both replay paths.
Exact model wording, tool arguments, and first-attempt tool success are not interface guarantees.
A local native-response fixture checks a handled tool failure followed by success and nonliteral
answers. The demo checks retain their exact expected values. Browser evidence is saved before the
memory-only server stops; `CHAT_TEST_OUTPUT_DIR` selects its output directory.

Missing provider configuration is reported. At least one live provider is required. A local provider
needs both a URL and a model; a URL alone is not a configured test. Vertex needs a project and valid
application credentials. Image generation is checked when its model is configured. State the actual
provider and image coverage in the release notes. Never describe skipped live checks as verified.

Fix a failed check before publication. The release script verifies that the checked commit and
worktree did not change, then creates an annotated tag. After the push, it downloads the module from
the Go proxy, checks its contents, installs the released CLI, and creates the GitHub release.
If a check fails after the tag is pushed, keep the tag and investigate. Do not rewrite a public tag.

## Interactive scenario coverage

The streaming chat server replaces the interactive integration CLI. The original airline and
billing Go scenarios remain in the gate, including SearchChain, compaction, and JavaScript tool
variants. The gate also runs `TestLiveChatScenarios` over HTTP/SSE for both tasks with every
configured chat provider: OpenAI, Anthropic, Gemini, and xAI. Run each task separately so it keeps
its 300-second test deadline. These calls verify the configured provider, streamed content, accepted
answers, and equivalent batch-only and RunStateSnapshot-plus-tail recovery. The demo browser gate
checks chat selection, reset during execution, stale replies, and the existing inspector flows.
