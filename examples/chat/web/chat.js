import {ContentType} from './state.js';
import {byId, element, richText, copyText} from './ui.js';

function outlineIcon(path) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', 'chat-icon');
  svg.setAttribute('aria-hidden', 'true');
  const line = document.createElementNS(svg.namespaceURI, 'path');
  line.setAttribute('d', path); svg.append(line);
  return svg;
}

function toolGroup() {
  const node = element('article', 'message tool-group');
  const details = element('details', 'tool-group-toggle');
  const summary = element('summary', 'tool-group-heading');
  summary.append(outlineIcon('M20 20H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h5l2 3h9' +
    'a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2Z'), element('span', '', 'Ran tools'));
  const list = element('div', 'tool-list');
  details.append(summary, list); node.append(details);
  return {node, list};
}

// Move only changed positions. Keeping disclosure nodes and summaries in place preserves the
// user's open state and keyboard focus while live updates become accepted messages.
function place(parent, node, position) {
  if (node !== position) parent.insertBefore(node, position);
  return node.nextElementSibling;
}

export function createChat(inspect, notify) {
  const nodes = new Map(), groups = new Map();
  const scroll = byId('chat-scroll');
  let follow = true;
  scroll.addEventListener('scroll', () => {
    follow = scroll.scrollHeight - scroll.scrollTop - scroll.clientHeight < 70;
    byId('jump-latest').hidden = follow;
  });
  byId('jump-latest').addEventListener('click', () => {
    follow = true; scroll.scrollTop = scroll.scrollHeight;
  });

  function render(items, active) {
    const keep = new Set(), keepGroups = new Set();
    let position = byId('messages').firstElementChild;
    let group, childPosition;
    for (const item of items) {
      const key = item.type === ContentType.Accepted ? item.sources?.[0] || item.id : item.id;
      keep.add(key);
      let saved = nodes.get(key);
      const signature = JSON.stringify(item);
      if (!saved) {
        saved = {node: element('article', 'message'), signature: ''};
        nodes.set(key, saved);
      }
      if (saved.signature !== signature) {
        update(saved.node, item);
        saved.signature = signature;
      }
      if (item.kind === 'tool') {
        // This is a display group of consecutive tools, not a claim that they ran in parallel.
        // Any text message starts a new group. The inspector keeps saved request relationships.
        if (!group) {
          group = groups.get(key) || toolGroup();
          groups.set(key, group); keepGroups.add(key);
          position = place(byId('messages'), group.node, position);
          childPosition = group.list.firstElementChild;
        }
        childPosition = place(group.list, saved.node, childPosition);
      } else {
        group = undefined;
        position = place(byId('messages'), saved.node, position);
      }
    }
    for (const [key, saved] of nodes) {
      if (!keep.has(key)) { saved.node.remove(); nodes.delete(key); }
    }
    for (const [key, saved] of groups) {
      if (!keepGroups.has(key)) { saved.node.remove(); groups.delete(key); }
    }
    byId('empty').hidden = items.length > 0;
    byId('working').hidden = !active;
    if (follow) scroll.scrollTop = scroll.scrollHeight;
  }

  function updateTool(article, item, body) {
    let details = article.querySelector('details');
    if (!details) {
      details = element('details', 'tool-toggle');
      details.append(element('summary'), element('div', 'disclosure-body tool-content'));
      article.replaceChildren(details);
    }
    const label = item.status === 'running' ? 'Running' :
      item.status === 'complete' ? 'Complete' : 'Needs attention';
    details.querySelector('summary').replaceChildren(
      outlineIcon('M8 8l4 4-4 4m6 0h3M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5' +
        'a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z'),
      element('span', 'tool-name', item.label), element('span', 'tool-preview', item.args || ''),
      element('span', 'tool-status', label));
    const content = details.querySelector('.tool-content');
    content.replaceChildren(element('h3', '', 'Input'),
      element('pre', 'tool-args', item.args || 'No input'), element('h3', '', 'Result'), body);
    if (!item.text) body.textContent = 'Waiting for the tool result…';
    const button = element('button', 'text-button', 'Inspect tool event ↗');
    button.addEventListener('click', () => inspect(item));
    content.append(button);
  }

  function update(article, item) {
    const reasoning = item.type === ContentType.Reasoning;
    const commentary = item.type === ContentType.Commentary;
    const kind = item.kind === 'user' ? 'user' : item.kind === 'tool' ? 'tool' :
      reasoning ? 'reasoning' : commentary ? 'commentary' : 'assistant';
    article.className = `message ${kind}`;
    article.id = `message-${item.id}`;
    article.dataset.type = item.type || '';
    article.dataset.delivery = item.accepted ? 'accepted' : 'live';
    article.dataset.status = item.status || '';
    article.setAttribute('aria-label', item.label || (reasoning ? 'Reasoning' : 'Assistant'));
    const body = element('div', 'message-body');
    if (kind === 'user') body.textContent = item.text;
    else body.append(richText(item.text));
    if (kind === 'tool') { updateTool(article, item, body); return; }
    // Exposed reasoning and commentary remain visible, separate text items. Only the accepted
    // answer receives final-answer actions; neither progress nor opaque context becomes an answer.
    article.replaceChildren(body);
    if (item.type === ContentType.Accepted) {
      const actions = element('div', 'answer-actions');
      const copy = element('button', '', 'Copy');
      copy.addEventListener('click', () => copyText(item.text, copy, notify));
      const source = element('button', 'source-link', 'Inspect response ↗');
      source.addEventListener('click', () => inspect(item));
      actions.append(copy, source);
      article.append(actions);
    }
  }
  return {render};
}
