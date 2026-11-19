// Build static pages from the checked examples and the current public Go declarations.
import {
  cpSync, existsSync, mkdirSync, readdirSync, readFileSync, realpathSync, rmSync, writeFileSync,
} from 'node:fs';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import MarkdownIt from 'markdown-it';
import highlight from 'highlight.js/lib/core';
import go from 'highlight.js/lib/languages/go';
import javascript from 'highlight.js/lib/languages/javascript';
import bash from 'highlight.js/lib/languages/bash';
import { parseFragment } from 'parse5';

export const docs = path.dirname(fileURLToPath(import.meta.url));
export const root = path.dirname(docs);
highlight.registerLanguage('go', (engine) => {
  const language = go(engine);
  const reserved = new Set(Object.values(language.keywords).flat());
  const exportedName = { scope: 'symbol', match: /\b[A-Z][A-Za-z0-9_]*\b/, relevance: 0 };
  // The base Go grammar leaves calls and exported names unmarked. These syntax rules also work
  // on short fragments; they do not need a complete file or claim to resolve Go types.
  language.contains.push({
    scope: 'title.function',
    match: /\b[A-Za-z_][A-Za-z0-9_]*(?=\s*\()/,
    relevance: 0,
    'on:begin': (match, response) => {
      if (reserved.has(match[0])) response.ignoreMatch();
    },
  }, exportedName);
  const signature = language.contains.find((mode) => mode.className === 'function');
  signature.contains.find((mode) => mode.className === 'params').contains = [exportedName];
  return language;
});
highlight.registerLanguage('js', javascript);
highlight.registerLanguage('bash', bash);

export function filesUnder(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const name = path.join(directory, entry.name);
    return entry.isDirectory() ? filesUnder(name) : [name];
  }).sort();
}

