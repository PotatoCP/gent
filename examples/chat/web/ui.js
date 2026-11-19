import {element} from './explorer/dom.js';
export {element, jsonTree} from './explorer/dom.js';

export const byId = id => document.getElementById(id);

// Model text is untrusted. Build a small Markdown subset with DOM nodes, never HTML strings.
function inline(parent, text) {
  const pattern = /\*\*(.+?)\*\*|`([^`]+)`|\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g;
  let start = 0;
  for (const match of text.matchAll(pattern)) {
    parent.append(document.createTextNode(text.slice(start, match.index)));
    const node = element(match[1] ? 'strong' : match[2] ? 'code' : 'a', '',
      match[1] || match[2] || match[3]);
    if (match[4]) {
      node.href = match[4]; node.target = '_blank';
      node.rel = 'noopener noreferrer';
    }
    parent.append(node);
    start = match.index + match[0].length;
  }
  parent.append(document.createTextNode(text.slice(start)));
}

export function richText(text) {
  const fragment = document.createDocumentFragment();
  const lines = text.split('\n');
  let index = 0;
  while (index < lines.length) {
    const line = lines[index++];
    if (!line.trim()) continue;
    if (line.startsWith('```')) {
      const code = [];
      while (index < lines.length && !lines[index].startsWith('```')) code.push(lines[index++]);
      index++;
      const pre = element('pre');
      pre.append(element('code', '', code.join('\n')));
      fragment.append(pre);
      continue;
    }
    const heading = line.match(/^(#{1,4})\s+(.+)/);
    if (heading) {
      const node = element(`h${Math.min(4, heading[1].length + 1)}`);
      inline(node, heading[2]); fragment.append(node); continue;
    }
    if (/^([-*]|\d+\.)\s/.test(line)) {
      const list = element(/^\d/.test(line) ? 'ol' : 'ul');
      let entry = line;
      do {
        const item = element('li'); inline(item, entry.replace(/^([-*]|\d+\.)\s+/, ''));
        list.append(item);
        if (index >= lines.length || !/^([-*]|\d+\.)\s/.test(lines[index])) break;
        entry = lines[index++];
      } while (true);
      fragment.append(list); continue;
    }
    const paragraph = [line];
    while (index < lines.length && lines[index].trim() &&
      !/^(```|#{1,4}\s|[-*]\s|\d+\.\s)/.test(lines[index])) paragraph.push(lines[index++]);
    const node = element(line.startsWith('> ') ? 'blockquote' : 'p');
    inline(node, paragraph.join('\n').replace(/^> /gm, ''));
    fragment.append(node);
  }
  return fragment;
}

export async function copyText(text, button, notify) {
  try {
    await navigator.clipboard.writeText(text);
    const label = button.textContent;
    button.textContent = 'Copied';
    setTimeout(() => { button.textContent = label; }, 1500);
  } catch (error) { notify(`Cannot copy: ${error.message}`); }
}
