import { test, expect } from '@playwright/test';

// Live test against a running gateway: the token must come from the
// environment (e.g. `set -a; . ~/.config/dockweave/web.env; set +a`).
// Never hard-code a fallback — this file is in a public repository.
// Checked inside the test so the mocked specs in the same run still execute.
const TOKEN = process.env.DOCKWEAVE_WEB_TOKEN ?? '';

test('every layout mode completes and never leaves the spinner stuck', async ({ page }) => {
  if (!TOKEN) {
    throw new Error('DOCKWEAVE_WEB_TOKEN is not set; source ~/.config/dockweave/web.env before running this live E2E test');
  }
  const errors: string[] = [];
  page.on('console', m => { if (m.type() === 'error' && !m.text().includes('ResizeObserver loop')) errors.push(m.text()); });
  page.on('pageerror', e => errors.push(String(e)));

  await page.context().addCookies([{ name: 'dockweave_web', value: TOKEN, url: 'http://127.0.0.1:8790' }]);
  await page.goto('http://127.0.0.1:8790/');
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('f-flow').waitFor({ state: 'visible', timeout: 15000 });

  const select = page.locator('select.small-select').first();
  for (const algo of ['mixed', 'hierarchy', 'columns', 'grid', 'radial', 'force', 'stress', 'star']) {
    await select.selectOption(algo);
    // The spinner may flash briefly but must disappear quickly.
    await expect(page.locator('.layout-spinner')).toHaveCount(0, { timeout: 5000 });
    await page.waitForTimeout(300);
  }

  // Display-mode switch must also stay responsive.
  await page.getByRole('button', { name: 'Ressourcen', exact: true }).click();
  await page.waitForTimeout(600);
  await expect(page.locator('.layout-spinner')).toHaveCount(0, { timeout: 5000 });
  await page.getByRole('button', { name: 'Organisation', exact: true }).click();
  await page.waitForTimeout(600);
  await expect(page.locator('.layout-spinner')).toHaveCount(0, { timeout: 5000 });

  expect(errors).toEqual([]);
});
