# Agent Chat

A working streaming chat server that uses Gent's public interfaces. One RunId owns the complete
conversation. Each new task or Continue restores a fresh worker from encoded bytes. The Restore
action reads the completed result through a fresh worker. RunView and RunState inspection decode
saved bytes without a worker. The server does not keep idle ExecutionContext values as saved state.

This is a development example, not a hosted service. The memory store survives worker Release and
Restore inside one server process. A process restart clears every run. No database or disk storage
is used. The server binds to localhost by default and has no accounts or authentication.

## Run

From this directory:

```sh
go run .
```

The server reads `.env` from the Gent project root and lists the supported providers with configured
credentials. Enter a provider number or name. The server then starts at <http://127.0.0.1:8088>.
Credentials are not shown in the menu or sent to the browser. Exported environment settings take
precedence over the file, including empty values. The file is parsed as data, not executed as shell
code. No `source` command is needed.

To select a provider without a prompt:

```sh
go run . -provider openai
```

To run without provider credentials:

```sh
go run . -provider demo
```

The demo model uses the real managed stream, native tool, and output paths with deterministic
responses. It reads a customer record and recalls it on later turns. Select a remote provider to
work through the full flight-change or billing task. Its reasoning text is fixture data, clearly
identified by the demo provider label.
Demo mode does not read `.env`. Without a provider flag, the server always asks for a selection,
even when only one provider is configured. Missing credentials or closed input stop startup with
instructions; neither case selects a provider automatically.

The supported example selections are openai, anthropic, gemini, and xai. Each uses
`GENT_TEST_<PROVIDER>_MODEL` and `GENT_TEST_<PROVIDER>_KEY`. Use an uppercase provider name. These
settings match the framework's live tests. An empty model selects the same default: GPT-5.4
Mini, Claude Sonnet 4.6, Gemini 2.5 Flash, or Grok 4.3. The optional `_URL` setting overrides the
endpoint. Credentials never enter browser data. The example does not select an unconfigured remote
provider or download a local model automatically.

For remote providers, policy search uses the same local multilingual model as the integration CLI.
If it is not installed, run these commands once from the repository root:

```sh
go run ./cmd/gent setup onnx
go run ./cmd/gent model download multilingual-e5-small
```

The server shares one ONNX embedder and closes it after workers stop. Demo mode uses a deterministic
word-count embedder with no native model. Its search signal is lexical, not semantic. The prompts,
policies, tool schemas, and fixture data still come from the integration packages.

## Reusable data explorer

RunView and RunState each use a complete reusable inspector. **Chronological** is the default view.
Select a task, model call, or tool-call group to read its saved input and output. Tools requested
together share one chronological item. Expand each tool to read its input, execution result,
errors, and recovery evidence. The next model request shows the exact prepared tool replies.
Task details show the system prompt first, then the user messages. The prompt is labeled as the
first saved model request after Hooks. Model calls start with **New content**: exact repeated
messages collapse in place under **Already sent**, with their original positions. Open a range
to inspect it, or select **Full request** to show all messages. Changed messages stay visible in
full, including changes to metadata and native continuation. Response content shows commentary,
exposed reasoning, and tool requests. Call details, options, and source events remain available.
All disclosures share one indentation rule. Expanded content starts under its heading text, with
a guide line for nested levels. The smaller mobile indent keeps deep source data readable.

RunView supplies the full accepted chronology. RunState shows only retained recovery evidence;
its chronology can be empty after completion. Its Messages view still shows retained context.
Other views cover active messages, tools, accepted events, history changes, counters, custom data,
and continuation fields. **Encoded Saved State** and **Recovered State JSON** are tabs in that same
component. Each has an exact byte size, Copy, and Download. Exports keep the original bytes.

The component is in [web/explorer](web/explorer/README.md). Copy that directory and pass the JSON
string produced by Go `json.Marshal` on a recovered RunView or RunState, plus the optional encoded
JSON string for the same saved position. Gent's Go codec continues
to own decoding and validation of storage bytes. The component has no chat server dependency.
Open <http://127.0.0.1:8088/explorer/demo.html> to inspect a downloaded recovered JSON file locally.

