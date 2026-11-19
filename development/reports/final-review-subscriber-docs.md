# Final review: subscriber cursor and source-page icon

This change fixes F002 and F003 from the final multi-turn review. It does not change runtime
subscriber behavior or the accepted savepoint contract.

## Subscriber cursor at stop

D130 defines `SubscriberStop.LastAcceptedEventSequenceNumber` as the accepted run position when the
subscription stops. Execution can accept more work after that point. The prior test instead compared
that cursor with the final completed-turn state. A channel-controlled schedule reproduced the
failure in all three cases: the stop cursor was 3 and the completed-turn cursor was 7.

The test now holds the accepted head at the first loop call. `OnEvent` waits until that head is
ready, then returns its result when the callback deadline expires. The loop waits for `OnStop`
before it accepts the answer. This fixes the order through public callbacks, without a longer
timeout or a change to runtime internals.

The assertions still compare the complete stop result, including its exact joined callback error.
They also check the completed result and prove that the accepted head advances after the subscriber
stops. The cancellation-only case must have no callback error. Both joined-failure cases must retain
the unexpected cause.

## Icons on every HTML template

Normal documentation pages declared the shared icon. The separate source-page template did not.
Chrome therefore requested `/favicon.ico`, which the preview server redirected to a 404 page. The
source-page content loaded, but the browser console correctly reported the failed resource.

Normal pages and source pages now use the same icon declaration. The existing relative-link
conversion keeps the asset valid for local files, root hosting, nested hosting, and a moved output
folder.

The other separate template generates immediate redirects. These pages also need an icon
declaration. An external icon request can be canceled by their immediate navigation; a browser trace
recorded `net::ERR_ABORTED` on a redirect page. Redirect pages therefore embed the same small SVG.
They do not start a resource request that their own navigation can interrupt.

The build test checks all 235 generated HTML files, including source, redirect, and 404 pages,
before and after moving the output. It requires one icon with the correct type and content or asset
location. The browser test checks the source-page icon in each hosting mode and in the moved folder.
Console and failed-request checks remain enabled.

## Changed files

- [Subscriber regression test](../../internal/core/cancellation_failure_test.go).
- [Documentation builder](../../docs/build.mjs).
- [Build tests](../../docs/tests/build.test.mjs).
- [Browser tests](../../docs/tests/browser.mjs).

## Verification

All Go commands used `GOMAXPROCS=4` and `GOCACHE=/tmp/gent-multi-turn-go-cache`. No timeout was
increased.

| Check | Result |
| --- | --- |
| Original controlled subscriber schedule, race mode | Failed all three cases, as expected |
| Corrected subscriber test, race mode, 20 runs | Passed |
| New icon check with the original builder | Failed, as expected |
| `go test -p 1 ./... -count=1 -timeout 300s` | Passed, all 31 packages |
| `CGO_ENABLED=0 go test -p 1 ./... -count=1 -timeout 300s` | Passed, all 31 packages |
| `go test -race -p 1 ./... -count=1 -timeout 300s` | Passed, all 31 packages |
| Documentation unit tests | Passed, all six tests |
| Full Chrome browser suite | Passed: file, root, nested, and moved-folder hosting |
| Original source-page browser probe | Passed: one icon, successful requests, no console error |
| Documentation Go examples and coverage check | Passed: all 115 Go example regions have coverage |
| Changed-line style, `gofmt`, and `git diff --check` | Passed |

The full race suite took 461.7 seconds across packages. The model package passed in 93.9 seconds.
Each full Go suite covered the same 31 packages: 24 with tests and seven without tests. No race was
reported. All Go source hashes stayed unchanged during verification.

The browser suite used `CHROMIUM_PATH=/opt/google/chrome/chrome`. The original source-page probe now
records successful requests for `assets/favicon.svg`, with no fallback request for `/favicon.ico`.
The first sandboxed documentation check could not run its Go child process (`spawnSync go EPERM`).
The completed documentation checks ran with permission to start the extractor and local browser
servers.

The commands, results, original file copies, source hashes, and browser trace are in
`/tmp/gent-final-fixes-20261119`. The original reviewer probes remain in
`/tmp/gent-final-review-20261119`. The root Go suites include the earlier checkpoint regression
tests. No live provider calls were run.

Other work changed files under `examples/chat/web` and `examples/chat/tests` during these checks.
This change did not edit or verify those files. Their paths are recorded in the verification
manifest. The root Go module does not include that nested module. These results therefore do not
certify the updated chat UI. No files were staged, unstaged, stashed, committed, or pushed.
