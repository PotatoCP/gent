# Codec table size experiment

This is a design measurement, not a Gent storage codec. It preserves existing JSON bytes through
local type/declaration strings, record layouts, and repeated-value references. See the
[size report](../../reports/run-state-codec-size.md) for results and recovery constraints.

Use an exact **Encoded saved state** download from the chat inspector. Run the script on a copy in
a temporary directory. It writes experimental `.stage1.json`, `.stage2.json`, and `.stage3.json`
files next to that copy. It also writes an `.expanded.json` file and checks exact byte equality.

```sh
python3 development/experiments/codec-tables/measure.py /tmp/run-state.json /tmp/run-view.json
```

The script prints JSON size records to standard output. It checks the input's JSON spelling before
measuring. It preserves numeric tokens without conversion to floating point. The greedy sharing
pass can be slow on a large archive. It is not a runtime implementation or performance benchmark.
Do not feed its experimental output to Gent's current decoder.

The final production design still needs the full recovery, corruption, ownership, custom-codec,
native-data, and resource-limit tests. A sample's exact round trip is not that complete proof.
