import { test, expect, Page, Route } from '@playwright/test';

// Ticket view (ADR 0003 D-7) against a fully mocked daemon API.

const now = new Date().toISOString();
const agents = [
  { id: 'ag_a', name: 'alpha', display_name: 'Alpha', type: 'opencode', image: 'x', state: 'running', created_at: now, updated_at: now },
  { id: 'ag_b', name: 'beta', display_name: 'Beta', type: 'opencode', image: 'x', state: 'running', created_at: now, updated_at: now },
];
const ALL = ['ticket.read', 'ticket.create', 'ticket.edit', 'ticket.comment', 'ticket.assign', 'ticket.approve', 'agent.wake', 'sync.run'];

interface Mock {
  configured: boolean;
  provider: string;
  threads: boolean;
  healthy: boolean;
  permissions: string[];
  tickets: Record<string, unknown>[];
  comments: Record<string, unknown>[];
  posts: { path: string; body: unknown }[];
  live: unknown[];
  mirror: boolean;
}

function mock(over: Partial<Mock> = {}): Mock {
  return {
    configured: true, provider: 'native', threads: true, healthy: true, permissions: ALL, mirror: false,
    tickets: [
      { ref: { provider: 'native', id: 'tk_1' }, identifier: 'DW-1', title: 'Login reparieren', description: 'Bug', status: 'in_progress',
        priority: 'high', assignee: { kind: 'agent', id: 'ag_b' }, created_at: now, updated_at: now, version: '3' },
      { ref: { provider: 'native', id: 'tk_2' }, identifier: 'DW-2', title: 'Doku schreiben', status: 'todo', created_at: now, updated_at: now, version: '1' },
    ],
    comments: [{ id: 'c1', ticket: { provider: 'native', id: 'tk_1' }, author: { kind: 'agent', id: 'ag_b' }, body: 'Bin dran\nSTATUS: IN_PROGRESS', created_at: now }],
    posts: [], live: [], ...over,
  };
}

const LIST = /\/(events|grants|mounts|memories|skills|mcp|requests|catalog|activity|conflicts|workspaces)(\?|$)/;

