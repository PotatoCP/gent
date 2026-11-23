import {byId, element} from './ui.js';
import {RequestStatus} from './request.js';

export function createConversations(request, select, notify) {
  const panel = byId('conversations');
  const wide = matchMedia('(min-width: 1200px)');
  const nodes = new Map();
  let selected = '', refreshNumber = 0;

  function show(open) {
    document.body.classList.toggle('chats-hidden', !open);
    byId('chats-toggle').setAttribute('aria-expanded', String(open));
    panel.inert = !open;
    if (open && !wide.matches) document.dispatchEvent(new Event('chat-list-open'));
    window.dispatchEvent(new Event('resize'));
  }
  show(wide.matches);
  wide.addEventListener('change', () => show(wide.matches));
  document.addEventListener('inspector-open', () => { if (!wide.matches) show(false); });
  byId('chats-toggle').addEventListener('click', () => {
    show(document.body.classList.contains('chats-hidden'));
  });
  byId('chats-close').addEventListener('click', () => {
    show(false); byId('chats-toggle').focus();
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && !wide.matches && !panel.inert) {
      show(false); byId('chats-toggle').focus();
    }
  });

  function mark(runId) {
    selected = runId;
    for (const [id, node] of nodes) {
      if (id === runId) node.setAttribute('aria-current', 'page');
      else node.removeAttribute('aria-current');
    }
  }

  async function refresh() {
    const current = ++refreshNumber;
    try {
      const response = await request('/api/conversations');
      if (current !== refreshNumber || response.status !== RequestStatus.Ready) return;
      const keep = new Set();
      let position = byId('conversation-list').firstElementChild;
      for (const chat of response.data) {
        keep.add(chat.runId);
        let node = nodes.get(chat.runId);
        if (!node) {
          node = element('a', 'conversation-link');
          node.href = `/?run=${encodeURIComponent(chat.runId)}`;
          node.dataset.runId = chat.runId;
          node.append(element('strong'), element('span', 'conversation-meta'));
          node.addEventListener('click', event => {
            if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
            event.preventDefault(); select(chat.runId);
            if (!wide.matches) show(false);
          });
          nodes.set(chat.runId, node);
        }
        node.querySelector('strong').textContent = chat.title || 'New conversation';
        const label = chat.scenario === 'airline' ? 'SkyWings' : 'TechGear';
        const status = chat.active ? 'Working' : chat.status === 'paused' ? 'Paused' :
          chat.status ? 'Saved' : 'Needs attention';
        node.querySelector('span').textContent = `${label} · ${status}`;
        node.dataset.active = chat.active;
        node.title = chat.title;
        if (node !== position) byId('conversation-list').insertBefore(node, position);
        position = node.nextElementSibling;
      }
      for (const [id, node] of nodes) {
        if (!keep.has(id)) { node.remove(); nodes.delete(id); }
      }
      byId('chats-empty').hidden = nodes.size > 0;
      mark(selected);
    } catch (error) {
      if (current === refreshNumber) notify(error.message);
    }
  }

  function clear() {
    refreshNumber++;
    nodes.clear(); byId('conversation-list').replaceChildren();
    byId('chats-empty').hidden = false;
  }
  return {refresh, mark, clear};
}
