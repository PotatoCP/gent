// Require execution coverage for every Go example with instrumented statements.
import { readFileSync } from 'node:fs';
import { parseArgs } from 'node:util';
import { examples } from '../build.mjs';

const { positionals } = parseArgs({ allowPositionals: true });
if (positionals.length !== 1) throw new Error('Supply one Go coverage profile path.');
const pattern = /^github.com\/rickchristie\/gent\/docs\/(.+):(\d+)\.\d+,(\d+)\.\d+ \d+ (\d+)$/;
const blocks = readFileSync(positionals[0], 'utf8').split('\n').flatMap((line) => {
  const match = pattern.exec(line);
  return match ? [{ file: match[1], start: Number(match[2]), end: Number(match[3]),
    count: Number(match[4]) }] : [];
});
if (!blocks.length) throw new Error('The profile contains no documentation example coverage.');
let checked = 0;
for (const [name, region] of Object.entries(examples())) {
  const matching = blocks.filter((block) => block.file === region.file
    && block.start <= region.endLine && block.end >= region.line);
  // Imports, types, and test bodies have no counters. TestMediaScript executes the JavaScript.
  if (!matching.length) continue;
  if (!matching.some((block) => block.count)) {
    throw new Error(`Example ${name} was compiled but not executed.`);
  }
  checked++;
}
console.log(`Verified execution coverage for ${checked} Go example regions.`);
