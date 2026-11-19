import {partText} from './content.js';
import {buildChronology} from './chronology.js';
export {partText} from './content.js';

// Inspection keeps unsafe integer tokens as text. Run positions must not round in JavaScript.
// The original JSON string remains the source for export and for Go recovery APIs.
export function parseInspectionJSON(json) {
  const tokens = /"(?:[^"\\]|\\.)*"|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;
  return JSON.parse(json.replace(tokens, (token, number) => {
    if (!number || /[.eE]/.test(number) || Number.isSafeInteger(Number(number))) return token;
    return JSON.stringify(number);
  }));
}

export function readRunData({kind, json}) {
  if (!['state', 'view'].includes(kind)) throw new Error('Select RunState or RunView.');
  const source = parseInspectionJSON(json);
  if (!source || typeof source !== 'object' || Array.isArray(source)) {
    throw new Error('Pass JSON of a recovered RunState or RunView, from Go json.Marshal.');
  }
  const state = kind === 'state';
  const runId = state ? source.RunId : source.runId;
  const history = state ? source.History : source.history;
  if (typeof runId !== 'string' || !history || !Object.hasOwn(history, 'revision')) {
    throw new Error('Pass JSON of a recovered RunState or RunView, from Go json.Marshal.');
  }
  const checkpoint = (state ? source.Checkpoint : source.checkpoint) || {};
  const runtime = (state ? source.Runtime : source.runtime) || {};
  const entries = history.entries || [];
  const selections = new Map();
  for (const change of history.changes || []) {
    for (const selection of change.selections || []) {
      selections.set(selection.contextId, selection.active || []);
    }
  }
  const activeIds = [...selections.values()].flat();
  const active = new Set(activeIds);
  const base = state ? '/History' : '/history';
  const messages = [];
  const tools = [];
  for (const [index, context] of (checkpoint.contexts || []).entries()) {
    for (const [offset, message] of (context.instructions || []).entries()) {
      messages.push(messageRow(message, {
        id: `instruction:${index}:${offset}`, order: 'S', Origin: 'instructions',
        contextId: context.contextId,
      }, `${state ? '/Checkpoint' : '/checkpoint'}/contexts/${index}/instructions/${offset}`,
      true, 'Instructions'));
    }
  }
  for (const [index, entry] of entries.entries()) {
    for (const [offset, message] of (entry.Messages || []).entries()) {
      const path = `${base}/entries/${index}/Messages/${offset}`;
      messages.push(messageRow(message, entry, path, active.has(entry.id)));
      for (const [toolIndex, tool] of
        (message.Metadata?.['gent:tool_call_records'] || []).entries()) {
        tools.push({id: `${path}:${toolIndex}`, title: tool.Name,
          subtitle: `${tool.Kind} · ${tool.Execution?.Status || tool.Recovery?.Status || 'saved'}`,
          text: tool.RawArguments || '', type: 'tool', source: tool,
          path: `${path}/Metadata/gent:tool_call_records/${toolIndex}`,
          order: entry.order, active: active.has(entry.id), entry});
      }
    }
  }
  const instructions = messages.filter(row => row.entry.Origin === 'instructions');
  const byEntry = new Map();
  for (const row of messages) {
    if (!byEntry.has(row.entry.id)) byEntry.set(row.entry.id, []);
    byEntry.get(row.entry.id).push(row);
  }
  const selectedMessages = instructions.concat(activeIds.flatMap(id => byEntry.get(id) || []));
  const events = (state ? source.Evidence : source.activity) || [];
  const activity = events.map((event, index) => ({
    id: `event:${event.eventSequenceNumber}`, title: event.type.replaceAll('_', ' '),
    subtitle: `Event ${event.eventSequenceNumber} · iteration ${event.iteration}`,
    text: event.eventName || '', type: 'event', source: event,
    path: `/${state ? 'Evidence' : 'activity'}/${index}`, order: event.eventSequenceNumber,
  }));
  const changes = (history.changes || []).map((change, index) => ({
    id: `revision:${change.revision}`, title: change.reason || 'Saved selection',
    subtitle: `Revision ${change.previousRevision} → ${change.revision}`,
    text: `${change.appendedEntries?.length || 0} appended entries · ` +
      `${(change.selections || []).reduce((n, item) =>
        n + (item.active?.length || 0), 0)} selected`,
    type: 'change', source: change, path: `${base}/changes/${index}`, order: change.revision,
  }));
  return {kind, source, runId, history, checkpoint, runtime, entries, messages, selectedMessages,
    tools, activity, changes, chronology: buildChronology(activity, entries), active,
    head: state ? source.EventSequenceNumber : source.lastAppliedEventSequenceNumber,
    outcome: state ? source.LastOutcome : checkpoint.lastOutcome};
}

function messageRow(message, entry, path, active, label) {
  const human = entry.Origin === 'user_input' ? 'User' : 'Guidance';
  const role = label || (message.Role === 'human' ? human :
    message.Role === 'ai' ? 'Assistant' : message.Role === 'tool' ? 'Tool' : message.Role);
  const types = [...new Set((message.Parts || []).flatMap(part => part.Type ? [part.Type] : []))];
  return {id: path, title: role, subtitle: types.join(' · ').replaceAll('_', ' ') ||
    entry.Origin?.replaceAll('_', ' ') || 'Saved message', text: partText(message.Parts),
    type: 'message', source: message, path, order: entry.order, active, entry};
}

export function counterRows(runtime) {
  return (runtime.contexts || []).flatMap(context =>
    Object.entries(context.counters || {}).map(([name, value]) => ({
      contextId: context.contextId, name, run: String(value),
      turn: String(BigInt(value) - BigInt(context.turnCounterBaselines?.[name] || 0)),
    })));
}

export function filterRows(rows, query) {
  const text = query.trim().toLocaleLowerCase();
  if (!text) return rows;
  return rows.filter(row => [row.title, row.subtitle, row.text, row.searchText, row.id, row.order,
    row.entry?.id, row.source?.ToolCallID, row.source?.turnId, row.source?.attemptId]
    .some(value => String(value ?? '').toLocaleLowerCase().includes(text)));
}
