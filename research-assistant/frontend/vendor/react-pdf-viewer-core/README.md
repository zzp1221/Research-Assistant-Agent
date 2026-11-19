# PDF.js 6 compatibility build

This local package preserves the existing React PDF Viewer 3.12 plugin API while
using the maintained `pdfjs-dist` 6 engine. Upstream 3.12 only declares PDF.js 2/3
support, so a peer-dependency override would hide an actual API incompatibility.
The original distribution and license are retained here.

Upstream package: `@react-pdf-viewer/core@3.12.0`, git revision
`ea84015146e8c5d4c78d7f10df2fba84567ef4aa`.

Local changes:

- `package.json` explicitly declares the tested PDF.js 6 peer dependency.
- `lib/index.js` uses the readable distribution in production and development.
- `lib/cjs/core.js` imports `pdfjs-compat.cjs` instead of the old engine directly.
- The adapter maps the removed `renderTextLayer` function to modern `TextLayer`,
  applies its CSS variable contract to the viewer's selectable spans, forwards
  rendering cancellation and forces `isEvalSupported: false` at the engine edge.
- The application uses the canvas renderer. PDF.js 6 removed the optional old
  `SVGGraphics` renderer; that unused upstream extension is not supported.

No PDF parser, font evaluator or worker implementation is copied from PDF.js 3.
All parsing/rendering uses the single engine locked by the application. The worker
is copied from the same package into a versioned local URL on each build, together
with cMaps, fonts and wasm. Old assets are removed before synchronization.

When upgrading either dependency, review this adapter against the current
`TextLayer` API and run `npm test`, `npm run build`, `npm run check:pdf-security`,
and the real browser PDF/selected-text chat tests. The package's original license
continues to apply.
