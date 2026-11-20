import {disclosure, element, jsonTree} from './dom.js';

export function sourceFields(value, path, label = 'Source fields') {
  const {node, body} = disclosure('ge-source', label);
  let loaded = false;
  node.addEventListener('toggle', () => {
    if (!node.open || loaded) return;
    loaded = true;
    const tree = element('div', 'json-tree');
    tree.append(jsonTree(value, 'Source'));
    if (path) body.append(element('code', 'ge-path', path));
    body.append(tree);
  });
  return node;
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

