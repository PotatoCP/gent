import assert from 'node:assert/strict';

function same(actual, expected, detail) {
  try { assert.deepEqual(actual, expected); }
  catch (cause) { throw new Error(detail, {cause}); }
}

function text(parts) {
  return (parts || []).map(part => part.Text ?? text(part.Content)).join('');
}

// The Go display uses JSON formatting for data parts. Compare their complete values, not
// JavaScript's formatting: Go escapes HTML characters and can use a different object-key order.
function checkReply(actual, parts) {
  const expected = parts.flatMap(part => Object.hasOwn(part, 'Data') ? [{Data: part.Data}] :
    text([part]) ? [{Text: text([part])}] : []);
  let remaining = actual;
  const values = [];
  try {
    for (const [index, part] of expected.entries()) {
      if (index) {
        assert.equal(remaining.slice(0, 2), '\n\n');
        remaining = remaining.slice(2);
      }
      if (Object.hasOwn(part, 'Data')) {
        assert.equal(remaining.slice(0, 8), '```json\n');
        const end = remaining.indexOf('\n```', 8);
        assert.ok(end >= 8);
        values.push({Data: JSON.parse(remaining.slice(8, end))});
        remaining = remaining.slice(end + 4);
      } else {
        values.push({Text: remaining.slice(0, part.Text.length)});
        remaining = remaining.slice(part.Text.length);
      }
    }
    assert.deepEqual(values, expected);
    assert.equal(remaining, '');
  } catch (cause) {
    throw new Error('Delivered tool results must keep complete content and status', {cause});
  }
}

export function historyTools(history) {
  return history.entries.flatMap(entry => (entry.Messages || []).flatMap(message =>
    message.Metadata?.['gent:tool_call_records'] || []));
}

