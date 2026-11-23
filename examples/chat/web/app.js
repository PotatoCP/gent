import {Action, ExecutionStatus, Frame, emptyState, resetState, receive,
  chatMessages} from './state.js';
import {byId} from './ui.js';
import {createChat} from './chat.js';
import {createInspector} from './inspector.js';
import {createRequest, RequestStatus} from './request.js';
import {createConversations} from './conversations.js';

let state = emptyState();
let stream;
let renderPending = false;
let submitting = false;
let provider = '';
let scenarios = [];
let archiveHead = 0;
let selection = 0;
let resetting = false;
const drafts = new Map();

function notice(text) { byId('notice').textContent = text; }
const request = createRequest(runId => {
  // An older request can finish after a new run starts. It must not clear that new conversation.
  if (state.runId !== runId) return;
  clearConversation();
  notice('That conversation is no longer available. You can start a new chat.');
  // Keep the draft. Missing history does not mean the user's next message must be discarded.
  render();
  chats.refresh();
}, () => {
  drafts.clear();
  // A reset removes saved runs, not the unsent text in this browser. Keep it for a retry,
  // including when another open page requested the server reset.
  clearConversation();
  chats.clear(); chats.refresh();
  notice('Server reset. All chats were cleared.');
});
const inspector = createInspector(request, notice);
const chat = createChat(inspector.inspect, notice);
const chats = createConversations(request, selectConversation, notice);

function clearConversation() {
  selection++;
  request.invalidate();
  stream?.close(); stream = undefined;
  state = emptyState(); archiveHead = 0; submitting = false;
  history.replaceState(null, '', '/');
  render();
}

async function selectConversation(runId, push = true) {
  if (resetting) return;
  drafts.set(state.runId, byId('prompt').value);
  request.invalidate();
  const current = ++selection;
  stream?.close(); stream = undefined;
  state = emptyState(); state.runId = runId;
  archiveHead = 0; submitting = Boolean(runId);
  byId('prompt').value = drafts.get(runId) || '';
  resizePrompt(); notice(''); render();
  const url = runId ? `/?run=${encodeURIComponent(runId)}` : '/';
  if (push && location.search !== new URL(url, location.href).search) {
    history.pushState(null, '', url);
  }
  if (!runId) return;
  try {
    const response = await request(`/api/state?runId=${encodeURIComponent(runId)}`);
    if (current !== selection || response.status !== RequestStatus.Ready) return;
    reset(response.data); connect(runId);
  } catch (error) {
    if (current === selection) notice(error.message);
  } finally {
    if (current === selection) { submitting = false; render(); }
  }
}

function reset(saved) {
  state = resetState(saved, state);
  archiveHead = saved.head;
}

function connect(runId) {
  stream?.close();
  state.runId = runId;
  history.replaceState(null, '', `/?run=${encodeURIComponent(runId)}`);
  const connection = new EventSource(`/api/events?runId=${encodeURIComponent(runId)}`);
  stream = connection;
  connection.onmessage = event => {
    if (stream !== connection) return;
    const frame = JSON.parse(event.data);
    if (frame.kind === Frame.Reset) {
      reset(frame.state);
      chats.refresh();
      notice('');
      if (state.previewGap) {
        notice('Some live previews are unavailable. Saved events are complete.');
      }
      if (state.outcome?.status === ExecutionStatus.Paused) {
        notice('Task paused. Continue it or send a new message.');
      }
      if (state.outcome?.detail) notice(state.outcome.detail);
      if (state.outcome?.status === ExecutionStatus.NoActiveTurn) {
        notice('There is no unfinished task. The last completed answer is still available.');
      }
    } else if (frame.kind === Frame.Event) {
      receive(state, frame.delivery, frame.event);
    } else if (frame.kind === Frame.Idle) {
      connect(runId); return;
    }
    scheduleRender();
  };
  connection.onerror = async () => {
    if (stream !== connection) return;
    notice('Connection lost. Reconnecting to saved events…');
    try { await request(`/api/state?runId=${encodeURIComponent(runId)}`); }
    catch (error) {
      if (stream !== connection) return;
      connection.close(); notice(error.message);
    }
  };
}

function scheduleRender() {
  if (renderPending) return;
  renderPending = true;
  requestAnimationFrame(() => { renderPending = false; render(); });
}

