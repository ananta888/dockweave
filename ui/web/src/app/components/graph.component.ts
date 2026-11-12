import { Component, Input, ViewChild, computed, output } from '@angular/core';
import { FCanvasComponent, FFlowModule, FMinimapComponent } from '@foblex/flow';
import { Agent, AgentActivity, AgentPaths, BrokerGrant, Grant, Workspace } from '../core/models';

interface GraphNode {
  id: string;
  kind: 'agent' | 'workspace' | 'memory' | 'credential' | 'knowledge';
  title: string;
  subtitle: string;
  detail: string;
  position: { x: number; y: number };
  state?: string;
}

interface GraphEdge {
  id: string;
  source: string;
  target: string;
  label: string;
  kind: string;
}

@Component({
  selector: 'dw-graph',
  standalone: true,
  imports: [FFlowModule, FMinimapComponent],
  template: `
    <div class="graph-view">
      <div class="graph-toolbar">
        <div>
          <strong>Agent graph</strong>
          <span class="muted">agents · workspaces · memory · credentials · knowledge</span>
        </div>
        <span class="spacer"></span>
        <button class="small" (click)="fit()">Fit</button>
      </div>
      <f-flow fDraggable fFlowId="dockweave-agent-graph" (fFullRendered)="fit()">
        <f-canvas fZoom [fZoomMinimum]="0.2" [fZoomMaximum]="2">
          @for (edge of edges(); track edge.id) {
            <f-connection
              [fConnectionId]="edge.id"
              [fSourceId]="edge.source + ':out'"
              [fTargetId]="edge.target + ':in'"
              fType="bezier"
              [fSelectionDisabled]="true"
              [fReassignDisabled]="true"
              [attr.data-kind]="edge.kind">
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
              [class.selected]="selectedAgentId === 'agent-' + agentIdOf(node)"
              (click)="nodeClicked(node.id)">
              <div class="node-head">
                <span class="kind">{{ node.kind }}</span>
                @if (node.state) {
                  <span class="state" [class.on]="node.state === 'running'" [class.err]="node.state === 'error'">{{ node.state }}</span>
                }
              </div>
              <div class="node-title">{{ node.title }}</div>
              <div class="node-sub">{{ node.subtitle }}</div>
              @if (node.detail) { <div class="node-detail">{{ node.detail }}</div> }
              <span fConnector fConnectorType="target" [fConnectorId]="node.id + ':in'" fConnectorConnectableSide="left"></span>
              <span fConnector fConnectorType="source" [fConnectorId]="node.id + ':out'" fConnectorConnectableSide="right"></span>
            </div>
          }
        </f-canvas>
        <f-minimap />
      </f-flow>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .graph-view { display:flex; flex-direction:column; height:100%; min-height:0; background:#0b0e14; }
    .graph-toolbar { display:flex; align-items:center; gap:.6rem; padding:.55rem .8rem; background:#161b22; border-bottom:1px solid #30363d; font-size:.82rem; }
    .graph-toolbar .muted { color:#8b949e; margin-left:.5rem; }
    .spacer { flex:1; }
    f-flow { flex:1; min-height:0; }
    .gw-node { position:relative; width:250px; padding:.6rem .7rem; border-radius:9px; background:#161b22; border:1px solid #30363d; color:#c9d1d9; cursor:pointer; box-shadow:0 4px 16px #0008; }
    .gw-node.selected { border-color:#58a6ff; box-shadow:0 0 0 2px #58a6ff55, 0 4px 16px #0008; }
    .gw-node.kind-agent { border-left:4px solid #58a6ff; }
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
    .edge-label { font-size:.65rem; padding:.1rem .35rem; border-radius:999px; background:#0d1117; border:1px solid #30363d; color:#8b949e; white-space:nowrap; }
    .edge-label.edge-rw { color:#3fb950; border-color:#238636; }
    .edge-label.edge-ro { color:#d29922; border-color:#9e6a03; }
    .edge-label.edge-memory { color:#bc8cff; border-color:#8957e5; }
    .edge-label.edge-credential { color:#d29922; border-color:#9e6a03; }
    .edge-label.edge-knowledge { color:#39c5cf; border-color:#1f6feb; }
    .small { padding:.2rem .5rem; font-size:.75rem; }
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

  @ViewChild(FCanvasComponent) private canvas?: FCanvasComponent;

  readonly nodes = computed(() => this.buildNodes());
  readonly edges = computed(() => this.buildEdges());

  fit(): void {
    setTimeout(() => this.canvas?.fitToScreen({ x: 60, y: 60 }, true), 60);
  }

  nodeClicked(id: string): void {
    if (id.startsWith('agent-')) this.agentSelected.emit(id.slice('agent-'.length));
  }

  agentIdOf(n: GraphNode): string {
    return n.kind === 'agent' ? n.id.slice('agent-'.length) : '';
  }

  private buildNodes(): GraphNode[] {
    const nodes: GraphNode[] = [];
    const sortedAgents = [...this.agents].sort((a, b) => a.name.localeCompare(b.name));
    sortedAgents.forEach((a, i) => {
      const p = this.paths[a.id];
      const act = this.activity.find(x => x.agent_id === a.id);
      nodes.push({
        id: `agent-${a.id}`, kind: 'agent', title: a.name,
        subtitle: `${a.type} · ${a.image}`,
        detail: `egress ${a.network_profile || 'unrestricted-legacy'}${act?.available ? ` · cpu ${act.cpu_percent.toFixed(0)}%` : ''}`,
        position: { x: 0, y: i * 190 }, state: a.state,
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
    const edges: GraphEdge[] = [];
    const agentNameToId = new Map(this.agents.map(a => [a.name, a.id]));
    const wsById = new Map(this.workspaces.map(w => [w.id, w]));
    for (const g of this.grants) {
      if (g.status === 'revoked' || g.status === 'unmount_failed') continue;
      const ws = wsById.get(g.workspace_id);
      if (!ws) continue;
      edges.push({
        id: `grant-${g.id}`, source: `agent-${g.agent_id}`, target: `workspace-${ws.id}`,
        label: g.mode, kind: g.mode === 'rw' ? 'rw' : 'ro',
      });
    }
    for (const a of this.agents) {
      const p = this.paths[a.id];
      if (p?.memory_id) {
        edges.push({ id: `memory-edge-${a.id}`, source: `agent-${a.id}`, target: `memory-${a.id}`, label: 'memory', kind: 'memory' });
      }
      if (p?.knowledge_dir) {
        edges.push({ id: `knowledge-edge-${a.id}`, source: `agent-${a.id}`, target: 'knowledge', label: 'knowledge', kind: 'knowledge' });
      }
    }
    for (const g of this.brokerGrants) {
      const agentId = agentNameToId.get(g.agent);
      if (!agentId) continue;
      edges.push({
        id: `credential-edge-${g.id}`, source: `agent-${agentId}`, target: `credential-${g.id}`,
        label: 'credential', kind: 'credential',
      });
    }
    return edges;
  }
}
