# Run data explorer

Copy this directory into your web application. It uses browser modules and scoped CSS. No build
step, framework, server route, or chat state is required. Open `demo.html` through an HTTP server
to try a recovered JSON file by itself.

## Input

The component accepts **serialized diagnostic JSON of a recovered Gent value**. It reads Gent's
RunView and RunState field layouts directly. No application-specific projection is required.

Keep the storage decoder in Go. Load bytes with `CodecRegistry.UnmarshalRunState` or
`CodecRegistry.Unmarshal`, check the result, then use `json.Marshal` on the recovered value.
Pass that JSON string to the component. This preserves Gent's type registry, custom codecs, and
recovery validation in one place. The browser does not implement a second Go codec or validate
recovery data. Diagnostic JSON must not be used as a replacement save format.

```html
<link rel="stylesheet" href="/explorer/style.css">
<div id="saved-run"></div>
```

```js
import {createRunExplorer} from '/explorer/index.js';

const explorer = createRunExplorer(document.querySelector('#saved-run'), {
  kind: 'state', // Use 'view' for RunView.
  json: recoveredJSON, // The exact string from Go json.Marshal.
  encoded: encodedJSON, // Optional exact CodecRegistry bytes, as a JSON string.
});

// Replace the data at a later accepted savepoint. No worker is started.
explorer.update({kind: 'state', json: nextRecoveredJSON, encoded: nextEncodedJSON});

// Remove this instance when the application removes its host.
explorer.destroy();
```

For the chat server's existing `/api/inspect` response, pass `{kind: result.kind,
json: result.decoded, encoded: result.encoded}`. Each RunView or RunState instance owns its tabs,
raw JSON trees, byte sizes, Copy, and Download. The same component supports both kinds; the host
does not need separate raw-data panels or export handlers. Supply both strings from the same
accepted position. The component does not need the server's URL or a live ExecutionContext.

The encoded string is optional when only recovered diagnostic JSON is available. Its tab then
states that no encoded data was supplied. The standalone page accepts a second file for encoded
bytes. Loading a new recovered file clears the previous encoded file to prevent a stale pair.

The optional third argument accepts `onSelect({kind, path, value})`. `path` is a JSON Pointer into
the diagnostic document. `value` is read-only data owned by this instance. Use the callback to
connect a separate source panel. Do not change the value. The built-in Source fields disclosure
already shows the selected record, including fields that have no special renderer.

## Exploration

- **Chronological** is the default view. Each point is a saved task, model call, or tool-call group.
  Select a point to read its input and output. Task points show the original user messages and
  the system messages from that task's first saved model request. Model points show complete
  effective messages, call options, response choices, commentary, exposed reasoning, and tool
  requests. Tools requested together share one point, whether execution was sequential or
  concurrent. Each tool has its own accordion for arguments, effective input, result, errors,
  recovery, and source events. More than one accordion can stay open for comparison.
- **Messages** shows instructions and saved messages. Switch to Active selection to read them in
  their saved selection order. Instructions stay outside compactable history. Saved origin
  distinguishes user input from generated guidance, even when both use the human role.
- **Tools** shows original arguments and execution results. Effective typed input after Hooks has
  its own disclosure. Source fields retain execution, recovery, and diagnostic evidence separately,
  with operation and attempt IDs. The next model request shows the exact prepared tool replies;
  the tool result view does not duplicate those replies or claim they have already been sent.
- **Activity** shows accepted events, with a payload view. RunState contains only retained recovery
  evidence. It cannot supply the earlier archive. RunView supplies the full accepted activity.
- **History** shows saved selection changes. RunView retains changes and original entries after
  compaction. RunState contains the selections needed for execution and recovery.
- **State** shows the saved position, turn, last completed answer, cumulative counters, turn
  counters, gauges, custom data, and continuation fields.
- **Encoded Saved State** shows the exact codec JSON, its UTF-8 byte size, Copy, and Download.
- **Recovered State JSON** shows the recovered value's JSON with the same controls. This JSON is
  for inspection. It is not a recovery format. Both tabs export the original supplied strings,
  including whitespace and large integer tokens. Parsing the display never changes the export.

Chronology uses EventSequenceNumber, not display timestamps. The execution-start event groups the
history records from one task admission, including supplied assistant and tool messages. Generated
guidance does not become a task merely because it uses a human role. A call's context, attempt, and
call ID join its start and finish. This keeps parallel calls separate. It also keeps recovery in a
later attempt separate from the unfinished original call. **No saved completion** means no finish
event is present. It does not claim that a worker is still running. A complete response
replaces its stream fragments in the display. If it is absent, saved fragments remain available.
Accepted output appears separately from the original candidate because output processing can
change it. Source disclosures keep the underlying events and their JSON paths.

Tool groups use the saved model response's native request IDs and names within their execution
scope. Accepted history can also link logical tool IDs to one request message, including IDs that
ReAct normalized. Nearby timestamps, matching tool names, or a reused ambiguous provider ID do not
prove a relationship. Calls without a saved link stay separate. Each attempt keeps its own group.

The task's system prompt is explicitly labeled as the **first saved effective request, after
Hooks**. RunView does not retain a separate instruction prefix on each task history entry. The
current checkpoint cannot prove an earlier prefix. If no request is present for that task, the
viewer states this limit. Each model point retains that call's own effective system messages.

RunState shows only its retained recovery evidence. Its chronology can be empty at a completed
savepoint. It does not contain the full activity archive. Use RunView for earlier tasks and calls,
and Messages for RunState's retained context. The component does not infer missing events from
history entry positions or enlarge RunState to create a full timeline.

Search covers the current section's text and identifiers. Lists show 12 records per page. Source
fields expand on demand and show large objects in groups of 50 fields. Model requests show 20
complete messages at a time, with a button to reveal more. No data is removed by paging. Wide
containers place the record list beside its details. Very wide containers also place a model
request beside its response. All model and user content is rendered as text, never HTML. Media
remain available in Source fields; the component does not fetch remote media or expose hidden
reasoning.

Unsafe JSON integer tokens remain decimal strings for display. Counter subtraction uses BigInt.
Pass the JSON **string**, not an object already parsed with `JSON.parse`, to avoid rounding an event
sequence before the component receives it. The original string remains the source for export.

Each instance owns its host contents. It registers no global event listeners and starts no network
requests. Copy reports clipboard failures inside the component. Downloads release their temporary
object URL after the browser starts reading it. An update preserves the current section and filter
for the same run and kind.
Invalid input throws and leaves the previous data visible. Parsing and indexes use memory in
proportion to the supplied document; paging bounds DOM work, not archive memory. Fetch large
archives on demand in the host application. RunState stays the compact execution view.

The CSS uses `--ge-ink`, `--ge-muted`, `--ge-line`, `--ge-accent`, and `--ge-paper` variables.
Override them on `.gent-run-explorer` to match the host application. Fonts use fallbacks when
Gent's example fonts are not present.
