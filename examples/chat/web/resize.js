import {byId} from './ui.js';

// Resizing changes only the display. The preferred width survives a refresh; viewport limits
// keep the composer usable and prevent horizontal overflow on small screens.
export function setupInspectorResize() {
  const handle = byId('inspector-resize');
  const root = document.documentElement;
  const preference = 'gent-inspector-width';
  let preferred;
  let dragging = false;
  let width;
  try {
    const saved = Number(localStorage.getItem(preference));
    if (Number.isFinite(saved) && saved > 0) preferred = saved;
  } catch (error) { console.warn('Cannot read inspector width preference.', error); }

  function bounds() {
    const available = root.clientWidth;
    const chats = parseFloat(getComputedStyle(document.body).getPropertyValue('--chats-width'));
    const maximum = available <= 800 ? available : available - chats - 320;
    return {minimum: Math.min(280, maximum), maximum};
  }

  function update(value) {
    const {minimum, maximum} = bounds();
    width = Math.round(Math.min(maximum, Math.max(minimum, value)));
    root.style.setProperty('--inspector-width', `${width}px`);
    handle.setAttribute('aria-valuemin', String(minimum));
    handle.setAttribute('aria-valuemax', String(maximum));
    handle.setAttribute('aria-valuenow', String(width));
    handle.setAttribute('aria-valuetext', `${width} pixels`);
  }

  function fit() {
    const preset = parseFloat(getComputedStyle(root).getPropertyValue('--inspector-default-width'));
    update(preferred ?? preset);
  }

  function save() {
    preferred = width;
    try { localStorage.setItem(preference, String(width)); }
    catch (error) { console.warn('Cannot save inspector width preference.', error); }
  }

  handle.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    event.preventDefault();
    handle.focus();
    dragging = true;
    handle.setPointerCapture(event.pointerId);
    document.body.classList.add('resizing-inspector');
  });
  handle.addEventListener('pointermove', event => {
    if (dragging) update(root.clientWidth - event.clientX);
  });
  handle.addEventListener('lostpointercapture', () => {
    if (!dragging) return;
    dragging = false;
    document.body.classList.remove('resizing-inspector');
    save();
  });
  handle.addEventListener('keydown', event => {
    const {minimum, maximum} = bounds();
    const values = {ArrowLeft: width + 32, ArrowRight: width - 32,
      Home: minimum, End: maximum};
    if (!(event.key in values)) return;
    event.preventDefault();
    update(values[event.key]);
    save();
  });
  window.addEventListener('resize', fit);
  fit();
}
