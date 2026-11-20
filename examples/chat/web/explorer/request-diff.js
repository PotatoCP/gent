// Compare the full message value, not just its text or ID. A role, tool ID, metadata, or native
// continuation change must stay visible. Object key order is not a content change.
function messageKey(value, depth = 0) {
  if (depth > 64) return null;
  if (typeof value === 'number' && !Number.isSafeInteger(value)) return null;
  // Inspection renders unsafe integer tokens as strings. Their displayed value alone cannot
  // prove that the original JSON types match. Leave those messages visible in either case.
  if (typeof value === 'string' && /^-?\d+$/.test(value) &&
    !Number.isSafeInteger(Number(value))) return null;
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  const parts = [];
  const array = Array.isArray(value);
  for (const key of array ? value.keys() : Object.keys(value).sort()) {
    const child = messageKey(value[key], depth + 1);
    if (child === null) return null;
    parts.push(array ? child : `${JSON.stringify(key)}:${child}`);
  }
  return array ? `[${parts.join(',')}]` : `{${parts.join(',')}}`;
}

// Match each previous occurrence at most once, in order. Repeated identical user inputs must not
// disappear because one earlier input has the same text. A reorder also leaves moved content
// visible. This conservative linear scan can show more context than an optimal sequence diff.
export function compareRequestMessages(messages, previous = []) {
  const positions = new Map();
  for (const [index, message] of previous.entries()) {
    const key = messageKey(message);
    if (key === null) continue;
    if (!positions.has(key)) positions.set(key, {items: [], next: 0});
    positions.get(key).items.push(index);
  }
  let last = -1;
  const repeated = messages.map(message => {
    const key = messageKey(message);
    const match = key === null ? null : positions.get(key);
    if (!match) return false;
    while (match.next < match.items.length && match.items[match.next] <= last) match.next++;
    if (match.next === match.items.length) return false;
    last = match.items[match.next++];
    return true;
  });
  const repeatedCount = repeated.filter(Boolean).length;
  return {repeated, repeatedCount, shownCount: messages.length - repeatedCount};
}
