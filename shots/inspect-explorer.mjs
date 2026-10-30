import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import path from 'node:path';
const require = createRequire('D:/项目/Research Assistant agent/research-assistant/frontend/package.json');
const { chromium } = require('playwright');
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe' });
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
const target = path.resolve('D:/项目/Research Assistant agent/style-explorer.html');
await page.goto(pathToFileURL(target).href, { waitUntil: 'load' });
await page.waitForTimeout(1800);
const overview = await page.evaluate(() => {
  const cards = [...document.querySelectorAll('.card')].map((card) => {
    const frame = card.querySelector('.frame');
    const stage = card.querySelector('.stage');
    const iframe = card.querySelector('iframe');
    const title = iframe?.contentDocument?.title || null;
    const body = iframe?.contentDocument?.body;
    const cr = card.getBoundingClientRect();
    const fr = frame?.getBoundingClientRect();
    const sr = stage?.getBoundingClientRect();
    return {
      id: card.dataset.id,
      name: card.querySelector('.cap .name')?.textContent,
      card: { x: Math.round(cr.x), y: Math.round(cr.y), w: Math.round(cr.width), h: Math.round(cr.height) },
      frame: fr ? { w: Math.round(fr.width), h: Math.round(fr.height) } : null,
      stage: sr ? { w: Math.round(sr.width), h: Math.round(sr.height) } : null,
      iframeTitle: title,
      iframeBody: body ? { scrollW: body.scrollWidth, scrollH: body.scrollHeight, clientW: body.clientWidth, clientH: body.clientHeight } : null,
      ready: stage?.classList.contains('ready')
    };
  });
  return {
    title: document.title,
    viewport: { w: innerWidth, h: innerHeight },
    doc: { scrollW: document.documentElement.scrollWidth, clientW: document.documentElement.clientWidth, scrollH: document.documentElement.scrollHeight, clientH: document.documentElement.clientHeight },
    grid: getComputedStyle(document.querySelector('#grid')).gridTemplateColumns,
    cards
  };
});
console.log('OVERVIEW\n' + JSON.stringify(overview, null, 2));
await page.click('[data-layout="loupe"]');
await page.waitForTimeout(700);
const loupe = await page.evaluate(() => ({
  visible: !document.querySelector('#loupe').hidden,
  slides: document.querySelectorAll('.slide').length,
  current: document.querySelector('#film [aria-current="true"]')?.textContent?.trim(),
  stage: (() => { const s = document.querySelector('.slide.is-current .stage'); if (!s) return null; const r = s.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; })(),
  info: document.querySelector('#info-body')?.innerText?.slice(0, 200)
}));
console.log('LOUPE\n' + JSON.stringify(loupe, null, 2));
await page.click('[data-layout="compare"]');
await page.waitForTimeout(400);
await browser.close();
