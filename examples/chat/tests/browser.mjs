import assert from 'node:assert/strict';
import {execFileSync, spawn} from 'node:child_process';
import {once} from 'node:events';
import {mkdir, mkdtemp, rm} from 'node:fs/promises';
import {createRequire} from 'node:module';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {checkAdditionalEvidence, checkChronology, checkFullscreen,
  checkRawTabs, checkRequestPaging} from './browser-chronology.mjs';
import {checkToolGroups} from './browser-tool-groups.mjs';
import {checkChatTranscript, watchLivePreview} from './browser-chat.mjs';
import {checkDisclosureHierarchy} from './browser-disclosures.mjs';

const require = createRequire(new URL('../../../docs/package.json', import.meta.url));
const {chromium, expect} = require('playwright/test');
const folder = await mkdtemp(join(tmpdir(), 'gent-chat-browser-'));
const binary = join(folder, 'chat');
const shots = fileURLToPath(new URL('../screenshots/', import.meta.url));
await mkdir(shots, {recursive: true});
const provider = process.env.CHAT_TEST_PROVIDER || 'demo';
const live = provider !== 'demo';
let child, browser;
let logs = '';
const errors = [];

async function start() {
  child = spawn(binary, ['-addr', '127.0.0.1:0', '-provider', provider], {env: process.env});
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`Server start timeout: ${logs}`)), 15000);
    child.stderr.on('data', chunk => {
      logs += chunk;
      const match = String(chunk).match(/LISTEN (http:\/\/[^ ]+)/);
      if (match) { clearTimeout(timer); resolve(match[1]); }
    });
    child.on('exit', code => {
      clearTimeout(timer);
      reject(new Error(`Server exit ${code}: ${logs}`));
    });
  });
}

async function stop() {
  if (!child || child.exitCode !== null) return;
  const done = once(child, 'exit');
  child.kill('SIGTERM');
  await done;
}

