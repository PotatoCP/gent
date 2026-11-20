Small examples teach one Gent operation. Complete programs include the surrounding application
setup. Run commands from the stated directory with the same source version as these pages.

## Hello World

From the repository root, with the two environment variables from Setup:

```bash
go -C docs run ./examples/hello
```

This makes a real model call. The command's teaching code is on [Hello World](/start/hello-world/).

## Tools and search

The complete source files linked beside each code block compile as `docs/examples`. Their tests run
the addition tool, schemas, data returns, recovery inspection, search chains, scripts, and policy
retrieval with controlled inputs:

```bash
go -C docs test ./examples/... -count=1 -timeout 300s
```

## Saved conversations

The repository's `examples/history` command demonstrates accepted batches and restoration:

```bash
go -C examples run ./history
```

The documentation tests also compare full replay and RunStateSnapshot plus tail with fresh objects.

## Streaming chat

Conversation Lab is a separate example module under `examples/chat`. Its README contains setup and
provider settings. For its local mode, run the module from that directory. The application shows
reasoning, tools, media, accepted answers, Stop, Continue, and later turns in one run.

Its memory store holds encoded bytes but clears on process exit. It proves the interface flow; it is
not a durable deployment. This application is excluded from Gent release source archives.

## Custom implementations

The full source links on Custom loops, LoopData, Custom models, and Custom tool chains include all
methods and setup. Their tests check the public call flow. Keep those complete files as references;
do not copy unrelated test or server code into a small application example.
