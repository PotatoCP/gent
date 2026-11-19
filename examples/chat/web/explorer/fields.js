import {element, jsonTree} from './dom.js';

export function sourceFields(value, path, label = 'Source fields') {
  const details = element('details', 'ge-source');
  details.append(element('summary', '', label));
  let loaded = false;
  details.addEventListener('toggle', () => {
    if (!details.open || loaded) return;
    loaded = true;
    const tree = element('div', 'json-tree');
    tree.append(jsonTree(value, 'Source'));
    if (path) details.append(element('code', 'ge-path', path));
    details.append(tree);
  });
  return details;
}

export function facts(items) {
  const list = element('dl', 'ge-facts');
  for (const [name, value] of items) {
    const pair = element('div');
    pair.append(element('dt', '', name), element('dd', '', String(value ?? '—')));
    list.append(pair);
  }
  return list;
}

export function section(title, note) {
  const node = element('section', 'ge-section');
  node.append(element('h5', '', title));
  if (note) node.append(element('p', 'ge-note', note));
  return node;
}

