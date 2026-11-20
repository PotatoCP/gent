import assert from 'node:assert/strict';
import {join} from 'node:path';

// A short response can become accepted in one frame. Observe actual visible DOM updates before
// submission so polling cannot miss a preview or select a tool inside a closed disclosure.
export async function watchLivePreview(page) {
  await page.evaluate(() => {
    window.livePreview = undefined;
    const observer = new MutationObserver(() => {
      const preview = [...document.querySelectorAll('.message[data-delivery="live"]')]
        .find(node => node.checkVisibility() && node.querySelector('.message-body')?.textContent);
      if (!preview) return;
      const working = document.querySelector('#working');
      const style = getComputedStyle(working);
      const dot = working.querySelector('.pulse').getBoundingClientRect();
      const text = working.querySelector('span:last-child').getBoundingClientRect();
      window.livePreview = {
        text: preview.querySelector('.message-body').textContent,
        working: {visible: working.checkVisibility(), display: style.display,
          alignItems: style.alignItems, gap: style.gap},
        center: Math.abs(dot.y + dot.height / 2 - text.y - text.height / 2),
        gap: text.x - dot.x - dot.width,
      };
      observer.disconnect();
    });
    observer.observe(document.querySelector('#messages'),
      {subtree: true, attributes: true, childList: true, characterData: true});
  });
  return async () => {
    await page.waitForFunction(() => window.livePreview, null, {timeout: 90000});
    const preview = await page.evaluate(() => window.livePreview);
    assert.ok(preview.text.trim(), JSON.stringify(preview));
    assert.deepEqual(preview.working,
      {visible: true, display: 'flex', alignItems: 'center', gap: '8px'});
    assert.ok(preview.center <= 1, JSON.stringify(preview));
    assert.equal(preview.gap, 8);
  };
}

