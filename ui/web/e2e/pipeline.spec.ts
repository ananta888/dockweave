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
    if (p === '/graph' && req.method() === 'GET') return json(route, { saved: true, body: graphDoc, revision: 1, pipeline_bindings: { 'conn-1': 'pe-1' } });
    if (p === '/graph' && req.method() === 'PUT') {
      const body = req.postDataJSON();
      m.puts.push(body);
      return json(route, {
        saved: true, body, revision: (body.revision ?? 1) + 1, pipeline_bindings: { 'conn-1': 'pe-1' },
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
  await expect(page.locator('select[name="gateTrue"] option')).toHaveText([/^allow/, /^wake/, /^deny/, /^rework/]);
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

// The Paperclip workspace lives in the agent's connector popover.
async function workspaceInput(page: Page) {
  await page.locator('[data-node-id="agent-ag_a"]').click({ button: 'right' });
  await page.getByRole('button', { name: 'Connectoren konfigurieren…' }).click();
  return page.locator('input[name="paperclipWorkspace"]');
}

async function closeSettings(page: Page): Promise<void> {
  await page.locator('.popover-wide header button').click();
}

test('conflict freezes autosave and keeps a recoverable draft across reload', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/graph', async route => {
    if (route.request().method() !== 'PUT') return route.fallback();
    m.puts.push(route.request().postDataJSON());
    return route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"revision conflict"}' });
  });
  await openGraph(page);
  const input = await workspaceInput(page);
  await input.fill('/draft/one');
  await expect(page.locator('.graph-save-status')).toContainText('409');
  await input.fill('/draft/two');
  await closeSettings(page);
  await page.clock.install();
  await page.clock.fastForward(1000);
  expect(m.puts).toHaveLength(1);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('dockweave.graphRecovery') || '{}').paperclip.workspace)).toBe('/draft/two');
  await page.getByRole('button', { name: 'Serverstand laden', exact: true }).click();
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  expect(m.puts).toHaveLength(1);
  await expect(page.getByRole('button', { name: 'Lokalen Entwurf exportieren' })).toBeVisible();
  const current = await workspaceInput(page);
  await expect(current).toHaveValue('/ws');
});

test('autosave queues edits behind one in-flight write and advances the base revision', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  let release: (() => void) | undefined;
  await page.route('**/graph', async route => {
    if (route.request().method() !== 'PUT') return route.fallback();
    const body: { revision: number; paperclip: { workspace: string } } = route.request().postDataJSON();
    m.puts.push(body);
    if (m.puts.length === 1) await new Promise<void>(resolve => { release = resolve; });
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ saved: true, body, revision: body.revision + 1 }) });
  });
  await openGraph(page);
  const input = await workspaceInput(page);
  await input.fill('/first');
  await expect.poll(() => m.puts.length).toBe(1);
  await input.fill('/second');
  await page.clock.install();
  await page.clock.fastForward(1000);
  expect(m.puts).toHaveLength(1);
  release?.();
  await expect.poll(() => m.puts.length).toBe(2);
  expect(m.puts[1]).toMatchObject({ revision: 2, paperclip: { workspace: '/second' } });
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  await page.clock.fastForward(1000);
  expect(m.puts).toHaveLength(2);
});

test('a slow initial graph load cannot replace an edit or silently start saving it', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  let release: (() => void) | undefined;
  await page.route('**/graph', async route => {
    if (route.request().method() !== 'GET') return route.fallback();
    await new Promise<void>(resolve => { release = resolve; });
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ saved: true, revision: 1, body: graphDoc }) });
  });
  await openGraph(page);
  const input = await workspaceInput(page);
  await input.fill('/while-loading');
  release?.();
  await expect(page.locator('.graph-save-status')).toContainText('Während des Ladens geändert');
  await expect(input).toHaveValue('/while-loading');
  expect(m.puts).toHaveLength(0);
});

