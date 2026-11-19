import {createRunExplorer} from './index.js';
import {parseInspectionJSON} from './data.js';
let explorer;
let input;
let generation = 0;
document.querySelector('#file').addEventListener('change', async event => {
  const current = ++generation;
  const file = event.target.files[0];
  if (!file) return;
  const notice = document.querySelector('#notice');
  try {
    const json = await file.text();
    if (current !== generation) return;
    const source = parseInspectionJSON(json);
    const kind = source && Object.hasOwn(source, 'RunId') ? 'state' : 'view';
    const next = {kind, json};
    if (explorer) explorer.update(next);
    else explorer = createRunExplorer(document.querySelector('#explorer'), next);
    input = next;
    document.querySelector('#encoded-file').value = '';
    document.querySelector('#encoded-file').disabled = false;
    notice.dataset.status = 'ready';
    notice.textContent = `${file.name} · ${file.size} bytes · Loaded locally`;
  } catch (error) {
    if (current !== generation) return;
    notice.dataset.status = 'error';
    notice.textContent = error.message;
  }
});
document.querySelector('#encoded-file').addEventListener('change', async event => {
  const current = ++generation;
  const file = event.target.files[0];
  if (!file) return;
  const notice = document.querySelector('#notice');
  try {
    const encoded = await file.text();
    if (current !== generation) return;
    input = {...input, encoded}; explorer.update(input);
    notice.dataset.status = 'ready';
    notice.textContent = `${file.name} · ${file.size} bytes · Loaded locally`;
  } catch (error) {
    if (current !== generation) return;
    notice.dataset.status = 'error'; notice.textContent = error.message;
  }
});
