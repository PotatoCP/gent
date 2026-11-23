import assert from 'node:assert/strict';
import {writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {acceptedEvidence, historyTools} from './accepted-evidence.mjs';

// Keep the actual response bytes before any assertion. The server stores them only in memory.
export async function saveRunEvidence(request, url, runId, folder, label) {
  const result = {};
  const paths = {state: `/api/state?runId=${encodeURIComponent(runId)}`};
  for (const kind of ['view', 'state']) {
    for (const path of ['batches', 'capture']) {
      paths[`${kind}-${path}`] = `/api/inspect?runId=${encodeURIComponent(runId)}` +
        `&kind=${kind}&path=${path}`;
    }
  }
  const failures = [];
  for (const [name, path] of Object.entries(paths)) {
    try {
      const response = await request.get(url + path);
      const body = await response.text();
      await writeFile(join(folder, `${label}-${name}.json`), body);
      assert.equal(response.status(), 200, `${name}: ${body}`);
      result[name] = JSON.parse(body);
    } catch (error) { failures.push(error); }
  }
  if (failures.length) throw new AggregateError(failures, 'Cannot capture complete saved evidence');
  for (const kind of ['state', 'view']) {
    assert.deepEqual(result[`${kind}-batches`], result[`${kind}-capture`],
      `${kind}: full replay and RunStateSnapshot replay must agree exactly`);
  }
  return result;
}

export async function checkAcceptedChat(page, expect, saved, prompts) {
  const view = JSON.parse(saved['view-capture'].decoded);
  const evidence = acceptedEvidence(saved.state, view, prompts);
  const answers = page.locator('.message[data-type="final_answer"]');
  await expect(answers).toHaveCount(evidence.answers.length);
  assert.deepEqual(await answers.evaluateAll(nodes => nodes.map(node => node.id)),
    evidence.answers.map(answer => `message-${answer.id}`));
  const tools = page.locator('.message.tool');
  assert.deepEqual(await tools.evaluateAll(nodes => nodes.map(node => node.id)),
    evidence.tools.map(tool => `message-${tool.id}`));
  for (const [index, tool] of evidence.tools.entries()) {
    const node = tools.nth(index);
    await expect(node).toHaveAttribute('data-delivery', 'accepted');
    await expect(node).toHaveAttribute('data-status', tool.status);
    await expect(node.locator('.tool-name')).toHaveText(tool.label);
    await expect(node.locator('.tool-args')).toHaveText(tool.args);
    await expect(node.locator('.tool-status')).toHaveText(
      tool.status === 'complete' ? 'Complete' : 'Needs attention');
  }
  // Compare the full Markdown rendering. Exact source text is checked above against the archive;
  // markup can hide syntax in textContent, so a substring or plain-text match is insufficient.
  for (const [nodes, items] of [[answers, evidence.answers], [tools, evidence.tools]]) {
    const expected = await page.evaluate(async items => {
      const {richText} = await import('/ui.js');
      return items.map(item => {
        const body = document.createElement('div'); body.append(richText(item.text));
        return body.innerHTML;
      });
    }, items);
    assert.deepEqual(await nodes.locator('.message-body').evaluateAll(nodes =>
      nodes.map(node => node.innerHTML)), expected);
  }
  await expect(page.locator('.message[data-type="final_answer_candidate"]')).toHaveCount(0);
  return evidence;
}

export async function checkSavedTools(page, expect, explorer, history) {
  const tools = historyTools(history);
  const search = explorer.getByRole('searchbox', {name: 'Search saved records'});
  await explorer.getByRole('button', {name: 'Tools', exact: true}).click();
  await search.fill('');
  if (!tools.length) await expect(explorer.locator('.ge-row')).toHaveCount(0);
  for (const record of tools) {
    await search.fill(record.ToolCallID);
    await expect(explorer.locator('.ge-row')).toHaveCount(1);
    await explorer.locator('.ge-row').click();
    const detail = explorer.locator('.ge-detail');
    await expect(detail.locator(':scope > h5')).toHaveText(record.Name);
    await expect(detail.locator(':scope > .ge-facts dd')).toHaveText(
      [record.ToolCallID, record.AttemptID]);
    // Use the renderer only to check complete selection and display wiring. The independent
    // archive checks and deterministic demo tests check the content and renderer contracts.
    const html = await page.evaluate(async record => {
      const {renderToolResult} = await import('/explorer/chronology-render.js');
      return renderToolResult(record).innerHTML;
    }, record);
    assert.equal(await detail.locator('.ge-tool-data').innerHTML(), html);
  }
  return tools;
}
