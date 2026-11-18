import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30000,
  use: {
    baseURL: 'http://127.0.0.1:8790',
    headless: true,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