test('unavailable admission is not presented as an unsynced or successful gate', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/api/v1/pipelines/admission', route => route.fulfill({ status: 503, body: '{}' }));
  await openGraph(page);
  await expect(page.locator('.edge-label.edge-proceed')).toHaveClass(/edge-state-unavailable/);
  await expect(page.locator('[data-node-id="agent-ag_b"] .pipeline-badge')).toContainText('Status unbekannt');
  await page.setViewportSize({ width: 390, height: 844 });
});

test('a delayed expression validation cannot save a changed editor snapshot', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  let release: (() => void) | undefined;
  const pending = new Promise<void>(resolve => { release = resolve; });
  let validations = 0;
  await page.route('**/graph/validate-expression', async route => {
    const request: { expression: string } = route.request().postDataJSON();
    validations++;
    if (request.expression === 'true') await pending;
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ ok: request.expression === 'true', error: 'invalid' }) });
  });
  await openGraph(page);
  await page.locator('[data-node-id="connector-conn-1"]').dblclick();
  await page.locator('input[name="ecExpression"]').fill('true');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => validations).toBeGreaterThan(0);
  await page.locator('input[name="ecCommand"]').fill('different command');
  release?.();
  await expect(page.locator('.notice.error').filter({ hasText: 'Formular inzwischen geändert' })).toBeVisible();
  expect(m.puts).toHaveLength(0);
});

test('validation server failures do not save an unverified expression', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/graph/validate-expression', route => route.fulfill({ status: 500, body: '{}' }));
  await openGraph(page);
  await page.locator('[data-node-id="connector-conn-1"]').dblclick();
  await page.locator('input[name="ecExpression"]').fill('true');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.locator('.notice.error').filter({ hasText: 'Ausdruck nicht verifiziert' })).toBeVisible();
  expect(m.puts).toHaveLength(0);
});

test('a conflict draft survives later edits and can be restored onto the server revision', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/graph', async route => {
    if (route.request().method() !== 'PUT') return route.fallback();
    const body = route.request().postDataJSON();
    m.puts.push(body);
    if (m.puts.length === 1) return route.fulfill({ status: 409, contentType: 'application/json', body: '{"error":"revision conflict"}' });
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ saved: true, body, revision: body.revision + 1 }) });
  });
  await openGraph(page);
  const draftKey = () => page.evaluate(() => JSON.parse(localStorage.getItem('dockweave.graphConflictDraft') || 'null')?.paperclip?.workspace);
  let input = await workspaceInput(page);
  await input.fill('/mine');
  await expect(page.locator('.graph-save-status')).toContainText('Konflikt mit dem Serverstand');
  await closeSettings(page);
  await page.getByRole('button', { name: 'Serverstand laden', exact: true }).click();
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  expect(await draftKey()).toBe('/mine');

  // A later edit is autosaved normally but never overwrites the frozen draft.
  input = await workspaceInput(page);
  await expect(input).toHaveValue('/ws');
  await input.fill('/later');
  await expect.poll(() => m.puts.length).toBe(2);
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  expect(await draftKey()).toBe('/mine');
  await closeSettings(page);

  // Explicit "keep mine": saved on top of the confirmed server revision.
  await page.getByRole('button', { name: 'Entwurf übernehmen', exact: true }).click();
  await expect.poll(() => m.puts.length).toBe(3);
  expect(m.puts[2]).toMatchObject({ revision: 2, paperclip: { workspace: '/mine' } });
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  expect(await draftKey()).toBeUndefined();
  await expect(page.getByRole('button', { name: 'Entwurf übernehmen' })).toHaveCount(0);
});

test('revision drift is shown as unsynchronized, not as an outage', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/api/v1/pipelines/admission', route => route.fulfill({
    contentType: 'application/json', body: JSON.stringify({ ...admissions(false), revision: 0 }) }));
  await openGraph(page);
  const badge = page.locator('[data-node-id="agent-ag_b"] .pipeline-badge');
  await expect(badge).toContainText('Nicht synchronisiert');
  await expect(badge).not.toContainText('Status unbekannt');
  await expect(page.locator('.edge-label.edge-proceed')).toHaveClass(/edge-state-unsynced/);
  await expect(page.locator('.pipeline-drift')).toContainText('Revision 0');
});

