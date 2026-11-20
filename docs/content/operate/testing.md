Test behavior through a real Runner. Use a deterministic Model and tools so the expected values are
known. The [custom model example](/extend/models/) supplies one such implementation.

## Run the example checks

This test runs a real Runner and checks the complete result:

{{example:test-agent}}

From the repository root:

```bash
go -C docs test ./examples/... -count=1 -timeout 300s
```

The checks execute the teaching code, including model request fixtures, tools, output validation,
turn continuation, byte replay, custom data, subscriptions, search, and resource cleanup. The site
build extracts each displayed Go region from those compiled files. Missing or duplicate regions fail
the build.

## Check recovery

Use a byte store and fresh objects. Compare full batch replay with RunStateSnapshot plus its
accepted tail at the same accepted position. Confirm that archive history remains complete and
accepted tool results and output processing are not repeated.

## Check failures

Cover expected pause and rejection, unexpected errors, failed saves, duplicate delivery, and
canceled callbacks. Check complete expected values. Native, CGO-disabled, and race suites cover
different runtime configurations. Nested example modules need their own commands.

## Check providers separately

A constructor or local HTTP fixture proves adapter behavior, not remote model support. Configure
live credentials and the intended model before a remote check. Do not treat a skipped check as a
pass. Unsaved model work can repeat after interruption; tests must not promise identical later text.
