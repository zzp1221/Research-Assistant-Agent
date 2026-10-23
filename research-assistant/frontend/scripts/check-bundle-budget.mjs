#!/usr/bin/env node
/**
 * Bundle budget gate.
 *
 * The initial entry must stay small enough for a research workspace to become
 * interactive quickly. Heavy feature surfaces (PDF, reports, graph canvas) are
 * allowed to live in lazy chunks with their own ceiling.
 */
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';
import process from 'node:process';

const distDir = path.resolve('dist');
const indexPath = path.join(distDir, 'index.html');

const readGzipKb = (filePath) => {
  const bytes = fs.readFileSync(filePath);
  return {
    rawKb: bytes.length / 1024,
    gzipKb: zlib.gzipSync(bytes).length / 1024,
  };
};

const format = (value) => value.toFixed(1).padStart(7, ' ');

if (!fs.existsSync(indexPath)) {
  console.error('[bundle] dist/index.html not found. Run "npm run build" first.');
  process.exit(1);
}

const html = fs.readFileSync(indexPath, 'utf8');
const entryMatch = html.match(/<script[^>]*src="([^"]+\.js)"/);
const cssMatch = html.match(/<link[^>]*href="([^"]+\.css)"/);

if (!entryMatch) {
  console.error('[bundle] Could not find the entry script in dist/index.html.');
  process.exit(1);
}

const resolveAsset = (url) => path.join(distDir, url.replace(/^\//, ''));
const entryPath = resolveAsset(entryMatch[1]);
const entry = readGzipKb(entryPath);
const css = cssMatch ? readGzipKb(resolveAsset(cssMatch[1])) : { rawKb: 0, gzipKb: 0 };

const entryGzipBudget = Number(process.env.BUNDLE_ENTRY_GZIP_KB || 320);
const entryCssGzipBudget = Number(process.env.BUNDLE_ENTRY_CSS_GZIP_KB || 35);
const lazyChunkGzipBudget = Number(process.env.BUNDLE_LAZY_CHUNK_GZIP_KB || 400);

const assetsDir = path.join(distDir, 'assets');
const lazyChunks = fs.existsSync(assetsDir)
  ? fs.readdirSync(assetsDir)
      .filter((file) => file.endsWith('.js') && path.join(assetsDir, file) !== entryPath)
      .map((file) => {
        const filePath = path.join(assetsDir, file);
        return { file, ...readGzipKb(filePath) };
      })
      .sort((a, b) => b.gzipKb - a.gzipKb)
  : [];

const failures = [];
if (entry.gzipKb > entryGzipBudget) {
  failures.push(`entry JS gzip ${entry.gzipKb.toFixed(1)} KB > ${entryGzipBudget} KB`);
}
if (css.gzipKb > entryCssGzipBudget) {
  failures.push(`entry CSS gzip ${css.gzipKb.toFixed(1)} KB > ${entryCssGzipBudget} KB`);
}
for (const chunk of lazyChunks) {
  if (chunk.gzipKb > lazyChunkGzipBudget) {
    failures.push(`lazy chunk ${chunk.file} gzip ${chunk.gzipKb.toFixed(1)} KB > ${lazyChunkGzipBudget} KB`);
  }
}

console.log('\n[bundle] Budget report');
console.log('  asset                                      raw KB   gzip KB');
console.log(`  entry ${path.basename(entryPath).padEnd(38)} ${format(entry.rawKb)}  ${format(entry.gzipKb)}`);
console.log(`  entry CSS ${path.basename(cssMatch?.[1] || 'n/a').padEnd(34)} ${format(css.rawKb)}  ${format(css.gzipKb)}`);
for (const chunk of lazyChunks) {
  console.log(`  lazy  ${chunk.file.padEnd(38)} ${format(chunk.rawKb)}  ${format(chunk.gzipKb)}`);
}
console.log(`\n  limits: entry JS gzip ${entryGzipBudget} KB, entry CSS gzip ${entryCssGzipBudget} KB, lazy chunk gzip ${lazyChunkGzipBudget} KB\n`);

if (failures.length > 0) {
  console.error('[bundle] FAILED');
  for (const failure of failures) console.error('  - ' + failure);
  process.exit(1);
}

console.log('[bundle] OK');
