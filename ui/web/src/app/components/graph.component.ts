import { Component, Input, ViewChild, computed, inject, output, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { FCanvasComponent, FFlowModule, FMinimapComponent } from '@foblex/flow';
import { ApiService } from '../core/api.service';
import { Agent, AgentActivity, AgentPaths, BrokerGrant, Grant, Workspace } from '../core/models';

type GraphMode = 'organization' | 'resources';
type NodeKind = 'org' | 'unit' | 'agent' | 'workspace' | 'memory' | 'credential' | 'knowledge';

interface GraphNode {
  id: string;
  kind: NodeKind;
  title: string;
  subtitle: string;
  detail: string;
  position: { x: number; y: number };
  state?: string;
  role?: string;
  unit?: string;
}

interface GraphEdge {
  id: string;
  source: string;
  target: string;
  label: string;
  kind: string;
  sourceSide: 'top' | 'bottom' | 'left' | 'right';
  targetSide: 'top' | 'bottom' | 'left' | 'right';
  type: 'segment' | 'bezier' | 'adaptive-curve';
}

@Component({
  selector: 'dw-graph',
  standalone: true,
  imports: [FFlowModule, FMinimapComponent, FormsModule],
  template: `
    <div class="graph-view">
      <div class="graph-toolbar">
        <div class="mode-switch">
          <button [class.active]="mode() === 'organization'" (click)="setMode('organization')">Organisation</button>
          <button [class.active]="mode() === 'resources'" (click)="setMode('resources')">Ressourcen</button>
        </div>
        <span class="muted">{{ mode() === 'organization' ? 'units · roles · agents' : 'agents · workspaces · memory · credentials · knowledge' }}</span>
        <span class="spacer"></span>
        <button class="small" (click)="fit()">Fit</button>
      </div>

      <div class="graph-body">
        <f-flow fDraggable fFlowId="dockweave-agent-graph" (fFullRendered)="fit()">
          <f-canvas fZoom [fZoomMinimum]="0.2" [fZoomMaximum]="2">
            @for (edge of edges(); track edge.id) {
              <f-connection
                [fConnectionId]="edge.id"
                [fSourceId]="edge.source + ':out:' + edge.sourceSide"
                [fTargetId]="edge.target + ':in:' + edge.targetSide"
                [fSourceSide]="edge.sourceSide"
                [fTargetSide]="edge.targetSide"
                [fType]="edge.type"
                [fRadius]="14"
                [fSelectionDisabled]="true"
                [fReassignDisabled]="true"
                [attr.data-kind]="edge.kind">
                <svg fMarker type="end-all-states" [width]="10" [height]="10" [refX]="10" [refY]="5" markerUnits="userSpaceOnUse" viewBox="0 0 10 10">
                  <path d="M0 0 L10 5 L0 10 L2.5 5 Z" fill="context-stroke"></path>
                </svg>
                <div fConnectionContent [position]="0.5" class="edge-label" [class]="'edge-' + edge.kind">{{ edge.label }}</div>
              </f-connection>
            }
            @for (node of nodes(); track node.id) {
              <div
                fGroup
                fDragHandle
                [fGroupId]="node.id"
                [fGroupPosition]="node.position"
                class="gw-node"
                [class]="'kind-' + node.kind"
                [class.selected]="selectedAgentId === agentIdOf(node)"
                (click)="nodeClicked(node)">
                @if (node.kind === 'agent' && mode() === 'organization') {
                  <div class="figure" [class.on]="node.state === 'running'">
                    <svg viewBox="0 0 80 108" class="avatar" aria-hidden="true">
                      <circle cx="40" cy="22" r="16" [attr.fill]="avatarColor(node)"></circle>
                      <path d="M12 104 C12 69 25 51 40 51 C55 51 68 69 68 104 Z" [attr.fill]="avatarColor(node)"></path>
                      <circle cx="34" cy="20" r="2" fill="#0b0e14"></circle>
                      <circle cx="46" cy="20" r="2" fill="#0b0e14"></circle>
                      <path d="M34 28 Q40 33 46 28" stroke="#0b0e14" stroke-width="2" fill="none"></path>
                    </svg>
                    <div class="figure-name">{{ node.title }}</div>
                    <div class="role-badge">{{ node.role || 'no role' }}</div>
                    <div class="figure-sub">{{ node.state }}</div>
                  </div>
                } @else {
                  <div class="node-head">
                    <span class="kind">{{ node.kind }}</span>
                    @if (node.state) {
                      <span class="state" [class.on]="node.state === 'running'" [class.err]="node.state === 'error'">{{ node.state }}</span>
                    }
                  </div>
                  <div class="node-title">{{ node.title }}</div>
                  <div class="node-sub">{{ node.subtitle }}</div>
                  @if (node.detail) { <div class="node-detail">{{ node.detail }}</div> }
                  @if (node.role) { <div class="role-badge inline">{{ node.role }}</div> }
                }
                <span fConnector fConnectorType="target" [fConnectorId]="node.id + ':in:left'" fConnectorConnectableSide="left" class="gw-port"></span>
                <span fConnector fConnectorType="target" [fConnectorId]="node.id + ':in:top'" fConnectorConnectableSide="top" class="gw-port"></span>
                <span fConnector fConnectorType="source" [fConnectorId]="node.id + ':out:right'" fConnectorConnectableSide="right" class="gw-port"></span>
                <span fConnector fConnectorType="source" [fConnectorId]="node.id + ':out:bottom'" fConnectorConnectableSide="bottom" class="gw-port"></span>
                <span fConnector fConnectorType="target" [fConnectorId]="node.id + ':in:right'" fConnectorConnectableSide="right" class="gw-port"></span>
                <span fConnector fConnectorType="source" [fConnectorId]="node.id + ':out:top'" fConnectorConnectableSide="top" class="gw-port"></span>
              </div>
            }
          </f-canvas>
          <f-minimap />
        </f-flow>

        @if (editor(); as a) {
          <aside class="inspector">
            <header>
              <div>
                <div class="eyebrow">AGENT SETTINGS</div>
                <h3>{{ a.display_name || a.name }}</h3>
              </div>
              <button class="ghost small" (click)="editor.set(null)">✕</button>
            </header>
            <label>Display name
              <input [(ngModel)]="editDisplay" name="display" placeholder="{{ a.name }}" />
            </label>
            <label>Organization unit
              <input [(ngModel)]="editUnit" name="unit" list="unit-list" placeholder="e.g. Executive, Engineering" />
              <datalist id="unit-list">
                @for (u of unitSuggestions(); track u) { <option [value]="u"></option> }
              </datalist>
            </label>
            <label>Role
              <input [(ngModel)]="editRole" name="role" list="role-list" placeholder="e.g. CEO, CTO, Developer" />
              <datalist id="role-list">
                @for (r of roles; track r) { <option [value]="r"></option> }
              </datalist>
            </label>
            <div class="inspector-row">
              <span class="muted">{{ a.type }} · {{ a.state }}</span>
              <span class="spacer"></span>
              <button class="small" (click)="startAgent(a)" [disabled]="a.state === 'running' || busy()">Start</button>
              <button class="small" (click)="stopAgent(a)" [disabled]="a.state !== 'running' || busy()">Stop</button>
            </div>
            <div class="inspector-actions">
              <button class="primary" (click)="saveOrganization()" [disabled]="busy()">Save</button>
              <button class="ghost" (click)="openAgent(a)">Open agent</button>
            </div>
            @if (message()) { <div class="notice">{{ message() }}</div> }
            @if (error()) { <div class="notice error">{{ error() }}</div> }
          </aside>
        }
      </div>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .graph-view { display:flex; flex-direction:column; height:100%; min-height:0; background:#0b0e14; }
    .graph-toolbar { display:flex; align-items:center; gap:.6rem; padding:.5rem .8rem; background:#161b22; border-bottom:1px solid #30363d; font-size:.8rem; }
    .mode-switch { display:flex; gap:.2rem; }
    .mode-switch button { background:transparent; border:1px solid #30363d; color:#8b949e; padding:.3rem .65rem; border-radius:6px; cursor:pointer; font-size:.78rem; }
    .mode-switch button.active { border-color:#58a6ff; color:#58a6ff; background:#1f6feb1a; }
    .muted { color:#8b949e; }
    .spacer { flex:1; }
    .graph-body { flex:1; min-height:0; display:flex; }
    f-flow { flex:1; min-height:0; }
    .gw-node { position:relative; width:250px; padding:.6rem .7rem; border-radius:9px; background:#161b22; border:1px solid #30363d; color:#c9d1d9; cursor:pointer; box-shadow:0 4px 16px #0008; box-sizing:border-box; }
    .gw-node.selected { border-color:#58a6ff; box-shadow:0 0 0 2px #58a6ff55, 0 4px 16px #0008; }
    .gw-node.kind-agent { padding:0; width:170px; background:transparent; border:none; box-shadow:none; }
    .gw-node.kind-org { border-left:4px solid #58a6ff; background:#11263b; }
    .gw-node.kind-unit { border-left:4px solid #3fb950; background:#12261a; }
    .gw-node.kind-workspace { border-left:4px solid #3fb950; }
    .gw-node.kind-memory { border-left:4px solid #bc8cff; }
    .gw-node.kind-credential { border-left:4px solid #d29922; }
    .gw-node.kind-knowledge { border-left:4px solid #39c5cf; }
    .node-head { display:flex; align-items:center; justify-content:space-between; gap:.4rem; }
    .kind { font-size:.62rem; text-transform:uppercase; letter-spacing:.08em; color:#8b949e; }
    .state { font-size:.62rem; border-radius:999px; padding:.05rem .4rem; background:#21262d; color:#8b949e; }
    .state.on { color:#3fb950; background:#23863633; }
    .state.err { color:#f85149; background:#da363333; }
    .node-title { font-weight:700; font-size:.9rem; margin-top:.25rem; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .node-sub { font-size:.72rem; color:#8b949e; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .node-detail { font-size:.68rem; color:#79c0ff; font-family:ui-monospace, monospace; margin-top:.2rem; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .role-badge { display:inline-block; font-size:.62rem; color:#58a6ff; background:#1f6feb22; border:1px solid #1f6feb66; border-radius:999px; padding:.05rem .4rem; margin-top:.25rem; }
    .role-badge.inline { margin-top:.2rem; }
    .figure { display:flex; flex-direction:column; align-items:center; padding:.4rem; border-radius:12px; border:1px solid #30363d; background:#161b22; transition:border-color .15s, box-shadow .15s; }
    .figure.on { border-color:#3fb950; box-shadow:0 0 0 2px #3fb95033; }
    .figure.selected { border-color:#58a6ff; }
    .avatar { width:70px; height:94px; }
    .figure-name { font-weight:700; font-size:.8rem; margin-top:.1rem; max-width:150px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .figure-sub { font-size:.62rem; color:#8b949e; text-transform:uppercase; }
    .edge-label { font-size:.65rem; padding:.1rem .35rem; border-radius:999px; background:#0d1117; border:1px solid #30363d; color:#8b949e; white-space:nowrap; }
    .edge-label.edge-rw { color:#3fb950; border-color:#238636; }
    .edge-label.edge-ro { color:#d29922; border-color:#9e6a03; }
    .edge-label.edge-memory { color:#bc8cff; border-color:#8957e5; }
    .edge-label.edge-credential { color:#d29922; border-color:#9e6a03; }
    .edge-label.edge-knowledge { color:#39c5cf; border-color:#1f6feb; }
    .edge-label.edge-unit { color:#3fb950; border-color:#238636; }
    .edge-label.edge-org { color:#58a6ff; border-color:#1f6feb; }
    .gw-port { width:7px; height:7px; border-radius:50%; background:#58a6ff88; border:1px solid #58a6ff; opacity:0; transition:opacity .15s; }
    .gw-node:hover .gw-port, .gw-node.selected .gw-port { opacity:1; }
    .small { padding:.2rem .5rem; font-size:.75rem; }
    .inspector { width:300px; border-left:1px solid #30363d; background:#161b22; padding:.9rem; display:flex; flex-direction:column; gap:.65rem; overflow:auto; }
    .inspector header { display:flex; align-items:flex-start; gap:.5rem; }
    .inspector h3 { margin:.1rem 0; font-size:1rem; flex:1; }
    .eyebrow { font-size:.62rem; letter-spacing:.08em; color:#8b949e; }
    .inspector label { display:flex; flex-direction:column; gap:.25rem; font-size:.75rem; color:#8b949e; }
    .inspector-row { display:flex; align-items:center; gap:.4rem; }
    .inspector-actions { display:flex; gap:.5rem; }
    .notice { padding:.45rem .6rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; font-size:.78rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .ghost { background:transparent; border:1px solid #30363d; color:#c9d1d9; }
  `],
})
export class GraphComponent {
  @Input() agents: Agent[] = [];
  @Input() activity: AgentActivity[] = [];
  @Input() workspaces: Workspace[] = [];
  @Input() grants: Grant[] = [];
  @Input() brokerGrants: BrokerGrant[] = [];
  @Input() paths: Record<string, AgentPaths> = {};
  @Input() selectedAgentId: string | null = null;

