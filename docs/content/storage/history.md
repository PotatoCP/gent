RunState keeps the compact logical state needed for execution. RunView reconstructs the complete
accepted archive. Compaction changes active selection, not archived originals.

## Reconstruct the archive

`saved` contains complete batches from the first batch through the desired accepted savepoint.

{{example:archive-replay}}

Read RunView's events, history, requests, responses, tool evidence, and accepted outcomes as
read-only values. EventSequenceNumber defines event order. Timestamps are for display and diagnosis.

Use `CodecRegistry.Marshal` and `Unmarshal` to store and load a reconstructed RunView. The loaded
view can accept later batches. Loading checks that its retained execution projection agrees with
the archive. This check compares saved logical values, including pointer-keyed metadata. Decoded
copies have separate mutable values; they do not need the same process addresses.

## Compare original and effective content

The archive retains original tasks, effective Hook inputs, native response evidence, rejected
candidates, corrections, processed output, and compaction changes. Source identities connect
accepted answers to their candidates. A trace is a display view of this evidence.

A compact RunStateSnapshot plus its tail cannot reconstruct earlier archive history that the
application no longer retains. Unsaved live output and provider reasoning that was not exposed are
outside the archive guarantee. Loading the archive must not run models, tools, or output validation.

See [Save batches](/storage/save-batches/) for accepted savepoints and
[Restore and Continue](/storage/restore/) for recovery. For related contracts, see
[Compaction](/run/compaction/) and [Ownership and concurrency](/operate/ownership/).
