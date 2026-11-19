// This server is only for local preview. Published documentation consists of static files.
import { createServer } from 'node:http';
import { readFile, realpath, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const docs = path.dirname(fileURLToPath(import.meta.url));
const types = { '.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8', '.json': 'application/json', '.svg': 'image/svg+xml',
  '.woff2': 'font/woff2', '.go': 'text/plain; charset=utf-8' };
const within = (root, file) => file === root || file.startsWith(root + path.sep);

export async function createDocsServer(directory, { basePath = '/' } = {}) {
  if (!/^\/[A-Za-z0-9_/-]*\/$/.test(basePath) && basePath !== '/') {
    throw new Error('The base path must start and end with a slash.');
  }
  if (basePath.includes('//')) throw new Error('The base path cannot contain empty segments.');
  const root = await realpath(directory);
  await stat(path.join(root, 'index.html'));
  return createServer(async (request, response) => {
    const url = new URL(request.url, 'http://localhost');
    if (!url.pathname.startsWith(basePath)) {
      response.writeHead(404).end('Page not found.');
      return;
    }
    let name;
    try {
      name = decodeURIComponent(url.pathname.slice(basePath.length));
    } catch {
      response.writeHead(400).end('Invalid path encoding.');
      return;
    }
    let file = path.resolve(root, name);
    if (!within(root, file)) {
      response.writeHead(403).end('Path outside the documentation.');
      return;
    }
    try {
      if ((await stat(file)).isDirectory()) {
        if (!url.pathname.endsWith('/')) {
          response.writeHead(307, { Location: `${url.pathname}/${url.search}` }).end();
          return;
        }
        file = path.join(file, 'index.html');
      }
      if (!within(root, await realpath(file))) {
        response.writeHead(403).end('Path outside the documentation.');
        return;
      }
      const data = await readFile(file);
      response.writeHead(name === '404.html' ? 404 : 200,
        { 'Content-Type': types[path.extname(file)] ?? 'application/octet-stream' });
      response.end(data);
    } catch (error) {
      if (error.code === 'ENOENT' || error.code === 'ENOTDIR') {
        // Relative assets need the real page URL, even when the missing route is deeply nested.
        response.writeHead(307, { Location: `${basePath}404.html` }).end();
        return;
      }
      console.error(error);
      response.writeHead(500).end('Could not read the documentation file.');
    }
  });
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { values } = parseArgs({ options: {
    directory: { type: 'string', default: path.join(docs, 'dist') },
    port: { type: 'string', default: '4173' },
  } });
  const server = await createDocsServer(values.directory);
  server.listen(Number(values.port), '127.0.0.1', () => {
    console.log(`Gent docs: http://127.0.0.1:${server.address().port}`);
  });
  process.once('SIGINT', () => server.close());
  process.once('SIGTERM', () => server.close());
}
