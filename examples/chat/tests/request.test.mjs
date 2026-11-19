import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequest, RequestStatus} from '../web/request.js';

const cases = [
  {name: 'ready', input: {url: '/api/state?runId=run', status: 200, data: {head: 7}},
    expected: {result: {status: RequestStatus.Ready, data: {head: 7}}, missing: [], error: ''}},
  {name: 'missing saved URL', input: {url: '/api/state?runId=old', status: 404,
    data: {detail: 'Conversation not found.'}},
    expected: {result: {status: RequestStatus.MissingRun}, missing: ['old'], error: ''}},
  {name: 'missing run on submit', input: {url: '/api/action', status: 404,
    body: {runId: 'old', action: 'turn', prompt: 'Keep this draft.'}, data: {detail: 'Missing'}},
    expected: {result: {status: RequestStatus.MissingRun}, missing: ['old'], error: ''}},
  {name: 'missing inspection', input: {url: '/api/inspect?kind=state&runId=old', status: 404,
    data: {detail: 'Missing'}},
    expected: {result: {status: RequestStatus.MissingRun}, missing: ['old'], error: ''}},
  {name: 'restore failure is not missing history', input: {url: '/api/state?runId=run', status: 500,
    data: {detail: 'Saved conversation cannot be restored.'}},
    expected: {result: undefined, missing: [], error: 'Saved conversation cannot be restored.'}},
  {name: 'unknown endpoint is not a missing run', input: {url: '/api/config', status: 404,
    data: {detail: 'Unknown endpoint.'}},
    expected: {result: undefined, missing: [], error: 'Unknown endpoint.'}},
];
for (const scenario of cases) {
  test(scenario.name, async t => {
    const input = scenario.input;
    const previous = globalThis.location;
    globalThis.location = new URL('http://localhost/');
    t.after(() => { globalThis.location = previous; });
    t.mock.method(globalThis, 'fetch', async (url, options) => {
      assert.equal(url, input.url);
      assert.deepEqual(options, input.body ? {method: 'POST',
        headers: {'Content-Type': 'application/json'}, body: JSON.stringify(input.body)} : {});
      return new Response(JSON.stringify(input.data), {status: input.status});
    });
    const missing = [];
    const request = createRequest(runId => missing.push(runId));
    let result, error = '';
    try { result = await request(input.url, input.body); }
    catch (failure) { error = failure.message; }
    assert.deepEqual({result, missing, error}, scenario.expected);
  });
}
