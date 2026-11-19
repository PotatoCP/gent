# Model package timeout: repeated saved JSON decoding

The model package used too much CPU time to decode nested saved values. Each nested `json.Unmarshal`
call scanned the remaining JSON and copied it into `json.RawMessage` again. A large saved prompt
therefore cost more to decode at each level of its containing state. The race detector increased
the cost of these repeated reads and copies.

The fix removes this repeated work in the shared history codec. It does not increase test timeouts,
remove recovery checks, or change the public API or saved format.

## Evidence

The earlier full race run reached the 300-second package timeout while a newly started test encoded
a batch. That stack identified the work in progress, but did not explain the accumulated package
time. A passing repeat was not sufficient evidence of a fix.

A native CPU profile of the complete model package attributed 43.74% of sampled CPU time to
`encoding/json.Unmarshal`. Its allocation profile attributed about 814 MiB to
`json.RawMessage.UnmarshalJSON`. The fixture tests save, decode, and replay batches and capture
RunStateSnapshots. They therefore expose the cost of the shared serialization path.

The slowest profiled test was `TestProvidersUnfinishedResume`. The same test was then measured with
the race detector before and after the code change:

| Measurement | Before | After |
| --- | ---: | ---: |
| Test duration with CPU and allocation profiles | 105.978 s | 35.252 s |
| Total sampled allocated memory | 1,879 MiB | 1,623 MiB |
| Sampled memory allocated by `RawMessage.UnmarshalJSON` | 353 MiB | 42 MiB |

The profiles show repeated local decode work. They do not show a provider network stall or a
deadlock. Timing and sampled allocation values depend on the host; they are measurements, not
performance guarantees.

An isolated benchmark holds the saved prompt size constant and changes the depth of its typed
application state. The table gives medians from three samples:

| State depth | Decode before | Decode after | Bytes per decode before | Bytes per decode after |
| --- | ---: | ---: | ---: | ---: |
| 1 | 7.44 ms | 1.71 ms | 852,303 | 212,979 |
| 8 | 21.51 ms | 1.78 ms | 2,371,361 | 231,969 |
| 24 | 60.51 ms | 1.98 ms | 5,997,827 | 282,975 |

The original decoder was tested through a Go overlay of the saved source files. The benchmark,
input, test assertions, and runtime settings stayed the same. This comparison shows that the fix
removes repeated copies of the prompt as state depth increases. Small metadata allocations remain;
the fix reduces allocated bytes, not every allocation count.

## Code change

The [saved JSON reader](../../internal/core/history_codec_json.go) first uses `json.Valid` to check
the complete document. It then records object and array boundaries once. Typed readers use these
boundaries to read nested values without parsing and copying their descendants at each level.

The index borrows input only during decoding. Decoded values own their retained data. Custom codecs
still receive an independent buffer because their contract permits them to retain or change it.
Framework codecs share the same index. This keeps the change inside the existing codec interfaces.

Plain metadata strings and integers use small conversion functions. Escaped strings, invalid UTF-8,
null values, and invalid kinds retain standard-library decoding behavior. This avoids a full JSON
decoder allocation for each small metadata field. The first index implementation reduced copies
but increased other allocations in the recovery test; this measured issue was fixed before final
verification.

Type, version, depth, size, and saved-state checks remain in place. Complete syntax validation still
precedes custom codec calls. Unknown stream fields are still rejected. Duplicate fields, Unicode
case matching, null metadata, and custom errors retain their existing contracts.

## Regression checks

The [codec tests](../../internal/core/history_codec_json_test.go) cover:

- Exact custom JSON bytes and independent buffer ownership.
- Nested arrays and maps, escaped brackets, field order, and large integer values.
- Duplicate scalar, array, and object fields, including stream content.
- Unicode field names, escaped metadata strings, invalid UTF-8, and null metadata.
- Arbitrary valid nested custom JSON compared with `encoding/json` through a fuzz test.
- Increasing saved-state depth through the decode benchmark above.

The new contract tests also pass against the original decoder through the source overlay. The final
30-second fuzz check completed 676,295 executions. Invalid JSON inputs are filtered; valid inputs
are compared with the standard decoder. The existing codec and replay tests also passed.

## Full verification

All final checks passed on 18 November 2026. Each root suite covered all 31 packages listed by
`go list ./...`, including packages with no test files.

| Suite | Result | Model package duration |
| --- | --- | ---: |
| Full root native | Pass | 10.346 s |
| Full root CGO-disabled | Pass | 12.742 s |
| Full root race | Pass | 82.128 s |
| Chat module native | Pass | Not applicable |
| Chat module CGO-disabled | Pass | Not applicable |
| Chat module race | Pass | Not applicable |

The full root race command exited with code 0 in 371.654 seconds. The 300-second timeout applies
per package. This is one successful complete invocation, not combined results from a failed full
run and a package retry. No timeout or assertion was relaxed. No data race was reported.

Each full root suite uses `GOMAXPROCS=4`, a temporary build cache, and these commands:

```sh
go test ./... -count=1 -timeout 300s -p 1
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s -p 1
go test -race ./... -count=1 -timeout 300s -p 1
```

The separate `examples/chat` module uses the same three commands. Tests use fixtures and local HTTP
servers. These checks do not load provider credentials or make paid provider calls.

Go source hashes stayed unchanged across final verification. Formatting, changed-line whitespace,
the 100-character line limit, and report links passed. Earlier profiles and benchmark logs remain
in `/tmp/gent-model-timeout` for this workspace session.
