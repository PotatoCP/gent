import test from 'node:test';
import assert from 'node:assert/strict';
import {readRunData} from '../web/explorer/data.js';

function event(sequence, type, payload, ids = {}) {
  return {eventSequenceNumber: sequence, type, payload, contextId: 'root', turnId: 'turn',
    attemptId: 'attempt', iteration: 1, ...ids};
}
function request(id, name) {
  return {id, type: 'function', function: {name, arguments: '{}'}};
}
function model(sequence, id, requests, ids = {}) {
  return event(sequence, 'model_call_finished', {Response: {Choices: [{Parts: requests}]}},
    {modelCallId: id, ...ids});
}
function tool(sequence, id, native, name, ids = {}, failed = false) {
  return event(sequence, 'tool_call_finished', {Output: {ToolCallID: id, Name: name,
    Kind: failed ? 'call_error' : 'execution', Error: failed ? {message: 'Unavailable'} : null,
    Response: {ToolCallID: native, Name: name, Parts: []}}}, {toolCallId: id, ...ids});
}
const first = model(1, 'model:1', [request('native:a', 'lookup'), request('native:b', 'charge')]);
const a = tool(5, 'a', 'native:a', 'lookup');
const b = tool(4, 'b', 'native:b', 'charge', {}, true);
const history = [{id: 'entry:1', contextId: 'root', Messages: [
  {Role: 'ai', Parts: [request('native:a', 'lookup'), request('native:b', 'charge')]},
  {Role: 'tool', Metadata: {'gent:tool_call_records': [a.payload.Output, b.payload.Output]}},
]}];

for (const scenario of [
  {name: 'parallel calls from one response remain one item despite reverse completion',
    input: {events: [first,
      event(2, 'tool_call_started', {ToolName: 'lookup'}, {toolCallId: 'a'}),
      event(3, 'tool_call_started', {ToolName: 'charge'}, {toolCallId: 'b'}), b, a]},
    expected: [{order: 2, title: 'Tool calls · 2', status: 'Error',
      calls: [{id: 'a', events: [2, 5]}, {id: 'b', events: [3, 4]}]}]},
  {name: 'nearby calls with no saved relationship stay separate', input: {events: [a, b]},
    expected: [
      {order: 4, title: 'Tool call', status: 'Error', calls: [{id: 'b', events: [4]}]},
      {order: 5, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [5]}]},
    ]},
  {name: 'saved history links calls without the earlier model event',
    input: {events: [a, b], entries: history}, expected: [
      {order: 4, title: 'Tool calls · 2', status: 'Error',
        calls: [{id: 'b', events: [4]}, {id: 'a', events: [5]}]},
    ]},
  {name: 'a reused native ID in another iteration does not merge model responses',
    input: {events: [first, a,
      model(6, 'model:2', [request('native:a', 'lookup')], {iteration: 2}),
      tool(7, 'c', 'native:a', 'lookup', {iteration: 2})]}, expected: [
      {order: 5, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [5]}]},
      {order: 7, title: 'Tool call', status: 'Complete', calls: [{id: 'c', events: [7]}]},
    ]},
  {name: 'ambiguous native IDs do not imply a common response',
    input: {events: [first, model(2, 'model:2', [request('native:a', 'lookup')]), a,
      tool(6, 'c', 'native:a', 'lookup')]}, expected: [
      {order: 5, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [5]}]},
      {order: 6, title: 'Tool call', status: 'Complete', calls: [{id: 'c', events: [6]}]},
    ]},
  {name: 'a later recovery attempt stays separate', input: {events: [first, a,
    tool(6, 'a', 'native:a', 'lookup', {attemptId: 'recovered'})]}, expected: [
    {order: 5, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [5]}]},
    {order: 6, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [6]}]},
  ]},
  {name: 'an unfinished tool is not attached by its name alone', input: {events: [first, a,
    event(6, 'tool_call_started', {ToolName: 'charge'}, {toolCallId: 'b'})]}, expected: [
    {order: 5, title: 'Tool call', status: 'Complete', calls: [{id: 'a', events: [5]}]},
    {order: 6, title: 'Tool call', status: 'No saved completion',
      calls: [{id: 'b', events: [6]}]},
  ]},
]) {
  for (const kind of ['view', 'state']) {
    test(`${kind}: ${scenario.name}`, () => {
      const {events, entries = []} = scenario.input;
      const input = kind === 'view' ?
        {runId: 'run', history: {revision: 1, entries}, activity: events} :
        {RunId: 'run', History: {revision: 1, entries}, Evidence: events};
      const json = JSON.stringify(input);
      const rows = readRunData({kind, json}).chronology.filter(row => row.calls);
      assert.deepEqual(rows.map(row => ({order: row.order, title: row.title, status: row.status,
        calls: row.calls.map(call => ({id: call.callId, events: call.events.map(e => e.order)}))})),
      scenario.expected);
      assert.equal(JSON.stringify(input), json);
    });
  }
}
