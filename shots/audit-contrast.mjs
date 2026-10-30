import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
const require = createRequire('D:/项目/Research Assistant agent/research-assistant/frontend/package.json');
const { chromium } = require('playwright');
import path from 'node:path';

const root = process.cwd();
const pages = process.argv.slice(2);
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
const results = [];

function parseColor(value) {
  const m = value.match(/rgba?\(([^)]+)\)/);
  if (!m) return { r: 0, g: 0, b: 0, a: 1 };
  const parts = m[1].split(',').map(s => parseFloat(s.trim()));
  return { r: parts[0] || 0, g: parts[1] || 0, b: parts[2] || 0, a: parts.length > 3 ? parts[3] : 1 };
}
function composite(fg, bg) {
  const a = fg.a + bg.a * (1 - fg.a);
  return { r: (fg.r * fg.a + bg.r * bg.a * (1 - fg.a)) / a, g: (fg.g * fg.a + bg.g * bg.a * (1 - fg.a)) / a, b: (fg.b * fg.a + bg.b * bg.a * (1 - fg.a)) / a, a };
}
function lum(c) {
  const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
  return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
}
function contrast(a, b) {
  const l1 = lum(a), l2 = lum(b);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

for (const page of pages) {
  const pageObj = await ctx.newPage();
  const file = path.resolve(root, page);
  await pageObj.goto(pathToFileURL(file).href, { waitUntil: 'load' });
  await pageObj.waitForTimeout(250);
  const audit = await pageObj.evaluate(() => {
    const composite = (fg, bg) => {
      const a = fg.a + bg.a * (1 - fg.a);
      return { r: (fg.r * fg.a + bg.r * bg.a * (1 - fg.a)) / a, g: (fg.g * fg.a + bg.g * bg.a * (1 - fg.a)) / a, b: (fg.b * fg.a + bg.b * bg.a * (1 - fg.a)) / a, a };
    };
    const luminance = (c) => {
      const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
      return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
    };
    const contrast = (a, b) => {
      const l1 = luminance(a), l2 = luminance(b);
      return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
    };
    const isVisible = (el) => {
      const s = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      return s.display !== 'none' && s.visibility !== 'hidden' && parseFloat(s.opacity) > 0.05 && r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
    };
    const all = [...document.querySelectorAll('body *')];
    const texts = all.filter(el => {
      if (!isVisible(el)) return false;
      if (['SCRIPT','STYLE','SVG','PATH','DEFS','SYMBOL','USE','TEXTAREA','INPUT','SELECT'].includes(el.tagName)) return false;
      const own = [...el.childNodes].filter(n => n.nodeType === Node.TEXT_NODE).map(n => n.textContent.trim()).join('').trim();
      return own.length > 0;
    });
    const getBg = (el) => {
      let alpha = 1;
      let current = el;
      let bg = null;
      while (current) {
        const s = getComputedStyle(current);
        alpha *= parseFloat(s.opacity || '1');
        const c = s.backgroundColor.match(/rgba?\(([^)]+)\)/);
        if (c) {
          const p = c[1].split(',').map(Number);
          const a = p.length > 3 ? p[3] : 1;
          if (a > 0) {
            const candidate = { r: p[0], g: p[1], b: p[2], a };
            bg = bg ? composite(bg, candidate) : candidate;
            if (bg.a >= 0.99) break;
          }
        }
        current = current.parentElement;
      }
      if (!bg) bg = { r: 255, g: 255, b: 255, a: 1 };
      return { ...bg, a: alpha };
    };
    const getOpacity = (el) => {
      let a = 1, n = el;
      while (n) { a *= parseFloat(getComputedStyle(n).opacity || '1'); n = n.parentElement; }
      return a;
    };
    const describe = (el) => {
      let id = el.tagName.toLowerCase();
      if (el.className && typeof el.className === 'string') id += '.' + el.className.trim().split(/\s+/).slice(0,2).join('.');
      const t = (el.innerText || '').trim().replace(/\s+/g, ' ').slice(0, 44);
      return id + (t ? '「' + t + '」' : '');
    };
    const low = [];
    for (const el of texts) {
      const s = getComputedStyle(el);
      const size = parseFloat(s.fontSize);
      const weight = parseInt(s.fontWeight, 10) || 400;
      const fg = (() => {
        const m = s.color.match(/rgba?\(([^)]+)\)/);
        const p = m ? m[1].split(',').map(Number) : [0,0,0,1];
        return { r:p[0], g:p[1], b:p[2], a:p.length>3?p[3]:1 };
      })();
      const bg = getBg(el);
      const op = getOpacity(el);
      const color = composite({ ...fg, a: fg.a * op }, bg);
      const ratio = contrast(color, bg);
      const large = size >= 24 || (size >= 18.6 && weight >= 700);
      if (ratio < (large ? 3 : 4.5)) low.push({ el: describe(el), ratio: +ratio.toFixed(2), size, color: s.color, bg: `rgb(${Math.round(bg.r)}, ${Math.round(bg.g)}, ${Math.round(bg.b)})` });
    }
    const overflow = [];
    for (const el of all) {
      if (!isVisible(el)) continue;
      if (el.scrollWidth > el.clientWidth + 1 && getComputedStyle(el).overflowX !== 'visible') overflow.push(describe(el));
    }
    const rootEl = document.documentElement;
    return {
      title: document.title,
      viewport: { w: innerWidth, h: innerHeight },
      doc: { scrollW: rootEl.scrollWidth, clientW: rootEl.clientWidth, scrollH: rootEl.scrollHeight, clientH: rootEl.clientHeight },
      low,
      overflow: overflow.slice(0, 20)
    };
  });
  results.push({ page, ...audit });
  await pageObj.close();
}
await browser.close();
console.log(JSON.stringify(results, null, 2));
