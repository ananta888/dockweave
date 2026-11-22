// Live status of the 2D graph: which nodes and edges pulse (green = active /
// talking, yellow/blue/grey = hints, red = problems by severity).
//
// Pure function over real data only. Anything unknown is never shown as
// green: missing monitor data marks nothing, an unreachable daemon marks the
// agents "unknown" (red, medium).

import type { AgentActivity, FileConflict, Grant, LiveWork, Workspace } from '../core/models';
import type { Admission } from '../core/services/pipeline.service';
import type { GateReviewView, GateRunView } from '../core/services/gate.service';

export type LiveTone = 'unsynced' | 'transition' | 'review' | 'override' | 'active' | 'err1' | 'err2' | 'err3';

/** Severity order: the most severe tone wins when several apply. */
export const LIVE_RANK: Record<LiveTone, number> = {
  unsynced: 1, transition: 2, review: 3, override: 4, active: 5, err1: 6, err2: 7, err3: 8,
};

export const LIVE_TONE_TEXT: Record<LiveTone, string> = {
  active: 'aktiv',
  transition: 'Zustandswechsel',
  review: 'Review ausstehend',
  override: 'Override aktiv',
  unsynced: 'nicht synchronisiert',
  err1: 'Problem (leicht)',
  err2: 'Problem (mittel)',
  err3: 'Problem (schwer)',
};

export interface LiveMark { tone: LiveTone; reasons: string[] }

export interface LiveGraphNode { id: string; kind: string }
export interface LiveGraphEdge { id: string; source: string; target: string; kind: string }

export interface LiveGateConnector {
  id: string;
  /** Agent the gate checks (upstream). */
  agentId: string;
  /** Agent the gate releases (downstream). */
  targetAgentId?: string;
  name: string;
  reviewRequired: boolean;
}

export interface LiveInputs {
  now: number;
  daemonReachable: boolean;
  agents: { id: string; name: string; state: string }[];
  activity: AgentActivity[];
  conflicts: FileConflict[];
  /** null = live work unknown (older daemon or request failed). */
  live: LiveWork[] | null;
  workspaces: Workspace[];
  grants: Grant[];
  /** Pipeline status answered by the daemon for the current revision. */
  pipelineKnown: boolean;
  pipelineDrift: boolean;
  admissions: Admission[];
  bindings: Record<string, string>;
  gates: LiveGateConnector[];
  gateRuns: GateRunView[];
  reviews: GateReviewView[];
  nodes: LiveGraphNode[];
  edges: LiveGraphEdge[];
}

export interface LiveStatus {
  nodes: Map<string, LiveMark>;
  edges: Map<string, LiveMark>;
}

/** A proceed edge that released work within this window pulses green. */
export const RECENT_RELEASE_MS = 60_000;
/** CPU share that counts as working even without file evidence. */
export const ACTIVE_CPU_PERCENT = 10;

const SEVERE = new Set(['tamper']);
const MEDIUM = new Set(['failed', 'error', 'internal', 'unknown_gate', 'fingerprint_unavailable']);
const LIGHT = new Set(['no_run', 'stale', 'expired', 'denied']);

export function codeTone(code: string): LiveTone | null {
  if (SEVERE.has(code)) return 'err3';
  if (MEDIUM.has(code)) return 'err2';
  if (LIGHT.has(code)) return 'err1';
  if (code === 'override') return 'override';
  return null;
}

function mark(map: Map<string, LiveMark>, id: string, tone: LiveTone, reason: string): void {
  const cur = map.get(id);
  if (!cur) { map.set(id, { tone, reasons: [reason] }); return; }
  if (!cur.reasons.includes(reason)) cur.reasons.push(reason);
  if (LIVE_RANK[tone] > LIVE_RANK[cur.tone]) cur.tone = tone;
}

function within(path: string, root: string): boolean {
  if (!path || !root) return false;
  const r = root.endsWith('/') ? root.slice(0, -1) : root;
  return path === r || path.startsWith(r + '/');
}

function since(now: number, iso: string): string {
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  return s < 60 ? `seit ${s}s` : `seit ${Math.round(s / 60)}min`;
}

