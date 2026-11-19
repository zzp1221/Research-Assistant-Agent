import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { test } from 'node:test';

const source = readFileSync(new URL('../vendor/react-pdf-viewer-core/lib/cjs/pdfjs-compat.cjs', import.meta.url), 'utf8');

test('PDF adapter forcibly disables eval even when the caller requests it', () => {
  let received;
  const module = { exports: {} };
  runInNewContext(source, { module, require: () => ({ getDocument: (options) => { received = options; return 'loading-task'; } }) });
  assert.equal(module.exports.getDocument({ url: 'example.pdf', isEvalSupported: true }), 'loading-task');
  assert.equal(received.isEvalSupported, false);
  assert.equal(received.url, 'example.pdf');
});

test('PDF adapter bridges modern text rendering, styles and cancellation for selection plugins', async () => {
  const declarations = {};
  let options;
  let cancelled = false;
  const span = { style: {} };
  class TextLayer {
    constructor(input) { options = input; this.textDivs = [span]; }
    render() { return Promise.resolve(); }
    cancel() { cancelled = true; }
  }
  const module = { exports: {} };
  runInNewContext(source, { module, require: () => ({ TextLayer }) });
  const container = { style: { setProperty: (key, value) => { declarations[key] = value; } } };
  const content = { items: [] };
  const task = module.exports.renderTextLayer({ container, textContent: content, viewport: { scale: 1.5 } });
  await task.promise;
  assert.equal(options.textContentSource, content);
  assert.equal(declarations['--total-scale-factor'], '1.5');
  assert.match(span.style.fontSize, /--font-height/);
  assert.match(span.style.transform, /--scale-x/);
  task.cancel();
  assert.equal(cancelled, true);
});
