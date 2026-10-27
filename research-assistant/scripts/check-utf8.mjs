#!/usr/bin/env node
// Fails when a tracked text file is not valid UTF-8 or contains mojibake /
// replacement characters. Run from the repository root: node scripts/check-utf8.mjs
import fs from 'node:fs';
import path from 'node:path';

const ROOTS = ['research-assistant', 'docker-core', 'tool', 'docs', 'reports'];
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', 'build', 'coverage', 'test-results', 'playwright-report', '.dsh-tmp', '.oil-ui', 'ui-audit']);
const TEXT_EXT = new Set(['.go', '.ts', '.tsx', '.js', '.mjs', '.cjs', '.json', '.md', '.yml', '.yaml', '.css', '.html', '.sh', '.ps1', '.py', '.toml', '.txt', '.conf', '.template', '.env', '.example']);
const EXTRA_FILES = ['research-assistant/docker-compose.yml', 'research-assistant/backend.env.example', 'go.work', 'README.md'];

const decoder = new TextDecoder('utf-8', { fatal: true });
const problems = [];

function walk(target) {
  let stat;
  try {
    stat = fs.statSync(target);
  } catch {
    return;
  }
  if (stat.isDirectory()) {
    if (SKIP_DIRS.has(path.basename(target))) return;
    for (const entry of fs.readdirSync(target)) walk(path.join(target, entry));
    return;
  }
  if (!stat.isFile()) return;
  if (!TEXT_EXT.has(path.extname(target)) && !target.endsWith('Dockerfile')) return;
  let buffer;
  try {
    buffer = fs.readFileSync(target);
  } catch (error) {
    problems.push(target + ': cannot read (' + error.message + ')');
    return;
  }
  let text;
  try {
    text = decoder.decode(buffer);
  } catch {
    problems.push(target + ': invalid UTF-8');
    return;
  }
  if (text.includes('\uFFFD')) {
    const index = text.indexOf('\uFFFD');
    problems.push(target + ': replacement character at offset ' + index);
  }
  if (text.includes('锟斤拷')) {
    problems.push(target + ': mojibake marker 锟斤拷');
  }
}

for (const root of ROOTS) walk(root);
for (const file of EXTRA_FILES) walk(file);

if (problems.length) {
  console.error('UTF-8/mojibake check failed:');
  for (const problem of problems) console.error(' - ' + problem);
  process.exit(1);
}
console.log('UTF-8 check passed');