## Shared scenarios

Choose a scenario on the empty chat page:

- **SkyWings Airlines** uses the airline fixture. John Smith wants to move flight AA100 from JFK to
  LAX to a later flight. Tools cover customers, bookings, flights, seats, policy search, changes,
  cancellations, and notifications.
- **TechGear Store** uses the e-commerce fixture. Alex Rivera reports two $79.99 charges for a
  Mighty
  Mouse purchase. Tools cover customers, paginated orders, payments, gateway checks, policy search,
  refunds, cases, and credit requests.

The starter button copies the integration scenario's full task into the composer. It does not send
it until you select Send. A conversation keeps its scenario after refresh, restore, and NewTurn.
Select **New chat** to choose another scenario.

Both the default CLI chat and one-shot integration tests use the same `NewChatConfig` functions in
[integrationtest/airline](../../integrationtest/airline/chat.go) and
[integrationtest/ecommerce](../../integrationtest/ecommerce/chat.go). The web server uses their tool
registration callbacks, prompts, critical rules, policy suggestions, and iteration allowances. It
uses a native registry with all scenario tools available. The integration CLI also retains its
search and JavaScript tool modes.

Each conversation owns one fixture. It represents an external service and stays alive across worker
restores. A new conversation gets separate booking and payment data. Agent execution still restores
from bytes on each attempt. Fixture data is not LoopData and does not enter the RunStateSnapshot.
A server restart clears both the simulated services and the byte store.

Try this sequence:

1. Choose a scenario, select its starter task, then send the task.
2. Follow the streamed commentary and exposed reasoning. Open **Ran tools**, then open a tool to
   read its input and result. The final answer stays separate from this progress text.
3. Ask: "What did I first ask you to do, and what did the earlier lookup find?"
4. Open the right inspector. Use both replay sources under **Recovery controls**. The saved answer
   remains unchanged. Inspect Events, RunView, and RunState without running the agent.
5. Submit another task and select **Stop** while it runs. Then use **Continue saved
   task**, or submit a new task to replace the unfinished one.
6. Select Continue after completion. Gent reports NoActiveTurn; GetLastTurnResult still exposes the
   last completed result without running the model or output processor again.

The output processor removes whitespace at the edges of accepted text. **Inspect response** opens
the original candidate, with its separate identity, in the event inspector. The chat replaces a
candidate only after Accepted delivery of its processed answer. The archive retains both values.
A provider's native continuation data stays available in the inspector. It is not readable
reasoning.

The conversation uses a flat text layout. Commentary and exposed reasoning stream as visible text.
Consecutive tool calls share a collapsed **Ran tools** group. Each tool has its own toggle for
input, result, and event inspection. The header is fixed; no model call generates a summary.
Open groups and tool details keep their state and keyboard focus as results arrive and previews
become accepted data. Text messages divide groups, so progress remains in its original order.
This display grouping does not claim that tools ran in parallel. The inspector groups tools by
their saved request relationships. Final answers keep their separate identity and actions.

Enter sends a message. Shift+Enter adds a line. You can prepare the next message during execution.
The conversation follows new output until you scroll up. **Latest message** returns to new output.
On small screens, **Inspector** opens the same technical view as a side panel.
Drag its left edge to change the width. When the edge has keyboard focus, use the left and right
arrow keys to resize it. Home selects the minimum width; End selects the maximum width. The browser
keeps your preferred width across refreshes and limits it to the available screen space.
Select **Full screen** to expand the inspector across the browser window. Wide layouts place the
chronology beside the selected record and the model request beside its response. **Exit full
screen** or Escape returns to the previous panel width. Keyboard focus stays inside the expanded
inspector. A second Escape closes the panel.

