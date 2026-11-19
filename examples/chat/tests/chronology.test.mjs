import test from 'node:test';
import assert from 'node:assert/strict';
import {readRunData} from '../web/explorer/data.js';

const message = (role, text) => ({Role: role, Parts: [{Text: text}]});
const request = text => ({Messages: [message('system', text), message('human', 'Task')]});
function event(sequence, type, payload, ids = {}) {
  return {eventSequenceNumber: sequence, type, payload, runId: 'run', contextId: 'root',
    turnId: 'turn', attemptId: 'attempt', ts: '2026-11-19T00:00:00Z', iteration: 1, ...ids};
}
const admitted = event(1, 'state_change', {appendedEntries: [
  {id: 'input', Origin: 'user_input', Messages: [message('human', 'Task')]},
  {id: 'fix', Origin: 'output_correction', Messages: [message('human', 'Generated correction')]},
]});
const started = event(2, 'execution_started', {});
const response = {Choices: [{Parts: [{Type: 'reasoning', Content: [{Text: 'Reason'}]},
  {Type: 'commentary', Content: [{Text: 'Progress'}]},
  {id: 'native-id', type: 'function', function: {name: 'lookup', arguments: '{"id":1}'}}]}]};
const modelIds = {modelCallId: 'model:1'};
const toolA = {Name: 'lookup', Kind: 'execution', Execution: {Status: 'executed'},
  Response: {Parts: [{Text: 'Result A'}]}};
const toolB = {Name: 'charge', Kind: 'call_error', Error: {message: 'Payment unavailable'},
  Response: {Parts: [{Text: 'Try another action'}]}};

for (const scenario of [
  {name: 'task and full model exchange ignore timestamp order', input: [
    event(4, 'model_call_finished', {Request: request('Effective prefix'), Response: response},
      {...modelIds, ts: '2026-11-18T00:00:00Z'}), admitted, started,
    event(3, 'model_call_started', {Request: request('Effective prefix')}, modelIds),
  ], expected: [
    {kind: 'task-step', sequence: 1, status: 'Saved', events: [1, 2], systems: ['Effective prefix'],
      messages: [message('human', 'Task')], request: null, response: null, output: null},
    {kind: 'model-step', sequence: 3, status: 'Complete', events: [3, 4], systems: null,
      messages: null, request: request('Effective prefix'), response, output: null},
  ]},
  {name: 'parallel tools pair by identity, not finish order', input: [
    event(10, 'tool_call_started', {ToolName: 'lookup'}, {toolCallId: 'a'}),
    event(11, 'tool_call_started', {ToolName: 'charge'}, {toolCallId: 'b'}),
    event(12, 'tool_call_finished', {Output: toolB}, {toolCallId: 'b'}),
    event(13, 'tool_call_finished', {Output: toolA}, {toolCallId: 'a'}),
  ], expected: [
    {kind: 'tool-step', sequence: 10, status: 'Complete', events: [10, 13], systems: null,
      messages: null, request: null, response: null, output: toolA},
    {kind: 'tool-step', sequence: 11, status: 'Error', events: [11, 12], systems: null,
      messages: null, request: null, response: null, output: toolB},
  ]},
  {name: 'recovery in a new attempt does not complete the old attempt', input: [
    event(10, 'tool_call_started', {ToolName: 'lookup'}, {toolCallId: 'a'}),
    event(12, 'tool_call_finished', {Output: {Name: 'lookup', Kind: 'recovery',
      Recovery: {Status: 'completed'}}}, {toolCallId: 'a', attemptId: 'recovered'}),
  ], expected: [
    {kind: 'tool-step', sequence: 10, status: 'No saved completion', events: [10], systems: null,
      messages: null, request: null, response: null, output: null},
    {kind: 'tool-step', sequence: 12, status: 'Recovery · completed', events: [12], systems: null,
      messages: null, request: null, response: null,
      output: {Name: 'lookup', Kind: 'recovery', Recovery: {Status: 'completed'}}},
  ]},
  {name: 'an older task cannot take the current turn system prompt', input: [admitted, started,
    event(3, 'model_call_started', {Request: request('Different turn')},
      {...modelIds, turnId: 'other-turn'}),
  ], expected: [
    {kind: 'task-step', sequence: 1, status: 'Saved', events: [1, 2], systems: null,
      messages: [message('human', 'Task')], request: null, response: null, output: null},
    {kind: 'model-step', sequence: 3, status: 'No saved completion', events: [3], systems: null,
      messages: null, request: request('Different turn'), response: null, output: null},
  ]},
  {name: 'one task can supply multiple conversation messages', input: [admitted,
    event(2, 'state_change', {appendedEntries: [{Origin: '',
      Messages: [message('ai', 'Earlier answer')]}]}),
    event(3, 'state_change', {appendedEntries: [{Origin: 'user_input',
      Messages: [message('human', 'Follow up')]}]}),
    event(4, 'execution_started', {}),
    event(5, 'state_change', {appendedEntries: [{Origin: 'system_injected',
      Messages: [message('human', 'Generated guidance')]}]}),
  ], expected: [
    {kind: 'task-step', sequence: 1, status: 'Saved', events: [1, 2, 3, 4], systems: null,
      messages: [message('human', 'Task'), message('ai', 'Earlier answer'),
        message('human', 'Follow up')], request: null, response: null, output: null},
  ]},
]) {
  test(scenario.name, () => {
    const source = {runId: 'run', history: {revision: 1}, activity: scenario.input};
    const json = JSON.stringify(source);
    const data = readRunData({kind: 'view', json});
    const actual = data.chronology.flatMap(row => row.calls || [row]).map(row => ({
      kind: row.type, sequence: row.order,
      status: row.status, events: row.events.map(item => item.order),
      systems: row.systems?.map(item => item.Parts[0].Text) ?? null,
      messages: row.messages ?? null, request: row.request ?? null,
      response: row.after?.source.payload.Response ?? null,
      output: row.after?.source.payload.Output ?? null}));
    assert.deepEqual(actual, scenario.expected);
    assert.equal(JSON.stringify(source), json);
    for (const row of data.chronology) {
      for (const item of row.events) {
        const index = Number(item.path.split('/').at(-1));
        assert.deepEqual(item.source, source.activity[index]);
      }
    }
  });
}

test('RunState shows only retained evidence and preserves uint64 order', () => {
  const input = {RunId: 'run', History: {revision: 1, entries: [
    {id: 'old-input', Origin: 'user_input', Messages: [message('human', 'Old task')]},
  ]}, Evidence: [
    event('18446744073709551614', 'model_call_finished', {Request: request('Retained'),
      Response: response}, modelIds),
    event('18446744073709551613', 'model_call_started', {Request: request('Retained')}, modelIds),
  ]};
  const data = readRunData({kind: 'state', json: JSON.stringify(input)});
  assert.deepEqual(data.chronology.map(row => ({kind: row.type, sequence: row.order,
    paths: row.events.map(item => item.path), response: row.after.source.payload.Response})),
  [{kind: 'model-step', sequence: '18446744073709551613',
    paths: ['/Evidence/1', '/Evidence/0'], response}]);
});
