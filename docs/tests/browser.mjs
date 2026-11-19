import assert from 'node:assert/strict';
import { cp, mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';
import { build, docs } from '../build.mjs';
import { createDocsServer } from '../serve.mjs';

const temporary = await mkdtemp(path.join(tmpdir(), 'gent-docs-browser-'));
const moved = path.join(temporary, 'moved site', 'gent');
const output = path.join(docs, 'dist');
const screenshots = path.join(docs, 'screenshots');
const servers = [];
let browser;

async function start(basePath) {
  const server = await createDocsServer(output, { basePath });
  servers.push(server);
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  return `http://127.0.0.1:${server.address().port}${basePath}`;
}

try {
  const manifest = build(output);
  await cp(output, moved, { recursive: true });
  await mkdir(screenshots, { recursive: true });
  const root = await start('/');
  const nested = await start('/gent/');
  browser = await chromium.launch({ headless: true,
    executablePath: process.env.CHROMIUM_PATH || undefined });
  const modes = [
    { name: 'file', base: pathToFileURL(output + path.sep).href },
    { name: 'root', base: root },
    { name: 'nested', base: nested },
  ];

  for (const mode of modes) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 },
      permissions: ['clipboard-read', 'clipboard-write'] });
    try {
      const page = await context.newPage();
      const failures = [];
      page.on('pageerror', (error) => failures.push(error.message));
      page.on('requestfailed', (request) => failures.push(request.url()));
      page.on('console', (message) => {
        if (message.type() === 'error') failures.push(message.text());
      });
      const address = (file) => new URL(file, mode.base).href;
      async function load(file) {
        const response = await page.goto(address(file));
        assert.equal(response.status(), 200, page.url());
        await page.evaluate(() => document.fonts.ready);
      }
      async function layout() {
        assert.equal(await page.locator('h1').count(), 1);
        assert.equal(await page.evaluate(() =>
          document.documentElement.scrollWidth > innerWidth), false, page.url());
        assert.equal(await page.locator('html').evaluate((node) =>
          getComputedStyle(node).backgroundColor), 'rgb(251, 250, 246)');
        const fonts = await page.evaluate(async () =>
          (await document.fonts.load('16px "DM Sans"')).map((font) => font.status));
        assert.deepEqual(fonts, ['loaded']);
      }
      for (const entry of manifest.pages) {
        await load(entry.file);
        await layout();
        assert.equal(await page.locator('h1').textContent(), entry.title);
      }
      await load('index.html');
      await page.screenshot({ path: path.join(screenshots, `${mode.name}-home.png`),
        fullPage: true });
      await page.getByRole('link', { name: 'Run Hello World' }).click();
      assert.equal(page.url(), address('start/hello-world/index.html'));
      const sample = manifest.examples['hello-world'].code;
      const figure = page.locator('[data-example="hello-world"]');
      assert.equal(await figure.locator('code').textContent(), sample + '\n');
      await figure.locator('.copy').click();
      await page.waitForFunction(() =>
        document.querySelector('#copy-status').textContent === 'Code copied.');
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), sample);
      await page.screenshot({ path: path.join(screenshots, `${mode.name}-hello.png`),
        fullPage: true });
      const completeSource = await figure.locator('figcaption a').getAttribute('href');
      assert.equal(new URL(completeSource, page.url()).href,
        address('_source/examples/basics.go'));

      await page.keyboard.press('Control+k');
      await page.locator('#search-input').fill('RunState.Apply');
      await page.locator('#search-results a').first().waitFor();
      assert.equal(await page.locator('#search-results strong').first().textContent(),
        'RunState.Apply');
      await page.keyboard.press('ArrowDown');
      assert.equal(await page.evaluate(() => document.activeElement.closest('nav').id),
        'search-results');
      await page.keyboard.press('Enter');
      await page.waitForURL(address('reference/api/gent/index.html#runstate-apply'));
      await page.keyboard.press('Control+k');
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('#search-dialog').evaluate((node) => node.open), false);
      const source = page.locator('#runstate-apply .source-link');
      const expectedSource = new URL(await source.getAttribute('href'), page.url()).href;
      await source.click();
      await page.waitForURL(expectedSource);
      assert.equal(await page.locator(new URL(expectedSource).hash).count(), 1);
      assert.equal(await page.locator('link[rel="icon"]').evaluate((node) => node.href),
        address('assets/favicon.svg'));

      await load('agent/tools/index.html');
      await page.locator('.page-toc a').filter({ hasText: 'Block an action' }).click();
      assert.equal(new URL(page.url()).hash, '#block-an-action');
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ path: path.join(screenshots, `${mode.name}-tools.png`),
        fullPage: true });
      await page.setViewportSize({ width: 390, height: 844 });
      for (const file of ['index.html', 'start/hello-world/index.html', 'agent/tools/index.html',
        'storage/restore/index.html', 'reference/api/gent/index.html']) {
        await load(file);
        await layout();
      }
      await load('start/hello-world/index.html');
      await page.getByRole('button', { name: 'Open navigation' }).click();
      await page.locator('#menu-dialog').getByRole('link', { name: 'Tools', exact: true }).click();
      await page.waitForURL(address('agent/tools/index.html'));
      await layout();
      await page.screenshot({ path: path.join(screenshots, `${mode.name}-mobile.png`),
        fullPage: true });
      const configuration = JSON.parse(await readFile(path.join(docs, 'site.json')));
      for (const [old, destination] of Object.entries(configuration.redirects)) {
        await page.goto(address(old));
        await page.waitForURL(address(destination + 'index.html'));
        await layout();
      }
      if (mode.name !== 'file') {
        const missing = await context.request.get(address('missing/deep/page/'));
        assert.equal(missing.status(), 404);
        assert.equal(missing.url(), address('404.html'));
        assert.equal(await missing.text(), await readFile(path.join(output, '404.html'), 'utf8'));
      }
      assert.deepEqual(failures, [], mode.name);
      console.log(`Passed ${mode.name}: ${manifest.pages.length} pages, styles, fonts, links, `
        + 'search, copy, and mobile navigation.');
    } finally {
      await context.close();
    }
  }

  // Moving the same output to a path with spaces must not require another build.
  const page = await browser.newPage();
  const failures = [];
  page.on('pageerror', (error) => failures.push(error.message));
  page.on('requestfailed', (request) => failures.push(request.url()));
  page.on('console', (message) => {
    if (message.type() === 'error') failures.push(message.text());
  });
  await page.goto(pathToFileURL(path.join(moved, 'agent/tools/index.html')).href);
  await page.getByRole('button', { name: 'Search docs' }).click();
  await page.locator('#search-input').fill('Hello World');
  await page.locator('#search-results a').first().click();
  await page.waitForURL(pathToFileURL(path.join(moved, 'start/hello-world/index.html')).href);
  assert.equal(await page.locator('h1').textContent(), 'Hello World');
  await page.goto(pathToFileURL(path.join(moved, 'reference/api/gent/index.html')).href);
  const source = page.locator('#runstate-apply .source-link');
  const expectedSource = new URL(await source.getAttribute('href'), page.url()).href;
  await source.click();
  await page.waitForURL(expectedSource);
  assert.equal(await page.locator('link[rel="icon"]').evaluate((node) => node.href),
    pathToFileURL(path.join(moved, 'assets/favicon.svg')).href);
  assert.deepEqual(failures, [], 'moved folder');
  console.log('Passed a moved local folder with spaces.');
} finally {
  try {
    if (browser) await browser.close();
  } finally {
    await Promise.all(servers.filter((server) => server.listening).map((server) =>
      new Promise((resolve, reject) => {
        server.close((error) => error ? reject(error) : resolve());
      })));
    await rm(temporary, { recursive: true, force: true });
  }
}
