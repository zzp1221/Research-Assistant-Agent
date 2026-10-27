// Minimal UI smoke test. Run inside the Playwright container with the frontend
// directory mounted and NODE_PATH pointing at its node_modules.
//   docker run --rm -v <frontend>:/src -w /src mcr.microsoft.com/playwright:v1.64.1-jammy node test/e2e/ui-smoke.mjs
import { chromium } from 'playwright';

const BASE = process.env.UI_URL || 'http://host.docker.internal:5173';
const SHOT = process.env.SHOT || '/out/ui.png';
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });
page.on('pageerror', (error) => errors.push('pageerror: ' + error.message));
await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 });
await page.waitForTimeout(1500);
const body = await page.locator('body').innerText();
const checks = [];
checks.push(['title renders', body.includes('研究') || body.includes('Research')]);
checks.push(['no fake volume masthead', !body.includes('第 12 卷') && !body.includes('第 4 期')]);
checks.push(['composer visible', (await page.locator('textarea, [contenteditable="true"]').count()) > 0]);
checks.push(['auto-run switch visible', (await page.locator('[role="switch"]').count()) > 0]);
await page.screenshot({ path: SHOT, fullPage: false });
await browser.close();
for (const [name, ok] of checks) console.log((ok ? 'ok   ' : 'FAIL ') + name);
if (errors.length) console.log('console errors: ' + errors.slice(0, 5).join(' | '));
if (checks.some(([, ok]) => !ok) || errors.length > 0) process.exit(1);
console.log('UI smoke passed');
