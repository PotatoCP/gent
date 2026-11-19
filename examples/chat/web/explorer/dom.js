export function element(tag, className = '', text = '') {
  const node = document.createElement(tag);
  node.className = className;
  node.textContent = text;
  return node;
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
  const details = element('details');
  const entries = compound ? Object.entries(value) : [];
  const label = longText ? `${value.length} characters` :
    `${entries.length} ${Array.isArray(value) ? 'items' : 'fields'}`;
  details.append(element('summary', '', `${name} · ${label}`));
  let loaded = false;
  function load() {
    if (loaded) return;
    loaded = true;
    if (longText) { details.append(element('div', 'json-string', value)); return; }
    let cursor = 0;
    const more = element('button', 'json-more', 'Show next 50 fields');
    function next() {
      more.remove();
      const end = Math.min(entries.length, cursor + 50);
      for (; cursor < end; cursor++) {
        const [key, child] = entries[cursor];
        details.append(jsonTree(child, key, depth + 1));
      }
      if (cursor < entries.length) details.append(more);
    }
    more.addEventListener('click', next);
    next();
  }
  details.addEventListener('toggle', () => { if (details.open) load(); });
  if (depth === 0) { details.open = true; load(); }
  return details;
}

