import assert from 'node:assert/strict';

export async function checkChronology(page, expect, kind, decoded, live) {
  const explorer = page.getByRole('region', {name: 'Run data explorer'});
  await expect(explorer.getByRole('button', {name: 'Chronological', exact: true}))
    .toHaveAttribute('aria-pressed', 'true');
  if (kind === 'state') {
    await expect(explorer.locator('.ge-row')).toHaveCount(0);
    await expect(explorer.locator('.ge-controls + .ge-note')).toHaveText(
      'Partial chronology: RunState retains recovery evidence, not the full archive. ' +
      'Open RunView for tasks and earlier calls; Messages shows retained context.');
    return;
  }
  const detail = explorer.locator('.ge-detail');
  await expect(detail.locator(':scope > .ge-section > h5')).toHaveText(
    ['System prompt', 'Task', 'Saved evidence']);
  const firstCall = decoded.activity.find(event => event.type === 'model_call_started');
  const system = detail.locator('.ge-section').filter({
    has: page.getByRole('heading', {name: 'System prompt', exact: true})});
  assert.deepEqual(await system.locator('.ge-content').allTextContents(),
    firstCall.payload.Request.Messages.filter(message => message.Role === 'system')
      .flatMap(message => message.Parts.map(part => part.Text)));
  await expect(system.locator('.ge-note')).toHaveText(
    `System messages in the first saved model call, after Hooks. Source event ` +
    `${firstCall.eventSequenceNumber}. Each model call below keeps its own effective request.`);
  if (live) return;
  assert.deepEqual(await explorer.locator('.ge-row').evaluateAll(rows =>
    rows.map(row => row.dataset.kind)),
  ['task-step', 'model-step', 'tool-group-step', 'model-step', 'task-step', 'model-step']);
  await explorer.locator('.ge-row[data-kind="model-step"]').first().click();
  const sent = detail.locator('.ge-call-columns > section').first();
  const returned = detail.locator('.ge-call-columns > section').last();
  assert.deepEqual(await sent.locator('.ge-model-message').evaluateAll(messages =>
    messages.map(message => ({role: message.dataset.role,
      parts: [...message.querySelectorAll('.ge-content')].map(part => part.textContent)}))),
  firstCall.payload.Request.Messages.map(message =>
    ({role: message.Role, parts: message.Parts.map(part => part.Text)})));
  await expect(returned.locator('.ge-part-label')).toHaveText(
    ['Commentary', 'Tool request · get_customer_info']);
  await expect(returned.locator('.ge-content')).toHaveText("I'll check the customer record first.");
  await expect(returned.locator('.ge-code')).toHaveText('{"email":"john.smith@email.com"}');
  await explorer.locator('.ge-row[data-kind="model-step"]').nth(1).click();
  const secondCall = decoded.activity.filter(event => event.type === 'model_call_started')[1];
  const repeatedCount = firstCall.payload.Request.Messages.length;
  const requestMessages = secondCall.payload.Request.Messages;
  assert.equal(requestMessages.length, 5);
  assert.deepEqual(requestMessages.slice(0, 2), firstCall.payload.Request.Messages.slice(0, 2));
  assert.deepEqual(requestMessages.at(-1), firstCall.payload.Request.Messages.at(-1));
  const repeated = sent.locator('.ge-repeated-messages').first();
  await expect(repeated).not.toHaveAttribute('open');
  await expect(sent.locator('.ge-repeated-messages > summary')).toHaveText([
    'Messages 1–2 · Already sent', 'Message 5 · Already sent']);
  await expect(sent.locator('.ge-request-count')).toHaveText(
    `${requestMessages.length - repeatedCount} new or changed · ${repeatedCount} repeated`);
  await expect(sent.locator('.ge-role')).toHaveText(['3 · Ai', '4 · Tool']);
  await sent.getByRole('button', {name: 'Full request', exact: true}).click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(requestMessages.length);
  await expect(sent.locator('.ge-request-count')).toHaveText(
    `${requestMessages.length} messages · Complete request`);
  assert.deepEqual(await sent.locator('.ge-content').allTextContents(),
    requestMessages.flatMap(message => message.Parts.flatMap(part => part.Text ? [part.Text] :
      (part.Content || part.Parts || []).flatMap(item => item.Text ? [item.Text] : []))));
  await sent.getByRole('button', {name: 'New content', exact: true}).click();
  await repeated.locator('summary').click();
  await expect(repeated.locator('.ge-model-message')).toHaveCount(2);
  await repeated.locator('summary').click();
  await expect(returned.locator('.ge-part-label')).toHaveText(
    ['Reasoning', 'Final answer candidate', 'Final answer']);
  assert.deepEqual(await returned.locator('.ge-content').allTextContents(), [
    'The saved lookup supplies the customer facts. Earlier user input is still present.',
    `
I found John Smith (C001), using john.smith@email.com.
`,
    'I found John Smith (C001), using john.smith@email.com.',
  ]);
  await explorer.locator('.ge-row[data-kind="tool-group-step"]').click();
  await detail.locator('.ge-tool-accordion > summary').click();
  await expect(detail.locator('.ge-code')).toHaveText('{"email":"john.smith@email.com"}');
  await expect(detail.getByText('Execution · executed', {exact: true})).toBeVisible();
  await expect(detail.getByRole('heading', {name: 'Reply sent to model'})).toHaveCount(0);
  await expect(detail.getByText('Effective input · after Hooks', {exact: true})).toBeVisible();
  await expect(detail.locator('.json-tree').first().locator('.json-leaf')).toHaveText([
    'email: "john.smith@email.com"', 'frequent_flyer_tier: "gold"', 'id: "C001"',
    'name: "John Smith"', 'phone: "+1-555-0123"',
  ]);
  await checkToolAlignment(page, expect, detail);
  await explorer.locator('.ge-row[data-kind="model-step"]').nth(1).click();
}

