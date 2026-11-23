import test from 'node:test';
import assert from 'node:assert/strict';
import {acceptedEvidence} from './accepted-evidence.mjs';

function fixture() {
  const prompts = ['Find customer C001. Remember Cedar-742.', 'What code did I give you?'];
  const activity = [], events = [], entries = [];
  function add(type, payload, turnId = 'turn:1', fields = {}, items = []) {
    const eventSequenceNumber = activity.length + 1;
    activity.push({eventSequenceNumber, type, payload, turnId, ...fields});
    events.push({eventSequenceNumber, items});
  }
  const user = prompt => ({Role: 'human', Parts: [{Text: prompt}]});
  entries.push({Origin: 'user_input', Messages: [user(prompts[0])]});
  for (const [index, status] of ['attention', 'complete'].entries()) {
    const native = {id: `native:${index}`, type: 'function',
      function: {name: 'lookup', arguments: JSON.stringify({id: index ? 'C001' : 'missing'})}};
    add('model_call_started', {Request: {Messages: [user(prompts[0])]}}, 'turn:1',
      {modelCallId: `model:${index}`});
    add('model_call_finished', {Error: null, Response: {Choices: [{Parts: [native]}]}}, 'turn:1',
      {modelCallId: `model:${index}`});
    const parts = [{Text: index ? 'Found customer C001.' : 'The lookup failed. Try another ID.'}];
    const record = {ToolCallID: `tool:${index}`, AttemptID: 'attempt:1', Name: 'lookup',
      RawArguments: native.function.arguments, Input: {type: 'fixture.input', data: {id: index}},
      Response: {ToolCallID: native.id, Kind: index ? 'execution' : 'call_error',
        ExecutionStatus: index ? 'executed' : '', Parts: parts}};
    add('tool_call_started', {ToolCallId: record.ToolCallID, Args: record.Input}, 'turn:1', {},
      [{id: record.ToolCallID, label: record.Name, args: native.function.arguments,
        text: '', status: 'running'}]);
    add('tool_call_finished',
      {ToolCallId: record.ToolCallID, ToolName: record.Name, Output: record},
      'turn:1', {toolCallId: record.ToolCallID},
      [{id: record.ToolCallID, label: record.Name, text: parts[0].Text, status}]);
    entries.push({Messages: [{Role: 'ai', Parts: [native]}, {Role: 'tool',
      Parts: [record.Response], Metadata: {'gent:tool_call_records': [structuredClone(record)]}}]});
  }
  const answers = ['**Found** customer C001.', 'Your code is **Cedar-742**.'];
  for (const [index, answer] of answers.entries()) {
    const turn = `turn:${index + 1}`, source = `candidate:${index}`, id = `${turn}:answer`;
    if (index) entries.push({Origin: 'user_input', Messages: [user(prompts[index])]});
    add('model_call_started', {Request: {Messages: prompts.slice(0, index + 1).map(user)}}, turn,
      {modelCallId: `answer:${index}`});
    add('model_call_finished', {Error: null, Response: {Choices: [{Parts: [{MessageId: source,
      Type: 'final_answer_candidate', Content: [{Text: `  ${answer}  `}]}]}]}}, turn,
    {modelCallId: `answer:${index}`});
    add('accepted_answer', {Content: [{Type: 'final_answer', MessageId: id,
      Content: [{Text: answer}]}], SourceMessageIds: [source]}, turn, {},
    [{id, text: answer, sources: [source], type: 'final_answer'}]);
  }
  const view = {runId: 'run', lastAppliedEventSequenceNumber: activity.length,
    history: {entries}, activity};
  const state = {runId: 'run', head: activity.length, active: false, outcome: {status: 'finished'},
    lastAnswer: answers.at(-1), events};
  const expected = {answers: answers.map((text, index) =>
    ({id: `turn:${index + 1}:answer`, text, sources: [`candidate:${index}`]})), tools: [
    {id: 'tool:0', label: 'lookup', args: '{"id":"missing"}', status: 'attention',
      text: 'The lookup failed. Try another ID.'},
    {id: 'tool:1', label: 'lookup', args: '{"id":"C001"}', status: 'complete',
      text: 'Found customer C001.'},
  ]};
  return {state, view, prompts, expected};
}

