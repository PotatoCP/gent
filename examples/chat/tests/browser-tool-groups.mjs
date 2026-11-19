import assert from 'node:assert/strict';

// The viewer consumes public diagnostic JSON. Keep request IDs, logical IDs, and execution
// results separate so a test cannot pass by treating a prepared model reply as the tool result.
function groupedFixture(json) {
  const view = JSON.parse(json);
  const original = view.activity.find(event => event.type === 'model_call_finished');
  const names = ['get_customer_info', 'get_flight_info', 'search_policy'];
  const results = ['Customer C001: John Smith.', 'Flight AA100: JFK to LAX.'];
  const replies = ['Customer reply prepared for the model.', 'Flight reply prepared for the model.',
    'Policy lookup failed. Try another source.'];
  const requests = names.map((name, index) => ({id: `native:${index}`, type: 'function',
    function: {name, arguments: JSON.stringify({query: name})}}));
  function event(sequence, type, payload, ids = {}) {
    return {...original, eventSequenceNumber: sequence, type, payload, ...ids};
  }
  const records = names.map((name, index) => ({
    ToolCallID: `tool:${index}`, AttemptID: original.attemptId, Name: name,
    RawArguments: requests[index].function.arguments,
    Kind: index === 2 ? 'call_error' : 'execution',
    Input: {type: 'fixture/input', version: 1, data: {query: name}},
    Execution: index === 2 ? null : {Status: 'executed', Parts: [{Text: results[index]}]},
    Error: index === 2 ? {type: 'error', data: {message: 'Policy service unavailable'}} : null,
    Response: {ToolCallID: requests[index].id, Name: name, Parts: [{Text: replies[index]}]},
  }));
  const calls = records.map(record => ({modelCallId: undefined, toolCallId: record.ToolCallID}));
  const model = original.modelCallId;
  view.activity = [
    event(1, 'model_call_started', {Request: original.payload.Request}, {modelCallId: model}),
    event(2, 'model_call_finished', {Request: original.payload.Request,
      Response: {Choices: [{Parts: requests}]}}, {modelCallId: model}),
    ...records.map((record, index) => event(index + 3, 'tool_call_started',
      {ToolName: record.Name, Args: record.Input}, calls[index])),
    ...[2, 1, 0].map((index, order) => event(order + 6, 'tool_call_finished',
      {ToolName: records[index].Name, Output: records[index]}, calls[index])),
    event(9, 'model_call_started', {Request: {Messages: [
      {Role: 'ai', Parts: requests},
      ...records.map(record => ({Role: 'tool', Parts: [record.Response]})),
    ]}}, {modelCallId: `${model}:next`, iteration: 2}),
  ];
  view.history = {revision: 0, entries: [], changes: []};
  view.lastAppliedEventSequenceNumber = 9;
  return {json: JSON.stringify(view), names, results, replies};
}

export async function checkToolGroups(page, expect, json, screenshot) {
  const fixture = groupedFixture(json);
  await page.evaluate(async json => {
    const {createRunExplorer} = await import('/explorer/index.js');
    const host = document.createElement('div'); host.id = 'grouped-tools';
    document.body.append(host);
    const explorer = createRunExplorer(host, {kind: 'view', json});
    host.addEventListener('remove-explorer', () => { explorer.destroy(); host.remove(); },
      {once: true});
  }, fixture.json);
  const explorer = page.locator('#grouped-tools');
  const rows = explorer.locator('.ge-row');
  await expect(rows.locator('strong')).toHaveText(['Model call', 'Tool calls · 3', 'Model call']);
  await rows.nth(1).click();
  const tools = explorer.locator('.ge-tool-accordion');
  await expect(tools.locator(':scope > summary strong')).toHaveText(fixture.names);
  await expect(explorer.locator('.ge-tool-data')).toHaveCount(0);
  await tools.first().locator(':scope > summary').focus();
  await page.keyboard.press('Enter');
  await expect(tools.first()).toHaveAttribute('open', '');
  await tools.nth(1).locator(':scope > summary').click();
  await tools.nth(2).locator(':scope > summary').click();
  await expect(explorer.locator('.ge-tool-accordion[open]')).toHaveCount(3);
  await expect(explorer.locator('.ge-tool-data')).toHaveCount(3);
  assert.deepEqual(await explorer.locator('.ge-tool-data .ge-content').allTextContents(),
    [...fixture.results, 'Policy service unavailable']);
  await expect(explorer.getByRole('heading', {name: 'Reply sent to model'})).toHaveCount(0);
  await explorer.screenshot({path: screenshot});
  await tools.first().locator(':scope > summary').focus();
  await page.keyboard.press('Space');
  assert.equal(await tools.first().getAttribute('open'), null);
  await rows.last().click();
  assert.deepEqual(await explorer.locator('[data-role="tool"] .ge-content').allTextContents(),
    fixture.replies);
  await explorer.getByRole('searchbox', {name: 'Search saved records'}).fill('tool:2');
  await expect(rows.locator('strong')).toHaveText('Tool calls · 3');
  await explorer.evaluate(host => host.dispatchEvent(new Event('remove-explorer')));
  await expect(explorer).toHaveCount(0);
}
