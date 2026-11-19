import assert from 'node:assert/strict';
import { after, before, test } from 'node:test';
import {
  cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { runInNewContext } from 'node:vm';
import { parse } from 'parse5';
import { build, docs, examples, filesUnder, relativeLink, root } from '../build.mjs';

let temporary;
let output;
let moved;
let manifest;
const read = (name) => readFileSync(name, 'utf8');
const text = (node) => node.nodeName === '#text' ? node.value
  : (node.childNodes ?? []).map(text).join('');

function inspect(source) {
  const result = { ids: [], links: [], icons: [], examples: {} };
  function walk(node, example) {
    const attributes = Object.fromEntries((node.attrs ?? [])
      .map((attr) => [attr.name, attr.value]));
    if (attributes.id) result.ids.push(attributes.id);
    if (node.tagName === 'link' && attributes.rel === 'icon') {
      result.icons.push({ href: attributes.href, type: attributes.type });
    }
    for (const key of ['href', 'src']) {
      if (attributes[key]) result.links.push(attributes[key]);
    }
    example = attributes['data-example'] ?? example;
    if (node.tagName === 'code' && example) result.examples[example] = text(node);
    for (const child of node.childNodes ?? []) walk(child, example);
  }
  walk(parse(source));
  return result;
}

before(() => {
  temporary = mkdtempSync(path.join(tmpdir(), 'gent-docs-check-'));
  output = path.join(temporary, 'site');
  moved = path.join(temporary, 'moved site', 'gent');
  manifest = build(output);
  cpSync(output, moved, { recursive: true });
});
after(() => rmSync(temporary, { recursive: true, force: true }));

test('all pages have an icon and valid links before and after moving the folder', () => {
  const redirects = JSON.parse(read(path.join(docs, 'site.json'))).redirects;
  for (const directory of [output, moved]) {
    const iconPath = path.join(directory, 'assets/favicon.svg');
    const inlineIcon = 'data:image/svg+xml,' + encodeURIComponent(read(iconPath));
    const pages = new Map(filesUnder(directory).filter((name) => name.endsWith('.html'))
      .map((name) => [name, inspect(read(name))]));
    for (const [name, page] of pages) {
      assert.equal(page.ids.length, new Set(page.ids).size, `Duplicate ID: ${name}`);
      const file = path.relative(directory, name).split(path.sep).join('/');
      assert.deepEqual(page.icons.map((icon) => ({ type: icon.type,
        url: new URL(icon.href, pathToFileURL(name)).href })),
      [{ type: 'image/svg+xml',
        url: redirects[file] ? inlineIcon : pathToFileURL(iconPath).href }], name);
      for (const link of page.links) {
        if (/^(?:[a-z]+:|\/\/)/i.test(link)) continue;
        assert.equal(link.startsWith('/'), false, `${name}: absolute local path ${link}`);
        const destination = new URL(link, pathToFileURL(name));
        const file = fileURLToPath(destination);
        assert.equal(statSync(file).isFile(), true, `${name}: ${link}`);
        if (destination.hash && file.endsWith('.html')) {
          assert.equal(pages.get(file).ids.includes(decodeURIComponent(destination.hash.slice(1))),
            true, `${name}: missing fragment ${link}`);
        }
      }
    }
  }
});

test('every displayed example equals its complete source region', () => {
  const shown = {};
  for (const name of filesUnder(output).filter((file) => file.endsWith('index.html'))) {
    Object.assign(shown, inspect(read(name)).examples);
  }
  const expected = Object.fromEntries(Object.entries(examples()).map(([name, item]) =>
    [name, item.code + '\n']));
  assert.deepEqual(shown, expected);
  assert.deepEqual(Object.keys(manifest.examples).sort(), Object.keys(expected).sort());
});

test('navigation, public declarations, and search use the same source', () => {
  const navigation = JSON.parse(read(path.join(docs, 'site.json')));
  const teaching = navigation.groups.flatMap((group) => group.pages.map((page) => page.path));
  const content = filesUnder(path.join(docs, 'content')).map((name) =>
    path.relative(path.join(docs, 'content'), name).split(path.sep).join('/').replace(/\.md$/, ''));
  assert.deepEqual(teaching.toSorted(), content.toSorted());
  assert.equal(teaching.length, 38);
  assert.equal(teaching.every((route) => manifest.pages.some((item) => item.path === route)), true);
  const api = JSON.parse(read(path.join(output, 'api.json')));
  const keys = api.map((item) => `${item.package}/${item.name}`);
  assert.equal(keys.length, new Set(keys).size);
  assert.deepEqual([...new Set(api.map((item) => item.package))].sort(), [
    'gent', 'executor', 'agents', 'agents/react', 'models', 'toolchain', 'toolchain/jsruntime',
    'output', 'schema', 'compaction', 'format', 'search', 'policy', 'trace', 'common',
  ].sort());
  for (const name of ['ExecutionContext', 'RunState', 'RunView', 'Config', 'Recorder',
    'ExecutionContext.RunId', 'RunState.Apply', 'Subscriber', 'Output']) {
    assert.equal(keys.includes(`gent/${name}`), true, name);
  }
  assert.equal(keys.includes('executor/Runner.NewTurn'), true);
  const scope = {};
  runInNewContext(read(path.join(output, 'assets/search-index.js')), scope);
  for (const item of api) {
    assert.equal(scope.gentSearchIndex.some((entry) => entry.title === item.name), true, item.name);
  }
  const sections = new Map();
  for (const entry of scope.gentSearchIndex) {
    assert.equal(entry.url.startsWith('/'), false);
    const destination = new URL(entry.url, pathToFileURL(output + path.sep));
    const file = fileURLToPath(destination);
    assert.equal(statSync(file).isFile(), true, entry.url);
    if (destination.hash) {
      if (!sections.has(file)) sections.set(file, inspect(read(file)).ids);
      assert.equal(sections.get(file).includes(destination.hash.slice(1)), true, entry.url);
    }
  }
});

test('the build includes exact Go and JavaScript source files', () => {
  for (const name of filesUnder(path.join(docs, 'examples'))) {
    assert.deepEqual(readFileSync(path.join(output, '_source', path.relative(docs, name))),
      readFileSync(name));
  }
});

test('output checks preserve unrelated files and reject source aliases', () => {
  const directory = path.join(temporary, 'application');
  mkdirSync(directory);
  const keep = path.join(directory, 'keep.txt');
  writeFileSync(keep, 'Application data');
  assert.throws(() => build(directory));
  assert.equal(read(keep), 'Application data');
  for (const name of [docs, root, path.join(docs, 'examples')]) {
    assert.throws(() => build(name));
  }
  const alias = path.join(temporary, 'source-link');
  symlinkSync(docs, alias, 'dir');
  assert.throws(() => build(alias));
});

test('relative links include explicit files and keep fragments', () => {
  const cases = [
    { input: ['index.html', '/start/setup/'], expected: 'start/setup/index.html' },
    { input: ['agent/tools/index.html', '/'], expected: '../../index.html' },
    { input: ['agent/tools/index.html', '/assets/site.css'], expected: '../../assets/site.css' },
    { input: ['reference/api/gent/index.html', '/reference/api/gent/#runstate-apply'],
      expected: 'index.html#runstate-apply' },
    { input: ['source/internal/core/run.go.html', '/reference/api/'],
      expected: '../../../reference/api/index.html' },
  ];
  for (const { input, expected } of cases) assert.equal(relativeLink(...input), expected);
});