const portable = (name) => name.split(path.sep).join('/');
const read = (name) => readFileSync(name, 'utf8');
const slug = (value) => value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
const escaped = (value) => String(value).replace(/[&<>"']/g,
  (char) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);

function textContent(node) {
  if (node.nodeName === '#text') return node.value;
  return (node.childNodes ?? []).map(textContent).join('');
}

function plainText(source) {
  return textContent(parseFragment(source)).replace(/\s+/g, ' ').trim();
}

export function examples() {
  const found = {};
  for (const name of filesUnder(path.join(docs, 'examples'))) {
    if (!['.go', '.js'].includes(path.extname(name))) continue;
    let active;
    let first;
    let lines = [];
    for (const [offset, line] of read(name).split(/\r?\n/).entries()) {
      const start = line.match(/^\s*\/\/ docs:start ([a-z0-9-]+)$/);
      const end = line.match(/^\s*\/\/ docs:end ([a-z0-9-]+)$/);
      if (start) {
        if (active || found[start[1]]) throw new Error(`Duplicate example: ${name}:${offset + 1}`);
        active = start[1];
        first = offset + 2;
        lines = [];
      } else if (end) {
        if (active !== end[1]) throw new Error(`Unmatched example: ${name}:${offset + 1}`);
        const prefixes = lines.filter((value) => value.trim())
          .map((value) => value.match(/^\s*/)[0]);
        let indent = prefixes[0] ?? '';
        for (const prefix of prefixes) {
          while (!prefix.startsWith(indent)) indent = indent.slice(0, -1);
        }
        const code = lines.map((value) => value.slice(indent.length)).join('\n')
          .replace(/\t/g, '    ').replace(/^\n+|\n+$/g, '');
        found[active] = { code, file: portable(path.relative(docs, name)), line: first,
          endLine: offset, language: name.endsWith('.go') ? 'go' : 'js' };
        active = undefined;
      } else if (active) {
        lines.push(line);
      }
    }
    if (active) throw new Error(`Unclosed example: ${name}`);
  }
  return found;
}

function codeBlock(source, language = 'go', sourceLink, identifier) {
  const code = source.replace(/\n$/, '') + '\n';
  const rendered = highlight.highlight(code, { language }).value;
  const link = sourceLink ? `<a href="${escaped(sourceLink)}">Complete source ↗</a>` : '';
  const attribute = identifier ? ` data-example="${identifier}"` : '';
  return `<figure class="code-example"${attribute}><figcaption>`
    + `<span>${escaped(language)}</span><span>${link}`
    + '<button class="copy" type="button" aria-label="Copy code">Copy</button>'
    + `</span></figcaption><pre><code>${rendered}</code></pre></figure>`;
}

// Explicit relative filenames work with file:// and at any HTTP mount path.
export function relativeLink(from, target) {
  const [, pathname, suffix] = target.match(/^([^?#]*)(.*)$/);
  let destination = pathname.replace(/^\//, '');
  if (!destination || destination.endsWith('/')) destination += 'index.html';
  return (path.posix.relative(path.posix.dirname(from), destination)
    || path.posix.basename(destination)) + suffix;
}

function relativeLinks(source, file) {
  return source.replace(/\b(href|src)="(\/(?!\/)[^"]*)"/g,
    (_, attribute, target) => `${attribute}="${relativeLink(file, target)}"`);
}

function physicalPath(name) {
  if (existsSync(name)) return realpathSync(name);
  return path.join(physicalPath(path.dirname(name)), path.basename(name));
}

function prepareOutput(destination) {
  const output = physicalPath(path.resolve(destination));
  const repo = realpathSync(root);
  const within = (parent, child) => child === parent || child.startsWith(parent + path.sep);
  if (within(output, repo) || (within(repo, output) && output !== path.join(repo, 'docs/dist'))) {
    throw new Error('Use docs/dist or a dedicated directory outside the repository for output.');
  }
  const marker = path.join(output, '.gent-docs-output');
  if (existsSync(output) && readdirSync(output).length && !existsSync(marker)) {
    throw new Error('The non-empty output directory is not a Gent documentation build.');
  }
  mkdirSync(output, { recursive: true });
  // Replace generated output only. Never treat source or application data as build output.
  for (const entry of readdirSync(output)) {
    rmSync(path.join(output, entry), { recursive: true, force: true });
  }
  writeFileSync(marker, 'Generated by docs/build.mjs. This directory can be replaced.\n');
  return output;
}

function markdownPage(source) {
  const markdown = new MarkdownIt({ html: true });
  markdown.renderer.rules.fence = (tokens, index) =>
    codeBlock(tokens[index].content, tokens[index].info.trim() || 'bash');
  const tokens = markdown.parse(source, {});
  const headings = [];
  const ids = new Map();
  for (let index = 0; index < tokens.length; index++) {
    if (tokens[index].type !== 'heading_open') continue;
    const title = plainText(markdown.renderInline(tokens[index + 1].content));
    const base = slug(title);
    const count = ids.get(base) ?? 0;
    ids.set(base, count + 1);
    const id = count ? `${base}-${count}` : base;
    tokens[index].attrSet('id', id);
    headings.push(`<li><a href="#${id}">${escaped(title)}</a></li>`);
  }
  return { body: markdown.renderer.render(tokens, markdown.options, {}),
    toc: headings.length ? `<ul>${headings.join('')}</ul>` : '' };
}

export function build(destination = path.join(docs, 'dist')) {
  const output = prepareOutput(destination);
  const site = JSON.parse(read(path.join(docs, 'site.json')));
  const samples = examples();
  const used = new Set();
  cpSync(path.join(docs, 'assets'), path.join(output, 'assets'), { recursive: true });
  // Go ignores underscore directories. Published source must not become a second test package.
  cpSync(path.join(docs, 'examples'), path.join(output, '_source/examples'), { recursive: true });
  const api = JSON.parse(execFileSync('go', ['run', './docs/tools/api.go'], {
    cwd: root, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024,
  }));
  const version = execFileSync('git', ['rev-parse', '--short', 'HEAD'], {
    cwd: root, encoding: 'utf8',
  }).trim();
  const search = [];
  const manifest = { version, pages: [], examples: {}, symbols: api.length };
  const write = (file, content) => {
    mkdirSync(path.dirname(path.join(output, file)), { recursive: true });
    writeFileSync(path.join(output, file), content);
  };

  function navigation(current) {
    const groups = site.groups.map((group) => {
      const links = group.pages.map((page) => {
        const selected = current === page.path ? ' aria-current="page"' : '';
        return `<a href="/${page.path}/"${selected}>${escaped(page.title)}</a>`;
      });
      return `<section><p class="nav-label">${escaped(group.title)}</p>${links.join('')}</section>`;
    });
    return `<nav aria-label="Documentation">${groups.join('')}</nav>`;
  }

  // Every HTML template needs an icon. Otherwise browsers can request /favicon.ico outside
  // the documentation folder. relativeLinks keeps this asset valid at each hosting path.
  const icon = '<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">';

  function writePage(route, title, group, body, { toc = '', description = '', neighbors } = {}) {
    let previousNext = '';
    if (neighbors) {
      const links = neighbors.map((page, index) => page
        ? `<a href="/${page.path}/"><small>${index ? 'Next' : 'Previous'}</small>`
          + `${escaped(page.title)}</a>` : '<span></span>');
      previousNext = `<nav class="page-links" aria-label="Pages">${links.join('')}</nav>`;
    }
    const file = route ? `${route}/index.html` : 'index.html';
    const document = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="description" content="${escaped(description)}">
<meta name="theme-color" content="#152925">
<title>${escaped(title)} · Gent</title>
${icon}
<link rel="stylesheet" href="/assets/site.css">
<script src="/assets/site.js" defer data-docs></script>
</head><body>
<a class="skip" href="#main">Skip to content</a>
<header class="topbar">
<a class="brand" href="/" aria-label="Gent home">gent<span class="brand-dot">.</span></a>
<span class="edition">Go agent framework <span>Development · ${version}</span></span>
<button id="search-open" class="search-open" type="button">Search docs <kbd>⌘ K</kbd></button>
<a class="repo-link" href="https://github.com/rickchristie/gent">Source ↗</a>
<button id="menu-open" class="menu-open" type="button" aria-label="Open navigation">Menu</button>
</header>
<div class="layout">
<aside class="sidebar">${navigation(route)}</aside>
<main id="main" tabindex="-1">
<p class="eyebrow">${escaped(group)}</p><h1>${escaped(title)}</h1>
${body}
${previousNext}
<footer>Gent documentation · Source-based examples · Go</footer>
</main>
<aside class="page-toc" aria-label="On this page" ${toc ? '' : 'hidden'}>
<p>On this page</p>${toc}</aside>
</div>
<dialog id="search-dialog" aria-label="Search documentation">
<form method="dialog"><button class="dialog-close" aria-label="Close search">Close</button></form>
<label for="search-input">Search Gent</label>
<input id="search-input" type="search" autocomplete="off" placeholder="Try tools or recovery">
<p id="search-status" role="status">Search pages and public API names.</p>
<nav id="search-results" aria-label="Search results"></nav>
</dialog>
<dialog id="menu-dialog" aria-label="Documentation navigation">
<form method="dialog">
<button class="dialog-close" aria-label="Close navigation">Close</button></form>
${navigation(route)}</dialog>
<span id="copy-status" class="sr-only" role="status"></span>
</body></html>`;
    write(file, relativeLinks(document, file));
    search.push({ title, group, url: file, text: plainText(body).slice(0, 16000) });
    manifest.pages.push({ path: route, title, file });
    return document;
  }

  const pages = site.groups.flatMap((group) => group.pages.map((page) => ({ ...page,
    group: group.title })));
  for (const [index, page] of pages.entries()) {
    let source = read(path.join(docs, 'content', `${page.path}.md`));
    const fragments = new Map();
    const insert = (name, value) => {
      fragments.set(name, value);
      return `<div data-doc-fragment="${name}"></div>`;
    };
    source = source.replace(/\{\{example:([a-z0-9-]+)\}\}/g, (_, key) => {
      const item = samples[key];
      if (!item) throw new Error(`Missing example ${key} on ${page.path}`);
      if (used.has(key)) throw new Error(`Example ${key} has more than one teaching location`);
      used.add(key);
      manifest.examples[key] = { ...item, page: page.path,
        digest: createHash('sha256').update(item.code).digest('hex') };
      return insert(`example:${key}`,
        codeBlock(item.code, item.language, `/_source/${item.file}`, key));
    });
    source = source.replace(/\{\{values:([A-Za-z0-9]+)\}\}/g, (_, name) => {
      const pattern = new RegExp(`^const \\w+ ${name} =`);
      const items = api.filter((item) => item.package === 'gent' && pattern.test(item.code));
      if (!items.length) throw new Error(`No named values for ${name}`);
      const rows = items.map((item) => '<tr><td>'
        + `<a href="/reference/api/gent/#${slug(item.name)}"><code>${item.name}</code></a></td>`
        + `<td>${escaped(item.doc)}</td></tr>`);
      return insert(`values:${name}`,
        '<table><thead><tr><th>Value</th><th>Meaning</th></tr></thead><tbody>'
        + rows.join('') + '</tbody></table>');
    });
    let { body, toc } = markdownPage(source);
    // Insert rendered code after Markdown parsing. Blank lines inside code must stay unchanged.
    body = body.replace(/<div data-doc-fragment="([^"]+)"><\/div>/g,
      (_, name) => fragments.get(name));
    writePage(page.path, page.title, page.group, body, { toc, description: page.description,
      neighbors: [pages[index - 1], pages[index + 1]] });
  }
  const unused = Object.keys(samples).filter((key) => !used.has(key));
  if (unused.length) throw new Error(`Examples without a teaching page: ${unused.join(', ')}`);

  for (const name of [...new Set(api.map((item) => item.package))].sort()) {
    const items = api.filter((item) => item.package === name);
    let body = '<p>Declarations come from the current source. Values and views follow their '
      + 'documented ownership contracts.</p><div class="symbol-index">';
    body += items.map((item) => `<a href="#${slug(item.name)}">${escaped(item.name)}</a>`).join('');
    body += '</div>';
    for (const item of items) {
      const id = slug(item.name);
      body += `<section class="api-symbol" id="${id}"><h2>${escaped(item.name)}</h2>`
        + `<p class="symbol-kind">${escaped(item.kind)}</p>${codeBlock(item.code)}`
        + `<div class="api-doc">${escaped(item.doc)}</div>`
        + `<a class="source-link" href="/source/${item.source}.html#L-${item.line}">`
        + 'Source ↗</a></section>';
      search.push({ title: item.name, group: `API · ${name}`,
        url: `reference/api/${name}/index.html#${id}`, text: item.doc.slice(0, 1200) });
    }
    writePage(`reference/api/${name}`, name, 'API reference', body,
      { description: `Gent public Go declarations in ${name}.` });
  }

  // Include exact source from this checkout, including APIs that are not in a release yet.
  const sources = new Set([...api.map((item) => item.source), ...site.sourceFiles]);
  for (const name of [...sources].sort()) {
    const file = `source/${name}.html`;
    const lines = highlight.highlight(read(path.join(root, name)), { language: 'go' }).value
      .split('\n').map((line, index) => `<a class="line-number" id="L-${index + 1}"`
        + ` href="#L-${index + 1}">${index + 1}</a>${line}`).join('\n');
    write(file, relativeLinks(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escaped(name)} · Gent source</title>
${icon}
<link rel="stylesheet" href="/assets/site.css"></head><body class="source-page">
<a href="/reference/api/">API reference</a><h1>${escaped(name)}</h1>
<div class="code-example"><pre><code>${lines}</code></pre></div></body></html>`, file));
  }

  const home = `<p class="lead">Build agents.<br>Keep control.</p>
<p class="home-intro">Gent gives your Go application typed tools, custom agent loops,
streaming events, and recoverable conversations.</p>
<a class="primary-link" href="/start/hello-world/">Run Hello World <span>→</span></a>
<div class="home-rule"></div>
<h2 id="start-small">Start small. Add what you need.</h2>
<div class="home-topics">
<a href="/agent/tools/"><span>01 / BUILD</span><h3>Give the agent tools</h3>
<p>Use typed Go functions. Keep application checks in your code.</p></a>
<a href="/storage/restore/"><span>02 / RECOVER</span><h3>Continue saved work</h3>
<p>Restore accepted state from batches or a RunStateSnapshot and its tail.</p></a>
<a href="/observe/streaming/"><span>03 / OBSERVE</span><h3>Show what happens</h3>
<p>Display model output, tool activity, and complete accepted answers.</p></a>
<a href="/extend/loops/"><span>04 / EXTEND</span><h3>Write your own loop</h3>
<p>Use explicit interfaces with the same managed execution services.</p></a>
</div>
<p class="development-note">These pages describe the development checkout.
Use <a href="/start/setup/">Setup</a> to run the examples with matching source.</p>`;
  writePage('', 'Gent, in Go.', 'Documentation', home,
    { description: 'Build Go agents with typed tools and recoverable conversations.' });
  const missing = writePage('404', 'Page not found', 'Documentation',
    '<p>Use search or <a href="/">open the documentation home</a>.</p>');
  write('404.html', relativeLinks(missing, '404.html'));
  // An immediate redirect can cancel an external icon request. Embed the same small SVG so
  // these pages do not start a request that their own navigation can interrupt.
  const redirectIcon = '<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,'
    + encodeURIComponent(read(path.join(docs, 'assets/favicon.svg'))) + '">';
  for (const [old, next] of Object.entries(site.redirects)) {
    const target = escaped(relativeLink(old, '/' + next));
    write(old, '<!doctype html><html lang="en"><head><meta charset="utf-8">'
      + '<title>Gent documentation moved</title>'
      + redirectIcon
      + `<meta http-equiv="refresh" content="0; url=${target}">`
      + `<link rel="canonical" href="${target}"></head>`
      + `<body><p><a href="${target}">Open the current documentation.</a></p></body></html>`);
  }
  // A classic script loads on both file:// and HTTP. Search does not need fetch or a server.
  write('assets/search-index.js', `globalThis.gentSearchIndex = ${JSON.stringify(search)};\n`);
  write('manifest.json', JSON.stringify(manifest, null, 2));
  write('api.json', JSON.stringify(api));
  write('.nojekyll', '');
  console.log(`Built ${manifest.pages.length} pages, ${used.size} examples, `
    + `and ${api.length} API entries in ${output}`);
  return manifest;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { values } = parseArgs({ options: { output: { type: 'string' } } });
  build(values.output);
}
