#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';

const root = path.resolve(process.argv[2] || 'research-assistant');
const backend = path.join(root, 'backend', 'internal');
const banned = [
  'not_installed_offline_mock',
  'offline_mock_framework_benchmark',
  'deterministicFrameworkBenchmarkCode',
  'buildDeterministicFrameworkBenchmarkCode',
  'SEED_DATA',
  'fake metric',
  'random prediction',
];

// Known-safe hits:
// - coder.go: RC4 Framework benchmark mock, explicitly out of scope for this
//   non-RC4 remediation and reported as a baseline.
// - benchmark_adapter.go / paper_debug_harness.go: prompt guardrail strings
//   that tell the model NOT to emit fake/random metrics or predictions.
const knownSafe = new Map([
  ['backend/internal/agent/coder.go', 'RC4 baseline (out of scope)'],
  ['backend/internal/agent/benchmark_adapter.go', 'prompt guardrail text'],
  ['backend/internal/agent/paper_debug_harness.go', 'prompt guardrail text'],
]);

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'demo' || entry.name === 'node_modules') continue;
      walk(full, out);
    } else if (entry.isFile() && (entry.name.endsWith('.go') || entry.name.endsWith('.py'))) {
      if (entry.name.endsWith('_test.go')) continue;
      out.push(full);
    }
  }
  return out;
}

const files = walk(backend);
const hits = [];
for (const file of files) {
  const text = fs.readFileSync(file, 'utf8');
  const lines = text.split(/\r?\n/);
  lines.forEach((line, idx) => {
    for (const term of banned) {
      if (line.includes(term)) {
        const rel = path.relative(root, file).split(path.sep).join('/');
        hits.push({ file: rel, line: idx + 1, term, text: line.trim() });
      }
    }
  });
}

const baselineHits = hits.filter((h) => knownSafe.has(h.file));
const newHits = hits.filter((h) => !knownSafe.has(h.file));

console.log('mock-audit production scan');
console.log('  files scanned:', files.length);
console.log('  total hits:', hits.length);
if (baselineHits.length) {
  console.log('  known-safe baseline (reported only):');
  for (const hit of baselineHits) console.log('    ' + hit.file + ':' + hit.line + ' [' + hit.term + ']');
}
if (newHits.length) {
  console.error('  NEW production mock/fake hits:');
  for (const hit of newHits) console.error('    ' + hit.file + ':' + hit.line + ' [' + hit.term + '] ' + hit.text);
  process.exit(1);
}
console.log('  no new production mock/fake hits');