export async function checkToolAlignment(page, expect, detail) {
  const viewport = page.viewportSize();
  for (const size of [viewport, {width: 390, height: 850}]) {
    await page.setViewportSize(size);
    if (size.width <= 800) {
      await expect(page.locator('#inspector-toggle')).toHaveAttribute('aria-expanded', 'false');
      await page.locator('#inspector-toggle').click();
    }
    const tool = detail.locator('.ge-tool-accordion[open]').first();
    await expect(tool.locator('.ge-tool-accordion-body')).toBeVisible();
    const title = await tool.locator(':scope > summary .ge-disclosure-heading > strong')
      .boundingBox();
    for (const selector of ['.ge-tool-accordion-body', '.ge-section > h5', '.ge-call-info']) {
      const content = await tool.locator(selector).first().boundingBox();
      assert.ok(Math.abs(content.x - title.x) <= 1, `${selector} must align with the tool title`);
    }
    assert.equal(await page.evaluate(() =>
      document.documentElement.scrollWidth <= innerWidth), true);
  }
  await page.setViewportSize(viewport);
  await expect(page.locator('#inspector-toggle')).toHaveAttribute('aria-expanded', 'true');
}

export async function checkFullscreen(page, expect, screenshot) {
  const panel = page.locator('#inspector');
  const button = page.locator('#inspector-fullscreen');
  await button.click();
  await expect(panel).toHaveAttribute('role', 'dialog');
  await expect(panel).toHaveAttribute('aria-modal', 'true');
  await expect(button).toHaveAttribute('aria-pressed', 'true');
  const viewport = page.viewportSize();
  assert.deepEqual(await panel.boundingBox(), {x: 0, y: 0, ...viewport});
  assert.deepEqual(await page.evaluate(() => ({
    header: document.querySelector('.app-header').inert,
    conversation: document.querySelector('.conversation').inert,
    overflow: document.documentElement.scrollWidth > innerWidth,
  })), {header: true, conversation: true, overflow: false});
  await expect(page.locator('#inspector-resize')).toBeHidden();
  // Focus stays in the expanded inspector. Escape restores the previous panel width.
  await button.focus();
  await page.keyboard.press('Shift+Tab');
  assert.equal(await page.evaluate(() => document.activeElement.parentElement.className),
    'identity');
  await page.keyboard.press('Tab');
  await expect(button).toBeFocused();
  await page.locator('.inspector-body').evaluate(node => { node.scrollTop = 0; });
  await page.screenshot({path: screenshot, fullPage: true});
  await page.keyboard.press('Escape');
  await expect(button).toHaveAttribute('aria-pressed', 'false');
  await expect(button).toBeFocused();
  assert.equal(await panel.getAttribute('role'), null);
  if (viewport.width > 800) {
    await expect(panel).toHaveCSS('width', '640px');
    assert.equal(await page.locator('.conversation').evaluate(node => node.inert), false);
  }
}