async function backend(page: Page, m: Mock): Promise<void> {
  const json = (r: Route, body: unknown, status = 200) => r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/*', async route => {
    const req = route.request();
    const url = new URL(req.url());
    const p = decodeURIComponent(url.pathname);
    if (!p.startsWith('/api/') && !p.startsWith('/graph') && !p.startsWith('/paperclip') && !p.startsWith('/v1/')) return route.continue();
    if (p === '/api/v1/agents') return json(route, agents);
    if (p === '/api/v1/live') return json(route, { items: m.live, now });
    if (p === '/api/v1/tickets/provider') {
      if (!m.configured) return json(route, { configured: false, available: ['native', 'paperclip'], error: 'ticketing is not configured: set TICKET_PROVIDER=native|paperclip in dockweaved.conf' });
      const caps = { threads: m.threads, inbox: true, children: true, approvals: false, wake: m.provider === 'paperclip', authorship: m.provider === 'native' };
      return json(route, { configured: true, available: ['native', 'paperclip'], primary: { name: m.provider, health: { ok: m.healthy, detail: m.healthy ? '' : 'connection refused', checked: now }, capabilities: caps },
        mirror: m.mirror ? { name: 'paperclip', health: { ok: true, checked: now }, capabilities: caps } : undefined });
    }
    if (p === '/api/v1/tickets/me') return json(route, { actor: { kind: 'human', id: 'iss|alice', name: 'alice' }, roles: ['author'], permissions: m.permissions, source: 'groups' });
    if (p === '/api/v1/tickets/sync') {
      if (req.method() === 'POST') { m.posts.push({ path: p, body: req.postDataJSON() }); return json(route, { report: {}, complete: true }); }
      return json(route, m.mirror ? { configured: true, status: { primary: 'native', mirror: 'paperclip', linked: 7, lag_seconds: 12, conflicts_total: 1 } } : { configured: false });
    }
    if (p === '/api/v1/tickets' && req.method() === 'GET') {
      if (!m.configured) return json(route, { error: { code: 'ticketing_unavailable', message: 'not configured' } }, 503);
      const q = url.searchParams.get('q') || '';
      return json(route, { provider: m.provider, tickets: m.tickets.filter(t => !q || String(t['title']).toLowerCase().includes(q.toLowerCase())) });
    }
    if (p === '/api/v1/tickets' && req.method() === 'POST') {
      const body = req.postDataJSON();
      m.posts.push({ path: p, body });
      const t = { ref: { provider: m.provider, id: 'tk_9' }, identifier: 'DW-9', status: 'todo', created_at: now, updated_at: now, version: '1', ...body };
      m.tickets.push(t);
      return json(route, t, 201);
    }
    if (/\/api\/v1\/tickets\/[^/]+\/comments$/.test(p)) {
      if (req.method() === 'POST') {
        const body = req.postDataJSON();
        m.posts.push({ path: p, body });
        m.comments.push({ id: 'c' + m.comments.length + 1, ticket: { provider: 'native', id: 'tk_1' }, author: { kind: 'human', id: 'iss|alice', name: 'alice' }, body: body.body, created_at: now });
        return json(route, m.comments[m.comments.length - 1], 201);
      }
      return json(route, { comments: m.comments });
    }
    if (/\/api\/v1\/tickets\/[^/]+$/.test(p) && req.method() === 'PATCH') {
      const body = req.postDataJSON();
      m.posts.push({ path: p, body });
      const t = m.tickets[0];
      Object.assign(t, body.status ? { status: body.status } : {}, body.title ? { title: body.title } : {}, { version: String(Number(t['version']) + 1) });
      return json(route, t);
    }
    if (p === '/api/v1/tickets/run') { m.posts.push({ path: p, body: req.postDataJSON() }); return json(route, { kind: 'ran', status: 'done' }); }
    if (p === '/api/v1/gates/runs') return json(route, { runs: [{ id: 'g1', workspace: '/ws', issue_id: 'native:tk_1', status: 'passed', gates: 2, passed: 2, duration_ms: 5, started_at: now }] });
    if (p === '/api/v1/gates/reviews') return json(route, { reviews: [] });
    if (p === '/api/v1/gates/approvals') { m.posts.push({ path: p, body: req.postDataJSON() }); return json(route, { actor: 'iss|alice', approved_at: now }); }
    if (p.startsWith('/api/v1/gates/approvals/')) return json(route, { error: { code: 'not_found', message: 'no approval' } }, 404);
    if (p.startsWith('/paperclip/')) return json(route, { issues: [] });
    if (p === '/graph') return json(route, { saved: false, body: null, revision: 0 });
    if (LIST.test(p + url.search)) return json(route, []);
    return json(route, {});
  });
}

async function openTickets(page: Page): Promise<void> {
  await page.goto('/');
  await page.getByRole('button', { name: 'Tickets', exact: true }).click();
  await page.locator('.tickets').waitFor();
}

test('unconfigured ticketing explains how to set it up', async ({ page }) => {
  await backend(page, mock({ configured: false }));
  await openTickets(page);
  await expect(page.locator('.unconfigured')).toContainText('Ticketing ist nicht eingerichtet');
  await expect(page.locator('.unconfigured')).toContainText('dockweave ticket provider detect');
  await expect(page.getByRole('button', { name: 'Neues Ticket' })).toHaveCount(0);
});

test('native provider: list, detail with thread, gate run and live work', async ({ page }) => {
  const m = mock({ live: [{ id: '1', kind: 'endpoint_job', agent_id: 'ag_b', issue_id: 'native:tk_1', started_at: now }] });
  await backend(page, m);
  await openTickets(page);
  await expect(page.locator('.provider')).toContainText('native · erreichbar');
  const row = page.locator('[data-ticket="native:tk_1"]');
  await expect(row).toContainText('Login reparieren');
  await expect(row.locator('.live')).toContainText('läuft');
  await row.click();
  const detail = page.locator('.detail');
  await expect(detail.locator('.comment')).toContainText('Bin dran');
  await expect(detail.locator('.comment .author')).toContainText('Beta (Agent)');
  await expect(detail.locator('.gate-run')).toContainText('passed');
  await expect(detail.locator('.live-chip')).toContainText('Agent arbeitet (Beta)');

  // Reply, status change and heartbeat go to the daemon API.
  await detail.locator('textarea[name="reply"]').fill('Bitte auch Tests');
  await detail.getByRole('button', { name: 'Senden' }).click();
  await expect(detail.locator('.comment')).toHaveCount(2);
  await detail.locator('select[name="detailStatus"]').selectOption('in_review');
  await expect.poll(() => m.posts.some(p => p.path === '/api/v1/tickets/native:tk_1' && (p.body as { status?: string; expected_version?: string }).status === 'in_review'
    && (p.body as { expected_version?: string }).expected_version === '3')).toBe(true);
  await detail.getByRole('button', { name: 'Heartbeat jetzt' }).click();
  await expect(page.locator('.notice').filter({ hasText: 'Heartbeat Beta: ran' })).toBeVisible();
  await detail.getByRole('button', { name: 'Freigeben' }).click();
  await expect.poll(() => m.posts.some(p => p.path === '/api/v1/gates/approvals' && (p.body as { issue_id: string }).issue_id === 'native:tk_1')).toBe(true);
});

test('creating a ticket assigns an agent by id', async ({ page }) => {
  const m = mock();
  await backend(page, m);
  await openTickets(page);
  await page.getByRole('button', { name: 'Neues Ticket' }).click();
  await page.locator('input[name="newTitle"]').fill('Neues Feature');
  await page.locator('select[name="newAssignee"]').selectOption('ag_a');
  await page.getByRole('button', { name: 'Anlegen' }).click();
  await expect(page.locator('[data-ticket="native:tk_9"]')).toContainText('Neues Feature');
  const post = m.posts.find(p => p.path === '/api/v1/tickets') as { body: { title: string; assignee: { kind: string; id: string } } };
  expect(post.body.title).toBe('Neues Feature');
  expect(post.body.assignee).toEqual({ kind: 'agent', id: 'ag_a' });
});

test('a reader sees tickets but no actions', async ({ page }) => {
  await backend(page, mock({ permissions: ['ticket.read'] }));
  await openTickets(page);
  await page.locator('[data-ticket="native:tk_1"]').click();
  const detail = page.locator('.detail');
  await expect(detail.locator('.comment')).toContainText('Bin dran');
  await expect(page.getByRole('button', { name: 'Neues Ticket' })).toHaveCount(0);
  await expect(detail.locator('textarea[name="reply"]')).toHaveCount(0);
  await expect(detail.getByRole('button', { name: 'Freigeben' })).toHaveCount(0);
  await expect(detail.getByRole('button', { name: 'Heartbeat jetzt' })).toHaveCount(0);
  await expect(detail.locator('select[name="detailStatus"]')).toBeDisabled();
  await expect(detail.locator('input.title-edit')).toHaveCount(0);
});

test('capabilities hide what the provider cannot do; paperclip keys and mirror status', async ({ page }) => {
  const m = mock({ provider: 'paperclip', threads: false, healthy: false, mirror: true,
    tickets: [{ ref: { provider: 'paperclip', id: 'uuid-1' }, identifier: 'PC-12', title: 'Paperclip-Issue', status: 'todo', created_at: now, updated_at: now, version: 'v1',
      assignee: { kind: 'agent', id: 'ag_a' } }],
    live: [{ id: '2', kind: 'endpoint_job', agent_id: 'ag_a', issue_id: 'uuid-1', started_at: now }] });
  await backend(page, m);
  await openTickets(page);
  await expect(page.locator('.provider')).toHaveClass(/bad/);
  await expect(page.locator('.provider')).toContainText('nicht erreichbar');
  await expect(page.locator('.mirror')).toContainText('Spiegel paperclip');
  await expect(page.locator('.sync')).toContainText('7 verknüpft');
  // The bare Paperclip id is the live-work key (dispatch.IssueKey).
  await expect(page.locator('[data-ticket="paperclip:uuid-1"] .live')).toBeVisible();
  await page.locator('[data-ticket="paperclip:uuid-1"]').click();
  await expect(page.locator('.detail h4', { hasText: 'Verlauf' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Sync jetzt' }).click();
  await expect.poll(() => m.posts.some(p => p.path === '/api/v1/tickets/sync')).toBe(true);
});

test('board view groups by status', async ({ page }) => {
  await backend(page, mock());
  await openTickets(page);
  await page.getByRole('button', { name: 'Board' }).click();
  await expect(page.locator('[data-col="in_progress"] [data-ticket="native:tk_1"]')).toBeVisible();
  await expect(page.locator('[data-col="todo"] [data-ticket="native:tk_2"]')).toBeVisible();
});
