import {partText} from './content.js';
import {groupToolCalls} from './tool-groups.js';

function compareSequence(a, b) {
  const left = BigInt(a.order), right = BigInt(b.order);
  return left < right ? -1 : left > right ? 1 : 0;
}

// Admission can append several records, including caller-supplied assistant and tool messages.
// The execution_started boundary identifies one task. A human role alone is not an admission.
function taskRows(activity) {
  const rows = [], pending = new Map(), started = new Set();
  for (const reference of activity) {
    const event = reference.source;
    const key = JSON.stringify([event.contextId, event.turnId]);
    if (started.has(key)) continue;
    if (event.type === 'state_change' && event.payload?.appendedEntries?.length) {
      if (!pending.has(key)) pending.set(key, []);
      pending.get(key).push(reference);
    }
    if (event.type !== 'execution_started') continue;
    started.add(key);
    const events = pending.get(key) || []; pending.delete(key);
    const entries = events.flatMap(item => item.source.payload.appendedEntries)
      .filter(entry => ['', 'user_input'].includes(entry.Origin));
    if (!entries.some(entry => entry.Origin === 'user_input')) continue;
    const messages = entries.flatMap(entry => entry.Messages || []);
    const first = events[0];
    rows.push({...first, id: `task:${key}`, title: 'User input (Task)', type: 'task-step',
      subtitle: `Event ${first.order} · Task admitted`,
      text: messages.map(message => partText(message.Parts)).join('\n\n'), messages,
      events: [...events, reference], status: 'Saved', systems: null, systemSource: null});
  }
  return rows;
}

// Use accepted positions, not timestamps. An attempt is part of a call's identity: recovery
// must not attach a later worker's result to an earlier unfinished operation.
export function buildChronology(activity, entries = []) {
  const ordered = [...activity].sort(compareSequence);
  const rows = taskRows(ordered);
  const calls = new Map();
  for (const reference of ordered) {
    const event = reference.source;
    const model = event.type.startsWith('model_') || event.type === 'accepted_answer';
    const tool = event.type === 'tool_call_started' || event.type === 'tool_call_finished';
    if (!model && !tool) continue;
    const callId = model ? event.modelCallId : event.toolCallId;
    if (!callId) continue;
    const key = JSON.stringify([
      event.contextId, event.attemptId, model ? 'model' : 'tool', callId]);
    let row = calls.get(key);
    if (!row) {
      row = {...reference, id: key, type: model ? 'model-step' : 'tool-step', callId,
        before: null, after: null, chunks: [], accepted: [], request: null, events: []};
      calls.set(key, row); rows.push(row);
    }
    if (event.type.endsWith('_started')) row.before = reference;
    if (event.type.endsWith('_finished')) row.after = reference;
    if (event.type === 'model_stream_chunk') row.chunks.push(reference);
    if (event.type === 'accepted_answer') row.accepted.push(reference);
    row.events.push(reference);
  }
  for (const row of calls.values()) describeCall(row);
  const pending = new Map();
  rows.sort(compareSequence);
  for (const row of rows) {
    const key = JSON.stringify([row.source.turnId, row.source.contextId]);
    if (row.type === 'task-step') {
      if (!pending.has(key)) pending.set(key, []);
      pending.get(key).push(row);
    }
    if (row.type !== 'model-step' || !row.request) continue;
    for (const task of pending.get(key) || []) {
      task.systems = (row.request.Messages || []).filter(message => message.Role === 'system');
      task.systemSource = row.requestSource;
    }
    pending.delete(key);
  }
  return groupToolCalls(rows, entries);
}

function describeCall(row) {
  const before = row.before?.source.payload;
  const after = row.after?.source.payload;
  row.request = before?.Request || after?.Request || null;
  row.requestSource = before?.Request ? row.before : row.after;
  const output = after?.Output;
  const failed = Boolean(after?.Error || output?.Error ||
    output?.Kind === 'call_error' || output?.Kind === 'recovery_error');
  row.status = failed ? 'Error' : row.after ? 'Complete' : 'No saved completion';
  const range = row.after ? `${row.order}–${row.after.order}` : String(row.order);
  row.subtitle = `Events ${range} · ${row.status}`;
  if (row.type === 'model-step') {
    const payload = after || before || {};
    row.title = 'Model call';
    row.text = [payload.Provider, payload.Model, payload.StreamTopicId].filter(Boolean).join(' · ');
    if (!row.text) row.text = row.callId;
    return;
  }
  const name = output?.Name || before?.ToolName || after?.ToolName || row.callId;
  row.toolName = name;
  row.title = `Tool call · ${name}`;
  row.text = output?.RawArguments || name;
  if (output?.Kind === 'recovery') {
    row.status = `Recovery · ${output.Recovery?.Status || 'saved'}`;
    row.subtitle = `Events ${range} · ${row.status}`;
  }
}
