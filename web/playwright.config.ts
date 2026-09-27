import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { BASE_URL } from './e2e/support/stack';

const HERE = path.dirname(fileURLToPath(import.meta.url));

/**
 * V7-17C's own deterministic full-journey E2E config (docs/design/
 * 09-v7-alpha-ui.md V7-17). `globalSetup`/`globalTeardown` build and run a
 * real `aw serve --ui-dist web/dist` + `aw worker` pair against a fresh,
 * throwaway installation (see e2e/support/stack.ts) — `web/dist` must
 * already exist (`pnpm build`) before this suite runs. One worker: the
 * whole suite drives ONE continuous journey against ONE shared backend
 * installation, the same reason `internal/integration/v6accept`'s own Go
 * acceptance journey is a single sequential test, never parallelized.
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  globalSetup: path.join(HERE, 'e2e/support/global-setup.ts'),
  globalTeardown: path.join(HERE, 'e2e/support/global-teardown.ts'),
  use: {
    baseURL: BASE_URL,
    // A stuck action should fail fast and name itself, never silently eat
    // the whole test's own generous overall timeout (this journey's real
    // process/network round trips need that budget for genuine work, not
    // for one action to sit retrying against a locator that was simply
    // wrong).
    actionTimeout: 10_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
});
