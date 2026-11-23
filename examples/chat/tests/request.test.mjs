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

// A request from an old chat cannot restore its UI after selection changes. A reset also
// changes the write identity, so a delayed first prompt cannot create a conversation afterward.
for (const operation of ['select', 'reset']) {
  test(`fence pending responses after ${operation}`, async t => {
    const previous = globalThis.location;
    globalThis.location = new URL('http://localhost/');
    t.after(() => { globalThis.location = previous; });
    const missing = [], resets = [];
    const request = createRequest(id => missing.push(id), () => resets.push('reset'));
    let resolveOld;
    const headers = {'X-Gent-Server': 'first'};
    const calls = [];
    t.mock.method(globalThis, 'fetch', async (url, options) => {
      calls.push({url, options});
      if (url === '/api/old') return new Promise(resolve => { resolveOld = resolve; });
      if (url === '/api/reset') return new Response('{}', {
        headers: {'X-Gent-Server': 'second'},
      });
      return new Response('{}', {headers});
    });
    await request('/api/config');
    const pending = request('/api/old', {runId: 'old'});
    if (operation === 'reset') {
      assert.deepEqual(await request('/api/reset', {}), {status: RequestStatus.ServerReset});
    } else request.invalidate();
    resolveOld(new Response('{}', {status: 404, headers}));
    assert.deepEqual(await pending, {status: RequestStatus.Superseded});
    assert.deepEqual(missing, []);
    assert.deepEqual(resets, operation === 'reset' ? ['reset'] : []);
    // Use the retained identity for the next write, not the identity from the stale response.
    headers['X-Gent-Server'] = operation === 'reset' ? 'second' : 'first';
    await request('/api/action', {action: 'turn'});
    assert.deepEqual(calls.at(-1), {url: '/api/action', options: {method: 'POST',
      headers: {'Content-Type': 'application/json', 'X-Gent-Server': headers['X-Gent-Server']},
      body: '{"action":"turn"}'}});
  });
}
