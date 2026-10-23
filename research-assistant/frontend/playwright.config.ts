import process from 'node:process';
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './test/e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://127.0.0.1:15173',
    headless: true,
    trace: 'retain-on-failure',
    // The runner image exports HTTP_PROXY for package downloads; Chromium must
    // talk directly to the compose-network frontend service.
    launchOptions: { args: ['--no-proxy-server', '--proxy-server=direct://', '--proxy-bypass-list=*'] },
  },
});
