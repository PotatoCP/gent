import {element, jsonTree} from './dom.js';
import {counterRows, partText} from './data.js';
import {facts, section, sourceFields} from './fields.js';
import {renderChronology, renderToolResult} from './chronology-render.js';

export function renderDetail(row) {
  if (row.type.endsWith('-step')) return renderChronology(row);
  const node = element('article', 'ge-detail');
  node.setAttribute('aria-label', 'Selected record');
  node.append(element('h5', '', row.title), element('p', 'ge-note', row.subtitle));
  if (row.type === 'message') {
    node.append(facts([['Source', row.entry.Origin || 'Unspecified'],
      ['Entry', row.entry.id], ['Context', row.entry.contextId]]));
    node.append(element('div', 'ge-content', row.text));
    if (row.source.Continuation) {
      node.append(element('p', 'ge-note', 'Native continuation is present. Open Source fields.'));
    }
  } else if (row.type === 'tool') {
    const tool = row.source;
    node.append(facts([['Operation', tool.ToolCallID], ['Attempt', tool.AttemptID]]));
    node.append(renderToolResult(tool, {path: row.path}));
  } else if (row.type === 'event') {
    const event = row.source;
    node.append(facts([['Event sequence', event.eventSequenceNumber], ['Turn', event.turnId],
      ['Attempt', event.attemptId], ['Time', event.ts]]));
    const payload = element('div', 'json-tree');
    payload.append(jsonTree(event.payload, 'Payload'));
    node.append(payload);
  } else {
    node.append(element('p', 'ge-note', row.text));
    node.append(element('p', 'ge-note', 'Selections are ordered entry IDs. An archived original ' +
      'can remain here after it leaves the active message list.'));
  }
  node.append(sourceFields(row.source, row.path));
  return node;
}

export function renderState(data) {
  const node = element('div', 'ge-state');
  const context = data.checkpoint.contexts?.[0];
  const identity = section('Saved position',
    'This describes saved state. It does not run the agent.');
  identity.append(facts([['Run', data.runId], ['Event sequence', data.head],
    ['History revision', data.history.revision], ['Save phase', data.checkpoint.phase],
    ['Current turn', context?.turnId], ['Turn open', context ? context.turnOpen : 'No turn']]));
  const outcome = section('Last completed answer');
  if (data.outcome) {
    const result = data.outcome.result;
    outcome.append(facts([['Status', result.Status], ['Reason', result.TerminationReason],
      ['Turn', result.TurnId]]), element('div', 'ge-content', partText(result.Output || [])),
    sourceFields(data.outcome, data.kind === 'state' ? '/LastOutcome' : '/checkpoint/lastOutcome'));
  } else outcome.append(element('p', 'ge-note', 'No completed answer is saved.'));
  const counters = section('Counters',
    'Run totals are cumulative. Turn values subtract the saved ' +
    'turn baseline; Continue does not reset them.');
  const table = element('table', 'ge-table');
  const header = element('tr');
  for (const name of ['Counter', 'Run', 'Turn']) {
    const cell = element('th', '', name); cell.scope = 'col'; header.append(cell);
  }
  const thead = element('thead'); thead.append(header); table.append(thead);
  const body = element('tbody');
  for (const row of counterRows(data.runtime)) {
    const tr = element('tr');
    const label = element('th', '', row.name); label.scope = 'row';
    tr.append(label, element('td', '', row.run), element('td', '', row.turn));
    body.append(tr);
  }
  table.append(body); counters.append(table);
  for (const runtime of data.runtime.contexts || []) {
    const gauges = element('details', 'ge-source');
    gauges.append(element('summary', '', 'Current gauges'),
      facts(Object.entries(runtime.gauges || {})));
    counters.append(gauges);
  }
  const saved = section('Custom data & continuation', 'Saved task, LoopData, and loop progress. ' +
    'Typed values stay in their codec form for inspection.');
  for (const [index, checkpoint] of (data.checkpoint.contexts || []).entries()) {
    const path = `${data.kind === 'state' ? '/Checkpoint' : '/checkpoint'}/contexts/${index}`;
    saved.append(facts([['Context', checkpoint.name], ['Iteration', checkpoint.iteration],
      ['Completed iteration', checkpoint.completedIteration]]),
    sourceFields(checkpoint.task, `${path}/task`, 'Accepted task'),
    sourceFields(checkpoint.state, `${path}/state`, 'Loop progress'));
    for (const [name, extension] of Object.entries(checkpoint.extensions || {})) {
      const key = name.replaceAll('~', '~0').replaceAll('/', '~1');
      saved.append(sourceFields(extension, `${path}/extensions/${key}`,
        `Custom data · ${name} · ${extension.type} v${extension.version}`));
    }
  }
  node.append(identity, outcome, counters, saved);
  return node;
}
