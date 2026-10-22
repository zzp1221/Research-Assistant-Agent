import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// pdfjs-dist ships its worker, character maps and standard fonts inside the npm
// package. Copying them into public/ keeps the viewer fully offline and avoids
// loading a worker from unpkg at runtime.
const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
const source = path.join(root, 'node_modules', 'pdfjs-dist');
const target = path.join(root, 'public', 'pdfjs');

const files = [
  ['build/pdf.worker.min.js', 'pdf.worker.min.js'],
];
const directories = ['cmaps', 'standard_fonts'];

if (!fs.existsSync(source)) {
  console.error('[pdfjs] node_modules/pdfjs-dist is missing; run npm ci first');
  process.exit(1);
}

fs.mkdirSync(target, { recursive: true });
for (const [from, to] of files) {
  const fromPath = path.join(source, from);
  if (!fs.existsSync(fromPath)) {
    console.error(`[pdfjs] missing ${from}`);
    process.exit(1);
  }
  fs.copyFileSync(fromPath, path.join(target, to));
}
for (const directory of directories) {
  const fromPath = path.join(source, directory);
  if (!fs.existsSync(fromPath)) {
    console.error(`[pdfjs] missing ${directory}`);
    process.exit(1);
  }
  fs.rmSync(path.join(target, directory), { recursive: true, force: true });
  fs.cpSync(fromPath, path.join(target, directory), { recursive: true });
}
console.log('[pdfjs] synced worker, cmaps and standard fonts into public/pdfjs');
