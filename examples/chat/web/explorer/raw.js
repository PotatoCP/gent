import {element, jsonTree} from './dom.js';
import {parseInspectionJSON} from './data.js';

function sizeLabel(json) {
  const bytes = new TextEncoder().encode(json).byteLength;
  const exact = `${new Intl.NumberFormat('en-US').format(bytes)} bytes`;
  if (bytes < 1024) return exact;
  const unit = bytes < 1024 * 1024 ? 'KiB' : 'MiB';
  const divisor = unit === 'KiB' ? 1024 : 1024 * 1024;
  return `${(bytes / divisor).toFixed(1)} ${unit} · ${exact}`;
}

// Export the original string. Re-encoding the parsed tree can change large integers and the
// exact bytes covered by RunStateSnapshot integrity checks. The raw view is not a Go decoder.
export function renderRaw(data, raw, section) {
  const encoded = section === 'encoded';
  const json = encoded ? raw.encoded : raw.json;
  const node = element('section', 'ge-raw');
  const heading = element('div', 'ge-raw-heading');
  heading.append(element('h5', '', encoded ? 'Encoded Saved State' : 'Recovered State JSON'));
  node.append(heading, element('p', 'ge-note', encoded ?
    'Exact codec bytes. Use this form for storage and recovery.' :
    'JSON of the recovered value. For inspection, not recovery.'));
  if (json === undefined) {
    node.append(element('p', 'ge-empty',
      'No encoded saved state was supplied to this inspector.'));
    return node;
  }
  heading.append(element('span', 'ge-size', sizeLabel(json)));
  const actions = element('div', 'ge-raw-actions');
  const copy = element('button', '', 'Copy JSON'); copy.type = 'button';
  const download = element('button', '', 'Download JSON'); download.type = 'button';
  const notice = element('p', 'ge-note'); notice.setAttribute('role', 'status');
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(json);
      notice.textContent = 'Copied JSON.';
    } catch (error) { notice.textContent = `Cannot copy JSON: ${error.message}`; }
  });
  download.addEventListener('click', () => {
    try {
      const url = URL.createObjectURL(new Blob([json], {type: 'application/json'}));
      try {
        const link = element('a'); link.href = url;
        link.download = `${data.kind}-${encoded ? 'encoded' : 'decoded'}-` +
          `${data.runId}-${data.head}.json`;
        link.click();
      } finally {
        // Keep the URL alive until the browser has started the download, then release it.
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      }
    } catch (error) { notice.textContent = `Cannot download JSON: ${error.message}`; }
  });
  actions.append(copy, download); node.append(actions, notice);
  const tree = element('div', 'json-tree');
  try {
    tree.append(jsonTree(encoded ? parseInspectionJSON(json) : data.source,
      encoded ? (data.kind === 'state' ? 'RunStateSnapshot' : 'Encoded RunView') :
        (data.kind === 'state' ? 'Recovered RunState' : 'Recovered RunView')));
  } catch (error) {
    // The caller can still export the exact supplied bytes when their JSON cannot be viewed.
    notice.textContent = `Cannot display JSON: ${error.message}`;
  }
  node.append(tree);
  return node;
}
