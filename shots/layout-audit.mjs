import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
const require = createRequire('D:/项目/Research Assistant agent/research-assistant/frontend/package.json');
const { chromium } = require('playwright');
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
const pages = process.argv.slice(2);
for (const page of pages) {
  const p = await ctx.newPage();
  await p.goto(pathToFileURL(path.resolve(page)).href, { waitUntil: 'load' });
  await p.waitForTimeout(350);
  const data = await p.evaluate(() => {
    const selectors = ['.app','.rail','.chat','.canvas','.graph','.canvas-foot','.messages','.composer','.topbar','.workspace','.sessions','.inspector','.dock','.main','.schematic','.schematic-body','.notebook','.notes','.composer-card'];
    const rect = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); return { x: +r.x.toFixed(1), y: +r.y.toFixed(1), w: +r.width.toFixed(1), h: +r.height.toFixed(1), bottom: +r.bottom.toFixed(1), right: +r.right.toFixed(1) }; };
    const out = { title: document.title, viewport: [innerWidth, innerHeight], doc: [document.documentElement.scrollWidth, document.documentElement.scrollHeight], elements: {}, nodes: [] };
    for (const s of selectors) out.elements[s] = rect(document.querySelector(s));
    for (const n of document.querySelectorAll('.node')) {
      const r = rect(n);
      out.nodes.push({ text: n.querySelector('h3')?.textContent, ...r });
    }
    return out;
  });
  console.log(JSON.stringify({ page, ...data }, null, 2));
  await p.close();
}
await browser.close();
