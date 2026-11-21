import { defineConfig } from '@playwright/test';

// Self-contained UI test for the gate -> agent pipeline: serves the built
// bundle statically and mocks every backend call (no daemon, no token).
// Run: npm run build && npm run e2e:pipeline
export default defineConfig({
  testDir: './e2e',
  testMatch: /pipeline\.spec\.ts/,
  timeout: 45000,
  use: { baseURL: 'http://127.0.0.1:8797', headless: true },
  webServer: {
    command: 'python3 -m http.server 8797 --bind 127.0.0.1 --directory dist/dockweave-web/browser',
    url: 'http://127.0.0.1:8797/',
    reuseExistingServer: false,
    timeout: 20000,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