export function computeLiveStatus(i: LiveInputs): LiveStatus {
  const nodes = new Map<string, LiveMark>();
  const edges = new Map<string, LiveMark>();
  const nodeIds = new Set(i.nodes.map(n => n.id));
  const name = (id: string) => i.agents.find(a => a.id === id)?.name || id;
  const agentNode = (id: string) => `agent-${id}`;
  const nodeMark = (id: string, tone: LiveTone, reason: string) => { if (nodeIds.has(id)) mark(nodes, id, tone, reason); };
  const edgeIds = new Set(i.edges.map(e => e.id));
  const edgeMark = (id: string, tone: LiveTone, reason: string) => { if (edgeIds.has(id)) mark(edges, id, tone, reason); };

  // ---- agents: own state and observed activity ----
  const active = new Set<string>();
  for (const a of i.agents) {
    const id = agentNode(a.id);
    if (!i.daemonReachable) { nodeMark(id, 'err2', 'Daemon nicht erreichbar – Zustand unbekannt'); continue; }
    if (a.state === 'created' || a.state === 'restarting') nodeMark(id, 'transition', `startet (${a.state})`);
    else if (a.state === 'removing') nodeMark(id, 'transition', 'stoppt (removing)');
    else if (a.state === 'dead' || a.state === 'error') nodeMark(id, 'err2', `Container-Zustand ${a.state}`);
    const act = i.activity.find(x => x.agent_id === a.id);
    if (act?.available && a.state === 'running') {
      const busy = act.activity === 'READING' || act.activity === 'WRITING' || act.cpu_percent >= ACTIVE_CPU_PERCENT;
      if (busy) {
        active.add(a.id);
        nodeMark(id, 'active', `arbeitet (${act.activity.toLowerCase()}, cpu ${act.cpu_percent.toFixed(0)}%)`);
      }
    }
  }
  if (!i.daemonReachable) return { nodes, edges };

  // ---- endpoint jobs and gate runs in flight ----
  const jobs = new Map<string, LiveWork>();
  for (const w of i.live ?? []) {
    if (w.kind === 'endpoint_job' && w.agent_id) {
      jobs.set(w.agent_id, w);
      active.add(w.agent_id);
      nodeMark(agentNode(w.agent_id), 'active', `Endpoint-Auftrag läuft ${since(i.now, w.started_at)}${w.issue_id ? ` (Issue ${w.issue_id})` : ''}`.trim());
    }
    if (w.kind === 'gate_run' && w.upstream_agent_id) {
      for (const g of i.gates.filter(x => x.agentId === w.upstream_agent_id)) {
        const reason = `Gate „${g.name}“ prüft die Arbeit von ${name(g.agentId)} ${since(i.now, w.started_at)}`.trim();
        nodeMark(`connector-${g.id}`, 'active', reason);
        edgeMark(`conn-${g.id}`, 'active', reason);
        nodeMark(agentNode(g.agentId), 'active', reason);
      }
    }
  }

  // ---- pipeline: gate -> agent edges ----
  const gated = new Set(i.gates.filter(g => g.targetAgentId).map(g => g.targetAgentId as string));
  for (const g of i.gates) {
    if (!g.targetAgentId) continue;
    const proceed = `proceed-${g.id}`;
    const gateNode = `connector-${g.id}`;
    const target = agentNode(g.targetAgentId);
    if (!i.pipelineKnown) {
      edgeMark(proceed, 'err2', 'Pipeline-Status unbekannt');
      nodeMark(target, 'err2', `Pipeline-Status unbekannt – Freigabe von ${name(g.targetAgentId)} ungewiss`);
      continue;
    }
    if (i.pipelineDrift) {
      edgeMark(proceed, 'unsynced', 'Graph und erzwungene Pipeline weichen ab');
      nodeMark(target, 'unsynced', 'Pipeline nicht synchronisiert');
      continue;
    }
    const binding = i.bindings[g.id];
    const adm = i.admissions.find(a => a.agent_id === g.targetAgentId);
    const edge = binding ? adm?.edges.find(e => e.edge_id === binding) : undefined;
    if (!edge) { edgeMark(proceed, 'unsynced', 'Kante im Daemon noch nicht bekannt'); continue; }
    const tone = codeTone(edge.code);
    if (tone) {
      const reason = `Gate „${g.name}“: ${edge.code}${edge.message ? ` – ${edge.message}` : ''}`;
      edgeMark(proceed, tone, reason);
      if (tone !== 'override') {
        nodeMark(target, tone, `blockiert durch ${reason}`);
        if (tone === 'err3' || tone === 'err2') nodeMark(gateNode, tone, reason);
      } else {
        nodeMark(gateNode, 'override', reason);
      }
    }
    if (edge.satisfied && adm?.allowed) {
      // Flow: the downstream agent works on a released edge, or the edge
      // released work just now.
      const finished = edge.run_finished_at ? Date.parse(edge.run_finished_at) : NaN;
      const fresh = Number.isFinite(finished) && i.now - finished >= 0 && i.now - finished <= RECENT_RELEASE_MS;
      const job = jobs.get(g.targetAgentId);
      if (job || fresh) {
        const reason = job
          ? `Pipeline-Fluss: ${name(g.targetAgentId)} arbeitet nach Gate „${g.name}“`
          : `Gate „${g.name}“ hat ${name(g.targetAgentId)} soeben freigegeben`;
        edgeMark(proceed, 'active', reason);
        nodeMark(gateNode, 'active', reason);
        nodeMark(target, 'active', reason);
      }
    }
  }
  for (const id of gated) {
    const adm = i.admissions.find(a => a.agent_id === id);
    if (i.pipelineKnown && !i.pipelineDrift && adm && !adm.allowed && !adm.edges.some(e => !e.satisfied)) {
      nodeMark(agentNode(id), 'err1', 'Pipeline blockiert');
    }
  }

  // ---- review pending: last passed run of the checked agent has no verdict ----
  for (const g of i.gates.filter(x => x.reviewRequired)) {
    const runs = i.gateRuns.filter(r => r.upstream_agent_id === g.agentId)
      .sort((a, b) => (Date.parse(b.finished_at || b.started_at || '') || 0) - (Date.parse(a.finished_at || a.started_at || '') || 0));
    const last = runs[0];
    if (last?.status === 'passed' && last.issue_id && !i.reviews.some(rv => rv.issue_id === last.issue_id)) {
      nodeMark(`connector-${g.id}`, 'review', `Review für Issue ${last.issue_id} ausstehend`);
    }
  }

  // ---- shared workspace: active agents granted the same workspace ----
  const wsAgents = new Map<string, string[]>();
  for (const gr of i.grants) {
    if (gr.status && gr.status !== 'active') continue;
    if (!active.has(gr.agent_id)) continue;
    const list = wsAgents.get(gr.workspace_id) ?? [];
    if (!list.includes(gr.agent_id)) list.push(gr.agent_id);
    wsAgents.set(gr.workspace_id, list);
  }
  // Same observed project path without a grant edge in this view.
  const byPath = new Map<string, string[]>();
  for (const id of active) {
    const p = i.activity.find(x => x.agent_id === id)?.workspace;
    if (!p) continue;
    byPath.set(p, [...(byPath.get(p) ?? []), id]);
  }
  const sharing = new Map<string, Set<string>>();
  const pair = (a: string, b: string) => {
    sharing.set(a, (sharing.get(a) ?? new Set()).add(b));
    sharing.set(b, (sharing.get(b) ?? new Set()).add(a));
  };
  for (const [wsId, ids] of wsAgents) {
    if (ids.length < 2) continue;
    const ws = i.workspaces.find(w => w.id === wsId);
    const reason = `gemeinsamer Workspace ${ws?.name || wsId}: ${ids.map(name).join(', ')}`;
    nodeMark(`workspace-${wsId}`, 'active', reason);
    for (const gr of i.grants.filter(g => g.workspace_id === wsId && ids.includes(g.agent_id))) edgeMark(`grant-${gr.id}`, 'active', reason);
    for (const a of ids) { nodeMark(agentNode(a), 'active', reason); for (const b of ids) if (a !== b) pair(a, b); }
  }
  for (const [p, ids] of byPath) {
    if (ids.length < 2) continue;
    for (const a of ids) { nodeMark(agentNode(a), 'active', `gemeinsamer Workspace ${p}`); for (const b of ids) if (a !== b) pair(a, b); }
  }

  // ---- agent <-> agent edges: both ends active = talking ----
  for (const e of i.edges) {
    if (!e.source.startsWith('agent-') || !e.target.startsWith('agent-')) continue;
    const a = e.source.slice(6), b = e.target.slice(6);
    if (active.has(a) && active.has(b)) {
      edgeMark(e.id, 'active', sharing.get(a)?.has(b)
        ? `${name(a)} und ${name(b)} arbeiten im selben Workspace`
        : `${name(a)} und ${name(b)} arbeiten gleichzeitig`);
    }
  }

  // ---- file conflicts: the most severe signal ----
  for (const c of i.conflicts) {
    const writers = c.writers?.length ? ` (schreibend: ${c.writers.map(name).join(', ')})` : '';
    const reason = `Dateikonflikt ${c.path}${writers}`;
    for (const id of c.agent_ids) nodeMark(agentNode(id), 'err3', reason);
    const ws = i.workspaces.find(w => within(c.path, w.host_path));
    if (ws) {
      nodeMark(`workspace-${ws.id}`, 'err3', reason);
      for (const gr of i.grants.filter(g => g.workspace_id === ws.id && c.agent_ids.includes(g.agent_id))) edgeMark(`grant-${gr.id}`, 'err3', reason);
    }
    for (const e of i.edges) {
      if (!e.source.startsWith('agent-') || !e.target.startsWith('agent-')) continue;
      if (c.agent_ids.includes(e.source.slice(6)) && c.agent_ids.includes(e.target.slice(6))) edgeMark(e.id, 'err3', reason);
    }
  }
  return { nodes, edges };
}
