import { test, expect, Page, Route } from '@playwright/test';

// Live status of the 2D graph (green = active/talking, red = problems by
// severity), against a fully mocked backend.

const now = new Date().toISOString();
const old = new Date(Date.now() - 10 * 60_000).toISOString();

interface State {
  agentsDown: boolean;
  agentStates: Record<string, string>;
  activity: unknown[];
  conflicts: unknown[];
  live: unknown[];
  code: string;
  allowed: boolean;
  finishedAt: string;
  runs: unknown[];
  reviews: unknown[];
  reviewRequired: boolean;
  chain: boolean;
  grants: unknown[];
}

function base(): State {
  return {
    agentsDown: false, agentStates: { ag_a: 'running', ag_b: 'running' }, activity: [], conflicts: [], live: [],
    code: 'ok', allowed: true, finishedAt: old, runs: [], reviews: [], reviewRequired: false, chain: false, grants: [],
  };
}

const LIST_PATHS = /\/(events|grants|mounts|memories|skills|mcp|requests|catalog)(\?|$)/;

async function mockBackend(page: Page, s: State): Promise<void> {
  const json = (route: Route, body: unknown, status = 200) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/*', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const p = url.pathname;
    if (!p.startsWith('/api/') && !p.startsWith('/graph') && !p.startsWith('/paperclip') && !p.startsWith('/v1/') && p !== '/healthz') {
      return route.continue();
    }
    if (p === '/api/v1/agents') {
      if (s.agentsDown) return json(route, { error: { code: 'unavailable', message: 'daemon down' } }, 502);
      return json(route, ['ag_a', 'ag_b'].map((id, i) => ({
        id, name: i ? 'beta' : 'alpha', display_name: i ? 'Beta' : 'Alpha', type: 'opencode', image: 'x',
        state: s.agentStates[id], created_at: now, updated_at: now,
      })));
    }
    if (p === '/api/v1/activity') return json(route, s.activity);
    if (p === '/api/v1/conflicts') return json(route, s.conflicts);
    if (p === '/api/v1/live') return json(route, { items: s.live, now: new Date().toISOString() });
    if (p === '/api/v1/workspaces') return json(route, [{ id: 'ws1', name: 'proj', host_path: '/srv/proj', created_at: now }]);
    if (p === '/api/v1/grants') return json(route, s.grants);
    if (p === '/api/v1/pipelines/admission') {
      return json(route, { revision: 1, admissions: [{
        agent_id: 'ag_b', agent: 'beta', allowed: s.allowed, action: s.allowed ? 'allow' : 'rework', revision: 1, checked_at: now,
        edges: [{ edge_id: 'pe-1', gate_id: 'tests', upstream_agent_id: 'ag_a', workspace: '/ws', satisfied: s.allowed,
          code: s.code, run_id: 'gate-42', run_finished_at: s.finishedAt, action: s.allowed ? 'allow' : 'rework',
          message: s.code === 'ok' ? '' : `gate ${s.code}` }],
      }] });
    }
    if (p === '/graph' && req.method() === 'GET') {
      const contract = JSON.stringify({ gate_id: 'tests', command: 'npm test', on_true: 'allow', on_false: 'rework', review_required: s.reviewRequired });
      const connectors: unknown[] = [{ id: 'conn-1', agentId: 'ag_a', targetAgentId: 'ag_b', kind: 'gate', name: 'Tests', direction: 'out', enabled: true, contract }];
      if (s.chain) connectors.push({ id: 'ch-1', agentId: 'ag_a', targetAgentId: 'ag_b', kind: 'agent-chain', name: 'handoff', direction: 'out', enabled: true });
      return json(route, { saved: true, revision: 1, pipeline_bindings: { 'conn-1': 'pe-1' },
        body: { version: 1, revision: 1, connectors, paperclip: { workspace: '/ws', syncGates: true } } });
    }
    if (p === '/graph' && req.method() === 'PUT') {
      const body = req.postDataJSON();
      return json(route, { saved: true, body, revision: (body.revision ?? 1) + 1, pipeline_bindings: { 'conn-1': 'pe-1' }, pipeline_sync: { pushed: true, edges: 1 } });
    }
    if (p === '/graph/gates/status') return json(route, { enabled: true, exists: true, managed: true });
    if (p === '/api/v1/gates/runs') return json(route, { runs: s.runs });
    if (p === '/api/v1/gates/reviews') return json(route, { reviews: s.reviews });
    if (p.startsWith('/paperclip/')) return json(route, { issues: [] });
    if (LIST_PATHS.test(p + url.search)) return json(route, []);
    return json(route, {});
  });
}

async function openGraph(page: Page, mode: 'Organisation' | 'Ressourcen' = 'Organisation'): Promise<void> {
  await page.goto('/');
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('f-flow').waitFor({ state: 'visible', timeout: 15000 });
  if (mode === 'Ressourcen') {
    // Switch only after the saved document is applied, or it resets the mode.
    await expect(page.locator('.graph-save-status')).toContainText('gespeichert');
    await page.getByRole('button', { name: 'Ressourcen', exact: true }).click();
  }
}

const node = (page: Page, id: string) => page.locator(`[data-node-id="${id}"]`);
const proceedEdge = (page: Page) => page.locator('f-connection[data-kind="proceed"]');
const act = (id: string, activity: string, cpu = 1, workspace = '') =>
  ({ agent_id: id, state: 'running', activity, cpu_percent: cpu, memory_bytes: 0, processes: 1, available: true, workspace, timestamp: now });

test('running but idle agents do not pulse; only observed work is green', async ({ page }) => {
  const s = base();
  s.activity = [act('ag_a', 'IDLE', 0), act('ag_b', 'WRITING', 3)];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'agent-ag_b')).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'agent-ag_b')).toHaveAttribute('title', /aktiv: arbeitet \(writing, cpu 3%\)/);
  await expect(node(page, 'agent-ag_b').locator('.live-dot')).toHaveText('●');
  await expect(node(page, 'agent-ag_a')).not.toHaveAttribute('data-live', /.+/);
});

test('unavailable monitor data is never shown as green', async ({ page }) => {
  const s = base();
  s.activity = [{ ...act('ag_a', 'WRITING', 90), available: false, unavailable_reason: 'no procfs' }];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'agent-ag_b')).toBeVisible();
  await expect(node(page, 'agent-ag_a')).not.toHaveAttribute('data-live', /.+/);
});

test('endpoint job on a released edge lights the pipeline flow gate → agent', async ({ page }) => {
  const s = base();
  s.live = [{ id: '1', kind: 'endpoint_job', agent_id: 'ag_b', issue_id: 'ISS-7', started_at: now }];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(proceedEdge(page)).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'agent-ag_b')).toHaveAttribute('title', /Endpoint-Auftrag läuft .*Issue ISS-7.*Pipeline-Fluss: Beta arbeitet nach Gate „Tests“/);
  await expect(page.locator('.edge-label.edge-proceed')).toHaveAttribute('title', /Pipeline-Fluss/);
});

test('a gate run in flight lights the checked agent and its gate', async ({ page }) => {
  const s = base();
  s.live = [{ id: '2', kind: 'gate_run', upstream_agent_id: 'ag_a', workspace: '/ws', started_at: now }];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('title', /Gate „Tests“ prüft die Arbeit von Alpha/);
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('data-live', 'active');
});

test('a gate that released just now pulses; an old release does not', async ({ page }) => {
  const s = base();
  s.finishedAt = new Date().toISOString();
  await mockBackend(page, s);
  await openGraph(page);
  await expect(proceedEdge(page)).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'agent-ag_b')).toHaveAttribute('title', /soeben freigegeben/);
});

test('two active agents on a chain edge are talking', async ({ page }) => {
  const s = base();
  s.chain = true;
  s.activity = [act('ag_a', 'READING'), act('ag_b', 'WRITING')];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(page.locator('f-connection[data-kind="chain"]')).toHaveAttribute('data-live', 'active');
  await expect(page.locator('.edge-label.edge-chain')).toHaveAttribute('title', /Alpha und Beta arbeiten gleichzeitig/);
});

test('shared workspace lights both grant edges and the workspace node', async ({ page }) => {
  const s = base();
  s.activity = [act('ag_a', 'WRITING'), act('ag_b', 'READING')];
  s.grants = [
    { id: 'g1', workspace_id: 'ws1', agent_id: 'ag_a', mode: 'rw', exclusive: false, status: 'active', created_at: now },
    { id: 'g2', workspace_id: 'ws1', agent_id: 'ag_b', mode: 'ro', exclusive: false, status: 'active', created_at: now },
  ];
  await mockBackend(page, s);
  await openGraph(page, 'Ressourcen');
  await expect(node(page, 'workspace-ws1')).toHaveAttribute('data-live', 'active');
  await expect(node(page, 'workspace-ws1')).toHaveAttribute('title', /gemeinsamer Workspace proj: Alpha, Beta/);
  await expect(page.locator('f-connection[data-live="active"][data-kind="rw"]')).toHaveCount(1);
  await expect(page.locator('f-connection[data-live="active"][data-kind="ro"]')).toHaveCount(1);
});

for (const [code, tone] of [['stale', 'err1'], ['no_run', 'err1'], ['failed', 'err2'], ['error', 'err2'], ['tamper', 'err3']] as const) {
  test(`admission ${code} marks the downstream agent ${tone}`, async ({ page }) => {
    const s = base();
    s.allowed = false;
    s.code = code;
    // Even a running job does not make a blocked agent green: the worst state wins.
    s.activity = [act('ag_b', 'WRITING', 50)];
    await mockBackend(page, s);
    await openGraph(page);
    await expect(proceedEdge(page)).toHaveAttribute('data-live', tone);
    await expect(node(page, 'agent-ag_b')).toHaveAttribute('data-live', tone);
    await expect(node(page, 'agent-ag_b')).toHaveAttribute('title', new RegExp(`blockiert durch Gate „Tests“: ${code}`));
  });
}

test('an active override is a yellow hint on the edge, not an error', async ({ page }) => {
  const s = base();
  s.code = 'override';
  await mockBackend(page, s);
  await openGraph(page);
  await expect(proceedEdge(page)).toHaveAttribute('data-live', 'override');
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('data-live', 'override');
  await expect(node(page, 'agent-ag_b')).not.toHaveAttribute('data-live', /err/);
});

test('a file conflict blinks hard on both agents and their chain edge', async ({ page }) => {
  const s = base();
  s.chain = true;
  s.activity = [act('ag_a', 'WRITING'), act('ag_b', 'WRITING')];
  s.conflicts = [{ path: '/srv/proj/main.go', agent_ids: ['ag_a', 'ag_b'], writers: ['ag_a', 'ag_b'], detected_at: now }];
  await mockBackend(page, s);
  await openGraph(page);
  for (const id of ['agent-ag_a', 'agent-ag_b']) {
    await expect(node(page, id)).toHaveAttribute('data-live', 'err3');
    await expect(node(page, id)).toHaveAttribute('title', /Dateikonflikt \/srv\/proj\/main.go \(schreibend: Alpha, Beta\)/);
  }
  await expect(page.locator('f-connection[data-kind="chain"]')).toHaveAttribute('data-live', 'err3');
  await expect(node(page, 'agent-ag_a').locator('.live-dot')).toHaveText('✖');
  // Hard blink: the fastest rhythm.
  const duration = await node(page, 'agent-ag_a').evaluate(el => getComputedStyle(el).animationDuration);
  expect(duration).toBe('0.4s');
});

test('starting and stopping agents are blue transitions', async ({ page }) => {
  const s = base();
  s.agentStates = { ag_a: 'restarting', ag_b: 'removing' };
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('data-live', 'transition');
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('title', /startet/);
  await expect(node(page, 'agent-ag_b')).toHaveAttribute('title', /stoppt/);
});

test('review pending is a yellow hint on the gate', async ({ page }) => {
  const s = base();
  s.reviewRequired = true;
  s.runs = [{ id: 'r1', workspace: '/ws', issue_id: 'ISS-9', status: 'passed', gates: 1, passed: 1, duration_ms: 5, upstream_agent_id: 'ag_a', started_at: now, finished_at: now }];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('data-live', 'review');
  await expect(node(page, 'connector-conn-1')).toHaveAttribute('title', /Review für Issue ISS-9 ausstehend/);
});

test('an unreachable daemon turns agents red (unknown), never green', async ({ page }) => {
  const s = base();
  s.activity = [act('ag_a', 'WRITING', 80)];
  await mockBackend(page, s);
  await openGraph(page);
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('data-live', 'active');
  s.agentsDown = true;
  // The shell refreshes every 5 s.
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('data-live', 'err2', { timeout: 12000 });
  await expect(node(page, 'agent-ag_a')).toHaveAttribute('title', /Daemon nicht erreichbar/);
});

test('reduced motion keeps colour and symbol but stops the animation', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const s = base();
  s.allowed = false;
  s.code = 'tamper';
  await mockBackend(page, s);
  await openGraph(page);
  const b = node(page, 'agent-ag_b');
  await expect(b).toHaveAttribute('data-live', 'err3');
  expect(await b.evaluate(el => getComputedStyle(el).animationName)).toBe('none');
  await expect(b.locator('.live-dot')).toHaveText('✖');
  expect(await proceedEdge(page).locator('.f-connection-path').first().evaluate(el => getComputedStyle(el).animationName)).toBe('none');
});