If a run no longer exists, the frontend returns to `/` and clears the run ID and old display state.
It keeps any draft message so you can submit it as a new conversation. A restore error keeps the run
URL: missing history and a failure to read existing history require different actions.

The right inspector has three tabs:

- **Events** shows separate Live and Accepted deliveries. Search by event name, EventSequenceNumber,
  turn, or attempt. Page through all entries and select any event to inspect its full encoded
  RecordedEvent.
- **RunView** loads the complete accepted archive, including original prompts, model responses,
  tool records, and history changes. It cannot use a compact RunStateSnapshot alone.
- **RunState** loads execution state and encodes it as a RunStateSnapshot. The selected replay
  source controls whether this uses all batches or the saved capture plus its accepted tail.

Data inspection shows the accepted head for that read. It never reads a live worker's mutable
state. Refresh loads a newer head. RunView and RunState each show two sections at that same head:

- **Encoded saved state** uses Gent's codec, including native type information and, for a
  RunStateSnapshot, integrity data. This represents the recovered value after tail replay, not the
  sum of the stored batch bytes. Copy and Download preserve these exact encoded bytes.
- **Recovered state · JSON** uses Go's `json.Marshal` on the recovered RunView or RunState. This
  diagnostic form exposes the recovered fields without codec wrappers around the whole value.
  Fields whose declared type is EncodedValue still keep that type's representation. This JSON is
  for inspection; use the encoded section for recovery.

Each section shows the UTF-8 byte length of its compact JSON document, before HTTP encoding. This
is a serialized size, not a Go heap measurement. Reformatting a RunStateSnapshot can invalidate its
digest. The recovered JSON has separate Copy and Download controls.
The JSON tree expands on demand and pages large containers. These controls inspect data; they do not
execute models, tools, or custom restore callbacks. See the
[codec size review](../../development/reports/run-state-codec-size.md) for measurements of the
implemented type tables, layouts, and shared values. The codec uses no compression.

## Interface flow

| File | Responsibility |
| --- | --- |
| [agent.go](agent.go) | ReAct, output transformation, and deterministic model |
| [scenarios.go](scenarios.go) | Shared scenario setup and fixture ownership |
| [embedder.go](embedder.go) | Shared ONNX model for policy search and local demo alternative |
| [store.go](store.go) | Encoded batches, captures, atomic cursor and retry checks |
| [runtime.go](runtime.go) | Config, NewTurn, Restore, Continue, GetLastTurnResult, Stop routing |
| [events.go](events.go) | Typed public payload projection and bounded browser delivery |
| [http.go](http.go) | HTTP actions and server-sent events |
| [inspect.go](inspect.go) | Read-only archive and RunStateSnapshot inspection |
| [web/state.js](web/state.js) | Accepted/live reconciliation and complete-message replacement |
| [web/chat.js](web/chat.js) | Visible progress text, nested tool toggles, and accepted answers |
| [web/inspector.js](web/inspector.js) | Event delivery records and saved data inspection |
| [web/resize.js](web/resize.js) | Pointer and keyboard control of inspector width |
| [web/request.js](web/request.js) | Expected missing-run responses and HTTP failures |

Recorder.Save encodes the complete borrowed SaveBatch and sometimes calls its lazy capture
callback. One locked transaction checks the worker lease and expected cursor, stores immutable
bytes, records the exact retry receipt, and accepts the matching optional RunStateSnapshot. Every
third pre-terminal batch can capture state. The terminal tail remains visible in the UI to show
that restoration loads the capture and still applies later accepted batches.

Storage reads freeze a list of immutable byte slices at a fixed head. Gent's codecs decode them.
RunState.Apply restores execution, while RunView.Apply reconstructs the full accepted archive.
The example does not read private Gent fields or parse its encoded format. Typed expected replay
and restore results are checked before the server continues.

Two normal Subscriber registrations receive Live and Accepted evidence. The browser deduplicates
accepted events by RunId and EventSequenceNumber, and previews by RunId, AttemptId, and
EventSequenceNumber.
It advances its accepted cursor only through consecutive accepted events. Complete native messages
replace their own deltas. Accepted output has a separate identity and never overwrites its source.
Source identities select which candidates the chat hides after acceptance. The inspector keeps
those candidates available. Technical model progress stays in the inspector.