export async function checkAdditionalEvidence(page, expect, json) {
  const view = JSON.parse(json);
  const start = view.activity.find(event => event.type === 'model_call_started');
  const finish = view.activity.find(event => event.type === 'model_call_finished');
  const tool = view.activity.find(event => event.type === 'tool_call_finished');
  const messages = Array.from({length: 26}, (_, index) => ({Role: index ? 'human' : 'system',
    Parts: [{Text: `Complete message ${index}: <img src=x onerror="alert(1)">`}]}));
  start.payload.Request.Messages = messages;
  finish.payload.Request.Messages = messages;
  tool.payload.Output.Kind = 'call_error';
  tool.payload.Output.Execution = null;
  tool.payload.Output.Error = {type: 'error', data: {message: 'Customer service unavailable'}};
  tool.payload.Output.Response.Parts = [{Text: 'The lookup failed. Use another action.'}];
  view.activity = [start, finish, tool,
    {...start, eventSequenceNumber: 1000, modelCallId: 'unfinished'},
    {...start, eventSequenceNumber: 1001, modelCallId: 'unfinished', type: 'model_stream_chunk',
      payload: {Content: [{Type: 'reasoning', Content: [{Text: 'Saved partial reasoning'}]}]}},
  ];
  await page.evaluate(async source => {
    const {createRunExplorer} = await import('/explorer/index.js');
    const host = document.createElement('div'); host.id = 'extra-evidence';
    document.body.append(host);
    const explorer = createRunExplorer(host, {kind: 'view', json: source});
    host.addEventListener('remove-explorer', () => { explorer.destroy(); host.remove(); },
      {once: true});
  }, JSON.stringify(view));
  const explorer = page.locator('#extra-evidence');
  const sent = explorer.locator('.ge-call-columns > section').first();
  await expect(sent.locator('.ge-model-message')).toHaveCount(20);
  await sent.getByRole('button', {name: 'Show next 20 messages'}).click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(26);
  assert.deepEqual(await sent.locator('.ge-content').allTextContents(),
    messages.map(message => message.Parts[0].Text));
  await expect(sent.locator('img')).toHaveCount(0);
  await explorer.locator('.ge-row[data-kind="tool-group-step"]').click();
  await expect(explorer.locator('.ge-row .ge-badge'))
    .toHaveText(['Complete', 'Error', 'No saved completion']);
  await explorer.locator('.ge-tool-accordion > summary').click();
  await expect(explorer.locator('.ge-error .ge-content'))
    .toHaveText('Customer service unavailable');
  await expect(explorer.locator('.ge-content')).toHaveText('Customer service unavailable');
  await explorer.locator('.ge-row[data-kind="model-step"]').last().click();
  await expect(sent.locator('.ge-request-count')).toHaveText('0 new or changed · 26 repeated');
  await expect(sent.locator('.ge-model-message')).toHaveCount(0);
  await expect(sent.locator('.ge-request-empty')).toHaveText(
    'No new messages. Call options remain available below.');
  await sent.locator('.ge-repeated-messages > summary').click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(20);
  await sent.getByRole('button', {name: 'Show next 20 messages'}).click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(26);
  await sent.getByRole('button', {name: 'Full request', exact: true}).click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(20);
  await sent.getByRole('button', {name: 'Show next 20 messages'}).click();
  assert.deepEqual(await sent.locator('.ge-content').allTextContents(),
    messages.map(message => message.Parts[0].Text));
  await expect(sent.locator('img')).toHaveCount(0);
  const returned = explorer.locator('.ge-call-columns > section').last();
  await expect(returned.locator('.ge-note').last()).toHaveText(
    'No complete response is saved at this position.');
  await returned.getByText('Saved stream fragment · event 1001', {exact: true}).click();
  await expect(returned.locator('.json-tree')).toBeVisible();
  await explorer.evaluate(host => host.dispatchEvent(new Event('remove-explorer')));
  await expect(explorer).toHaveCount(0);
}

