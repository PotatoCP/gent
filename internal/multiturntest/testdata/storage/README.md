# Persistence compatibility fixtures

Keep the original fixture bytes when the writer, run schema, component codecs, or receipt algorithm
changes. Add a new corpus for each new representation. The current reader and Runner must pass all
historical cases. Generating the input with the current writer would hide a matching change on
both sides.

## V3 baseline

`v3.zip` contains exact v3 storage documents captured on 2026-11-25 with run schema 7. The ZIP
only bundles the fixtures. Gent receives each member's original bytes. Each line of
`batches.jsonl` is one complete Recorder.Save document. The separating newline is outside it.
The first three batches used plain tables. Subsequent batches used the default Snappy policy.

A ReAct Runner used the savepoint matrix model and managed tool, custom LoopData, and compaction.
Its task included a system instruction, an imported assistant message with binary and native
content, and a user prompt. The first managed write completed externally just before interruption.
A fresh Runner recovered it, compacted history, paused, and later finished the same turn.

| Member | Accepted position |
| --- | --- |
| `batches.jsonl` | All 11 batches, events 1 through 27 |
| `tool-ready.json` | RunStateSnapshot at event 8, before the first external write |
| `paused.json` | RunStateSnapshot at event 17, after recovery and compaction |
| `archive.json` | Complete RunView through event 17 |
| `finished.json` | RunStateSnapshot at event 27, with accepted answer `Done` |

The tests load these bytes with fresh registries, verify historical receipts and recovery behavior,
and execute with current code. Check the saved logical state and effects. A future writer can use
different output bytes. To inspect the files, use `unzip -l v3.zip` and `unzip -p v3.zip NAME`.
