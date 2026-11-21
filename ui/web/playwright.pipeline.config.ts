import { defineConfig } from '@playwright/test';

// Self-contained UI test for the gate -> agent pipeline: serves the built
// bundle statically and mocks every backend call (no daemon, no token).
// Run: npm run build && npm run e2e:pipeline
const port = Number(process.env['PIPELINE_E2E_PORT'] || 8797);
// PIPELINE_E2E_DIR=../../internal/webui/dist runs the suite against the exact
// bundle embedded into the Go binary (CI smoke test).
const dir = process.env['PIPELINE_E2E_DIR'] || 'dist/dockweave-web/browser';

export default defineConfig({
  testDir: './e2e',
  testMatch: /pipeline\.spec\.ts/,
  timeout: 45000,
  use: { baseURL: `http://127.0.0.1:${port}`, headless: true, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  webServer: {
    command: `python3 -m http.server ${port} --bind 127.0.0.1 --directory ${dir}`,
    url: `http://127.0.0.1:${port}/`,
    reuseExistingServer: false,
    timeout: 20000,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
