import { test, expect, Page, Route } from '@playwright/test';

// Gate -> agent pipeline in the 2D graph (GP-008/012/026/027), against a
// fully mocked backend.

const now = new Date().toISOString();
const agents = [
  { id: 'ag_a', name: 'alpha', display_name: 'Alpha', type: 'opencode', image: 'x', state: 'running', created_at: now, updated_at: now },
  { id: 'ag_b', name: 'beta', display_name: 'Beta', type: 'opencode', image: 'x', state: 'running', created_at: now, updated_at: now },
];

const gateContract = JSON.stringify({ gate_id: 'tests', command: 'npm test', on_true: 'allow', on_false: 'rework' });
const graphDoc = {
  version: 1, revision: 1,
  connectors: [{ id: 'conn-1', agentId: 'ag_a', targetAgentId: 'ag_b', kind: 'gate', name: 'Tests', direction: 'out', enabled: true, contract: gateContract }],
  paperclip: { workspace: '/ws', syncGates: true },
};

function admissions(blocked: boolean) {
  return {
    revision: 1,
    admissions: [{
      agent_id: 'ag_b', agent: 'beta', allowed: !blocked, action: blocked ? 'rework' : 'allow', revision: 1, checked_at: now,
      edges: [{
        edge_id: 'pe-1', gate_id: 'tests', upstream_agent_id: 'ag_a', workspace: '/ws', satisfied: !blocked,
        code: blocked ? 'failed' : 'ok', run_id: 'gate-42', run_status: blocked ? 'failed' : 'passed',
        run_finished_at: now, action: blocked ? 'rework' : 'allow', message: blocked ? 'tests red' : '',
      }],
    }],
  };
}

const LIST_PATHS = /\/(agents|activity|conflicts|events|workspaces|grants|mounts|memories|skills|mcp|requests|catalog)(\?|$)/;

interface Mock { blocked: boolean; puts: unknown[]; pipelineError: string }

async function mockBackend(page: Page, m: Mock): Promise<void> {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

  await page.route('**/*', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    if (!p.startsWith('/api/') && !p.startsWith('/graph') && !p.startsWith('/paperclip') && !p.startsWith('/v1/') && p !== '/healthz') {
      return route.continue(); // static bundle
    }
    if (p === '/api/v1/agents') return json(route, agents);
    if (p === '/api/v1/pipelines/admission') return json(route, admissions(m.blocked));
    if (p === '/graph' && req.method() === 'GET') return json(route, { saved: true, body: graphDoc, revision: 1 });
    if (p === '/graph' && req.method() === 'PUT') {
      const body = req.postDataJSON();
      m.puts.push(body);
      return json(route, {
        saved: true, body, revision: (body.revision ?? 1) + 1,
        gates: { written: true, gates: 1 },
        pipeline_sync: m.pipelineError ? { pushed: false, edges: 1, error: m.pipelineError } : { pushed: true, edges: 1 },
      });
    }
    if (p === '/graph/validate-expression') {
      const { expression } = req.postDataJSON();
      const bad = /==\s*$/.test(expression) || /\bfoo\b/.test(expression);
      return json(route, bad ? { ok: false, error: 'unexpected end of expression' }
        : { ok: true, uses_budget: /budget/.test(expression), warning: /budget/.test(expression) ? 'needs a budget' : undefined });
    }
    if (p === '/graph/gates/status') return json(route, { enabled: true, exists: true, managed: true });
    if (p === '/api/v1/gates/runs') return json(route, { runs: [] });
    if (p === '/api/v1/gates/reviews') return json(route, { reviews: [] });
    if (p.startsWith('/paperclip/')) return json(route, { issues: [] });
    if (LIST_PATHS.test(p + url.search)) return json(route, []);
    return json(route, {});
  });
}

async function openGraph(page: Page): Promise<void> {
  await page.goto('/');
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('f-flow').waitFor({ state: 'visible', timeout: 15000 });
}

