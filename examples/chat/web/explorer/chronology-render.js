import {element, jsonTree} from './dom.js';
import {facts, section, sourceFields} from './fields.js';

function label(value) {
  const text = String(value).replaceAll('_', ' ');
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function renderParts(parts = []) {
  const node = element('div', 'ge-parts');
  for (const part of parts) {
    const block = element('div', 'ge-part');
    if (part.Type && Array.isArray(part.Content)) {
      block.append(element('div', 'ge-part-label', label(part.Type)), renderParts(part.Content));
    } else if (typeof part.Text === 'string') {
      block.append(element('div', 'ge-content', part.Text));
    } else if (part.function) {
      block.append(element('div', 'ge-part-label', `Tool request · ${part.function.name}`),
        element('pre', 'ge-code', part.function.arguments));
    } else if (part.ToolCallID && Array.isArray(part.Parts)) {
      block.append(element('div', 'ge-part-label', `Tool reply · ${part.Name}`),
        renderParts(part.Parts));
    } else {
      const tree = element('div', 'json-tree');
      tree.append(jsonTree(Object.hasOwn(part, 'Data') ? part.Data : part, 'Content'));
      block.append(tree);
    }
    node.append(block);
  }
  return node;
}

// Render complete content in bounded groups. More messages stay available without building an
// archive-sized DOM tree when the user selects a model call with a long request.
function renderMessages(messages) {
  const node = element('div', 'ge-model-messages');
  let cursor = 0;
  const more = element('button', 'ge-more', 'Show next 20 messages'); more.type = 'button';
  function next() {
    more.remove();
    const end = Math.min(cursor + 20, messages.length);
    for (; cursor < end; cursor++) {
      const message = messages[cursor];
      const block = element('article', 'ge-model-message');
      block.dataset.role = message.Role;
      block.append(element('div', 'ge-role', `${cursor + 1} · ${label(message.Role)}`),
        renderParts(message.Parts || []));
      if (message.Continuation || message.Metadata) {
        block.append(sourceFields(message, '', 'Message metadata and native continuation'));
      }
      node.append(block);
    }
    if (cursor < messages.length) node.append(more);
  }
  more.addEventListener('click', next); next();
  return node;
}

function errorPanel(error) {
  const panel = section('Error');
  panel.classList.add('ge-error');
  const message = typeof error === 'string' ? error :
    error.message || error.Message || error.data?.message || 'Saved diagnostic data';
  panel.append(element('div', 'ge-content', message), sourceFields(error, '', 'Error details'));
  return panel;
}

function taskDetail(row, node) {
  const system = section('System prompt', row.systemSource ?
    `System messages in the first saved model call, after Hooks. Source event ` +
      `${row.systemSource.order}. Each model call below keeps its own effective request.` :
    'No model request is retained for this task. Its earlier system prompt cannot be inferred ' +
      'from the current checkpoint.');
  if (row.systems) {
    if (row.systems.length) system.append(renderMessages(row.systems));
    else system.append(element('p', 'ge-note', 'That request contains no system messages.'));
  }
  const task = section('Task', 'Original user input from the accepted history change.');
  task.append(renderMessages(row.messages));
  node.append(task, system);
}

function modelDetail(row, node) {
  const after = row.after?.source.payload;
  const columns = element('div', 'ge-call-columns');
  const sent = section('Sent to model',
    'Complete effective messages after Hooks, in request order.');
  if (row.request) {
    sent.append(renderMessages(row.request.Messages || []), sourceFields(row.request,
      `${row.requestSource.path}/payload/Request`, 'Full request and call options'));
  } else sent.append(element('p', 'ge-note', 'The request is not retained in this data.'));
  const returned = section('Returned by model',
    'Saved response content, including commentary, exposed reasoning, and tool requests.');
  const response = after?.Response;
  if (response) {
    for (const [index, choice] of (response.Choices || []).entries()) {
      const candidate = element('article', 'ge-model-choice');
      candidate.append(element('h6', '', `Choice ${index + 1} · ${choice.Completion?.Status ||
        'saved response'}`), renderParts(choice.Parts || []));
      if (choice.Completion?.Refusal || choice.Continuation || choice.CandidateSource) {
        candidate.append(sourceFields(choice, `${row.after.path}/payload/Response/Choices/${index}`,
          'Completion and native evidence'));
      }
      returned.append(candidate);
    }
    returned.append(sourceFields(response, `${row.after.path}/payload/Response`, 'Full response'));
  } else {
    returned.append(element('p', 'ge-note', 'No complete response is saved at this position.'));
    for (const chunk of row.chunks) {
      returned.append(sourceFields(chunk.source.payload, `${chunk.path}/payload`,
        `Saved stream fragment · event ${chunk.order}`));
    }
  }
  if (after?.Error) returned.append(errorPanel(after.Error));
  for (const accepted of row.accepted) {
    const answer = section('Accepted answer', 'Published after output processing and its save.');
    answer.append(renderParts(accepted.source.payload.Content || []));
    returned.append(answer);
  }
  columns.append(sent, returned); node.append(columns);
}

// A prepared model reply is not the tool result. The next effective model request shows what
// the adapter receives; tool views show execution, recovery, and diagnostic evidence instead.
export function renderToolResult(record, {effective = record?.Input, error = record?.Error,
  completed = true, path = ''} = {}) {
  const node = element('div', 'ge-tool-data');
  const input = section('Tool input');
  if (typeof record?.RawArguments === 'string') {
    input.append(element('p', 'ge-note', 'Original arguments'),
      element('pre', 'ge-code', record.RawArguments));
  }
  if (effective) {
    input.append(sourceFields(effective, '', 'Effective input · after Hooks'));
  } else input.append(element('p', 'ge-note', 'No effective input is retained.'));
  const output = section('Tool result');
  if (!completed) {
    output.append(element('p', 'ge-note', 'No completion is saved at this position.'));
  }
  const execution = record?.Execution || record?.Recovery?.Execution;
  if (execution) {
    output.append(element('p', 'ge-note', `Execution · ${execution.Status}`),
      renderParts(execution.Parts || []));
    if (execution.Instructions) {
      output.append(element('div', 'ge-content', execution.Instructions));
    }
  }
  if (record?.Recovery) {
    output.append(element('p', 'ge-note', `Recovery · ${record.Recovery.Status}`),
      sourceFields(record.Recovery, path ? `${path}/Recovery` : '', 'Recovery report'));
  }
  if (record != null && !record.Kind) {
    output.append(sourceFields(record, path, 'Output'));
  }
  if (error) output.append(errorPanel(error));
  if (record?.Error && record.Error !== error) {
    output.append(sourceFields(record.Error, '', 'Original diagnostic'));
  }
  if (record?.ReturnedResult || record?.ReturnedRecovery) {
    output.append(sourceFields(record, path, 'Original returned values and diagnostics'));
  }
  node.append(input, output);
  return node;
}

function executionDetails(row) {
  const after = row.after?.source.payload;
  const payload = after || row.before?.source.payload || {};
  const values = [['Turn', row.source.turnId], ['Attempt', row.source.attemptId],
    ['Time', row.source.ts]];
  if (row.callId) values.push(['Call', row.callId]);
  if (row.type === 'model-step') values.push(['Model', payload.Model || 'Not named'],
    ['Provider', payload.Provider || 'Not named'], ['Topic', payload.StreamTopicId],
    ['Input tokens', after?.InputTokens], ['Output tokens', after?.OutputTokens]);
  if (row.type === 'tool-step') values.push(['Outcome', after?.Output?.Kind || row.status]);
  const details = element('details', 'ge-call-info');
  details.append(element('summary', '', 'Saved execution details'), facts(values));
  return details;
}

function savedEvidence(events) {
  const sources = section('Saved evidence');
  for (const reference of events) {
    if (reference.source.type === 'model_stream_chunk') continue;
    sources.append(sourceFields(reference.source, reference.path,
      `Event ${reference.order} · ${reference.source.type.replaceAll('_', ' ')}`));
  }
  return sources;
}

function toolGroup(row, node) {
  node.append(element('p', 'ge-note',
    'Open a tool to read its input, result, and errors. Model replies appear in model requests.'));
  if (row.batch?.source) node.append(sourceFields(row.batch.source.source, row.batch.source.path,
    'Source model response'));
  for (const call of row.calls) {
    const details = element('details', 'ge-tool-accordion');
    details.dataset.callId = call.callId;
    const summary = element('summary');
    const status = element('span', 'ge-badge', call.status); status.dataset.status = call.status;
    summary.append(element('strong', '', call.toolName), status,
      element('span', 'ge-tool-position', call.subtitle));
    details.append(summary);
    let loaded = false;
    details.addEventListener('toggle', () => {
      if (!details.open || loaded) return;
      loaded = true;
      const after = call.after?.source.payload;
      const record = after?.Output;
      details.append(executionDetails(call), renderToolResult(record, {
        effective: record?.Input || call.before?.source.payload.Args || after?.Args,
        error: after?.Error || record?.Error, completed: Boolean(call.after),
        path: call.after ? `${call.after.path}/payload/Output` : '',
      }), savedEvidence(call.events));
    });
    node.append(details);
  }
}

export function renderChronology(row) {
  const node = element('article', 'ge-detail ge-chronology-detail');
  node.setAttribute('aria-label', 'Selected record');
  node.append(element('h5', '', row.title), element('p', 'ge-note', row.subtitle),
    executionDetails(row));
  if (row.type === 'task-step') taskDetail(row, node);
  if (row.type === 'model-step') modelDetail(row, node);
  if (row.type === 'tool-group-step') toolGroup(row, node);
  else node.append(savedEvidence(row.events));
  return node;
}