function render() {
  if (state.scenario) byId('scenario').value = state.scenario;
  const scenario = scenarios.find(item => item.id === byId('scenario').value);
  if (scenario) {
    byId('agent-name').textContent = scenario.name;
    byId('suggestion-title').textContent = scenario.description;
    byId('suggestion-detail').textContent = scenario.name + ' · Sample customer data';
  }
  byId('scenario').disabled = submitting || Boolean(state.runId);
  byId('suggestion').disabled = !scenario || submitting;
  byId('investigation').disabled = !scenario || submitting;
  chat.render(chatMessages(state), state.active);
  inspector.update(state);
  chats.mark(state.runId);
  byId('head').textContent = String(state.head);
  byId('batches').textContent = `${state.batchCount}${state.head > archiveHead ? '+' : ''}`;
  byId('tail').textContent = state.hasCapture ? String(state.captureTail) : '—';
  byId('run-id').textContent = state.runId || '—';
  const paused = state.outcome?.status === ExecutionStatus.Paused;
  byId('status').textContent = resetting ? 'Resetting' :
    submitting && state.runId && !state.head ? 'Loading' : state.active ? 'Working' :
    paused ? 'Paused' : state.lastResult ? 'Turn complete' : 'Ready';
  byId('status').dataset.active = state.active;
  byId('stop').hidden = !state.active;
  byId('stop').disabled = submitting || !state.attemptId || !state.turnId;
  byId('send').hidden = state.active;
  byId('send').disabled = resetting || submitting || !scenario || !byId('prompt').value.trim();
  byId('continue').disabled = resetting || submitting || state.active || !state.runId;
  byId('restore').disabled = resetting || submitting || state.active || !state.runId;
  byId('pause-bar').hidden = state.active || !paused;
  byId('resume').disabled = submitting;
  byId('server-reset').disabled = resetting;
  byId('task-note').textContent = state.active ?
    'You can prepare your next message while the agent works.' :
    paused ? 'A new message replaces the paused task and keeps the conversation.' :
      'One conversation. Your next prompt keeps its context.';
}

async function action(kind) {
  if (submitting || resetting) return;
  const current = selection;
  submitting = true;
  notice(''); render();
  try {
    const response = await request('/api/action', {action: kind, runId: state.runId,
      scenario: byId('scenario').value, prompt: byId('prompt').value, path: byId('path').value,
      turnId: state.turnId, attemptId: state.attemptId});
    if (current !== selection || response.status !== RequestStatus.Ready) return;
    const data = response.data;
    if (kind === Action.Restore) {
      reset(data);
      notice(`Restored ${data.batchCount} batches at event ${data.head}. ` +
        'The saved answer was read without running the agent.');
    } else if (kind !== Action.Stop) {
      if (kind === Action.Turn) {
        drafts.delete(state.runId);
        byId('prompt').value = ''; resizePrompt();
      }
      state.active = true;
      connect(data.runId);
    } else {
      notice('Stop requested. Waiting for the agent to save its progress.');
    }
  } catch (error) {
    if (current === selection) notice(error.message);
  } finally {
    if (current === selection) { submitting = false; render(); }
    chats.refresh();
  }
}

byId('new-chat').addEventListener('click', event => {
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  event.preventDefault(); selectConversation('');
});
window.addEventListener('popstate', () => {
  selectConversation(new URL(location.href).searchParams.get('run') || '', false);
});
window.addEventListener('focus', () => chats.refresh());
// Refresh background runs without decoding their archives or opening an SSE stream per chat.
setInterval(() => { if (!document.hidden && !resetting) chats.refresh(); }, 15000);

const resetDialog = byId('reset-dialog');
byId('server-reset').addEventListener('click', () => resetDialog.showModal());
byId('reset-cancel').addEventListener('click', () => resetDialog.close());
byId('reset-confirm').addEventListener('click', async () => {
  resetDialog.close();
  resetting = true;
  request.invalidate();
  stream?.close(); stream = undefined;
  notice('Stopping active work and clearing server state…'); render();
  try { await request('/api/reset', {}); }
  catch (error) {
    notice(error.message);
    if (state.runId) connect(state.runId);
  } finally { resetting = false; render(); }
});

function resizePrompt() {
  const prompt = byId('prompt');
  prompt.style.height = 'auto';
  prompt.style.height = `${Math.min(180, prompt.scrollHeight)}px`;
}
byId('composer').addEventListener('submit', event => {
  event.preventDefault();
  if (!state.active && byId('prompt').value.trim()) action(Action.Turn);
});
byId('prompt').addEventListener('input', () => { resizePrompt(); scheduleRender(); });
byId('prompt').addEventListener('keydown', event => {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
  event.preventDefault();
  if (!state.active && byId('prompt').value.trim()) action(Action.Turn);
});
for (const name of [Action.Continue, Action.Stop, Action.Restore]) {
  byId(name).addEventListener('click', () => action(name));
}
byId('resume').addEventListener('click', () => action(Action.Continue));
byId('scenario').addEventListener('change', render);
for (const [id, field] of [['suggestion', 'prompt'], ['investigation', 'investigation']]) {
  byId(id).addEventListener('click', () => {
    byId('prompt').value = scenarios.find(item => item.id === byId('scenario').value)[field];
    resizePrompt(); render(); byId('prompt').focus();
  });
}
render();
try {
  const config = await request('/api/config');
  if (config.status !== RequestStatus.Ready) throw new Error('Reload the server configuration.');
  provider = config.data.provider;
  scenarios = config.data.scenarios;
  for (const scenario of scenarios) {
    byId('scenario').append(new Option(scenario.name, scenario.id));
  }
  render();
  byId('provider').textContent = provider === 'demo' ? 'Demo · local fixture' : provider;
  chats.refresh();
  const runId = new URL(location.href).searchParams.get('run');
  if (runId) {
    // Check an old URL before opening SSE. A 404 returns to the empty main page immediately.
    await selectConversation(runId, false);
  }
} catch (error) { notice(error.message); }