An event connection subscribes before it reads the fixed archive head. Buffered overlap is safe;
there is no missing interval between reading storage and subscribing. Each browser has a bounded
queue. A slow consumer disconnects and reloads accepted bytes. That failure cannot reject a saved
batch or stop the agent. Current previews use a bounded cache; if it omits earlier preview text,
the UI says so. Accepted evidence remains complete in storage. Event pages limit DOM work, not the
records available for inspection. The browser keeps received preview records across reconnects in
that page, even when recovery discards them from the chat. A page reload loses that local delivery
record; it reloads the complete accepted archive and available current previews. Repeated deliveries
increase a receipt count. They do not duplicate chat text.

Stop routing checks RunId, TurnId, and AttemptId against the current worker under the application's
admission lock. A stale browser command returns HTTP 409. Only the worker's running method owns
cleanup and acceptance of the pause. Each action releases that worker before removing it from the
active map. NewTurn on idle unfinished state uses Gent's normal replacement contract. Live steering
is outside this example's scope.

The Hooks callback sets the effective model token allowance and adds local policy guidance to the
first three main model requests of each attempt. It derives search text from restored conversation
messages. Guidance changes the effective request; it does not enter saved user input. Tool errors
and model failures retain Gent's normal contracts. The chat uses safe tool
replies.
The local technical inspector exposes complete encoded records, including saved diagnostics and
native evidence. Server operation failures also go to the server log. Credentials do not enter these
records. The shared fixtures simulate external actions; they do not contact an airline or payment
service. Accepted tool results are reused by managed recovery. These existing fixture tools have no
external receipt lookup for interrupted actions. Gent reports an unresolved recovery as safe tool
guidance; it must not invent a receipt or automatically repeat the old action. Real service tools
need their own receipt lookup and `Tool.Recover` implementation.

## Verify

```sh
go test ./... -count=1 -timeout 300s
CGO_ENABLED=0 go test ./... -count=1 -timeout 300s
go test -race ./... -count=1 -timeout 300s
node --test tests/*.test.mjs
```

Browser checks reuse the repository's Playwright installation:

```sh
npm --prefix ../../docs ci
node tests/browser.mjs
```

The browser script uses `/opt/google/chrome/chrome` by default. Set CHROME_PATH for another Chromium
binary. It verifies both scenarios, shared starter tasks, structured tool results, streaming,
grouped tools with keyboard toggles and focus retention, separate progress and final answers,
later turns, refresh, both replay paths, original
response inspection, event pagination, encoded and recovered JSON downloads, byte sizes, panel
resizing, missing-run redirection with a retained draft,
Stop/Continue/replacement, completed-result inspection, safe text rendering, mobile layout, browser
errors, and memory-store lifetime. Screenshots go into the ignored screenshots directory.

Exercise the same browser flow against a real model configured in the project `.env`:

```sh
CHAT_TEST_PROVIDER=openai node tests/browser.mjs
```

Remote checks make paid model calls. Deterministic tests cover failures and interruption; the live
flow proves that the configured endpoint accepts retained conversation input and native evidence.

## Release exclusion

This directory is a separate Go module. Gent's module ZIP rules omit it, including its application
assets and dependencies. The root .gitattributes also excludes it from Git source archives. Do not
copy this application into the root module or embed its assets in a library package.

Verify the real module payload from current source without staging or committing:

```sh
go -C ../../scripts run ./releasecheck -root .. -out /tmp/gent-module.zip
```

The verifier supplies tracked and non-ignored source files, including this example, to Go's module
ZIP writer. It checks the produced ZIP and Git source archive. It rejects all docs and examples,
node_modules, and .env credentials. This checks the payload, not only the package build list.

Local fonts use the licenses included in [web/fonts](web/fonts).