const scenarios = [
  {name: 'handled error and complete nonliteral answers', input: () => {}, expected: ''},
  {name: 'failed execution', input: ({state}) => { state.outcome = {status: 'error'}; },
    expected: 'The turn must finish successfully'},
  {name: 'different accepted head', input: ({state}) => { state.head++; },
    expected: 'The display and archive must use the same accepted head'},
  {name: 'lost event', input: ({state}) => { state.events.splice(0, 1); },
    expected: 'Every accepted event must be delivered'},
  {name: 'changed original prompt', input: ({view}) => {
    view.history.entries[0].Messages[0].Parts[0].Text = 'Changed task';
  }, expected: 'The saved history must keep complete user input'},
  {name: 'original prompt lost from next request', input: ({view}) => {
    view.activity.filter(event => event.type === 'model_call_started').at(-1)
      .payload.Request.Messages.shift();
  }, expected: 'The effective request must keep earlier user input'},
  {name: 'invalid system order', input: ({view}) => {
    view.activity[0].payload.Request.Messages.push(
      {Role: 'system', Parts: [{Text: 'Late policy'}]});
  }, expected: 'System instructions must stay before conversation input'},
  {name: 'model call failed', input: ({view}) => { view.activity[1].payload.Error = 'failure'; },
    expected: 'A completed model call must not have an unexpected failure'},
  {name: 'changed native arguments', input: ({view}) => {
    view.activity[1].payload.Response.Choices[0].Parts[0].function.arguments = '{"id":"wrong"}';
  }, expected: 'The saved tool request must match the complete native request'},
  {name: 'lost handled failure', input: ({view}) => {
    view.history.entries[1].Messages[1].Metadata['gent:tool_call_records'] = [];
  }, expected: 'History must keep each complete tool result once'},
  {name: 'wrong correlation', input: ({view}) => { view.activity[3].toolCallId = 'tool:1'; },
    expected: 'Tool result identities must agree'},
  {name: 'wrong tool status', input: ({state}) => { state.events[3].items[0].status = 'complete'; },
    expected: 'Delivered tool results must keep complete content and status'},
  {name: 'changed tool text', input: ({state}) => { state.events[3].items[0].text = 'All good'; },
    expected: 'Delivered tool results must keep complete content and status'},
  {name: 'wrong answer source', input: ({view}) => {
    view.activity.at(-1).payload.SourceMessageIds = ['missing'];
  }, expected: 'Each answer source must exist in native response evidence'},
  {name: 'changed accepted content', input: ({view}) => {
    view.activity.at(-1).payload.Content[0].Content[0].Text = 'Changed answer';
  }, expected: 'Accepted text must equal the complete trimmed model output'},
  {name: 'changed displayed answer', input: ({state}) => {
    state.events.at(-1).items[0].text = 'Changed answer';
  }, expected: 'Delivered answers must match accepted content and sources'},
];
for (const scenario of scenarios) {
  test(scenario.name, () => {
    const input = fixture();
    scenario.input(input);
    let output, error = '';
    try { output = acceptedEvidence(input.state, input.view, input.prompts); }
    catch (failure) { error = failure.message; }
    assert.deepEqual({output, error}, {
      output: scenario.expected ? undefined : input.expected, error: scenario.expected,
    });
  });
}

for (const scenario of [
  {name: 'Go JSON escapes and numeric key order', input: false, expected: ''},
  {name: 'changed data value', input: true,
    expected: 'Delivered tool results must keep complete content and status'},
]) {
  test(scenario.name, () => {
    const input = fixture();
    const data = {'2': 'second', '10': '<first> & last'};
    const parts = [{Data: data}, {Text: 'Complete result.'}];
    input.view.activity[7].payload.Output.Response.Parts = parts;
    input.view.history.entries[2].Messages[1].Metadata['gent:tool_call_records'][0]
      .Response.Parts = structuredClone(parts);
    const body = `\`\`\`json
{
  "10": "\\u003cfirst\\u003e \\u0026 last",
  "2": "second"
}
\`\`\`

Complete result.`;
    input.state.events[7].items[0].text = body;
    input.expected.tools[1].text = body;
    if (scenario.input) input.state.events[7].items[0].text = body.replace('second', 'changed');
    let output, error = '';
    try { output = acceptedEvidence(input.state, input.view, input.prompts); }
    catch (failure) { error = failure.message; }
    assert.deepEqual({output, error}, {
      output: scenario.expected ? undefined : input.expected, error: scenario.expected,
    });
  });
}
