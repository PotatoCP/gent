/** Delivery identifies whether an event is saved or is only a preview. */
export const Delivery = Object.freeze({
  /** Accepted identifies an event in an accepted SaveBatch. */
  Accepted: 'accepted',
  /** Live identifies a provisional update from the active attempt. */
  Live: 'live',
});

/** Frame identifies the transport message, not a Gent recovery operation. */
export const Frame = Object.freeze({
  /** Reset replaces the browser projection with a fixed accepted archive head. */
  Reset: 'reset',
  /** Event carries a subscriber event. */
  Event: 'event',
  /** Idle asks the client to load accepted state after worker cleanup. */
  Idle: 'idle',
  /** Heartbeat keeps the connection open without changing conversation state. */
  Heartbeat: 'heartbeat',
});

/** Action names the server operation requested by the browser. */
export const Action = Object.freeze({
  /** Turn submits and executes a first, later, or replacement task. */
  Turn: 'turn',
  /** Continue executes the unfinished saved task. */
  Continue: 'continue',
  /** Restore reads accepted bytes and the last completed result. */
  Restore: 'restore',
  /** Stop requests a pause of the named active attempt. */
  Stop: 'stop',
});

/** ContentType mirrors the public AssistantContentType wire labels used by this view. */
export const ContentType = Object.freeze({
  /** Unspecified has no known provider classification. */
  Unspecified: '',
  /** Commentary contains readable assistant progress. */
  Commentary: 'commentary',
  /** Reasoning contains reasoning or a reasoning summary exposed by the provider. */
  Reasoning: 'reasoning',
  /** Intermediary contains ordinary intermediate assistant content. */
  Intermediary: 'intermediary',
  /** Candidate contains proposed output before Gent accepts it. */
  Candidate: 'final_answer_candidate',
  /** Accepted contains processed output after its save succeeds. */
  Accepted: 'final_answer',
});

/** ExecutionStatus mirrors the public result states; it does not replace Gent's lifecycle. */
export const ExecutionStatus = Object.freeze({
  /** Finished reports a completed turn with a termination reason. */
  Finished: 'finished',
  /** Paused reports an unfinished turn that can continue. */
  Paused: 'paused',
  /** Busy reports that the context already has active execution. */
  Busy: 'busy',
  /** NoActiveTurn reports that Continue has no unfinished turn to execute. */
  NoActiveTurn: 'no_active_turn',
});

export function emptyState() {
  return {runId: '', head: 0, active: false, events: new Map(), live: new Map(),
    pending: new Map(), receipts: new Map(), attemptId: '', turnId: '', batchCount: 0};
}

export function resetState(saved, previous = emptyState()) {
  const state = {...emptyState(), ...saved, events: new Map(), live: new Map(), pending: new Map()};
  // Keep received previews in the inspector across reconnects. They are evidence of delivery,
  // not accepted history, and must not return to the chat after recovery discards them.
  if (previous.runId === saved.runId) state.receipts = previous.receipts;
  for (const event of saved.events || []) {
    state.events.set(event.eventSequenceNumber, event);
    remember(state, Delivery.Accepted, event, 'archive');
  }
  for (const event of saved.previews || []) receive(state, Delivery.Live, event);
  return state;
}

export function receiptKey(delivery, event) {
  return `${event.attemptId}/${event.eventSequenceNumber}/${delivery}`;
}

function remember(state, delivery, event, source = 'stream') {
  const key = receiptKey(delivery, event);
  const previous = state.receipts.get(key);
  state.receipts.set(key, {key, delivery, event,
    count: (previous?.count || 0) + (source === 'stream' ? 1 : 0),
    source: previous?.source === 'stream' ? 'stream' : source});
}

export function receive(state, delivery, event) {
  if (state.runId && state.runId !== event.runId) return;
  state.runId = event.runId;
  remember(state, delivery, event);
  if (delivery === Delivery.Accepted) {
    if (event.eventSequenceNumber <= state.head) return;
    state.pending.set(event.eventSequenceNumber, event);
    // Advance only through contiguous accepted evidence. A live cursor never fills a gap.
    while (state.pending.has(state.head + 1)) {
      state.head++;
      state.events.set(state.head, state.pending.get(state.head));
      state.pending.delete(state.head);
    }
    for (const [key, preview] of state.live) {
      if (preview.eventSequenceNumber <= state.head) state.live.delete(key);
    }
    return;
  }
  if (event.eventSequenceNumber <= state.head) return;
  if (state.attemptId !== event.attemptId) {
    state.live.clear();
    state.attemptId = event.attemptId;
  }
  state.turnId = event.turnId || state.turnId;
  state.active = true;
  state.live.set(`${event.attemptId}/${event.eventSequenceNumber}`, event);
}

export function orderedEvents(state) {
  const saved = [...state.events.values()].map(event => ({...event, accepted: true}));
  const live = [...state.live.values()].map(event => ({...event, accepted: false}));
  return [...saved, ...live].sort((left, right) =>
    left.eventSequenceNumber - right.eventSequenceNumber);
}

export function messages(state) {
  const items = new Map();
  for (const event of orderedEvents(state)) {
    for (const item of event.items || []) {
      // Even a post-save Live answer waits for the Accepted channel in this UI.
      if (item.type === ContentType.Accepted && !event.accepted) continue;
      const previous = items.get(item.id);
      items.set(item.id, {...previous, ...item, accepted: event.accepted,
        eventSequenceNumber: event.eventSequenceNumber,
        text: item.delta ? (previous?.text || '') + item.text : item.text});
    }
  }
  return [...items.values()];
}

export function chatMessages(state) {
  const all = messages(state);
  const sources = new Set(all.filter(item => item.type === ContentType.Accepted)
    .flatMap(item => item.sources || []));
  return all.filter(item => item.kind !== 'activity' && !sources.has(item.id));
}

export function eventPage(state, query, delivery, page, size = 30) {
  const search = query.trim().toLowerCase();
  const rows = [...state.receipts.values()].filter(row => {
    const event = row.event;
    return (!delivery || row.delivery === delivery) &&
      `${event.eventSequenceNumber} ${event.name} ${event.attemptId} ${event.turnId}`
        .toLowerCase().includes(search);
  }).reverse();
  const pages = Math.max(1, Math.ceil(rows.length / size));
  const current = Math.max(0, Math.min(page, pages - 1));
  return {rows: rows.slice(current * size, (current + 1) * size), total: rows.length,
    page: current, pages};
}