export async function checkRequestPaging(page, expect, json) {
  const view = JSON.parse(json);
  const start = view.activity.find(event => event.type === 'model_call_started');
  const previous = Array.from({length: 22}, (_, index) =>
    ({Role: 'human', Parts: [{Text: `Previous ${index}`}]}));
  const added = previous.map((_, index) =>
    ({Role: 'tool', Parts: [{Text: `New result ${index}`}]}));
  const messages = previous.flatMap((message, index) => [message, added[index]]);
  view.activity = [
    {...start, payload: {Request: {Messages: previous}}},
    {...start, eventSequenceNumber: 1000, modelCallId: 'next',
      payload: {Request: {Messages: messages}}},
  ];
  await page.evaluate(async json => {
    const {createRunExplorer} = await import('/explorer/index.js');
    const host = document.createElement('div'); host.id = 'request-paging';
    document.body.append(host);
    const explorer = createRunExplorer(host, {kind: 'view', json});
    host.addEventListener('remove-explorer', () => { explorer.destroy(); host.remove(); },
      {once: true});
  }, JSON.stringify(view));
  const explorer = page.locator('#request-paging');
  await explorer.locator('.ge-row').last().click();
  const sent = explorer.locator('.ge-request-context');
  await expect(sent.locator('.ge-request-count')).toHaveText('22 new or changed · 22 repeated');
  await expect(sent.locator('.ge-model-message')).toHaveCount(10);
  await expect(sent.locator('.ge-repeated-messages')).toHaveCount(10);
  const more = sent.getByRole('button', {name: 'Show more request messages', exact: true});
  await more.click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(20);
  await more.click();
  await expect(more).toHaveCount(0);
  assert.deepEqual(await sent.locator('.ge-content').allTextContents(),
    added.map(message => message.Parts[0].Text));
  await expect(sent.locator('.ge-role')).toHaveText(
    added.map((_, index) => `${2 * index + 2} · Tool`));
  await sent.getByRole('button', {name: 'Full request', exact: true}).click();
  await expect(sent.locator('.ge-model-message')).toHaveCount(20);
  await sent.getByRole('button', {name: 'Show next 20 messages'}).click();
  await sent.getByRole('button', {name: 'Show next 20 messages'}).click();
  assert.deepEqual(await sent.locator('.ge-content').allTextContents(),
    messages.map(message => message.Parts[0].Text));
  await explorer.evaluate(host => host.dispatchEvent(new Event('remove-explorer')));
  await expect(explorer).toHaveCount(0);
}

export async function checkRawTabs(page, expect, explorer, inspected) {
  for (const [key, label] of [['encoded', 'Encoded Saved State'],
    ['decoded', 'Recovered State JSON']]) {
    await explorer.getByRole('button', {name: label, exact: true}).click();
    const raw = explorer.locator('.ge-raw');
    await expect(raw.locator('.json-tree > details')).toBeVisible();
    const bytes = Buffer.byteLength(inspected[key]);
    if (inspected[`${key}Bytes`] !== undefined) assert.equal(inspected[`${key}Bytes`], bytes);
    const unit = bytes < 1024 * 1024 ? 'KiB' : 'MiB';
    const divisor = unit === 'KiB' ? 1024 : 1024 * 1024;
    const exact = `${new Intl.NumberFormat('en-US').format(bytes)} bytes`;
    const size = bytes < 1024 ? exact : `${(bytes / divisor).toFixed(1)} ${unit} · ${exact}`;
    await expect(raw.locator('.ge-size')).toHaveText(size);
    await raw.getByRole('button', {name: 'Copy JSON', exact: true}).click();
    await expect(raw.getByRole('status')).toHaveText('Copied JSON.');
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), inspected[key]);
    const downloading = page.waitForEvent('download');
    await raw.getByRole('button', {name: 'Download JSON', exact: true}).click();
    const download = await downloading;
    const chunks = [];
    for await (const chunk of await download.createReadStream()) chunks.push(chunk);
    assert.equal(Buffer.concat(chunks).toString(), inspected[key]);
  }
}
