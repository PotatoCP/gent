import {Delivery, eventPage, receiptKey} from './state.js';
import {byId, element, jsonTree, copyText} from './ui.js';
import {RequestStatus} from './request.js';
import {setupInspectorResize} from './resize.js';
import {createRunExplorer} from './explorer/index.js';
import {parseInspectionJSON} from './explorer/data.js';

export function createInspector(request, notify) {
  let state;
  let tab = 'events';
  let page = 0;
  let selected = '';
  let detailRecord;
  let savedData;
  let explorer;
  let loading = false;
  let generation = 0;
  let rowSignature = '';
  const mobile = matchMedia('(max-width: 800px)');
  const panel = byId('inspector');
  const fullscreen = byId('inspector-fullscreen');
  let expanded = false;
  setupInspectorResize();

  function updateFocusScope() {
    const open = !document.body.classList.contains('inspector-hidden');
    document.querySelector('.conversation').inert = expanded || (open && mobile.matches);
    document.querySelector('.app-header').inert = expanded;
  }
  function expand(open) {
    expanded = open;
    document.body.classList.toggle('inspector-fullscreen', open);
    fullscreen.setAttribute('aria-pressed', String(open));
    fullscreen.setAttribute('aria-label', open ? 'Exit inspector full screen' :
      'Open inspector full screen');
    fullscreen.textContent = open ? '↙ Exit full screen' : '⛶ Full screen';
    if (open) {
      panel.setAttribute('role', 'dialog'); panel.setAttribute('aria-modal', 'true');
    } else {
      panel.removeAttribute('role'); panel.removeAttribute('aria-modal');
    }
    updateFocusScope();
  }
  function show(open) {
    if (!open) expand(false);
    document.body.classList.toggle('inspector-hidden', !open);
    byId('inspector-toggle').setAttribute('aria-expanded', String(open));
    updateFocusScope();
  }
  show(!mobile.matches);
  mobile.addEventListener('change', () => {
    if (!expanded) show(!mobile.matches);
  });
  fullscreen.addEventListener('click', () => expand(!expanded));
  byId('inspector-toggle').addEventListener('click', () => {
    show(document.body.classList.contains('inspector-hidden'));
  });
  byId('inspector-close').addEventListener('click', () => {
    show(false); byId('inspector-toggle').focus();
  });
  document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && expanded) {
      event.preventDefault(); expand(false); fullscreen.focus(); return;
    }
    if (event.key === 'Escape' && !document.body.classList.contains('inspector-hidden')) {
      show(false); byId('inspector-toggle').focus();
    }
    if (event.key !== 'Tab' || !expanded) return;
    const targets = [...panel.querySelectorAll(
      'button, input, select, textarea, a[href], summary, [tabindex="0"]')]
      .filter(node => !node.disabled && node.tabIndex >= 0 && node.getClientRects().length);
    const target = event.shiftKey ? targets.at(-1) : targets[0];
    const boundary = event.shiftKey ? targets[0] : targets.at(-1);
    if (document.activeElement === boundary || !panel.contains(document.activeElement)) {
      event.preventDefault(); target.focus();
    }
  });

  function selectTab(next) {
    tab = next;
    for (const name of ['events', 'view', 'state']) {
      byId(`tab-${name}`).setAttribute('aria-selected', String(tab === name));
      byId(`tab-${name}`).tabIndex = tab === name ? 0 : -1;
    }
    byId('panel-events').hidden = tab !== 'events';
    byId('panel-data').hidden = tab === 'events';
    byId('panel-data').setAttribute('aria-labelledby', `tab-${tab}`);
    generation++;
    loading = false;
    if (tab !== 'events') loadData();
  }
  const names = ['events', 'view', 'state'];
  for (const name of names) {
    byId(`tab-${name}`).addEventListener('click', () => selectTab(name));
    byId(`tab-${name}`).addEventListener('keydown', event => {
      if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
      event.preventDefault();
      const index = event.key === 'Home' ? 0 : event.key === 'End' ? 2 :
        (names.indexOf(name) + (event.key === 'ArrowRight' ? 1 : 2)) % 3;
      selectTab(names[index]); byId(`tab-${names[index]}`).focus();
    });
  }

  function renderEvents() {
    const result = eventPage(state, byId('event-search').value,
      byId('event-delivery').value, page);
    page = result.page;
    byId('events-empty').hidden = result.total > 0;
    byId('events-empty').textContent = state.runId ? 'No matching events.' :
      'Events appear here when the agent starts.';
    byId('event-count').textContent = String(state.receipts.size);
    byId('events-page').textContent = `${result.total} events · ${page + 1} / ${result.pages}`;
    byId('events-prev').disabled = page === 0;
    byId('events-next').disabled = page + 1 === result.pages;
    const signature = result.rows.map(row => row.key).join('|') + selected;
    if (signature === rowSignature) return;
    rowSignature = signature;
    const focus = document.activeElement?.dataset.key;
    byId('events').replaceChildren(...result.rows.map(row => {
      const item = element('li');
      const button = element('button', 'event-row');
      button.dataset.key = row.key;
      button.dataset.eventSequenceNumber = row.event.eventSequenceNumber;
      button.dataset.delivery = row.delivery;
      button.title = row.event.name;
      button.setAttribute('aria-pressed', String(selected === row.key));
      const position = String(row.event.eventSequenceNumber).padStart(3, '0');
      button.append(element('span', 'event-sequence-number', position),
        element('span', 'event-name', row.event.name),
        element('span', 'event-badge', row.delivery === Delivery.Accepted ? 'ACCEPTED' : 'LIVE'));
      button.addEventListener('click', () => selectEvent(row.key));
      item.append(button);
      return item;
    }));
    if (focus) {
      const buttons = [...byId('events').querySelectorAll('button')];
      buttons.find(node => node.dataset.key === focus)?.focus();
    }
  }

  function selectEvent(key) {
    selected = key;
    const row = state.receipts.get(key);
    byId('event-detail').hidden = !row;
    if (!row) return;
    byId('event-title').textContent = `#${row.event.eventSequenceNumber} · ${row.event.name}`;
    byId('event-meta').textContent = `${row.delivery} · ${row.source === 'archive' ?
      'Loaded from the archive' : `Stream delivery (${row.count})`} · ` +
      `Attempt ${row.event.attemptId || '—'}`;
    if (detailRecord !== row.event.record) {
      detailRecord = row.event.record;
      byId('event-json').replaceChildren(jsonTree(
        parseInspectionJSON(detailRecord), 'RecordedEvent'));
    }
    renderEvents();
  }
  for (const id of ['event-search', 'event-delivery']) {
    byId(id).addEventListener('input', () => { page = 0; renderEvents(); });
  }
  byId('events-prev').addEventListener('click', () => { page--; renderEvents(); });
  byId('events-next').addEventListener('click', () => { page++; renderEvents(); });
  byId('event-copy').addEventListener('click', () => {
    copyText(detailRecord, byId('event-copy'), notify);
  });

  function clearData() {
    savedData = undefined;
    explorer?.destroy();
    explorer = undefined;
    byId('data-meta').textContent = 'Start a conversation to inspect saved data.';
  }

  async function loadData() {
    const current = ++generation;
    clearData();
    const isView = tab === 'view';
    byId('data-title').textContent = isView ? 'RunView' : 'RunState';
    byId('data-description').textContent = isView ?
      'Complete accepted history from all saved batches. Sizes count UTF-8 JSON bytes.' :
      'Compact execution state with its accepted tail. Sizes count UTF-8 JSON bytes.';
    if (!state?.runId || !state.head) {
      byId('data-meta').textContent = 'Start a conversation to inspect saved data.';
      byId('data-refresh').disabled = true;
      return;
    }
    loading = true;
    byId('data-refresh').disabled = true;
    byId('data-meta').textContent = 'Reading accepted bytes…';
    try {
      const query = new URLSearchParams({runId: state.runId, kind: tab, path: byId('path').value});
      const response = await request(`/api/inspect?${query}`);
      if (current !== generation || response.status === RequestStatus.MissingRun) return;
      const result = response.data;
      savedData = result;
      explorer = createRunExplorer(byId('run-explorer'), {
        kind: tab, json: result.decoded, encoded: result.encoded,
      });
      dataMeta();
    } catch (error) {
      if (current === generation) byId('data-meta').textContent = error.message;
    } finally {
      if (current === generation) { loading = false; byId('data-refresh').disabled = false; }
    }
  }

  function dataMeta() {
    if (!savedData) return;
    byId('data-meta').textContent = `Accepted event ${savedData.head} · ` +
      `${savedData.batchCount} batches` + (savedData.head < state.head ?
        ` · New head: ${state.head}. Refresh to load it.` : ' · Current saved state');
  }
  byId('data-refresh').addEventListener('click', loadData);
  byId('path').addEventListener('change', () => { if (tab === 'state') loadData(); });
  function update(next) {
    if (state && state.runId !== next.runId) {
      selectTab('events');
      clearData();
      selected = '';
      detailRecord = undefined;
      byId('event-detail').hidden = true;
      byId('event-search').value = '';
      byId('event-delivery').value = '';
      page = 0;
    }
    state = next;
    renderEvents();
    if (selected) selectEvent(selected);
    byId('data-refresh').disabled = loading || !state.head;
    dataMeta();
  }

  function inspect(item) {
    show(true); selectTab('events');
    const source = item.sources?.[0] || item.id;
    const candidates = [...state.receipts.values()].filter(row =>
      row.delivery === Delivery.Accepted && row.event.items?.some(part =>
        part.id === source && !part.delta));
    const row = candidates.at(-1) || [...state.receipts.values()].find(row =>
      row.event.eventSequenceNumber === item.eventSequenceNumber);
    if (!row) return;
    byId('event-search').value = String(row.event.eventSequenceNumber);
    byId('event-delivery').value = Delivery.Accepted;
    page = 0;
    selectEvent(receiptKey(row.delivery, row.event));
    byId('event-detail').scrollIntoView({block: 'nearest'});
  }
  return {update, inspect};
}
