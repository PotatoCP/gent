import test from 'node:test';
import assert from 'node:assert/strict';
import {Delivery, emptyState, resetState, receive, messages, chatMessages,
  eventPage} from '../web/state.js';

function event(eventSequenceNumber, text, attemptId = 'a', type = 'final_answer_candidate',
  delta = true) {
  return {runId: 'run', turnId: 'turn', attemptId, eventSequenceNumber, name: 'stream',
    items: [{id: 'message', label: 'Original', text, type, delta}]};
}

test('accepted events fill gaps, replace previews, and ignore retry delivery', () => {
  const state = emptyState();
  receive(state, Delivery.Live, event(1, 'wrong'));
  receive(state, Delivery.Accepted, event(2, 'second'));
  assert.equal(state.head, 0);
  receive(state, Delivery.Accepted, event(1, 'first '));
  receive(state, Delivery.Accepted, event(1, 'first '));
  assert.equal(state.head, 2);
  assert.deepEqual(messages(state), [{id: 'message', label: 'Original', text: 'first second',
    type: 'final_answer_candidate', delta: true, accepted: true, eventSequenceNumber: 2}]);
});

test('restoration removes lost previews and permits reused unsaved positions', () => {
  let state = emptyState();
  receive(state, Delivery.Live, event(1, 'lost', 'old'));
  state = resetState({runId: 'run', head: 0, events: [], previews: [], attemptId: 'new'});
  receive(state, Delivery.Live, event(1, 'replacement', 'new'));
  receive(state, Delivery.Live, event(1, 'replacement', 'new'));
  assert.deepEqual(messages(state), [{id: 'message', label: 'Original', text: 'replacement',
    type: 'final_answer_candidate', delta: true, accepted: false, eventSequenceNumber: 1}]);
});

test('complete native text replaces deltas; transformed accepted text keeps its source', () => {
  const state = emptyState();
  receive(state, Delivery.Accepted, event(1, 'draft'));
  receive(state, Delivery.Accepted, event(2, 'Original complete text', 'a',
    'final_answer_candidate', false));
  const answer = event(3, 'Saved answer: Transformed text', 'a', 'final_answer', false);
  answer.items[0].id = 'accepted';
  answer.items[0].sources = ['message'];
  receive(state, Delivery.Live, answer);
  assert.equal(messages(state).length, 1);
  receive(state, Delivery.Accepted, answer);
  assert.deepEqual(messages(state), [
    {id: 'message', label: 'Original', text: 'Original complete text',
      type: 'final_answer_candidate', delta: false, accepted: true, eventSequenceNumber: 2},
    {id: 'accepted', label: 'Original', text: 'Saved answer: Transformed text',
      type: 'final_answer', delta: false, sources: ['message'], accepted: true,
      eventSequenceNumber: 3},
  ]);
});

test('chat replaces only accepted candidates and keeps their source in the inspector', () => {
  const state = emptyState();
  const original = event(1, 'Original text', 'a', 'final_answer_candidate', false);
  receive(state, Delivery.Accepted, original);
  const answer = event(2, 'Processed text', 'a', 'final_answer', false);
  answer.items[0].id = 'accepted';
  answer.items[0].sources = ['message'];
  receive(state, Delivery.Live, answer);
  assert.deepEqual(chatMessages(state), messages(state));
  receive(state, Delivery.Accepted, answer);
  assert.deepEqual(chatMessages(state), [{id: 'accepted', label: 'Original', text: 'Processed text',
    type: 'final_answer', delta: false, sources: ['message'], accepted: true,
    eventSequenceNumber: 2}]);
  assert.deepEqual([...state.receipts.values()].map(row =>
    [row.delivery, row.event.eventSequenceNumber]),
    [['accepted', 1], ['live', 2], ['accepted', 2]]);
});

test('tool results keep the input and technical progress stays in the inspector', () => {
  const state = emptyState();
  const before = {...event(1, ''), items: [{id: 'tool', kind: 'tool', label: 'get_customer_info',
    text: '', args: '{"email":"john.smith@email.com"}', status: 'running'}]};
  const after = {...event(2, ''), items: [{id: 'tool', kind: 'tool', label: 'get_customer_info',
    text: 'John Smith found.', status: 'complete'}]};
  const activity = {...event(3, ''), items: [{id: 'model', kind: 'activity', label: 'Model call',
    text: 'Response received'}]};
  for (const record of [before, after, activity]) receive(state, Delivery.Accepted, record);
  assert.deepEqual(chatMessages(state), [{id: 'tool', kind: 'tool', label: 'get_customer_info',
    text: 'John Smith found.', args: '{"email":"john.smith@email.com"}', status: 'complete',
    accepted: true, eventSequenceNumber: 2}]);
});

test('reconnect preserves delivery evidence without restoring lost previews into chat', () => {
  let state = emptyState();
  const old = event(1, 'lost', 'old');
  receive(state, Delivery.Live, old);
  receive(state, Delivery.Live, old);
  state = resetState({runId: 'run', head: 0, events: [], previews: [], attemptId: 'new'}, state);
  const replacement = event(1, 'replacement', 'new');
  receive(state, Delivery.Accepted, replacement);
  assert.deepEqual([...state.receipts.values()], [
    {key: 'old/1/live', delivery: 'live', event: old, count: 2, source: 'stream'},
    {key: 'new/1/accepted', delivery: 'accepted', event: replacement, count: 1, source: 'stream'},
  ]);
  assert.deepEqual(chatMessages(state), [{id: 'message', label: 'Original', text: 'replacement',
    type: 'final_answer_candidate', delta: true, accepted: true, eventSequenceNumber: 1}]);
  state = resetState({runId: 'another', head: 0, events: [], previews: []}, state);
  assert.deepEqual([...state.receipts], []);
});

test('pagination exposes every event, including older positions and separate live delivery', () => {
  const state = emptyState();
  for (let eventSequenceNumber = 1; eventSequenceNumber <= 120; eventSequenceNumber++) {
    receive(state, Delivery.Live, event(eventSequenceNumber, 'text'));
    receive(state, Delivery.Accepted, event(eventSequenceNumber, 'text'));
  }
  const actual = [];
  for (let page = 0; page < 8; page++) {
    const result = eventPage(state, '', '', page);
    assert.deepEqual({total: result.total, page: result.page, pages: result.pages},
      {total: 240, page, pages: 8});
    actual.push(...result.rows.map(row => row.key));
  }
  assert.deepEqual(actual, [...state.receipts.keys()].reverse());
  assert.deepEqual(eventPage(state, 'missing', '', 8), {rows: [], total: 0, page: 0, pages: 1});
  const filtered = eventPage(state, 'turn', 'accepted', 20);
  assert.deepEqual({total: filtered.total, page: filtered.page, pages: filtered.pages},
    {total: 120, page: 3, pages: 4});
  assert.deepEqual(filtered.rows.map(row => row.event.eventSequenceNumber),
    Array.from({length: 30}, (_, index) => 30 - index));
});