// Drive the same accepted/live projection and renderer as SSE. The fixture supplies parallel
// starts and results in a different order, so grouping cannot depend on completion order.
export async function checkChatTranscript(browser, url, expect, shots, errors) {
  const page = await browser.newPage({viewport: {width: 1440, height: 1200}});
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(`${message.text()} ${message.location().url}`);
  });
  await page.goto(url);
  await expect(page.locator('#suggestion')).toBeEnabled();
  await page.locator('#inspector-close').click();
  await page.evaluate(async () => {
    const {createChat} = await import('/chat.js');
    const {emptyState, receive, chatMessages, Delivery} = await import('/state.js');
    const state = emptyState();
    const inspected = [];
    const chat = createChat(item => inspected.push(item.id), text => { throw new Error(text); });
    let sequence = 0;
    const events = [];
    const push = (items, accepted = false) => {
      const event = {runId: 'chat-fixture', attemptId: 'attempt', turnId: 'turn',
        eventSequenceNumber: ++sequence, items};
      events.push(event);
      receive(state, accepted ? Delivery.Accepted : Delivery.Live, event);
      chat.render(chatMessages(state), true);
    };
    const tool = (id, label, args, status = 'running', text = '') =>
      ({id, kind: 'tool', label, args, status, text});
    window.chatFixture = {push, inspected, state, tool, chat,
      accept() {
        for (const event of events) receive(state, Delivery.Accepted, event);
        chat.render(chatMessages(state), false);
      },
      clear() { chat.render([], false); },
    };
    push([{id: 'user', kind: 'user', label: 'You',
      text: 'Find a later flight to Los Angeles.'}], true);
    push([{id: 'progress', kind: 'assistant', type: 'commentary',
      text: 'I will check ', delta: true}]);
    push([{id: 'progress', kind: 'assistant', type: 'commentary',
      text: 'your booking.', delta: true}]);
    push([tool('customer', 'get_customer_info', '{"email":"john.smith@email.com"}')]);
    push([tool('flight', 'get_flight_info', '{"flight_number":"AA100"}')]);
  });
  const groups = page.locator('.tool-group-toggle');
  const first = groups.first();
  const tools = first.locator('.message.tool');
  await expect(groups).toHaveCount(1);
  await expect(first.locator(':scope > summary')).toHaveText('Ran tools');
  await expect(first).not.toHaveAttribute('open');
  await expect(page.locator('.message.commentary')).toHaveText('I will check your booking.');
  await expect(tools).toHaveCount(2);
  await expect(tools.first()).toBeHidden();
  await first.locator(':scope > summary').focus();
  await page.keyboard.press('Enter');
  await expect(first).toHaveAttribute('open', '');
  await expect(tools.locator('.tool-name')).toHaveText(['get_customer_info', 'get_flight_info']);
  await expect(tools.locator('.tool-content').first()).toBeHidden();
  const summary = tools.first().locator('summary');
  await summary.focus();
  await page.keyboard.press('Space');
  await expect(tools.first().locator('details')).toHaveAttribute('open', '');
  await expect(tools.first().locator('.message-body')).toHaveText('Waiting for the tool result…');
  await page.evaluate(() => {
    const {push, tool} = window.chatFixture;
    push([tool('flight', 'get_flight_info', '{"flight_number":"AA100"}',
      'complete', 'Flight AA100 departs at 10:00.')]);
    push([tool('customer', 'get_customer_info', '{"email":"john.smith@email.com"}',
      'complete', 'John Smith · Booking BK001.')]);
    push([tool('policy', 'search_policy', '{"query":"same-day changes"}',
      'attention', 'Policy service unavailable. Try another source.')]);
  });
  await expect(summary).toBeFocused();
  await expect(first).toHaveAttribute('open', '');
  await expect(tools.first().locator('details')).toHaveAttribute('open', '');
  await expect(tools.locator('.tool-name')).toHaveText([
    'get_customer_info', 'get_flight_info', 'search_policy']);
  await expect(tools.locator('.tool-status')).toHaveText([
    'Complete', 'Complete', 'Needs attention']);
  await expect(tools.first().locator('.message-body')).toHaveText('John Smith · Booking BK001.');
  await tools.nth(2).locator('summary').click();
  await expect(tools.nth(2).locator('.message-body')).toHaveText(
    'Policy service unavailable. Try another source.');
  await page.evaluate(() => {
    const {push, tool} = window.chatFixture;
    push([{id: 'reason', kind: 'assistant', type: 'reasoning',
      text: 'The booking is ', delta: true}]);
    push([{id: 'reason', kind: 'assistant', type: 'reasoning',
      text: 'confirmed. I can check later flights.', delta: true}]);
    push([tool('search', 'search_flights', '{"destination":"LAX"}',
      'complete', 'Flight AA120 leaves at 18:00.')]);
    push([{id: 'candidate', kind: 'assistant', type: 'final_answer_candidate',
      text: 'Flight AA120 is available at 18:00.'}]);
    // The final answer must still wait for Accepted delivery, even if its live event arrives.
    push([{id: 'answer', kind: 'assistant', type: 'final_answer', sources: ['candidate'],
      text: 'Flight AA120 is available at 18:00. Shall I change your booking?'}]);
  });
  await expect(groups).toHaveCount(2);
  await expect(groups.locator(':scope > summary')).toHaveText(['Ran tools', 'Ran tools']);
  const reasoning = page.locator('.message.reasoning');
  await expect(reasoning.locator('details')).toHaveCount(0);
  await expect(reasoning.locator('.message-body')).toBeVisible();
  await expect(reasoning).toHaveText('The booking is confirmed. I can check later flights.');
  await expect(page.locator('.answer-actions')).toHaveCount(0);
  await summary.focus();
  await page.evaluate(() => window.chatFixture.accept());
  await expect(summary).toBeFocused();
  await expect(first).toHaveAttribute('open', '');
  await expect(tools.first().locator('details')).toHaveAttribute('open', '');
  await expect(page.locator('.message[data-type="final_answer_candidate"]')).toHaveCount(0);
  await expect(page.locator('.message[data-type="final_answer"] .message-body')).toHaveText(
    'Flight AA120 is available at 18:00. Shall I change your booking?');
  await expect(page.locator('.answer-actions')).toHaveCount(1);
  assert.deepEqual(await page.locator('#messages > article').evaluateAll(nodes =>
    nodes.map(node => node.className)), [
    'message user', 'message commentary', 'message tool-group', 'message reasoning',
    'message tool-group', 'message assistant',
  ]);
  await tools.first().getByRole('button', {name: 'Inspect tool event ↗'}).click();
  assert.deepEqual(await page.evaluate(() => window.chatFixture.inspected), ['customer']);
  await page.locator('#chat-scroll').evaluate(node => { node.scrollTop = 0; });
  await page.screenshot({path: join(shots, 'demo-chat-tools.png'), fullPage: true});
  await first.locator(':scope > summary').click();
  await expect(tools.first()).toBeHidden();
  await expect(reasoning.locator('.message-body')).toBeVisible();
  await first.locator(':scope > summary').click();
  await expect(tools.first().locator('details')).toHaveAttribute('open', '');
  await page.setViewportSize({width: 390, height: 844});
  await expect(first.locator(':scope > summary')).toBeVisible();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await expect(tools.nth(2).locator('.message-body')).toHaveText(
    'Policy service unavailable. Try another source.');
  await page.screenshot({path: join(shots, 'demo-chat-tools-mobile.png'), fullPage: true});
  await page.evaluate(() => window.chatFixture.clear());
  await expect(page.locator('#messages')).toBeEmpty();
  assert.deepEqual(errors, []);
  await page.close();
}