test('an invalid gate contract names the connector and keeps enforcement', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await page.route('**/graph', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({
      saved: true, body: graphDoc, revision: 1, pipeline_bindings: {},
      pipeline_sync: { pushed: false, edges: 0, error: 'connector "conn-1" to agent "ag_b": invalid gate contract',
        invalid_connector: { connector_id: 'conn-1', target_agent_id: 'ag_b', message: 'unexpected end of JSON input' } } }) });
  });
  await openGraph(page);
  await workspaceInput(page);
  await expect(page.locator('.invalid-connector')).toContainText('„Tests“ → ag_b');
  await expect(page.locator('.invalid-connector')).toContainText('bleibt unverändert');
});

test('a failed save is retried by the retry button and a reload keeps the local draft', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  let fail = true;
  await page.route('**/graph', async route => {
    if (route.request().method() !== 'PUT') return route.fallback();
    const body = route.request().postDataJSON();
    m.puts.push(body);
    if (fail) return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"disk full"}' });
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ saved: true, body, revision: body.revision + 1 }) });
  });
  await openGraph(page);
  let input = await workspaceInput(page);
  await input.fill('/unsaved');
  await expect(page.locator('.graph-save-status')).toContainText('Speichern fehlgeschlagen');
  await closeSettings(page);
  const failedPuts = m.puts.length;

  // No automatic retry while failed; the explicit retry saves the same state.
  fail = false;
  await page.getByRole('button', { name: 'Speichern erneut versuchen' }).click();
  await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
  expect(m.puts.length).toBe(failedPuts + 1);
  expect((m.puts.at(-1) as any).paperclip.workspace).toBe('/unsaved');

  // A later failure followed by a reload keeps the draft for export.
  fail = true;
  input = await workspaceInput(page);
  await input.fill('/second');
  await expect(page.locator('.graph-save-status')).toContainText('Speichern fehlgeschlagen');
  await page.reload();
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('f-flow').waitFor({ state: 'visible', timeout: 15000 });
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('dockweave.graphRecovery') || '{}').paperclip?.workspace)).toBe('/second');
  await expect(page.getByRole('button', { name: 'Lokalen Entwurf exportieren' })).toBeVisible();
});

test('pipeline status recovers after an admission outage', async ({ page }) => {
  const m: Mock = { blocked: true, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  let down = true;
  await page.route('**/api/v1/pipelines/admission', route => down
    ? route.fulfill({ status: 503, body: '{}' })
    : route.fulfill({ contentType: 'application/json', body: JSON.stringify(admissions(m.blocked)) }));
  await openGraph(page);
  const badge = page.locator('[data-node-id="agent-ag_b"] .pipeline-badge');
  await expect(badge).toContainText('Status unbekannt');
  down = false;
  await expect(badge).toContainText('blockiert', { timeout: 20000 });
  await expect(page.locator('.edge-label.edge-proceed')).toHaveClass(/edge-state-failed/);
});

test('the gate editor stores the symlink policy in the contract', async ({ page }) => {
  const m: Mock = { blocked: false, puts: [], pipelineError: '' };
  await mockBackend(page, m);
  await openGraph(page);
  await page.locator('[data-node-id="connector-conn-1"]').dblclick();
  await expect(page.locator('select[name="ecSymlinks"]')).toHaveValue('');
  await page.locator('select[name="ecSymlinks"]').selectOption('ignored');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => m.puts.length).toBeGreaterThan(0);
  const saved = (m.puts.at(-1) as any).connectors.find((c: any) => c.id === 'conn-1');
  expect(JSON.parse(saved.contract).symlink_policy).toBe('ignored');
});
