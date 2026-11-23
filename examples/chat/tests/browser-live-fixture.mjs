// Exercise the live browser path with fixed native responses. A handled failure, a later success,
// and nonliteral Markdown answers must pass the same evidence checks used with real providers.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdir, mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';

const folder = process.env.CHAT_TEST_OUTPUT_DIR ||
  await mkdtemp(join(tmpdir(), 'gent-live-fixture-'));
await mkdir(folder, {recursive: true});
const answers = ['  **Customer found.** John Smith is customer C001.  ',
  'Your saved code is **Cedar-742**.'];
const requests = [];
const server = createServer(async (request, response) => {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  requests.push(JSON.parse(Buffer.concat(chunks)));
  const index = requests.length;
  const args = index === 1 ? {customer_id: 'missing-customer', email: 'john.smith@email.com'} :
    {email: 'john.smith@email.com'};
  const parts = index <= 2 ? [{functionCall: {name: 'get_customer_info', args}}] :
    [{text: answers[index - 3]}];
  response.writeHead(200, {'Content-Type': 'text/event-stream'});
  response.write(`data: ${JSON.stringify({candidates: [{index: 0,
    content: {role: 'model', parts}}]})}\n\n`);
  setTimeout(() => response.end(`data: ${JSON.stringify({candidates: [{index: 0,
    content: {role: 'model', parts: []}, finishReason: 'STOP'}]})}\n\n`), 1000);
});
server.listen(0, '127.0.0.1');
await once(server, 'listening');
try {
  const child = spawn(process.execPath, [new URL('./browser.mjs', import.meta.url).pathname], {
    env: {...process.env, CHAT_TEST_OUTPUT_DIR: folder, CHAT_TEST_PROVIDER: 'gemini',
      GENT_TEST_GEMINI_KEY: 'local-fixture', GENT_TEST_GEMINI_MODEL: 'local-fixture',
      GENT_TEST_GEMINI_URL: `http://127.0.0.1:${server.address().port}`},
    stdio: 'inherit',
  });
  const [code] = await once(child, 'exit');
  await writeFile(join(folder, 'native-requests.json'), JSON.stringify(requests, null, 2));
  assert.equal(code, 0);
  assert.equal(requests.length, 4);
  const saved = JSON.parse(await readFile(join(folder, 'gemini-second-state.json'), 'utf8'));
  const outcomes = saved.events.flatMap(event => event.items || []).filter(item =>
    item.kind === 'tool' && item.status !== 'running');
  assert.deepEqual(outcomes.map(item => item.status), ['attention', 'complete']);
  assert.equal(new Set(outcomes.map(item => item.id)).size, 2);
  assert.equal(saved.lastAnswer, answers[1]);
  const prompts = saved.events.flatMap(event => event.items || [])
    .filter(item => item.kind === 'user').map(item => item.text);
  assert.deepEqual(requests[3].contents.filter(message => message.role === 'user')
    .flatMap(message => message.parts.filter(part => Object.hasOwn(part, 'text')))
    .map(part => part.text), prompts);
  console.log('PASS live fixture: handled failure, later success, complete answers, native input');
} finally {
  server.closeAllConnections();
  await new Promise(resolve => server.close(resolve));
}