  readonly agentSelected = output<string>();
  readonly changed = output<void>();

  mode = signal<GraphMode>('organization');
  editor = signal<Agent | null>(null);
  orgOverrides = signal<Record<string, { display_name: string; org_unit: string; role: string }>>(this.loadOverrides());
  busy = signal(false);
  message = signal('');
  error = signal('');

  editDisplay = '';
  editUnit = '';
  editRole = '';

  readonly roles = ['CEO', 'CTO', 'COO', 'CFO', 'Head of Engineering', 'Lead', 'Developer', 'Reviewer', 'QA', 'DevOps', 'Researcher', 'Operations'];

  @ViewChild(FCanvasComponent) private canvas?: FCanvasComponent;
  private readonly api = inject(ApiService);

  readonly nodes = computed(() => this.buildNodes());
  readonly edges = computed(() => this.buildEdges());
  readonly unitSuggestions = computed(() => {
    const units = new Set(this.agents.map(a => a.org_unit).filter(Boolean) as string[]);
    ['Executive', 'Engineering', 'Quality', 'Operations', 'Research'].forEach(u => units.add(u));
    return [...units].sort();
  });

  setMode(mode: GraphMode): void {
    this.mode.set(mode);
    this.editor.set(null);
    setTimeout(() => this.fit(), 100);
  }

