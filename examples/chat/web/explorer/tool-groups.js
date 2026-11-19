function requestKey(event, id, name) {
  return JSON.stringify([event.contextId, event.turnId, event.iteration, id, name]);
}

function nativeRequests(parts) {
  return (parts || []).filter(part => part.id && part.function);
}

function modelRequests(rows) {
  const index = new Map();
  for (const row of rows) {
    if (row.type !== 'model-step' || !row.after) continue;
    const choices = row.after.source.payload.Response?.Choices || [];
    for (const [choice, value] of choices.entries()) {
      const batch = {id: `${row.id}:choice:${choice}`, requests: nativeRequests(value.Parts),
        source: row.after, label: `Requested by model call ${row.callId}`};
      for (const request of batch.requests) {
        const key = requestKey(row.source, request.id, request.function.name);
        if (!index.has(key)) index.set(key, []);
        index.get(key).push(batch);
      }
    }
  }
  return index;
}

// Accepted tool history also links logical execution IDs to one native request message. This
// covers retained history and provider IDs that ReAct normalized after the original response.
function historyRequests(entries) {
  const index = new Map();
  for (const entry of entries) {
    let batch;
    for (const [offset, message] of (entry.Messages || []).entries()) {
      if (message.Role === 'ai') {
        batch = {id: `entry:${entry.id}:message:${offset}`,
          requests: nativeRequests(message.Parts), label: 'Requested in one saved message'};
      }
      if (!batch?.requests.length) continue;
      for (const record of message.Metadata?.['gent:tool_call_records'] || []) {
        if (!batch.requests.some(request => request.id === record.Response?.ToolCallID &&
          request.function.name === record.Response?.Name)) continue;
        index.set(JSON.stringify([entry.contextId, record.ToolCallID]), batch);
      }
    }
  }
  return index;
}

function parentRequest(index, row, id, name) {
  const matches = (index.get(requestKey(row.source, id, name)) || [])
    .filter(batch => BigInt(batch.source.order) < BigInt(row.order));
  // A provider ID belongs to one response, not the whole run. Ambiguous reuse is not proof
  // that executions belong together. Keep those calls separate unless history links them.
  return matches.length === 1 ? matches[0] : null;
}

function batchFor(row, models, history) {
  const saved = history.get(JSON.stringify([row.source.contextId, row.callId]));
  if (saved) {
    const parents = saved.requests.map(request =>
      parentRequest(models, row, request.id, request.function.name));
    if (parents[0] && parents.every(parent => parent === parents[0])) return parents[0];
    return saved;
  }
  const reply = row.after?.source.payload.Output?.Response;
  return reply ? parentRequest(models, row, reply.ToolCallID, reply.Name) : null;
}

function describeGroup(group) {
  const calls = group.calls;
  const last = calls.flatMap(call => call.events).reduce((a, b) =>
    BigInt(a.order) > BigInt(b.order) ? a : b);
  group.status = calls.some(call => call.status === 'Error') ? 'Error' :
    calls.some(call => !call.after) ? 'No saved completion' : 'Complete';
  group.title = calls.length === 1 ? 'Tool call' : `Tool calls · ${calls.length}`;
  group.text = calls.map(call => call.toolName).join(' · ');
  group.subtitle = `Events ${group.order}–${last.order} · ${group.status}`;
  group.events = calls.flatMap(call => call.events);
  group.searchText = calls.map(call => `${call.callId} ${call.text} ${call.status}`).join(' ');
}

// Group by saved request relationships, never by tool names or nearby timestamps. Recovery in
// a different attempt stays separate. Each child keeps its exact event positions and evidence.
export function groupToolCalls(rows, entries) {
  const models = modelRequests(rows), history = historyRequests(entries);
  const groups = new Map(), result = [];
  for (const row of rows) {
    if (row.type !== 'tool-step') { result.push(row); continue; }
    const batch = batchFor(row, models, history);
    const key = JSON.stringify([batch?.id || row.id, row.source.attemptId]);
    let group = groups.get(key);
    if (!group) {
      group = {id: `tools:${key}`, type: 'tool-group-step', calls: [], batch,
        order: row.order, source: row.source, path: row.path};
      groups.set(key, group); result.push(group);
    }
    group.calls.push(row);
  }
  for (const group of groups.values()) describeGroup(group);
  return result;
}
