import {element} from './dom.js';
import {filterRows, readRunData} from './data.js';
import {renderDetail, renderState} from './render.js';
import {renderRaw} from './raw.js';

// This component reads Gent diagnostic JSON, after Go has decoded and checked the saved bytes.
// It has no network, chat, or registry dependency. Each instance owns only its host element.
export function createRunExplorer(host, input, {onSelect = () => {}} = {}) {
  let data;
  let raw;
  let section = 'chronology';
  let page = 0;
  let selected = '';
  const pageSize = 12;
  const root = element('section', 'gent-run-explorer');
  root.setAttribute('aria-label', 'Run data explorer');
  const heading = element('div', 'ge-heading');
  const title = element('h4');
  const status = element('span', 'ge-status');
  heading.append(title, status);
  const note = element('p', 'ge-note');
  const metrics = element('div', 'ge-metrics');
  const nav = element('nav', 'ge-nav');
  nav.setAttribute('aria-label', 'Explore saved data');
  const tabs = new Map();
  for (const [name, label] of [['chronology', 'Chronological'],
    ['messages', 'Messages'], ['tools', 'Tools'],
    ['activity', 'Activity'], ['changes', 'History'], ['state', 'State'],
    ['encoded', 'Encoded Saved State'], ['decoded', 'Recovered State JSON']]) {
    const button = element('button', '', label);
    button.type = 'button';
    button.addEventListener('click', () => {
      section = name; page = 0; selected = ''; search.value = ''; render();
    });
    tabs.set(name, button); nav.append(button);
  }
  const controls = element('div', 'ge-controls');
  const search = element('input');
  search.type = 'search'; search.placeholder = 'Find text, type, or ID…';
  search.setAttribute('aria-label', 'Search saved records');
  const scope = element('select');
  scope.setAttribute('aria-label', 'Message selection');
  for (const [value, label] of [['all', 'All saved messages'], ['active', 'Active selection']]) {
    const option = element('option', '', label); option.value = value; scope.append(option);
  }
  controls.append(search, scope);
  const explanation = element('p', 'ge-note');
  const list = element('ol', 'ge-list');
  list.setAttribute('aria-label', 'Saved records');
  const pages = element('div', 'ge-pages');
  const previous = element('button', '', '←');
  const next = element('button', '', '→');
  const count = element('span'); count.setAttribute('role', 'status');
  previous.type = next.type = 'button';
  previous.setAttribute('aria-label', 'Previous records');
  next.setAttribute('aria-label', 'Next records');
  pages.append(previous, count, next);
  const detail = element('div', 'ge-detail-pane');
  const listPane = element('div', 'ge-list-pane'); listPane.append(list, pages);
  const browser = element('div', 'ge-browser'); browser.append(listPane, detail);
  root.append(heading, note, metrics, nav, controls, explanation, browser);
  host.replaceChildren(root);
  search.addEventListener('input', () => { page = 0; selected = ''; renderRows(); });
  scope.addEventListener('change', () => { page = 0; selected = ''; renderRows(); });
  previous.addEventListener('click', () => { page--; renderRows(); });
  next.addEventListener('click', () => { page++; renderRows(); });

  function rowsForSection() {
    if (section === 'messages') return scope.value === 'active' ?
      data.selectedMessages : data.messages;
    return data[section];
  }

  function renderRows() {
    const rows = filterRows(rowsForSection(), search.value);
    const totalPages = Math.max(1, Math.ceil(rows.length / pageSize));
    page = Math.max(0, Math.min(page, totalPages - 1));
    previous.disabled = page === 0; next.disabled = page === totalPages - 1;
    count.textContent = `${rows.length} records · ${page + 1} / ${totalPages}`;
    const visible = rows.slice(page * pageSize, (page + 1) * pageSize);
    if (section === 'chronology' && !visible.some(row => row.id === selected)) {
      selected = visible[0]?.id || '';
    }
    list.replaceChildren(...visible.map(row => {
      const li = element('li');
      const button = element('button', 'ge-row'); button.type = 'button';
      button.setAttribute('aria-pressed', String(selected === row.id));
      button.dataset.kind = row.type;
      const lead = element('span', 'ge-row-heading');
      lead.append(element('strong', '', row.title));
      if (row.status) {
        const status = element('span', 'ge-badge', row.status);
        status.dataset.status = row.status; lead.append(status);
      }
      if (row.active !== undefined) {
        lead.append(element('span', row.active ? 'ge-badge active' : 'ge-badge',
          row.active ? 'ACTIVE' : 'ARCHIVED'));
      }
      button.append(element('span', 'ge-position', row.order), lead,
        element('span', 'ge-preview', (row.text || row.subtitle).slice(0, 260)),
        element('span', 'ge-row-note', row.subtitle));
      button.addEventListener('click', () => {
        selected = row.id;
        for (const child of list.querySelectorAll('button')) {
          child.setAttribute('aria-pressed', String(child === button));
        }
        detail.replaceChildren(renderDetail(row));
        onSelect({kind: data.kind, path: row.path, value: row.source});
      });
      li.append(button); return li;
    }));
    if (!visible.length) {
      const text = section === 'chronology' && data.kind === 'state' && !search.value ?
        'No call evidence is retained at this saved position.' : 'No matching saved records.';
      list.append(element('li', 'ge-empty', text));
      selected = ''; detail.replaceChildren(); return;
    }
    const chosen = visible.find(row => row.id === selected);
    if (chosen) { detail.replaceChildren(renderDetail(chosen)); return; }
    selected = '';
    detail.replaceChildren(element('p', 'ge-pick',
      'Select a record to read its content and source.'));
  }

  function render() {
    root.dataset.section = section;
    for (const [name, button] of tabs) {
      button.setAttribute('aria-pressed', String(section === name));
    }
    const documentView = ['state', 'encoded', 'decoded'].includes(section);
    controls.hidden = listPane.hidden = documentView;
    browser.classList.toggle('ge-document', documentView);
    scope.hidden = section !== 'messages';
    explanation.textContent = section === 'chronology' ? (data.kind === 'state' ?
      'Partial chronology: RunState retains recovery evidence, not the full archive. ' +
      'Open RunView for tasks and earlier calls; Messages shows retained context.' :
      'Tasks and calls in accepted event order. Select a point to inspect its complete input ' +
      'and output. Activity and History keep the other saved events.') :
      section === 'activity' && data.kind === 'state' ?
      'Only evidence retained for execution is shown. Open RunView for the full accepted history.' :
      section === 'changes' && data.kind === 'state' ?
        'RunState keeps current and resumable selections. RunView keeps every accepted change.' :
        section === 'messages' ? 'Active means selected for context. Archived messages remain ' +
          'readable in RunView. Instructions are outside compactable history.' : '';
    if (section === 'state') { detail.replaceChildren(renderState(data)); return; }
    if (section === 'encoded' || section === 'decoded') {
      detail.replaceChildren(renderRaw(data, raw, section)); return;
    }
    renderRows();
  }

  function update(nextInput) {
    // Parse before changing the view. Invalid input leaves the previous inspected state intact.
    const nextData = readRunData(nextInput);
    const changed = !data || data.kind !== nextData.kind || data.runId !== nextData.runId;
    data = nextData;
    raw = {json: nextInput.json, encoded: nextInput.encoded};
    if (changed) {
      section = 'chronology'; page = 0; selected = ''; search.value = '';
      scope.value = data.kind === 'state' ? 'active' : 'all';
    }
    title.textContent = data.kind === 'state' ? 'Execution state' : 'Accepted archive';
    status.textContent = data.checkpoint.contexts?.some(context => context.turnOpen) ?
      'Unfinished turn' : data.outcome ? 'Turn complete' : 'No turn';
    note.textContent = data.kind === 'state' ? 'What the next worker can restore.' :
      'What happened, including records no longer selected for context.';
    metrics.replaceChildren();
    for (const [label, value] of [['Saved entries', data.entries.length],
      ['Selected entries', data.active.size], ['Event sequence', data.head]]) {
      const metric = element('div');
      metric.append(element('strong', '', value), element('span', '', label));
      metrics.append(metric);
    }
    render();
  }
  update(input);
  return {update, destroy() { root.remove(); }};
}
