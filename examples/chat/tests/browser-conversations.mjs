import assert from 'node:assert/strict';
import {join} from 'node:path';

export async function checkConversations(page, url, expect, shots) {
  await page.setViewportSize({width: 1440, height: 1000});
  await page.goto(url);
  await expect(page.locator('#conversation-list a').first()).toBeVisible();
  await page.locator('#inspector-close').click();
  const submit = async (prompt, done = true) => {
    await page.locator('#prompt').fill(prompt);
    await expect(page.locator('#send')).toBeEnabled();
    const admitted = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/action' && response.request().method() === 'POST');
    await page.locator('#prompt').press('Enter');
    assert.equal((await admitted).status(), 202);
    await expect(page).toHaveURL(/\?run=/);
    if (done) await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 20000});
    return new URL(page.url()).searchParams.get('run');
  };
  await page.locator('#prompt').fill('Remember Harbor.');
  await page.locator('#conversation-list a').first().click();
  await page.locator('#new-chat').click();
  await expect(page.locator('#prompt')).toHaveValue('Remember Harbor.');
  const original = await submit('Remember Harbor.');
  await page.locator('#new-chat').click();
  await expect(page).toHaveURL(url + '/');
  await expect(page.locator('#prompt')).toHaveValue('');
  await page.locator('#scenario').selectOption('ecommerce');
  const second = await submit('Remember Meadow.');
  const link = id => page.locator(`#conversation-list a[data-run-id="${id}"]`);
  await expect(link(original)).toBeVisible();
  await expect(link(second)).toHaveAttribute('aria-current', 'page');
  await page.locator('#prompt').fill('Unsent Meadow draft.');
  await link(original).click();
  await expect(page.locator('#status')).toHaveText('Turn complete');
  await expect(page.locator('#messages .message.user')).toHaveCount(1);
  await expect(page.locator('#messages .message.user')).toHaveText('Remember Harbor.');
  const resumed = await submit('What was my first prompt?');
  assert.equal(resumed, original);
  await expect(page.locator('.message[data-type="final_answer"] .message-body').last()).toHaveText(
    'Your first message was: Remember Harbor. Earlier lookup: ' +
    'I found John Smith (C001), using john.smith@email.com.');
  await page.reload();
  await expect(link(original)).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('#messages .message.user')).toHaveCount(2);
  await page.screenshot({path: join(shots, 'chat-history-desktop.png'), fullPage: true});
  await link(second).click();
  await expect(page.locator('#status')).toHaveText('Turn complete');
  // Page reload drops local drafts, but switching within a page must retain them.
  await page.locator('#prompt').fill('Keep my draft.');
  await link(original).click();
  await link(second).click();
  await expect(page.locator('#prompt')).toHaveValue('Keep my draft.');
  await page.goBack();
  await expect(link(original)).toHaveAttribute('aria-current', 'page');

  // The old request completes after a selection change. It must not replace the chosen chat.
  let release;
  const held = new Promise(resolve => { release = resolve; });
  let intercepted;
  const received = new Promise(resolve => { intercepted = resolve; });
  let fulfilled;
  const delivered = new Promise(resolve => { fulfilled = resolve; });
  await page.route('**/api/state?*', async route => {
    if (new URL(route.request().url()).searchParams.get('runId') !== second) {
      return route.continue();
    }
    const response = await route.fetch();
    intercepted(); await held; await route.fulfill({response}); fulfilled();
  });
  await link(second).click(); await received;
  await link(original).click();
  release(); await delivered; await page.unroute('**/api/state?*');
  await expect(link(original)).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('#messages .message.user')).toHaveCount(2);

  await link(second).click();
  await submit('Recall Meadow again.', false);
  await expect(page.locator('#status')).toHaveText('Working');
  await page.locator('#new-chat').click();
  await expect(page.locator('#empty')).toBeVisible();
  await link(original).click();
  await expect(page.locator('#status')).toHaveText('Turn complete');
  await link(second).click();
  await expect(page.locator('#status')).toHaveText('Turn complete', {timeout: 20000});
  await expect(page.locator('#messages .message.user')).toHaveCount(2);
  await link(original).click();

  // Reset must invalidate another open browser as well as the browser that pressed the button.
  const observer = await page.context().browser().newPage();
  await observer.goto(`${url}/?run=${encodeURIComponent(original)}`);
  await expect(observer.locator('#status')).toHaveText('Turn complete');
  await observer.locator('#prompt').fill('Keep this unsent message.');
  await page.locator('#server-reset').click();
  await page.locator('#reset-cancel').click();
  await expect(link(original)).toBeVisible();
  await page.locator('#server-reset').click();
  await page.locator('#reset-confirm').click();
  await expect(page.locator('#conversation-list a')).toHaveCount(0);
  await expect(page).toHaveURL(url + '/');
  await expect(observer).toHaveURL(url + '/');
  await expect(observer.locator('#messages')).toBeEmpty();
  await expect(observer.locator('#prompt')).toHaveValue('Keep this unsent message.');
  await observer.close();

  // Reset a working model and prove that a new conversation starts normally afterward.
  await submit('Reset while streaming.', false);
  await expect(page.locator('.message.reasoning')).toBeVisible();
  await page.locator('#server-reset').click();
  await page.locator('#reset-confirm').click();
  await expect(page.locator('#status')).toHaveText('Ready');
  await expect(page.locator('#conversation-list a')).toHaveCount(0);
  await expect(page.locator('#messages')).toBeEmpty();
  await submit('Fresh after reset.');

  await page.setViewportSize({width: 390, height: 844});
  await expect(page.locator('#conversations')).toBeHidden();
  await page.locator('#chats-toggle').click();
  await expect(page.locator('#conversations')).toBeVisible();
  await page.screenshot({path: join(shots, 'chat-history-mobile.png'), fullPage: true});
  await page.locator('#conversation-list a').first().click();
  await expect(page.locator('#conversations')).toBeHidden();
  await expect(page.locator('#status')).toHaveText('Turn complete');
  await page.locator('#inspector-toggle').click();
  await expect(page.locator('#inspector')).toBeVisible();
  await page.locator('#chats-toggle').click();
  await expect(page.locator('#inspector')).toBeHidden();
  await page.locator('#inspector-toggle').click();
  await expect(page.locator('#conversations')).toBeHidden();
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
}
