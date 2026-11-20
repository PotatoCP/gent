import test from 'node:test';
import assert from 'node:assert/strict';
import {compareRequestMessages} from '../web/explorer/request-diff.js';

const message = (text, extra = {}) => ({Role: 'human', Parts: [{Text: text}], ...extra});
const first = message('First task');
const second = message('Next task');
const system = message('Instructions', {Role: 'system'});
const tool = {Role: 'tool', Parts: [{ToolCallID: 'call:1', Name: 'lookup', Parts: [{Text: 'OK'}]}]};
let deep = 'value';
for (let index = 0; index < 70; index++) deep = {child: deep};

for (const scenario of [
  {name: 'first request shows all messages', input: {current: [system, first], previous: []},
    expected: {repeated: [false, false], repeatedCount: 0, shownCount: 2}},
  {name: 'new tool results and tasks follow repeated context',
    input: {current: [system, first, tool, second], previous: [system, first]},
    expected: {repeated: [true, true, false, false], repeatedCount: 2, shownCount: 2}},
  {name: 'changed system prompt remains visible',
    input: {current: [message('New instructions', {Role: 'system'}), first],
      previous: [system, first]},
    expected: {repeated: [false, true], repeatedCount: 1, shownCount: 1}},
  {name: 'same text with changed metadata remains visible',
    input: {current: [message('First task', {Metadata: {origin: 'updated'}})], previous: [first]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'native continuation changes are not hidden',
    input: {current: [message('First task', {Continuation: {Data: 'new native data'}})],
      previous: [message('First task', {Continuation: {Data: 'old native data'}})]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'same text with changed role remains visible',
    input: {current: [message('First task', {Role: 'ai'})], previous: [first]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'a new call ID makes the same tool output new content',
    input: {current: [{Role: 'tool', Parts: [{...tool.Parts[0], ToolCallID: 'call:2'}]}],
      previous: [tool]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'inserted messages keep the following context collapsed',
    input: {current: [system, second, first], previous: [system, first]},
    expected: {repeated: [true, false, true], repeatedCount: 2, shownCount: 1}},
  {name: 'an identical follow-up is still a new occurrence',
    input: {current: [system, first, first], previous: [system, first]},
    expected: {repeated: [true, true, false], repeatedCount: 2, shownCount: 1}},
  {name: 'moved content remains visible',
    input: {current: [second, first], previous: [first, second]},
    expected: {repeated: [true, false], repeatedCount: 1, shownCount: 1}},
  {name: 'object field order does not change the message',
    input: {current: [{Parts: [{Text: 'First task'}], Role: 'human'}], previous: [first]},
    expected: {repeated: [true], repeatedCount: 1, shownCount: 0}},
  {name: 'compaction leaves only retained matches',
    input: {current: [system, second, message('Summary')], previous: [system, first, second]},
    expected: {repeated: [true, true, false], repeatedCount: 2, shownCount: 1}},
  {name: 'unsafe displayed integers cannot prove identical original JSON',
    input: {current: [message('Same', {Metadata: {id: '18446744073709551615'}})],
      previous: [message('Same', {Metadata: {id: '18446744073709551615'}})]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'rounded numbers cannot prove identical original JSON',
    input: {current: [message('Same', {Metadata: {score: 0.5}})],
      previous: [message('Same', {Metadata: {score: 0.5}})]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
  {name: 'deep content stays visible without unbounded comparison recursion',
    input: {current: [message('Same', {Metadata: deep})],
      previous: [message('Same', {Metadata: deep})]},
    expected: {repeated: [false], repeatedCount: 0, shownCount: 1}},
]) {
  test(scenario.name, () => {
    const before = structuredClone(scenario.input);
    assert.deepEqual(compareRequestMessages(scenario.input.current, scenario.input.previous),
      scenario.expected);
    assert.deepEqual(scenario.input, before);
  });
}
