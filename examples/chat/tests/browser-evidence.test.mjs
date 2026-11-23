import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp, readFile, readdir, rm} from 'node:fs/promises';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {saveRunEvidence} from './browser-evidence.mjs';

for (const scenario of [
  {name: 'complete saved evidence', input: {status: 200, changeCapture: false}, expected: ''},
  {name: 'capture remaining documents after failed state read',
    input: {status: 500, changeCapture: false}, expected: 'Cannot capture complete saved evidence'},
  {name: 'reject unequal replay', input: {status: 200, changeCapture: true},
    expected: 'AssertionError'},
]) {
  test(scenario.name, async t => {
    const folder = await mkdtemp(join(tmpdir(), 'gent-browser-evidence-'));
    t.after(() => rm(folder, {recursive: true, force: true}));
    const calls = [];
    const expectedFiles = {};
    const request = {get: async url => {
      calls.push(url);
      const parsed = new URL(url);
      const kind = parsed.searchParams.get('kind');
      const path = parsed.searchParams.get('path');
      const name = kind ? `${kind}-${path}` : 'state';
      const body = JSON.stringify(kind ? {encoded: `${kind}:exact saved bytes`,
        decoded: path === 'capture' && scenario.input.changeCapture ? 'changed' : kind} :
        {runId: 'run', head: 12});
      expectedFiles[`test-${name}.json`] = body;
      return {status: () => kind ? 200 : scenario.input.status, text: async () => body};
    }};
    let result, error = '';
    try { result = await saveRunEvidence(request, 'http://example.test', 'run', folder, 'test'); }
    catch (failure) { error = failure.name === 'AssertionError' ? failure.name : failure.message; }
    assert.equal(error, scenario.expected);
    const files = {};
    for (const file of await readdir(folder)) {
      files[file] = await readFile(join(folder, file), 'utf8');
    }
    assert.deepEqual(files, expectedFiles);
    assert.deepEqual(calls, [
      'http://example.test/api/state?runId=run',
      'http://example.test/api/inspect?runId=run&kind=view&path=batches',
      'http://example.test/api/inspect?runId=run&kind=view&path=capture',
      'http://example.test/api/inspect?runId=run&kind=state&path=batches',
      'http://example.test/api/inspect?runId=run&kind=state&path=capture',
    ]);
    assert.deepEqual(result, scenario.expected ? undefined : Object.fromEntries(
      Object.entries(expectedFiles).map(([file, body]) =>
        [file.slice('test-'.length, -'.json'.length), JSON.parse(body)])));
  });
}