// The saved archive is the reference for a live model's choices. Do not turn a prompt preference
// into an output requirement. Check full values and identities, including handled tool failures.
export function acceptedEvidence(state, view, prompts) {
  same(state.active, false, 'The turn must have stopped');
  same(state.outcome, {status: 'finished'}, 'The turn must finish successfully');
  same([state.runId, state.head], [view.runId, view.lastAppliedEventSequenceNumber],
    'The display and archive must use the same accepted head');
  same(state.events.map(event => event.eventSequenceNumber),
    view.activity.map(event => event.eventSequenceNumber),
    'Every accepted event must be delivered');
  const users = view.history.entries.filter(entry => entry.Origin === 'user_input')
    .flatMap(entry => entry.Messages.map(message => ({Role: message.Role, Parts: message.Parts})));
  same(users, prompts.map(prompt => ({Role: 'human', Parts: [{Text: prompt}]})),
    'The saved history must keep complete user input');
  const delivered = new Map(state.events.map(event => [event.eventSequenceNumber, event]));
  const ends = view.activity.filter(event => event.type === 'accepted_answer');
  same(ends.length, prompts.length, 'Each completed turn must have one accepted answer');
  const starts = view.activity.filter(event => event.type === 'model_call_started');
  const responses = view.activity.filter(event => event.type === 'model_call_finished');
  same(responses.map(event => event.modelCallId), starts.map(event => event.modelCallId),
    'Every saved model request must have its own response');
  for (const event of starts) {
    const index = ends.findIndex(answer => answer.turnId === event.turnId);
    same(index >= 0, true, 'A model call must belong to a completed turn');
    const messages = event.payload.Request.Messages;
    const retained = messages.filter(message => message.Role === 'human' &&
      prompts.includes(text(message.Parts))).map(message =>
      ({Role: message.Role, Parts: message.Parts}));
    same(retained, users.slice(0, index + 1), 'The effective request must keep earlier user input');
    const first = messages.findIndex(message => message.Role !== 'system');
    same(messages.slice(first).some(message => message.Role === 'system'), false,
      'System instructions must stay before conversation input');
  }
  const parts = new Map();
  const nativeTools = [];
  for (const event of responses) {
    same(event.payload.Error, null, 'A completed model call must not have an unexpected failure');
    for (const choice of event.payload.Response.Choices) {
      for (const part of choice.Parts) {
        if (part.MessageId) parts.set(part.MessageId, part);
        if (part.type === 'function') nativeTools.push(part);
      }
    }
  }
  const savedRequests = view.history.entries.flatMap(entry => (entry.Messages || [])
    .filter(message => message.Role === 'ai').flatMap(message => message.Parts)
    .filter(part => part.type === 'function'));
  // Some providers omit request IDs. Gent assigns them before the request enters history.
  same(savedRequests.length, nativeTools.length,
    'Every native tool request must remain in history');
  same(savedRequests, nativeTools.map((part, index) =>
    ({...part, id: part.id || savedRequests[index].id})),
  'Saved tool requests must keep native content and supplied IDs');
  const requests = new Map(savedRequests.map(part => [part.id, part]));
  const answers = ends.map(event => {
    const content = event.payload.Content;
    same(content.length, 1, 'The text example must save one answer item');
    same(content[0].Type, 'final_answer', 'The saved answer must be accepted output');
    const sources = event.payload.SourceMessageIds;
    same(sources.length > 0, true, 'Accepted text must identify its original model response');
    const source = sources.map(id => {
      same(parts.has(id), true, 'Each answer source must exist in native response evidence');
      return parts.get(id);
    });
    const answer = {id: content[0].MessageId, text: text(content[0].Content), sources};
    same(answer.text, source.map(part => text(part.Content)).join('').trim(),
      'Accepted text must equal the complete trimmed model output');
    const items = delivered.get(event.eventSequenceNumber).items;
    same(items.map(item =>
      ({id: item.id, text: item.text, sources: item.sources, type: item.type})),
      [{...answer, type: 'final_answer'}],
      'Delivered answers must match accepted content and sources');
    return answer;
  });
  same(state.lastAnswer, answers.at(-1).text,
    'The completed result must match the accepted answer');
  const records = historyTools(view.history);
  const toolStarts = new Map(view.activity.filter(event => event.type === 'tool_call_started')
    .map(event => [event.payload.ToolCallId, event]));
  const tools = view.activity.filter(event => event.type === 'tool_call_finished').map(event => {
    const record = event.payload.Output;
    const start = toolStarts.get(record.ToolCallID);
    same(Boolean(start), true, 'Each tool result must have a matching start');
    same([event.toolCallId, event.payload.ToolCallId, event.payload.ToolName],
      [record.ToolCallID, record.ToolCallID, record.Name], 'Tool result identities must agree');
    const native = requests.get(record.Response.ToolCallID);
    same(Boolean(native), true, 'Each tool result must identify its native request');
    same([record.Name, record.RawArguments], [native.function.name, native.function.arguments],
      'The saved tool request must match the complete native request');
    same(records.filter(value => value.ToolCallID === record.ToolCallID), [record],
      'History must keep each complete tool result once');
    const input = delivered.get(start.eventSequenceNumber).items;
    same(input.length, 1, 'Each tool start must be displayed');
    same([input[0].id, input[0].label, input[0].status],
      [record.ToolCallID, record.Name, 'running'],
      'Displayed tool input must use the saved identity');
    if (record.Input) {
      same(start.payload.Args, record.Input, 'Effective tool input must stay saved');
    }
    const executed = record.Response.Kind === 'execution' &&
      record.Response.ExecutionStatus === 'executed';
    const items = delivered.get(event.eventSequenceNumber).items;
    same(items.length, 1, 'Each tool result must be displayed');
    checkReply(items[0].text, record.Response.Parts);
    const tool = {id: record.ToolCallID, label: record.Name, text: items[0].text,
      status: executed ? 'complete' : 'attention'};
    same(items.map(item =>
      ({id: item.id, label: item.label, text: item.text, status: item.status})), [tool],
    'Delivered tool results must keep complete content and status');
    return {...tool, args: input[0].args};
  });
  same(tools.map(tool => tool.id), [...toolStarts.keys()], 'Every started tool must have a result');
  same(records.length, tools.length, 'No tool result can be lost from the archive');
  return {answers, tools};
}
