import test from 'node:test';
import assert from 'node:assert/strict';
import {counterRows, filterRows, parseInspectionJSON, readRunData} from '../web/explorer/data.js';

for (const scenario of [
  {name: 'normal values', input: '{"n":7,"text":"123","flag":true,"none":null}',
    expected: {n: 7, text: '123', flag: true, none: null}},
  {name: 'maximum event position', input: '{"n":18446744073709551615,"safe":9007199254740991}',
    expected: {n: '18446744073709551615', safe: 9007199254740991}},
  {name: 'negative integer and escaped text',
    input: String.raw`{"n":-9007199254740992,"text":"Say \"18446744073709551615\"."}`,
    expected: {n: '-9007199254740992', text: 'Say "18446744073709551615".'}},
]) {
  test(`inspect numbers: ${scenario.name}`, () => {
    assert.deepEqual(parseInspectionJSON(scenario.input), scenario.expected);
  });
}

const history = {
  revision: 2,
  entries: [
    {id: 'first', order: 1, contextId: 'root', Origin: 'user_input',
      Messages: [{Role: 'human', Parts: [{Text: 'Original request'}]}]},
    {id: 'correction', order: 2, contextId: 'root', Origin: 'output_correction',
      Messages: [{Role: 'human', Parts: [{Text: 'Use valid JSON'}]}]},
    {id: 'later', order: 3, contextId: 'root', Origin: 'user_input',
      Messages: [{Role: 'human', Parts: [{Text: 'Later request'}]}]},
  ],
  changes: [
    {previousRevision: 0, revision: 1, selections: [{contextId: 'root', active: ['first']}]},
    {previousRevision: 1, revision: 2,
      selections: [{contextId: 'root', active: ['later', 'correction']}], reason: 'compaction'},
  ],
};
const checkpoint = {contexts: [{contextId: 'root', turnOpen: true,
  instructions: [{Role: 'system', Parts: [{Text: 'Use tools'}]}]}]};
for (const kind of ['view', 'state']) {
  test(`${kind}: saved origin, selection order, and source paths`, () => {
    const input = kind === 'view' ? {runId: 'run', lastAppliedEventSequenceNumber: 8,
      history, checkpoint} : {RunId: 'run', EventSequenceNumber: 8, History: history,
      Checkpoint: checkpoint};
    const json = JSON.stringify(input);
    const result = readRunData({kind, json});
    const base = kind === 'state' ? '/History' : '/history';
    assert.deepEqual({head: result.head, all: result.messages.map(row => row.text),
      selected: result.selectedMessages.map(row => row.text),
      roles: result.messages.map(row => row.title),
      active: result.messages.map(row => row.active),
      path: result.messages[1].path, matches: filterRows(result.messages, 'LATER REQUEST')
        .map(row => row.entry.id)},
    {head: 8, all: ['Use tools', 'Original request', 'Use valid JSON', 'Later request'],
      selected: ['Use tools', 'Later request', 'Use valid JSON'],
      roles: ['Instructions', 'User', 'Guidance', 'User'],
      active: [true, false, true, true], path: `${base}/entries/0/Messages/0`, matches: ['later']});
    assert.equal(JSON.stringify(input), json);
  });
}

test('counter differences keep full integer precision', () => {
  const input = {contexts: [{contextId: 'root', counters: {
    calls: '18446744073709551615', iterations: 8,
  }, turnCounterBaselines: {calls: '18446744073709551612', iterations: 6}}]};
  assert.deepEqual(counterRows(input), [
    {contextId: 'root', name: 'calls', run: '18446744073709551615', turn: '3'},
    {contextId: 'root', name: 'iterations', run: '8', turn: '2'},
  ]);
});

for (const scenario of [
  {name: 'unknown kind', input: {kind: 'event', json: '{}'},
    expected: 'Select RunState or RunView.'},
  {name: 'null document', input: {kind: 'state', json: 'null'},
    expected: 'Pass JSON of a recovered RunState or RunView, from Go json.Marshal.'},
  {name: 'raw codec bytes are not diagnostic JSON',
    input: {kind: 'state', json: '{"schemaVersion":7,"digest":"x","state":{}}'},
    expected: 'Pass JSON of a recovered RunState or RunView, from Go json.Marshal.'},
]) {
  test(scenario.name, () => {
    let error;
    try { readRunData(scenario.input); } catch (failure) { error = failure.message; }
    assert.equal(error, scenario.expected);
  });
}
