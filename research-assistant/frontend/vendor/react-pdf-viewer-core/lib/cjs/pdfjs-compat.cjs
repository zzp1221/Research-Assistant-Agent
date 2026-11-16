'use strict';

// Bridge only APIs removed since the upstream viewer's PDF.js 3 integration.
// Parsing and rendering always use the installed, patched PDF.js engine.
var pdfjs = require('pdfjs-dist');
module.exports = Object.assign({}, pdfjs, {
    getDocument: function (options) {
        return pdfjs.getDocument(Object.assign({}, options, { isEvalSupported: false }));
    },
    renderTextLayer: function (options) {
        options.container.style.setProperty('--total-scale-factor', String(options.viewport.scale));
        options.container.style.setProperty('--scale-round-x', '1px');
        options.container.style.setProperty('--scale-round-y', '1px');
        var layer = new pdfjs.TextLayer({
            textContentSource: options.textContentSource || options.textContent,
            container: options.container,
            viewport: options.viewport,
        });
        var promise = layer.render().then(function () {
            // Modern TextLayer emits font/transform CSS variables. The upstream
            // viewer supplies positioning/selection styles under its own class.
            layer.textDivs.forEach(function (span) {
                span.style.fontSize = 'calc(var(--total-scale-factor) * var(--min-font-size) * var(--font-height))';
                span.style.transform = 'rotate(var(--rotate, 0deg)) scaleX(var(--scale-x, 1)) scale(calc(1 / var(--min-font-size)))';
            });
        });
        return { promise: promise, cancel: function () { layer.cancel(); } };
    },
});
