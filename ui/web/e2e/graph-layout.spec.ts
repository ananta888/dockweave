import { test, expect } from '@playwright/test';

const TOKEN = process.env.DOCKWEAVE_WEB_TOKEN || 'dw-7607e59342eadecabb561e7d13594797e69a6b35762d1924';

test('every layout mode completes and never leaves the spinner stuck', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', m => { if (m.type() === 'error' && !m.text().includes('ResizeObserver loop')) errors.push(m.text()); });
  page.on('pageerror', e => errors.push(String(e)));

  await page.context().addCookies([{ name: 'dockweave_web', value: TOKEN, url: 'http://127.0.0.1:8790' }]);
  await page.goto('http://127.0.0.1:8790/');
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('f-flow').waitFor({ state: 'visible', timeout: 15000 });

  const select = page.locator('select.small-select').first();
  for (const algo of ['mixed', 'hierarchy', 'columns', 'grid', 'radial', 'force', 'stress']) {
    await select.selectOption(algo);
    // The spinner may flash briefly but must disappear quickly.
    await expect(page.locator('.layout-spinner')).toHaveCount(0, { timeout: 5000 });
    await page.waitForTimeout(300);
  }

  expect(errors).toEqual([]);
});
