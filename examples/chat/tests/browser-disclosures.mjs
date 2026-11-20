import assert from 'node:assert/strict';
import {join} from 'node:path';

function disclosureFixture(json) {
  const view = JSON.parse(json);
  const native = {Provider: 'fixture', Items: [{signature: 'Saved native value'}]};
  for (const event of view.activity) {
    const payload = event.payload;
    if (payload?.Request?.Messages[0]) payload.Request.Messages[0].Continuation = native;
    if (payload?.Response?.Choices[0]) payload.Response.Choices[0].Continuation = native;
    if (event.type !== 'tool_call_finished') continue;
    const record = payload.Output;
    record.Recovery = {Status: 'completed', Execution: record.Execution};
    record.Execution = null;
    record.Kind = 'recovery';
    record.Error = {message: 'The original call was interrupted.'};
    record.ReturnedRecovery = {Status: 'unresolved', Detail: 'Original provider report'};
  }
  const start = view.activity.find(event => event.type === 'model_call_started');
  view.activity.push({...start, eventSequenceNumber: 1000, modelCallId: 'unfinished'},
    {...start, eventSequenceNumber: 1001, modelCallId: 'unfinished', type: 'model_stream_chunk',
      payload: {Content: [{Type: 'reasoning', Content: [{Text: 'Saved partial reasoning'}]}]}});
  return JSON.stringify(view);
}

async function openDisclosures(detail, expect) {
  // Use the public toggles. JSON containers stay lazy; their nested layout is checked separately.
  for (let count = 0; count < 100; count++) {
    const closed = detail.locator('details:not([open]):not(.ge-json-node)');
    const index = await closed.evaluateAll(nodes => nodes.findIndex(node =>
      node.getBoundingClientRect().height > 0));
    if (index < 0) return;
    const disclosure = closed.nth(index);
    const summary = disclosure.locator(':scope > summary');
    // Hold the same node while the closed-details locator changes after this click.
    const handle = await disclosure.elementHandle();
    await summary.click();
    await expect.poll(() => handle.evaluate(node =>
      node.open && node.querySelector('.ge-disclosure-body').children.length > 0)).toBe(true);
    await handle.dispose();
  }
  assert.fail('The fixture must finish opening its disclosures');
}

async function checkGeometry(detail) {
  const result = await detail.evaluate(root => {
    const failures = [];
    const types = new Set();
    let opened = 0;
    for (const node of root.querySelectorAll('details')) {
      const summary = node.querySelector(':scope > summary');
      if (!summary.getBoundingClientRect().height) continue;
      if (!node.classList.contains('ge-disclosure')) {
        failures.push({name: summary.textContent, error: 'Missing shared disclosure layout'});
        continue;
      }
      for (const type of node.classList) if (type !== 'ge-disclosure') types.add(type);
      if (!node.open) continue;
      opened++;
      const heading = summary.querySelector('.ge-disclosure-heading').getBoundingClientRect();
      const body = node.querySelector(':scope > .ge-disclosure-body');
      const bounds = body.getBoundingClientRect();
      const first = body.firstElementChild?.getBoundingClientRect();
      const summaryBounds = summary.getBoundingClientRect();
      const step = parseFloat(getComputedStyle(body).marginInlineStart);
      if (Math.abs(bounds.x - heading.x) > 1 || bounds.right > summaryBounds.right + 1) {
        failures.push({name: summary.textContent, error: 'Body must align with heading text'});
      }
      if (first && first.y < summaryBounds.bottom + 3) {
        failures.push({name: summary.textContent, error: 'No gap below summary'});
      }
      for (const child of body.children) {
        if (!child.classList.contains('json-leaf')) continue;
        if (Math.abs(child.getBoundingClientRect().x - bounds.x - step) > 1) {
          failures.push({name: summary.textContent, error: 'JSON fields must share a text column'});
        }
      }
    }
    return {failures, opened, types: [...types].sort()};
  });
  assert.deepEqual(result.failures, []);
  assert.ok(result.opened > 0);
  return result.types;
}

export async function checkDisclosureHierarchy(browser, url, expect, json, shots, errors) {
  const page = await browser.newPage();
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(`${message.text()} ${message.location().url}`);
  });
  await page.goto(`${url}/explorer/demo.html`);
  await page.evaluate(async json => {
    const {createRunExplorer} = await import('/explorer/index.js');
    document.body.replaceChildren();
    document.body.style.maxWidth = 'none';
    const host = document.createElement('div'); host.id = 'hierarchy';
    document.body.append(host);
    window.hierarchyExplorer = createRunExplorer(host, {kind: 'view', json});
  }, disclosureFixture(json));
  const explorer = page.locator('#hierarchy');
  const detail = explorer.locator('.ge-detail');
  for (const layout of [
    {name: 'wide', width: 1440, height: 1100},
    {name: 'sidebar', width: 640, height: 1000},
    {name: 'mobile', width: 390, height: 850},
  ]) {
    await page.setViewportSize({width: layout.width, height: layout.height});
    const types = new Set();
    const rows = explorer.locator('.ge-row');
    for (let index = 0; index < await rows.count(); index++) {
      await rows.nth(index).click();
      await openDisclosures(detail, expect);
      for (const type of await checkGeometry(detail)) types.add(type);
    }
    assert.deepEqual([...types].sort(), ['ge-call-info', 'ge-json-node',
      'ge-repeated-messages', 'ge-source', 'ge-tool-accordion']);

    await explorer.locator('.ge-row[data-kind="model-step"]').first().click();
    const source = detail.locator('.ge-source').filter({has: page.getByText(
      'Full request and call options', {exact: true})});
    await source.locator(':scope > summary').click();
    const messages = JSON.parse(json).activity.find(event =>
      event.type === 'model_call_started').payload.Request.Messages;
    for (const text of [`Messages · ${messages.length} items`, '0 · 3 fields', 'Parts · 1 items',
      '0 · 1 fields', `Text · ${messages[0].Parts[0].Text.length} characters`]) {
      await source.getByText(text, {exact: true}).click();
    }
    await expect(source.locator('.ge-disclosure-body > .json-string')).toHaveText(
      messages[0].Parts[0].Text);
    await checkGeometry(detail);
    assert.equal(await page.evaluate(() =>
      document.documentElement.scrollWidth <= innerWidth), true);
    await detail.scrollIntoViewIfNeeded();
    await page.screenshot({path: join(shots, `disclosures-${layout.name}.png`), fullPage: true});

    // Closing and reopening a source keeps one body. Keyboard control stays native.
    const before = await source.locator('.ge-disclosure-body').count();
    await source.locator(':scope > summary').focus();
    await page.keyboard.press('Enter');
    await expect(source).not.toHaveAttribute('open');
    await page.keyboard.press('Space');
    await expect(source).toHaveAttribute('open', '');
    assert.equal(await source.locator('.ge-disclosure-body').count(), before);
  }
  await page.evaluate(() => window.hierarchyExplorer.destroy());
  await expect(explorer).toBeEmpty();
  await page.close();
}