test('proceed edge and downstream agent show the pipeline state', async ({ page }) => {
  const m: Mock = { blocked: true, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await openGraph(page);

  const label = page.locator('.edge-label.edge-proceed');
  await expect(label).toHaveCount(1, { timeout: 10000 });
  await expect(label).toHaveClass(/edge-state-failed/);
  await expect(label).toContainText('failed');
  // Text alternative and keyboard reachability.
  await expect(label).toHaveAttribute('tabindex', '0');
  await expect(label).toHaveAttribute('title', /Grund-Code: failed.*Lauf: gate-42/);

  const badge = page.locator('[data-node-id="agent-ag_b"] .pipeline-badge');
  await expect(badge).toHaveClass(/blocked/);
  await expect(badge).toHaveAttribute('aria-label', /Pipeline blockiert – tests: fehlgeschlagen/);
  await expect(page.locator('[data-node-id="agent-ag_a"] .pipeline-badge')).toHaveCount(0);

  // The status follows the next poll without a reload.
  m.blocked = false;
  await expect(label).toHaveClass(/edge-state-ok/, { timeout: 20000 });
  await expect(badge).not.toHaveClass(/blocked/);
});

test('editor validates expressions, offers routing actions and shows sync errors', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: 'pipeline transfer to the daemon failed: forbidden' };
  await mockBackend(page, m);
  await openGraph(page);

  await page.locator('[data-node-id="agent-ag_a"]').click({ button: 'right' });
  await page.getByRole('button', { name: 'Connectoren konfigurieren…' }).click();

  await page.locator('select[name="connKind"]').selectOption('gate');
  await page.locator('input[name="connName"]').fill('Lint');
  await page.locator('select[name="connTargetAgent"]').selectOption('ag_b');
  await expect(page.getByText('Gate mit Ziel-Agent, aber ohne Shell-Befehl')).toBeVisible();
  await page.locator('input[name="gateCommand"]').fill('npm run lint');

  // Routing vocabulary instead of free text.
  await expect(page.locator('select[name="gateTrue"] option')).toHaveText([/^allow/, /^wake/, /^deny/]);
  await page.locator('select[name="gateFalse"]').selectOption('rework');
  await page.locator('input[name="gateReasonFalse"]').fill('Lint rot');

  // Invalid expression: inline error, save refused.
  await page.locator('input[name="gateExpression"]').fill('command ==');
  await expect(page.getByRole('alert').filter({ hasText: 'Ungültiger Ausdruck' }).first()).toBeVisible();
  await page.getByRole('button', { name: 'Connector hinzufügen' }).click();
  await page.waitForTimeout(800);
  expect(m.puts.some((d: any) => (d.connectors || []).some((c: any) => c.name === 'Lint'))).toBe(false);

  // Valid expression: saved with actions and reasons.
  await page.locator('input[name="gateExpression"]').fill('command == 0');
  await page.getByRole('button', { name: 'Connector hinzufügen' }).click();
  await expect.poll(() => m.puts.some((d: any) => (d.connectors || []).some((c: any) => c.name === 'Lint')), { timeout: 5000 }).toBe(true);
  const saved = (m.puts.at(-1) as any).connectors.find((c: any) => c.name === 'Lint');
  expect(saved.targetAgentId).toBe('ag_b');
  const contract = JSON.parse(saved.contract);
  expect(contract).toMatchObject({ expression: 'command == 0', on_true: 'allow', on_false: 'rework', reason_false: 'Lint rot', command: 'npm run lint' });

  // The failed pipeline transfer is visible.
  await expect(page.locator('.pipeline-sync-error')).toContainText('forbidden');
});

test('legacy free-text routing opens as reason with the default action', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  graphDoc.connectors[0].contract = JSON.stringify({ gate_id: 'tests', command: 'npm test', expression: 'true', on_true: 'continue', on_false: 'tests red' });
  await mockBackend(page, m);
  await openGraph(page);
  await page.locator('[data-node-id="connector-conn-1"]').dblclick();
  await expect(page.locator('select[name="ecTrue"]')).toHaveValue('allow');
  await expect(page.locator('input[name="ecReasonTrue"]')).toHaveValue('continue');
  await expect(page.locator('select[name="ecFalse"]')).toHaveValue('deny');
  await expect(page.locator('input[name="ecReasonFalse"]')).toHaveValue('tests red');
  // expression "true" from older documents is valid.
  await expect(page.getByRole('alert').filter({ hasText: 'Ungültiger Ausdruck' })).toHaveCount(0);
  graphDoc.connectors[0].contract = gateContract;
});