  fit(): void {
    setTimeout(() => this.canvas?.fitToScreen({ x: 60, y: 60 }, true), 60);
  }

  nodeClicked(node: GraphNode): void {
    if (node.kind !== 'agent') return;
    const raw = this.agents.find(x => x.id === this.agentIdOf(node));
    if (!raw) return;
    const a = this.effective(raw);
    this.editor.set(a);
    this.editDisplay = a.display_name || '';
    this.editUnit = a.org_unit || '';
    this.editRole = a.role || '';
    this.message.set('');
    this.error.set('');
  }

  agentIdOf(node: GraphNode): string {
    return node.kind === 'agent' ? node.id.slice('agent-'.length) : '';
  }

  avatarColor(node: GraphNode): string {
    if (node.state === 'running') return '#3fb950';
    if (node.state === 'error') return '#f85149';
    if (node.role) {
      const r = node.role.toLowerCase();
      if (r.includes('ceo') || r.includes('cto') || r.includes('coo') || r.includes('cfo') || r.includes('head')) return '#d29922';
      if (r.includes('lead')) return '#58a6ff';
      if (r.includes('review')) return '#bc8cff';
      if (r.includes('qa')) return '#39c5cf';
    }
    return '#8b949e';
  }

  saveOrganization(): void {
    const a = this.editor();
    if (!a) return;
    this.busy.set(true);
    this.error.set('');
    this.api.setAgentOrganization(a.id, this.editDisplay, this.editUnit, this.editRole).subscribe({
      next: updated => {
        this.busy.set(false);
        this.message.set('Settings saved');
        const next = { ...this.orgOverrides() };
        delete next[a.id];
        this.orgOverrides.set(next);
        this.persistOverrides(next);
        this.editor.set(updated);
        this.changed.emit();
      },
      error: () => {
        // The running daemon may predate the organization endpoint. Keep the
        // graph fully usable by persisting the metadata in this browser; the
        // API path takes over automatically once the daemon is updated.
        const next = {
          ...this.orgOverrides(),
          [a.id]: { display_name: this.editDisplay, org_unit: this.editUnit, role: this.editRole },
        };
        this.orgOverrides.set(next);
        this.persistOverrides(next);
        this.busy.set(false);
        this.message.set('Saved locally (daemon update pending)');
        this.editor.set({ ...a, ...next[a.id] });
        this.changed.emit();
      },
    });
  }

