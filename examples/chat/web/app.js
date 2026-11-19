import {Action, ExecutionStatus, Frame, emptyState, resetState, receive,
  chatMessages} from './state.js';
import {byId} from './ui.js';
import {createChat} from './chat.js';
import {createInspector} from './inspector.js';
import {createRequest, RequestStatus} from './request.js';

let state = emptyState();
let stream;
let renderPending = false;
let submitting = false;
let provider = '';
let scenarios = [];
let archiveHead = 0;

function notice(text) { byId('notice').textContent = text; }
const request = createRequest(runId => {
  // An older request can finish after a new run starts. It must not clear that new conversation.
  if (state.runId !== runId) return;
  stream?.close();
  stream = undefined;
  state = emptyState();
  archiveHead = 0;
  history.replaceState(null, '', '/');
  notice('That conversation is no longer available. You can start a new chat.');
  // Keep the draft. Missing history does not mean the user's next message must be discarded.
  render();
});
const inspector = createInspector(request, notice);
const chat = createChat(inspector.inspect, notice);

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
  chat.render(chatMessages(state), state.active, provider);
  inspector.update(state);
  byId('head').textContent = String(state.head);
  byId('batches').textContent = `${state.batchCount}${state.head > archiveHead ? '+' : ''}`;
  byId('tail').textContent = state.hasCapture ? String(state.captureTail) : '—';
  byId('run-id').textContent = state.runId || '—';
  const paused = state.outcome?.status === ExecutionStatus.Paused;
  byId('status').textContent = state.active ? 'Working' :
    paused ? 'Paused' : state.lastResult ? 'Turn complete' : 'Ready';
  byId('status').dataset.active = state.active;
  byId('stop').hidden = !state.active;
  byId('stop').disabled = submitting || !state.attemptId || !state.turnId;
  byId('send').hidden = state.active;
  byId('send').disabled = submitting || !scenario || !byId('prompt').value.trim();
  byId('continue').disabled = submitting || state.active || !state.runId;
  byId('restore').disabled = submitting || state.active || !state.runId;
  byId('pause-bar').hidden = state.active || !paused;
  byId('resume').disabled = submitting;
  byId('task-note').textContent = state.active ?
    'You can prepare your next message while the agent works.' :
    paused ? 'A new message replaces the paused task and keeps the conversation.' :
      'One conversation. Your next prompt keeps its context.';
}

async function action(kind) {
  if (submitting) return;
  submitting = true;
  notice(''); render();
  try {
    const response = await request('/api/action', {action: kind, runId: state.runId,
      scenario: byId('scenario').value, prompt: byId('prompt').value, path: byId('path').value,
      turnId: state.turnId, attemptId: state.attemptId});
    if (response.status === RequestStatus.MissingRun) return;
    const data = response.data;
    if (kind === Action.Restore) {
      reset(data);
      notice(`Restored ${data.batchCount} batches at event ${data.head}. ` +
        'The saved answer was read without running the agent.');
    } else if (kind !== Action.Stop) {
      if (kind === Action.Turn) { byId('prompt').value = ''; resizePrompt(); }
      state.active = true;
      connect(data.runId);
    } else {
      notice('Stop requested. Waiting for the agent to save its progress.');
    }
  } catch (error) { notice(error.message); }
  finally { submitting = false; render(); }
}

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
byId('suggestion').addEventListener('click', () => {
  byId('prompt').value = scenarios.find(item => item.id === byId('scenario').value).prompt;
  resizePrompt(); render(); byId('prompt').focus();
});
render();
try {
  const config = await request('/api/config');
  provider = config.data.provider;
  scenarios = config.data.scenarios;
  for (const scenario of scenarios) {
    byId('scenario').append(new Option(scenario.name, scenario.id));
  }
  render();
  byId('provider').textContent = provider === 'demo' ? 'Demo · local fixture' : provider;
  const runId = new URL(location.href).searchParams.get('run');
  if (runId) {
    // Check an old URL before opening SSE. A 404 returns to the empty main page immediately.
    state.runId = runId;
    const response = await request(`/api/state?runId=${encodeURIComponent(runId)}`);
    if (response.status === RequestStatus.Ready && state.runId === runId) {
      reset(response.data);
      render();
      connect(runId);
    }
  }
} catch (error) { notice(error.message); }
