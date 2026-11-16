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

if (!fs.existsSync(source)) {
  console.error('[pdfjs] node_modules/pdfjs-dist is missing; run npm ci first');
  process.exit(1);
}

const { version } = JSON.parse(fs.readFileSync(path.join(source, 'package.json'), 'utf8'));
const files = [
  ['build/pdf.worker.min.mjs', `${version}/pdf.worker.min.mjs`],
];
const directories = ['cmaps', 'standard_fonts', 'wasm'];

function copyDirectory(from, to) {
  fs.mkdirSync(to, { recursive: true });
  for (const entry of fs.readdirSync(from, { withFileTypes: true })) {
    const source = path.join(from, entry.name);
    const destination = path.join(to, entry.name);
    if (entry.isDirectory()) {
      copyDirectory(source, destination);
    } else if (entry.isFile()) {
      fs.copyFileSync(source, destination);
    }
  }
}

// Remove obsolete workers on every build so deployments cannot mix engines.
fs.rmSync(target, { recursive: true, force: true });
fs.mkdirSync(target, { recursive: true });
for (const [from, to] of files) {
  const fromPath = path.join(source, from);
  if (!fs.existsSync(fromPath)) {
    console.error(`[pdfjs] missing ${from}`);
    process.exit(1);
  }
  fs.mkdirSync(path.dirname(path.join(target, to)), { recursive: true });
  fs.copyFileSync(fromPath, path.join(target, to));
}
for (const directory of directories) {
  const fromPath = path.join(source, directory);
  if (!fs.existsSync(fromPath)) {
    console.error(`[pdfjs] missing ${directory}`);
    process.exit(1);
  }
  fs.rmSync(path.join(target, directory), { recursive: true, force: true });
  copyDirectory(fromPath, path.join(target, directory));
}
fs.writeFileSync(path.join(target, 'manifest.json'), JSON.stringify({ version, isEvalSupported: false, worker: `${version}/pdf.worker.min.mjs` }) + '\n');
console.log(`[pdfjs] synced ${version} worker, cmaps, standard fonts and wasm into public/pdfjs`);