  startAgent(a: Agent): void {
    this.busy.set(true);
    this.api.startAgent(a.id).subscribe({
      next: () => { this.busy.set(false); this.changed.emit(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  stopAgent(a: Agent): void {
    this.busy.set(true);
    this.api.stopAgent(a.id).subscribe({
      next: () => { this.busy.set(false); this.changed.emit(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  openAgent(a: Agent): void {
    this.agentSelected.emit(a.id);
  }

  private effective(a: Agent): Agent {
    const o = this.orgOverrides()[a.id];
    return o ? { ...a, ...o } : a;
  }

  private loadOverrides(): Record<string, { display_name: string; org_unit: string; role: string }> {
    try {
      return JSON.parse(localStorage.getItem('dockweave.orgOverrides') || '{}');
    } catch {
      return {};
    }
  }

  private persistOverrides(v: Record<string, { display_name: string; org_unit: string; role: string }>): void {
    try { localStorage.setItem('dockweave.orgOverrides', JSON.stringify(v)); } catch { /* ignore */ }
  }

  private buildNodes(): GraphNode[] {
    return this.mode() === 'organization' ? this.buildOrgNodes() : this.buildResourceNodes();
  }

  private buildOrgNodes(): GraphNode[] {
    const nodes: GraphNode[] = [];
    const source = this.agents.map(a => this.effective(a));
    const sorted = [...source].sort((a, b) => (a.org_unit || 'Unassigned').localeCompare(b.org_unit || 'Unassigned') || a.name.localeCompare(b.name));
    const units = [...new Set(sorted.map(a => a.org_unit || 'Unassigned'))];
    const unitX = new Map<string, number>();
    units.forEach((u, i) => unitX.set(u, i * 380));
    const center = ((units.length - 1) * 380) / 2;
    nodes.push({
      id: 'org-root', kind: 'org', title: 'Organization', subtitle: 'DockWeave',
      detail: `${sorted.length} agents · ${units.length} units`, position: { x: center, y: 0 },
    });
    units.forEach(u => {
      const members = sorted.filter(a => (a.org_unit || 'Unassigned') === u);
      nodes.push({
        id: `unit-${slug(u)}`, kind: 'unit', title: u, subtitle: 'organizational unit',
        detail: `${members.length} agent${members.length === 1 ? '' : 's'}`, position: { x: unitX.get(u)!, y: 170 },
      });
      members.forEach((a, j) => {
        const act = this.activity.find(x => x.agent_id === a.id);
        nodes.push({
          id: `agent-${a.id}`, kind: 'agent', title: a.display_name || a.name,
          subtitle: `${a.type} · ${a.state}`, detail: act?.available ? `cpu ${act.cpu_percent.toFixed(0)}%` : '',
          position: { x: unitX.get(u)! + 105, y: 300 + j * 230 },
          state: a.state, role: a.role, unit: a.org_unit,
        });
      });
    });
    return nodes;
  }

  private buildResourceNodes(): GraphNode[] {
    const nodes: GraphNode[] = [];
    const source = this.agents.map(a => this.effective(a));
    const sortedAgents = [...source].sort((a, b) => a.name.localeCompare(b.name));
    sortedAgents.forEach((a, i) => {
      const p = this.paths[a.id];
      const act = this.activity.find(x => x.agent_id === a.id);
      nodes.push({
        id: `agent-${a.id}`, kind: 'agent', title: a.display_name || a.name,
        subtitle: `${a.type} · ${a.image}`,
        detail: `egress ${a.network_profile || 'unrestricted-legacy'}${act?.available ? ` · cpu ${act.cpu_percent.toFixed(0)}%` : ''}`,
        position: { x: 0, y: i * 190 }, state: a.state, role: a.role, unit: a.org_unit,
      });
      if (p?.memory_id) {
        nodes.push({
          id: `memory-${a.id}`, kind: 'memory', title: p.memory_id,
          subtitle: 'memory store', detail: p.memory_dir,
          position: { x: 430, y: i * 150 },
        });
      }
    });
    const sortedWs = [...this.workspaces].sort((a, b) => a.name.localeCompare(b.name));
    sortedWs.forEach((w, i) => nodes.push({
      id: `workspace-${w.id}`, kind: 'workspace', title: w.name,
      subtitle: 'workspace', detail: w.host_path,
      position: { x: -430, y: i * 150 },
    }));
    let ci = 0;
    for (const g of this.brokerGrants) {
      nodes.push({
        id: `credential-${g.id}`, kind: 'credential', title: g.credential,
        subtitle: `${g.provider} · ${g.agent}`,
        detail: (g.models || []).join(', ') || 'all models',
        position: { x: 860, y: ci++ * 130 },
      });
    }
    const knowledge = Object.values(this.paths).find(p => p.knowledge_dir)?.knowledge_dir;
    if (knowledge) {
      nodes.push({
        id: 'knowledge', kind: 'knowledge', title: '/knowledge',
        subtitle: 'shared knowledge store', detail: knowledge,
        position: { x: 430, y: Math.max(1, sortedAgents.length) * 150 + 40 },
      });
    }
    return nodes;
  }

  private buildEdges(): GraphEdge[] {
    return this.mode() === 'organization' ? this.buildOrgEdges() : this.buildResourceEdges();
  }

  private buildOrgEdges(): GraphEdge[] {
    const edges: GraphEdge[] = [];
    const units = [...new Set(this.agents.map(a => this.effective(a).org_unit || 'Unassigned'))];
    for (const u of units) {
      edges.push({ id: `org-unit-${slug(u)}`, source: 'org-root', target: `unit-${slug(u)}`, label: 'unit', kind: 'org', sourceSide: 'bottom', targetSide: 'top', type: 'segment' });
      for (const a of this.agents.map(x => this.effective(x)).filter(x => (x.org_unit || 'Unassigned') === u)) {
        edges.push({
          id: `unit-agent-${a.id}`, source: `unit-${slug(u)}`, target: `agent-${a.id}`,
          label: a.role || 'member', kind: 'unit', sourceSide: 'bottom', targetSide: 'top', type: 'segment',
        });
      }
    }
    return edges;
  }

  private buildResourceEdges(): GraphEdge[] {
    const edges: GraphEdge[] = [];
    const agentNameToId = new Map(this.agents.map(a => [a.name, a.id]));
    const wsById = new Map(this.workspaces.map(w => [w.id, w]));
    for (const g of this.grants) {
      if (g.status === 'revoked' || g.status === 'unmount_failed') continue;
      const ws = wsById.get(g.workspace_id);
      if (!ws) continue;
      edges.push({ id: `grant-${g.id}`, source: `agent-${g.agent_id}`, target: `workspace-${ws.id}`, label: g.mode, kind: g.mode === 'rw' ? 'rw' : 'ro', sourceSide: 'left', targetSide: 'right', type: 'bezier' });
    }
    for (const a of this.agents) {
      const p = this.paths[a.id];
      if (p?.memory_id) edges.push({ id: `memory-edge-${a.id}`, source: `agent-${a.id}`, target: `memory-${a.id}`, label: 'memory', kind: 'memory', sourceSide: 'right', targetSide: 'left', type: 'bezier' });
      if (p?.knowledge_dir) edges.push({ id: `knowledge-edge-${a.id}`, source: `agent-${a.id}`, target: 'knowledge', label: 'knowledge', kind: 'knowledge', sourceSide: 'bottom', targetSide: 'top', type: 'bezier' });
    }
    for (const g of this.brokerGrants) {
      const agentId = agentNameToId.get(g.agent);
      if (!agentId) continue;
      edges.push({ id: `credential-edge-${g.id}`, source: `agent-${agentId}`, target: `credential-${g.id}`, label: 'credential', kind: 'credential', sourceSide: 'right', targetSide: 'left', type: 'bezier' });
    }
    return edges;
  }
}

function slug(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'unassigned';
}
