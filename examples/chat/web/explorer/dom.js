export function element(tag, className = '', text = '') {
  const node = document.createElement(tag);
  node.className = className;
  node.textContent = text;
  return node;
}

// Every disclosure uses the same heading and body columns. Keep nested content under the
// heading text, not under its marker, so source data and human-readable content share a hierarchy.
export function disclosure(className, title) {
  const node = element('details', `ge-disclosure ${className}`);
  const summary = element('summary');
  const heading = element('span', 'ge-disclosure-heading', title);
  const body = element('div', 'ge-disclosure-body');
  summary.append(heading); node.append(summary, body);
  return {node, heading, body};
}

// Expand on demand and page large containers. Every field remains available without building
// an archive-sized DOM tree on each stream chunk. This viewer treats codec output as plain JSON.
export function jsonTree(value, name = 'data', depth = 0) {
  const compound = value !== null && typeof value === 'object';
  const longText = typeof value === 'string' && value.length > 300;
  if (!compound && !longText) {
    const row = element('div', 'json-leaf');
    row.append(element('span', 'json-key', `${name}: `), element('span',
      typeof value === 'string' ? 'json-string' : 'json-value', JSON.stringify(value)));
    return row;
  }
  const entries = compound ? Object.entries(value) : [];
  const label = longText ? `${value.length} characters` :
    `${entries.length} ${Array.isArray(value) ? 'items' : 'fields'}`;
  const {node, body} = disclosure('ge-json-node', `${name} · ${label}`);
  let loaded = false;
  function load() {
    if (loaded) return;
    loaded = true;
    if (longText) { body.append(element('div', 'json-string', value)); return; }
    let cursor = 0;
    const more = element('button', 'json-more', 'Show next 50 fields');
    function next() {
      more.remove();
      const end = Math.min(entries.length, cursor + 50);
      for (; cursor < end; cursor++) {
        const [key, child] = entries[cursor];
        body.append(jsonTree(child, key, depth + 1));
      }
      if (cursor < entries.length) body.append(more);
    }
    more.addEventListener('click', next);
    next();
  }
  node.addEventListener('toggle', () => { if (node.open) load(); });
  if (depth === 0) { node.open = true; load(); }
  return node;
}

