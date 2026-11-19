export function partText(parts = []) {
  return parts.map(part => {
    if (typeof part.Text === 'string') return part.Text;
    if (Array.isArray(part.Content)) return partText(part.Content);
    if (Array.isArray(part.Parts)) return partText(part.Parts);
    if (part.function) return `${part.function.name}(${part.function.arguments})`;
    if (Object.hasOwn(part, 'Data')) return JSON.stringify(part.Data, null, 2);
    return '[Native or media content — open source fields]';
  }).join('\n\n');
}

