import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const readJSON = (file) => JSON.parse(fs.readFileSync(path.join(root, file), 'utf8'));
const { version } = readJSON('node_modules/pdfjs-dist/package.json');
const parts = version.split('.').map(Number);
assert(parts[0] > 4 || (parts[0] === 4 && (parts[1] > 2 || (parts[1] === 2 && parts[2] >= 67))),
  `PDF.js ${version} is affected by CVE-2024-4367`);

const lock = readJSON('package-lock.json');
for (const [name, pkg] of Object.entries(lock.packages)) {
  if (name.endsWith('node_modules/pdfjs-dist')) assert.equal(pkg.version, version, 'A second PDF.js engine is present');
}
const manifest = readJSON('public/pdfjs/manifest.json');
assert.equal(manifest.version, version);
assert.equal(manifest.isEvalSupported, false);
assert.equal(manifest.worker, `${version}/pdf.worker.min.mjs`);
const digest = (file) => createHash('sha256').update(fs.readFileSync(path.join(root, file))).digest('hex');
assert.equal(digest('node_modules/pdfjs-dist/build/pdf.worker.min.mjs'), digest(`public/pdfjs/${manifest.worker}`));
assert(!fs.existsSync(path.join(root, 'public/pdfjs/pdf.worker.min.js')), 'An obsolete worker survived synchronization');
for (const directory of ['cmaps', 'standard_fonts', 'wasm']) {
  assert(fs.readdirSync(path.join(root, 'public/pdfjs', directory)).length > 0, `${directory} assets missing`);
}
console.log(`[pdf-security] PDF.js ${version}; one engine; matching versioned worker; local fonts, cmaps and wasm; CVE-2024-4367 excluded`);