try {
  execFileSync('go', ['build', '-o', binary, '.'], {
    cwd: fileURLToPath(new URL('../', import.meta.url)), env: process.env,
  });
  const url = await start();
  browser = await chromium.launch({headless: true, args: ['--no-sandbox'],
    executablePath: process.env.CHROME_PATH || '/opt/google/chrome/chrome'});
  const page = await browser.newPage({viewport: {width: 1440, height: 1000}});
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(`${message.text()} ${message.location().url}`);
  });
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], {origin: url});
  await page.goto(url);
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator('#empty')).toHaveCSS('opacity', '1');
  await expect(page.locator('.agent-avatar, .welcome-mark')).toHaveCount(0);
  await page.screenshot({path: join(shots, `${provider}-start.png`), fullPage: true});
  // Drag and keyboard resizing must keep both panes usable and survive a refresh.
  const resize = page.getByRole('separator', {name: 'Resize inspector'});
  const box = await resize.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + 150);
  await page.mouse.down();
  await page.mouse.move(1440 - 640, box.y + 150, {steps: 8});
  await page.mouse.up();
  await expect(page.locator('#inspector')).toHaveCSS('width', '640px');
  await resize.press('ArrowLeft');
  await expect(page.locator('#inspector')).toHaveCSS('width', '672px');
  await resize.press('ArrowRight');
  await expect(page.locator('#inspector')).toHaveCSS('width', '640px');
  await resize.press('End');
  await expect(page.locator('#inspector')).toHaveCSS('width', '1120px');
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await resize.press('Home');
  await expect(page.locator('#inspector')).toHaveCSS('width', '280px');
  const narrow = await resize.boundingBox();
  await page.mouse.move(narrow.x + narrow.width / 2, narrow.y + 150);
  await page.mouse.down();
  await page.mouse.move(1440 - 640, narrow.y + 150, {steps: 8});
  await page.mouse.up();
  await page.reload();
  await expect(page.locator('#inspector')).toHaveCSS('width', '640px');
  const config = await (await page.request.get(`${url}/api/config`)).json();
  await expect(page.locator('#agent-name')).toHaveText('SkyWings Airlines');
  await page.locator('#suggestion').click();
  await expect(page.locator('#prompt')).toHaveValue(config.scenarios[0].prompt);
  const first = live ? 'Find John Smith, john.smith@email.com. Remember my code Cedar-742. ' +
    'Use get_customer_info, then reply with exactly: Customer checked.' :
    config.scenarios[0].prompt;
  await page.locator('#prompt').fill(first);
  const checkPreview = await watchLivePreview(page);
  await page.locator('#prompt').press('Enter');
  await checkPreview();
  if (!live) {
    const reasoning = page.locator('.message.reasoning').first();
    await expect(reasoning.locator('details')).toHaveCount(0);
    await expect(reasoning.locator('.message-body')).toHaveText(
      'The saved lookup supplies the customer facts. Earlier user input is still present.');
    await expect(reasoning.locator('.message-body')).toBeVisible();
    await page.screenshot({path: join(shots, 'demo-streaming.png'), fullPage: true});
  }
  // Refresh reads accepted bytes and reconciles previews while execution continues.
  await page.reload();
  await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 120000});
  const runId = new URL(page.url()).searchParams.get('run');
  assert.ok(runId);
  const group = page.locator('.tool-group-toggle').first();
  await expect(group.locator(':scope > summary')).toHaveText('Ran tools');
  await expect(group).not.toHaveAttribute('open');
  await group.locator(':scope > summary').click();
  const tool = page.locator('.message.tool').first();
  await expect(tool.locator('.tool-name')).toHaveText('get_customer_info');
  await expect(tool.locator('.tool-status')).toHaveText('Complete');
  await tool.locator('summary').click();
  await expect(tool.locator('.tool-args')).toHaveText(
    '{"customer_id":"","email":"john.smith@email.com"}');
  assert.deepEqual(JSON.parse(await tool.locator('.message-body').innerText()), {
    id: 'C001', name: 'John Smith', email: 'john.smith@email.com', phone: '+1-555-0123',
    frequent_flyer_tier: 'gold', booking_ids: ['BK001', 'BK002'],
  });
  const answers = page.locator('.message[data-type="final_answer"]');
  await expect(answers).toHaveCount(1);
  await expect(answers.locator('.message-body')).toHaveText(live ? 'Customer checked.' :
    'I found John Smith (C001), using john.smith@email.com.');
  await expect(page.locator('.message[data-type="final_answer_candidate"]')).toHaveCount(0);
  if (!live) {
    await expect(page.locator('.message.commentary')).toHaveText(
      "I'll check the customer record first.");
    const reasoning = page.locator('.message.reasoning').first();
    await expect(reasoning.locator('.message-body')).toHaveText(
      'The saved lookup supplies the customer facts. Earlier user input is still present.');
    await expect(reasoning.locator('.message-body')).toBeVisible();
  }
  await answers.locator('.source-link').click();
  await expect(page.locator('#event-detail')).toBeVisible();
  await expect(page.locator('#event-json > details')).toBeVisible();
  await page.locator('#event-search').fill('');
  await page.locator('#event-delivery').selectOption('');
  await page.locator('#prompt').fill(live ? 'Reply with only the code I asked you to remember.' :
    'What was my first message and the earlier lookup?');
  await page.locator('#send').click();
  await expect(answers).toHaveCount(2, {timeout: 120000});
  await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 120000});
  assert.equal(new URL(page.url()).searchParams.get('run'), runId);
  await expect(answers.last().locator('.message-body')).toHaveText(live ? 'Cedar-742' :
    `Your first message was: ${first} ` +
    'Earlier lookup: I found John Smith (C001), using john.smith@email.com.');
  const accepted = await (await page.request.get(`${url}/api/state?runId=${runId}`)).json();
  // Inspection reads both complete archive data and replay state without starting a worker.
  for (const kind of ['view', 'state']) {
    await page.locator(`#tab-${kind}`).click();
    await expect(page.locator('#data-meta')).toHaveText(`Accepted event ${accepted.head} · ` +
      `${accepted.batchCount} batches · Current saved state`);
    const response = await page.request.get(`${url}/api/inspect?runId=${runId}` +
      `&kind=${kind}&path=capture`);
    const inspected = await response.json();
    assert.equal(inspected.head, accepted.head);
    const decoded = JSON.parse(inspected.decoded);
    assert.equal(kind === 'view' ? decoded.runId : decoded.RunId, runId);
    assert.equal(kind === 'view' ? decoded.lastAppliedEventSequenceNumber :
      decoded.EventSequenceNumber, accepted.head);
    const explorer = page.getByRole('region', {name: 'Run data explorer'});
    await expect(explorer).toBeVisible();
    await expect(explorer.locator('h4')).toHaveText(kind === 'state' ?
      'Execution state' : 'Accepted archive');
    await checkChronology(page, expect, kind, decoded, live);
    if (kind === 'view') {
      await checkFullscreen(page, expect, join(shots, `${provider}-chronology-fullscreen.png`));
    }
    const nav = explorer.getByRole('navigation', {name: 'Explore saved data'});
    await nav.getByRole('button', {name: 'Messages', exact: true}).click();
    await explorer.getByRole('searchbox', {name: 'Search saved records'})
      .fill(first.split('\n')[0]);
    const userRow = explorer.locator('.ge-row').filter({
      has: page.getByText('User', {exact: true})});
    await expect(userRow).toHaveCount(1);
    await userRow.click();
    await expect(explorer.locator('.ge-content')).toHaveText(first);
    await explorer.locator('.ge-source > summary').click();
    await expect(explorer.locator('.ge-path')).toHaveText(
      `${kind === 'view' ? '/history' : '/History'}/entries/0/Messages/0`);
    await nav.getByRole('button', {name: 'Tools', exact: true}).click();
    await expect(explorer.locator('.ge-row')).toHaveCount(1);
    await explorer.locator('.ge-row').click();
    await expect(explorer.locator('.ge-detail h5')).toHaveText([
      'get_customer_info', 'Tool input', 'Tool result']);
    await expect(explorer.locator('.json-tree').first().locator('.json-leaf')).toHaveText([
      'email: "john.smith@email.com"', 'frequent_flyer_tier: "gold"', 'id: "C001"',
      'name: "John Smith"', 'phone: "+1-555-0123"',
    ]);
    await nav.getByRole('button', {name: 'Activity', exact: true}).click();
    await expect(explorer.locator('.ge-row')).toHaveCount(kind === 'view' ? 12 : 0);
    if (kind === 'view') {
      await explorer.getByRole('button', {name: 'Next records'}).click();
      await explorer.locator('.ge-row').first().click();
      await expect(explorer.locator('.ge-detail h5')).toHaveText(
        decoded.activity[12].type.replaceAll('_', ' '));
    }
    await nav.getByRole('button', {name: 'State', exact: true}).click();
    await expect(explorer.locator('.ge-state h5')).toHaveText([
      'Saved position', 'Last completed answer', 'Counters', 'Custom data & continuation']);
    await expect(explorer.locator('.ge-content')).toHaveText(
      await answers.last().locator('.message-body').innerText());
    await nav.getByRole('button', {name: 'Messages', exact: true}).click();
    await checkRawTabs(page, expect, explorer, inspected);
    await page.locator('.inspector-body').evaluate(node => { node.scrollTop = 0; });
    await page.screenshot({path: join(shots, `${provider}-${kind}-inspection.png`),
      fullPage: true});
  }
  // The copied component runs on its own page, with a file and no chat API requests.
  const standalone = await browser.newPage({viewport: {width: 1050, height: 1050}});
  standalone.on('pageerror', error => errors.push(error.message));
  standalone.on('console', message => {
    if (message.type() === 'error') errors.push(`${message.text()} ${message.location().url}`);
  });
  const apiRequests = [];
  standalone.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/')) apiRequests.push(request.url());
  });
  await standalone.goto(`${url}/explorer/demo.html`);
  await standalone.context().grantPermissions(['clipboard-read', 'clipboard-write'], {origin: url});
  const savedView = await (await page.request.get(`${url}/api/inspect?runId=${runId}` +
    '&kind=view&path=capture')).json();
  await checkDisclosureHierarchy(browser, url, expect, savedView.decoded, shots, errors);
  await standalone.locator('#file').setInputFiles({name: 'recovered-view.json',
    mimeType: 'application/json', buffer: Buffer.from(savedView.decoded)});
  await expect(standalone.locator('.gent-run-explorer h4')).toHaveText('Accepted archive');
  await standalone.getByRole('button', {name: 'Encoded Saved State', exact: true}).click();
  await expect(standalone.locator('.ge-raw .ge-empty')).toHaveText(
    'No encoded saved state was supplied to this inspector.');
  await standalone.locator('#encoded-file').setInputFiles({name: 'encoded-view.json',
    mimeType: 'application/json', buffer: Buffer.from(savedView.encoded)});
  await checkRawTabs(standalone, expect, standalone.locator('.gent-run-explorer'), savedView);
  await standalone.getByRole('button', {name: 'Tools', exact: true}).click();
  await standalone.locator('.ge-row').click();
  await expect(standalone.locator('.ge-detail h5').first()).toHaveText('get_customer_info');
  await standalone.screenshot({path: join(shots, `${provider}-explorer-tools.png`),
    fullPage: true});
  await checkAdditionalEvidence(standalone, expect, savedView.decoded);
  await checkRequestPaging(standalone, expect, savedView.decoded);
  await checkToolGroups(standalone, expect, savedView.decoded,
    join(shots, `${provider}-tool-group.png`));
  await standalone.locator('#file').setInputFiles({name: 'invalid.json',
    mimeType: 'application/json', buffer: Buffer.from('{}')});
  await expect(standalone.locator('#notice')).toHaveText(
    'Pass JSON of a recovered RunState or RunView, from Go json.Marshal.');
  await expect(standalone.locator('.ge-detail h5').first()).toHaveText('get_customer_info');
  // Instance state and lifecycle stay local. Unsafe content must stay text in both instances.
  const isolated = await standalone.evaluate(async json => {
    const {createRunExplorer} = await import('/explorer/index.js');
    const source = JSON.parse(json);
    source.history.entries[0].Messages[0].Parts = [{Text: '<img src=x onerror="alert(1)">'}];
    const host = document.createElement('div'); document.body.append(host);
    const selected = [];
    const explorer = createRunExplorer(host, {kind: 'view', json: JSON.stringify(source)}, {
      onSelect: record => selected.push({kind: record.kind, path: record.path}),
    });
    [...host.querySelectorAll('.ge-nav button')].find(button =>
      button.textContent === 'Messages').click();
    const rows = host.querySelectorAll('.ge-row'); rows[1].click();
    const result = {instances: document.querySelectorAll('.gent-run-explorer').length,
      content: host.querySelector('.ge-content').textContent,
      images: host.querySelectorAll('img').length, selected};
    explorer.update({kind: 'view', json});
    result.updated = host.querySelector('.ge-content').textContent;
    explorer.destroy(); result.remaining = host.children.length; host.remove();
    return result;
  }, savedView.decoded);
  assert.deepEqual(isolated, {instances: 2, content: '<img src=x onerror="alert(1)">', images: 0,
    selected: [{kind: 'view', path: '/history/entries/0/Messages/0'}],
    updated: first, remaining: 0});
  await standalone.setViewportSize({width: 390, height: 850});
  assert.equal(await standalone.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
    true);
  await standalone.screenshot({path: join(shots, `${provider}-explorer-mobile.png`),
    fullPage: true});
  assert.deepEqual(apiRequests, []);
  await standalone.close();
  await page.locator('#tab-events').click();
  // Earlier events remain reachable. Pagination only bounds DOM work, not retained evidence.
  await page.locator('#event-delivery').selectOption('accepted');
  while (await page.locator('#events-next').isEnabled()) await page.locator('#events-next').click();
  const firstEvent = page.locator(
    '.event-row[data-event-sequence-number="1"][data-delivery="accepted"]');
  await firstEvent.click();
  await expect(firstEvent).toHaveAttribute('aria-pressed', 'true');
  const firstTitle = await page.locator('#event-title').textContent();
  assert.equal(firstTitle, `#1 · ${accepted.events[0].name}`);
  await page.locator('#event-search').fill('no-such-event');
  await expect(page.locator('#events-empty')).toHaveText('No matching events.');
  await page.locator('#event-search').fill('');
  await page.locator('#event-delivery').selectOption('');
  const stillSaved = await (await page.request.get(`${url}/api/state?runId=${runId}`)).json();
  assert.deepEqual(stillSaved, accepted);
  for (const path of ['batches', 'capture']) {
    await page.locator('#path').selectOption(path);
    await page.locator('#restore').click();
    await expect(page.locator('#notice')).toHaveText(`Restored ${accepted.batchCount} batches at ` +
      `event ${accepted.head}. ` +
      'The saved answer was read without running the agent.');
    await expect(answers).toHaveCount(2);
  }
  await page.locator('#continue').click();
  await expect(page.locator('#notice')).toHaveText('There is no unfinished task. ' +
    'The last completed answer is still available.');
  if (!live) {
    for (const action of ['continue', 'replace']) {
      await page.locator('#prompt').fill(`Use earlier facts for ${action}.`);
      await page.locator('#send').click();
      await expect(page.locator('#stop')).toBeEnabled();
      await page.locator('#stop').click();
      await expect(page.locator('#status')).toHaveText('Paused');
      if (action === 'continue') await page.locator('#continue').click();
      else {
        await page.locator('#prompt').fill('This replaces the task. Use the earlier facts.');
        await page.locator('#send').click();
      }
      await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 30000});
    }
  }
  await page.locator('#chat-scroll').evaluate(node => { node.scrollTop = 0; });
  await page.screenshot({path: join(shots, `${provider}-conversation.png`), fullPage: true});
  // Markdown stays inert even when a provider returns HTML or an unsafe link.
  const markup = await page.evaluate(async () => {
    const {richText} = await import('/ui.js');
    const node = document.createElement('div');
    node.append(richText('**Bold** and `code`\n\n<script>alert(1)</script>\n\n' +
      '[bad](javascript:alert(1))\n\n[good](https://example.com)'));
    return {html: node.querySelectorAll('script,img').length,
      bold: node.querySelector('strong').textContent, code: node.querySelector('code').textContent,
      links: [...node.querySelectorAll('a')].map(link => link.getAttribute('href'))};
  });
  assert.deepEqual(markup, {html: 0, bold: 'Bold', code: 'code', links: ['https://example.com']});
  if (!live) {
    // The second domain uses the same UI and recovery flow, with its own tools and fixture state.
    const billing = await browser.newPage({viewport: {width: 1440, height: 1000}});
    billing.on('pageerror', error => errors.push(error.message));
    billing.on('console', message => {
      if (message.type() === 'error') errors.push(`${message.text()} ${message.location().url}`);
    });
    await billing.goto(url);
    await billing.locator('#scenario').selectOption('ecommerce');
    await expect(billing.locator('#agent-name')).toHaveText('TechGear Store');
    await billing.locator('#suggestion').click();
    await expect(billing.locator('#prompt')).toHaveValue(config.scenarios[1].prompt);
    await billing.locator('#send').click();
    await expect(billing.locator('#status')).toHaveText('Turn complete', {timeout: 60000});
    await expect(billing.locator('.message[data-type="final_answer"] .message-body')).toHaveText(
      'I found Alex Rivera (C001), using alex.rivera@email.com.');
    const billingId = new URL(billing.url()).searchParams.get('run');
    assert.notEqual(billingId, runId);
    await billing.reload();
    await expect(billing.locator('#agent-name')).toHaveText('TechGear Store');
    await expect(billing.locator('#scenario')).toBeDisabled();
    await billing.locator('.tool-group-heading').click();
    await billing.locator('.message.tool summary').click();
    assert.deepEqual(JSON.parse(await billing.locator('.message.tool .message-body').innerText()), {
      id: 'C001', name: 'Alex Rivera', email: 'alex.rivera@email.com', phone: '+1-555-0321',
    });
    await billing.screenshot({path: join(shots, 'demo-ecommerce.png'), fullPage: true});
    await billing.close();
  }
  await page.setViewportSize({width: 390, height: 844});
  await expect(page.locator('#inspector')).toBeHidden();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await page.screenshot({path: join(shots, `${provider}-mobile.png`), fullPage: true});
  await page.locator('#inspector-toggle').click();
  await expect(page.locator('#inspector')).toBeVisible();
  await page.locator('#tab-state').click();
  await expect(page.locator('#run-explorer .gent-run-explorer')).toBeVisible();
  await checkFullscreen(page, expect, join(shots, `${provider}-mobile-fullscreen.png`));
  await page.screenshot({path: join(shots, `${provider}-mobile-inspector.png`), fullPage: true});
  await page.locator('#inspector-close').click();
  await page.locator('#prompt').fill('Line one');
  await page.locator('#prompt').press('Shift+Enter');
  await page.locator('#prompt').press('L');
  await expect(page.locator('#prompt')).toHaveValue('Line one\nL');
  await page.goto('about:blank');
  assert.deepEqual(errors, []);
  await checkChatTranscript(browser, url, expect, shots, errors);
  await stop();
  const fresh = await start();
  const missing = await page.request.get(`${fresh}/api/state?runId=${runId}`);
  assert.equal(missing.status(), 404);
  let releaseRead, reachedRead;
  const heldRead = new Promise(resolve => { releaseRead = resolve; });
  const reading = new Promise(resolve => { reachedRead = resolve; });
  await page.route('**/api/state?*', async route => {
    reachedRead();
    await heldRead;
    await route.continue();
  });
  const navigation = page.goto(`${fresh}/?run=${encodeURIComponent(runId)}`);
  await reading;
  await page.locator('#prompt').fill('Keep this draft after restart.');
  releaseRead();
  await navigation;
  await expect(page).toHaveURL(`${fresh}/`);
  await expect(page.locator('#prompt')).toHaveValue('Keep this draft after restart.');
  await expect(page.locator('#notice')).toHaveText(
    'That conversation is no longer available. You can start a new chat.');
  await expect(page.locator('#send')).toBeEnabled();
  await expect(page.locator('#restore')).toBeDisabled();
  await expect(page.locator('#continue')).toBeDisabled();
  await expect(page.locator('#messages')).toBeEmpty();
  // Chrome reports the expected HTTP 404 as a resource error. All other console errors fail.
  assert.deepEqual(errors.splice(0), [
    'Failed to load resource: the server responded with a status of 404 (Not Found) ' +
      `${fresh}/api/state?runId=${encodeURIComponent(runId)}`,
  ]);
  await page.unroute('**/api/state?*');
  if (!live) {
    await page.locator('#send').click();
    await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 30000});
    assert.notEqual(new URL(page.url()).searchParams.get('run'), runId);
    await expect(page.locator('.message.user')).toHaveText('Keep this draft after restart.');
  }
  assert.deepEqual(errors, []);
  console.log(`PASS ${provider}: shared scenarios, streaming text, nested tool groups, ` +
    'retained input, ' +
    'both replay paths, chronology, full-screen control, working indicator, ' +
    'encoded and recovered JSON, byte sizes, resizing, missing-run redirect, and recovery');
} catch (error) {
  for (const context of browser?.contexts() || []) {
    for (const page of context.pages()) {
      try {
        console.error('Browser failure state', await page.evaluate(() => ({
          url: location.href, status: document.querySelector('#status')?.textContent,
          notice: document.querySelector('#notice')?.textContent,
          prompt: document.querySelector('#prompt')?.value,
          answers: document.querySelectorAll('.message[data-type="final_answer"]').length,
        })));
      } catch (detailError) { console.error('Cannot read failure state', detailError); }
    }
  }
  console.error('Server log', logs);
  throw error;
} finally {
  await browser?.close();
  await stop();
  await rm(folder, {recursive: true, force: true});
  if (errors.length) console.error(errors);
}
