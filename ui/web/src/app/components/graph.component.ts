import { Component, Input, OnChanges, SimpleChanges, ViewChild, computed, inject, output, signal } from '@angular/core';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { FormsModule } from '@angular/forms';
import { FCanvasComponent, FFlowModule, FMinimapComponent } from '@foblex/flow';
import { ApiService } from '../core/api.service';
import { GateSyncStatus, GraphGateSyncResult, GraphService } from '../core/services/graph.service';
import { GateRunView, GateService } from '../core/services/gate.service';
import { PaperclipService } from '../core/services/paperclip.service';
import { BuiltinFigure, FIGURES, figureSvg, sanitizeSvg } from '../core/figures';
import { ConnectionType, DEFAULT_GRAPH_SCHEMA, GraphSchema, McpServerContract, McpToolContract, NodePorts, Side } from '../core/graph-schema';
import { LayoutEdge, LayoutNode, borderPoint, optimizeLayout } from '../core/graph-layout-optimizer';
import { RouterRect, routeAroundNodes } from '../core/graph-edge-router';
import { Agent, AgentActivity, AgentMCP, AgentPaths, AgentSkill, BrokerGrant, Grant, Workspace } from '../core/models';

type GraphMode = 'organization' | 'resources';
type LayoutAlgorithm = 'hierarchy' | 'columns' | 'grid' | 'radial' | 'force' | 'stress' | 'star' | 'free' | 'mixed';

interface GraphPort {
  id: string;
  type: 'in' | 'out';
  side: Side;
  left: number;
  top: number;
}

interface LayoutTemplateNode {
  id: string;
  kind: NodeKind;
  title: string;
  position: { x: number; y: number };
}

interface LayoutTemplate {
  id: string;
  name: string;
  createdAt: string;
  nodes: LayoutTemplateNode[];
}

interface ConnectorConfig {
  id: string;
  agentId: string;
  kind: 'mcp' | 'skill' | 'gate' | 'http' | 'agent-chain' | 'custom';
  name: string;
  targetAgentId?: string;
  url?: string;
  contract?: string;
  direction: 'out' | 'in' | 'both';
  enabled: boolean;
  draft?: boolean;
  connected?: boolean;
}

interface GhostNode {
  id: string;
  kind: NodeKind;
  title: string;
  position: { x: number; y: number };
  templateId: string;
  active: boolean;
}

interface CustomEdge {
  id: string;
  source: string;
  target: string;
  sourcePort: string;
  targetPort: string;
  label: string;
  kind: string;
  sourceSide: Side;
  targetSide: Side;
}
type NodeKind = 'org' | 'unit' | 'agent' | 'workspace' | 'memory' | 'credential' | 'knowledge' | 'ghost' | 'mcp' | 'skill' | 'gate' | 'http' | 'custom';

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
  ghost?: boolean;
  active?: boolean;
  templateId?: string;
  avatar?: string;
  avatarSvg?: string;
}

interface GraphEdge {
  id: string;
  source: string;
  target: string;
  sourcePort: string;
  targetPort: string;
  label: string;
  kind: string;
  sourceSide: Side;
  targetSide: Side;
  type: 'segment' | 'straight' | 'bezier' | 'adaptive-curve';
  /** Intermediate points used to steer the edge around unrelated nodes. */
  waypoints?: { x: number; y: number }[];
}

@Component({
  selector: 'dw-graph',
  standalone: true,
  imports: [FFlowModule, FMinimapComponent, FormsModule],
  template: `
    <div class="graph-view" (contextmenu)="openContext($event)" (click)="closeContext()">
      <div class="graph-toolbar">
        <div class="mode-switch">
          <button [class.active]="mode() === 'organization'" (click)="setMode('organization')">Organisation</button>
          <button [class.active]="mode() === 'resources'" (click)="setMode('resources')">Ressourcen</button>
        </div>
        <span class="muted">{{ mode() === 'organization' ? 'units · roles · agents' : 'agents · workspaces · memory · credentials · knowledge' }}</span>
        @if (gateCount() > 0) {
          <span class="muted" title="Deterministische Gates aus dem Graph-Dokument">Gates: {{ gateCount() }}</span>
        }
        @if (openReviews() > 0) {
          <span class="muted" title="Offene Review-Issues in Paperclip">offene Reviews: {{ openReviews() }}</span>
        }
        <span class="spacer"></span>
        <select class="small-select" [ngModel]="layout()" (ngModelChange)="setLayout($event)">
          <option value="mixed">Gemischt</option>
          <option value="force">Force (federnd)</option>
          <option value="stress">Stress (SMACOF)</option>
          <option value="star">Stern (Hubs)</option>
          <option value="hierarchy">Hierarchie (hart)</option>
          <option value="columns">Spalten (hart)</option>
          <option value="grid">Grid</option>
          <option value="radial">Radial</option>
          <option value="free">Frei (manuell)</option>
        </select>
        <button class="small" (click)="cleanup()">Aufräumen</button>
        <button class="small" (click)="openSettings()">Ports &amp; Schema</button>
        <button class="small" (click)="fit()">Fit</button>
      </div>

      <div class="graph-body" [class.drop-target]="draggingPalette()" (dragover)="onDragOver($event)" (drop)="onPaletteDrop($event)">
        @if (computing()) {
          <div class="layout-spinner">
            <span class="spinner"></span> Layout wird berechnet …
            <button class="spinner-cancel" (click)="cancelLayout()">Abbrechen</button>
          </div>
        }
        @if (connectingSource(); as src) {
          <div class="connect-banner">
            Verbindungstyp <b>{{ connectingType() }}</b> · Ziel-Node anklicken (Quelle: {{ src.title }})
            <button class="small" (click)="cancelConnect()">Abbrechen</button>
          </div>
        }
        @if (selectedEdge(); as edge) {
          <div class="edge-toolbar" [style.left.px]="edgeToolbarPos()?.x ?? 100" [style.top.px]="edgeToolbarPos()?.y ?? 100">
            <span>Verbindung: <b>{{ edge.label }}</b> · {{ edge.kind }}</span>
            <button class="small danger" (click)="deleteEdge(edge)">Löschen</button>
            <button class="small" (click)="selectedEdge.set(null)">✕</button>
          </div>
        }
        <aside class="node-palette">
          <h4>Node types</h4>
          <button draggable="true" (dragstart)="onPaletteDragStart($event, 'mcp')" (dragend)="draggingPalette.set(null)" (click)="palette('mcp')"><b>MCP</b><span>Server + contract</span></button>
          <button draggable="true" (dragstart)="onPaletteDragStart($event, 'skill')" (dragend)="draggingPalette.set(null)" (click)="palette('skill')"><b>Skill</b><span>agent skill</span></button>
          <button class="gate" draggable="true" (dragstart)="onPaletteDragStart($event, 'gate')" (dragend)="draggingPalette.set(null)" (click)="palette('gate')"><b>Gate</b><span>deterministic</span></button>
          <button draggable="true" (dragstart)="onPaletteDragStart($event, 'http')" (dragend)="draggingPalette.set(null)" (click)="palette('http')"><b>HTTP</b><span>API endpoint</span></button>
          <button draggable="true" (dragstart)="onPaletteDragStart($event, 'agent-chain')" (dragend)="draggingPalette.set(null)" (click)="palette('agent-chain')"><b>Chain</b><span>agent → agent</span></button>
          <button draggable="true" (dragstart)="onPaletteDragStart($event, 'custom')" (dragend)="draggingPalette.set(null)" (click)="palette('custom')"><b>Custom</b><span>free node</span></button>
          <p class="muted">Workspace, memory, credential and knowledge come from the agent data.</p>
        </aside>
        <f-flow fDraggable fFlowId="dockweave-agent-graph" (fFullRendered)="fit()">
          <f-canvas fZoom [fZoomMinimum]="0.2" [fZoomMaximum]="2">
            @for (edge of edges(); track edge.id) {
              <f-connection
                [fConnectionId]="edge.id"
                [fSourceId]="edge.sourcePort"
                [fTargetId]="edge.targetPort"
                [fSourceSide]="edge.sourceSide"
                [fTargetSide]="edge.targetSide"
                [fType]="edge.type"
                [fRadius]="14"
                [fSelectionDisabled]="true"
                [fReassignDisabled]="true"
                [attr.data-kind]="edge.kind"
                [class.selected-edge]="selectedEdge()?.id === edge.id"
                (click)="$event.stopPropagation(); selectEdge(edge, $event)"
                (contextmenu)="openEdgeContext($event, edge)">
                <svg fMarker type="end-all-states" [width]="10" [height]="10" [refX]="10" [refY]="5" markerUnits="userSpaceOnUse" viewBox="0 0 10 10">
                  <path d="M0 0 L10 5 L0 10 L2.5 5 Z" fill="context-stroke"></path>
                </svg>
                @if (edge.waypoints?.length) {
                  <f-connection-waypoints [waypoints]="edge.waypoints || []" [visibility]="false" />
                }
                <div fConnectionContent [position]="0.5" class="edge-content">
                  <span class="edge-label" [class]="'edge-' + edge.kind">{{ edge.label }}</span>
                  <span class="edge-actions">
                    <button fDragBlocker type="button" title="Node auf Kante einfügen" (click)="$event.stopPropagation(); insertNodeOnEdge(edge)">＋</button>
                    <button fDragBlocker type="button" title="Verbindung löschen" (click)="$event.stopPropagation(); deleteEdge(edge)">×</button>
                  </span>
                </div>
              </f-connection>
            }
            @for (node of nodes(); track node.id) {
              <div
                fGroup
                fDragHandle
                [fGroupId]="node.id"
                [attr.data-node-id]="node.id"
                [fGroupPosition]="animatedPos(node)"
                (fGroupPositionChange)="moveNode(node.id, $event)"
                class="gw-node"
                [class]="'kind-' + node.kind"
                [class.selected]="selectedAgentId === agentIdOf(node)"
                [class.active]="node.kind === 'agent' && node.state === 'running'"
                [class.inactive]="node.kind === 'agent' && node.state !== 'running' && node.state !== 'error'"
                [class.error]="node.kind === 'agent' && node.state === 'error'"
                [class.main-agent]="node.kind === 'agent' && agentIdOf(node) === mainAgentId"
                (click)="handleNodeClick(node)"
                (dblclick)="nodeDoubleClicked(node)"
                (contextmenu)="openContext($event, node)">
                @if (node.kind === 'ghost') {
                  <div class="ghost-card" [class.active]="node.active">
                    <div class="ghost-kind">PLACEHOLDER</div>
                    <div class="ghost-title">{{ node.title }}</div>
                    <div class="ghost-sub">{{ node.active ? 'active · right-click to disable' : 'disabled · right-click to activate' }}</div>
                  </div>
                } @else if (node.kind === 'agent' && mode() === 'organization') {
                  <div class="figure" [class.on]="node.state === 'running'" [class.error]="node.state === 'error'">
                    <div class="avatar" [innerHTML]="figureHtml(node)"></div>
                    @if (agentIdOf(node) === mainAgentId) { <div class="main-badge">★ HAUPTAGENT</div> }
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
                  @if (node.kind === 'agent' && agentIdOf(node) === mainAgentId) { <div class="main-badge">★ HAUPTAGENT</div> }
                  <div class="node-title">{{ node.title }}</div>
                  <div class="node-sub">{{ node.subtitle }}</div>
                  @if (node.detail) { <div class="node-detail">{{ node.detail }}</div> }
                  @if (node.role) { <div class="role-badge inline">{{ node.role }}</div> }
                }
                @if (node.kind !== 'ghost') {
                  <span class="node-actions">
                    <button fDragBlocker type="button" title="Verbindung hinzufügen" (click)="$event.stopPropagation(); beginConnect(node)">＋</button>
                    <button fDragBlocker type="button" title="Node entfernen" (click)="$event.stopPropagation(); removeNode(node)">×</button>
                  </span>
                }
                @for (p of portsFor(node); track p.id) {
                  <span
                    fConnector
                    [fConnectorType]="p.type === 'out' ? 'source' : 'target'"
                    [fConnectorId]="p.id"
                    [fConnectorConnectableSide]="p.side"
                    class="gw-port"
                    [style.left.%]="p.left"
                    [style.top.%]="p.top"></span>
                }
              </div>
            }
          </f-canvas>
          <f-minimap class="graph-minimap" />
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
            <div class="avatar-picker">
              <div class="avatar-preview" [innerHTML]="avatarPreview()"></div>
              <div class="avatar-grid">
                @for (f of figures; track f.id) {
                  <button class="avatar-choice" [class.active]="editAvatar === f.id" (click)="chooseAvatar(f.id)" [title]="f.label">
                    <span class="avatar-mini" [innerHTML]="trustFigure(f.svg)"></span>
                    <span>{{ f.label }}</span>
                  </button>
                }
              </div>
              <div class="inspector-row">
                <label class="file-button">SVG importieren<input type="file" accept=".svg,image/svg+xml" (change)="importAvatar($event)" hidden /></label>
                <button class="small" (click)="saveAvatar()" [disabled]="busy()">Avatar speichern</button>
              </div>
              @if (avatarError()) { <div class="notice error">{{ avatarError() }}</div> }
            </div>
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

      @if (settingsOpen()) {
        <div class="settings-backdrop floating">
          <div class="settings-modal popover popover-settings" [style.left.px]="popoverPos()?.x ?? 100" [style.top.px]="popoverPos()?.y ?? 64" (click)="$event.stopPropagation()">
            <header>
              <h3>Graph settings &amp; schema</h3>
              <button class="ghost small" (click)="settingsOpen.set(false)">✕</button>
            </header>
            <div class="settings-body">
              <section>
                <h4>Layout &amp; cleanup</h4>
                <div class="settings-row">
                  <select [ngModel]="layout()" (ngModelChange)="setLayout($event)">
                    <option value="mixed">Gemischt (Organisation hart, Ressourcen frei)</option>
                    <option value="hierarchy">Hierarchie (hart)</option>
                    <option value="columns">Spalten (hart)</option>
                    <option value="grid">Grid</option>
                    <option value="radial">Radial</option>
                    <option value="force">Force (federnd)</option>
                    <option value="stress">Stress (SMACOF, überlappungsfrei)</option>
                    <option value="star">Stern (Hubs, Nachbarn im Kreis)</option>
                    <option value="free">Frei (manuell, Positionen bleiben)</option>
                  </select>
                  <button class="primary" (click)="cleanup()">Aufräumen</button>
                </div>
              </section>

              <section>
                <h4>Layout templates</h4>
                <div class="settings-row">
                  <input [(ngModel)]="newTemplateName" name="newTemplateName" placeholder="Template name" />
                  <button class="primary" (click)="saveTemplate(newTemplateName)">Save current layout</button>
                  <span class="muted">{{ layoutTemplates().length }} template(s) · {{ ghosts().length }} placeholder(s)</span>
                </div>
                @for (t of layoutTemplates(); track t.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ t.name }}</span>
                    <span class="muted">{{ t.nodes.length }} nodes</span>
                    <span class="spacer"></span>
                    <button class="small" (click)="applyTemplateById(t.id)">apply</button>
                    <button class="small danger" (click)="deleteTemplate(t.id)">delete</button>
                  </div>
                }
              </section>

              <section>
                <h4>Ports per node kind (in / out)</h4>
                <table class="ports-table">
                  <thead>
                    <tr><th>Kind</th>@for (side of sides; track side) { <th>{{ side }}</th> }</tr>
                  </thead>
                  <tbody>
                    @for (kind of nodeKinds(); track kind) {
                      <tr>
                        <td class="mono">{{ kind }}</td>
                        @for (side of sides; track side) {
                          <td class="port-cell">
                            <input type="number" min="0" max="12"
                              [ngModel]="portCount(kind, side, 'inputs')" [ngModelOptions]="{ standalone: true }"
                              (ngModelChange)="setPortCount(kind, side, 'inputs', $event)" />
                            <span>/</span>
                            <input type="number" min="0" max="12"
                              [ngModel]="portCount(kind, side, 'outputs')" [ngModelOptions]="{ standalone: true }"
                              (ngModelChange)="setPortCount(kind, side, 'outputs', $event)" />
                          </td>
                        }
                      </tr>
                    }
                  </tbody>
                </table>
              </section>

              <section>
                <h4>JSON schema contract</h4>
                <p class="muted hint">Template for ports, connection types and reusable connection templates. Export it as a contract or load your own JSON.</p>
                <textarea rows="10" [(ngModel)]="schemaJson" name="schemaJson"></textarea>
                <div class="settings-row">
                  <button class="primary" (click)="applySchemaJson()">Apply JSON</button>
                  <button (click)="resetSchema()">Reset default</button>
                  <button (click)="downloadSchema()">Download</button>
                  <label class="file-button">Load file<input type="file" accept="application/json,.json" (change)="loadSchemaFile($event)" hidden /></label>
                </div>
                @if (schemaError()) { <div class="notice error">{{ schemaError() }}</div> }
              </section>

              <section>
                <h4>Connection builder</h4>
                <div class="settings-grid">
                  <label>Template
                    <select [(ngModel)]="connTemplate" (ngModelChange)="applyConnectionTemplate()" name="connTemplate">
                      <option value="">— custom —</option>
                      @for (t of schema().templates; track t.id) { <option [value]="t.id">{{ t.name }}</option> }
                    </select>
                  </label>
                  <label>Type
                    <select [(ngModel)]="connType" name="connType">
                      @for (t of schema().connectionTypes; track t.id) { <option [value]="t.id">{{ t.label }} ({{ t.id }})</option> }
                    </select>
                  </label>
                  <label>Source node
                    <select [(ngModel)]="connSource" name="connSource" (ngModelChange)="connSourcePort = ''">
                      <option value="">— select —</option>
                      @for (n of nodes(); track n.id) { <option [value]="n.id">{{ n.title }} · {{ n.kind }}</option> }
                    </select>
                  </label>
                  <label>Source port
                    <select [(ngModel)]="connSourcePort" name="connSourcePort">
                      <option value="">— select —</option>
                      @for (p of portsForId(connSource, 'out'); track p.id) { <option [value]="p.id">{{ p.side }} #{{ $index + 1 }} (out)</option> }
                    </select>
                  </label>
                  <label>Target node
                    <select [(ngModel)]="connTarget" name="connTarget" (ngModelChange)="connTargetPort = ''">
                      <option value="">— select —</option>
                      @for (n of nodes(); track n.id) { <option [value]="n.id">{{ n.title }} · {{ n.kind }}</option> }
                    </select>
                  </label>
                  <label>Target port
                    <select [(ngModel)]="connTargetPort" name="connTargetPort">
                      <option value="">— select —</option>
                      @for (p of portsForId(connTarget, 'in'); track p.id) { <option [value]="p.id">{{ p.side }} #{{ $index + 1 }} (in)</option> }
                    </select>
                  </label>
                  <label class="wide">Label
                    <input [(ngModel)]="connLabel" name="connLabel" placeholder="rw, memory, custom…" />
                  </label>
                </div>
                <div class="settings-row">
                  <button class="primary" (click)="addCustomEdge()">Add connection</button>
                  <span class="muted">{{ customEdges().length }} custom connection(s)</span>
                </div>
                @for (e of customEdges(); track e.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ e.label }}</span>
                    <span class="muted">{{ e.source }} → {{ e.target }} · {{ e.kind }}</span>
                    <span class="spacer"></span>
                    <button class="small danger" (click)="deleteCustomEdge(e.id)">delete</button>
                  </div>
                }
                @if (schemaError()) { <div class="notice error">{{ schemaError() }}</div> }
              </section>
            </div>
          </div>
        </div>
      }

      @if (connectorsOpen()) {
        <div class="settings-backdrop floating">
          <div class="settings-modal popover popover-wide" [style.left.px]="popoverPos()?.x ?? 100" [style.top.px]="popoverPos()?.y ?? 100" (click)="$event.stopPropagation()">
            <header>
              <h3>Connectoren · {{ connectorAgent()?.display_name || connectorAgent()?.name }}</h3>
              <button class="ghost small" (click)="connectorsOpen.set(false)">✕</button>
            </header>
            <div class="settings-body">
              <section>
                <div class="settings-row">
                  <span class="muted">Projekte: <b>{{ agentProjects(connectorAgent()?.id || '').join(', ') || 'keine' }}</b></span>
                  <span class="spacer"></span>
                  <button class="primary small" (click)="applyDefaults()" [disabled]="configBusy()">Typ-Defaults übernehmen</button>
                </div>
              </section>
              <section>
                <h4>MCP Server (Datenbank)</h4>
                @for (m of mcpServers(); track m.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ m.name }}</span>
                    <span class="muted">{{ m.transport }}{{ m.url ? ' · ' + m.url : '' }}{{ m.contract ? ' · ' + contractSummary(m.contract) : '' }}</span>
                    <span class="spacer"></span>
                    <button class="small" (click)="toggleMcp(m)">{{ m.enabled ? 'disable' : 'enable' }}</button>
                    <button class="small danger" (click)="deleteMcp(m)">delete</button>
                  </div>
                }
                @if (!mcpServers().length) { <p class="muted">Noch keine MCP-Server für diesen Agenten.</p> }
                <div class="settings-grid" style="margin-top:.5rem">
                  <label>Name <input [(ngModel)]="mcpName" name="mcpName" placeholder="filesystem" /></label>
                  <label>Transport
                    <select [(ngModel)]="mcpTransport" name="mcpTransport">
                      <option value="stdio">stdio</option>
                      <option value="http">http</option>
                      <option value="sse">sse</option>
                    </select>
                  </label>
                  @if (mcpTransport === 'stdio') {
                    <label>Command <input [(ngModel)]="mcpCommand" name="mcpCommand" placeholder="npx" /></label>
                    <label class="wide">Args (one per line) <textarea rows="3" [(ngModel)]="mcpArgs" name="mcpArgs"></textarea></label>
                  } @else {
                    <label class="wide">URL <input [(ngModel)]="mcpURL" name="mcpURL" placeholder="http://127.0.0.1:3001/mcp" /></label>
                  }
                  <label class="wide">JSON contract
                    <textarea rows="5" [(ngModel)]="mcpContract" name="mcpContract" placeholder='{"name":"filesystem","transport":"stdio","tools":[...]}'></textarea>
                  </label>
                </div>
                <div class="settings-row">
                  <button class="primary" (click)="addMcpServer()" [disabled]="configBusy()">MCP Server hinzufügen</button>
                  <button (click)="validateContract()">Validate contract</button>
                </div>
                <label class="wide">MCP import JSON <span class="muted">(mcpServers, initialize or tools/list)</span>
                  <textarea rows="4" [(ngModel)]="mcpImportJson" name="mcpImportJson" placeholder='{"mcpServers": {...}} or {"result": {"tools": [...]}}'></textarea>
                </label>
                <div class="settings-row">
                  <button (click)="importMcpServers()">Import mcpServers</button>
                  <button (click)="importMcpResponse()">Import initialize / tools/list</button>
                </div>
              </section>

              <section>
                <h4>Skills (Datenbank)</h4>
                @for (sk of skills(); track sk.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ sk.name }}</span>
                    <span class="muted">{{ sk.description || sk.source_path }}</span>
                    <span class="spacer"></span>
                    <button class="small" (click)="toggleSkill(sk)">{{ sk.enabled ? 'disable' : 'enable' }}</button>
                    <button class="small danger" (click)="deleteSkill(sk)">delete</button>
                  </div>
                }
                @if (!skills().length) { <p class="muted">Noch keine Skills für diesen Agenten.</p> }
                <div class="settings-grid" style="margin-top:.5rem">
                  <label>Name <input [(ngModel)]="skillName" name="skillName" placeholder="code-review" /></label>
                  <label>Quelle <input [(ngModel)]="skillSource" name="skillSource" placeholder="/pfad/zum/skill" /></label>
                  <label class="wide">Beschreibung <input [(ngModel)]="skillDescription" name="skillDescription" /></label>
                  <label class="wide">Config JSON (optional) <textarea rows="3" [(ngModel)]="skillConfig" name="skillConfig"></textarea></label>
                </div>
                <div class="settings-row">
                  <button class="primary" (click)="addSkill()" [disabled]="configBusy()">Skill hinzufügen</button>
                </div>
              </section>

              <section>
                <h4>Paperclip-Sync (Gates)</h4>
                <p class="muted">Schreibt aktivierte Gate-Connectoren beim Speichern nach <span class="mono">&lt;workspace&gt;/.paperclip/gates.json</span>. Gates ohne Shell-Befehl werden nicht ausgeführt; Review/Human-Freigabe wirken über die Flags.</p>
                <div class="settings-grid">
                  <label class="wide">Workspace (Host-Pfad)
                    <input [ngModel]="paperclipWorkspace()" (ngModelChange)="setPaperclipWorkspace($event)" name="paperclipWorkspace" placeholder="/home/krusty/projects/crystal-run" />
                  </label>
                  <label>Bei jedem Speichern synchronisieren
                    <input type="checkbox" [ngModel]="paperclipSync()" (ngModelChange)="setPaperclipSync($event)" name="paperclipSync" />
                  </label>
                </div>
                @if (paperclipSyncInfo()) { <p class="muted mono">{{ paperclipSyncInfo() }}</p> }
                <p class="muted mono">{{ gateSyncLabel() }}</p>
              </section>

              <section>
                <h4>Gate-Health (Sandbox)</h4>
                <p class="muted">Letzte Läufe aus dem Daemon-Runner (<span class="mono">dockweave gate run</span>). Die Gates laufen in einem Wegwerf-Container, nicht auf dem Host.</p>
                @if (gateRunError()) { <p class="muted">{{ gateRunError() }}</p> }
                @for (run of gateRuns(); track run.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ run.status }}</span>
                    <span class="muted">{{ run.passed }}/{{ run.gates }} · {{ run.duration_ms }}ms · {{ gateRunWhen(run) }}{{ run.tamper ? ' · TAMPER' : '' }}</span>
                    <span class="spacer"></span>
                    @if (run.reason) { <span class="muted" [title]="run.reason">{{ run.reason }}</span> }
                  </div>
                }
                @if (!gateRuns().length && !gateRunError()) { <p class="muted">Noch keine Läufe.</p> }
              </section>

              <section>
                <h4>Weitere Connectoren (lokal)</h4>
                <div class="settings-grid">
                  <label>Art
                    <select [(ngModel)]="connKind" name="connKind">
                      <option value="gate">Deterministisches Gate</option>
                      <option value="http">HTTP API</option>
                      <option value="agent-chain">Agenten-Kette</option>
                      <option value="custom">Custom</option>
                    </select>
                  </label>
                  <label>Name <input [(ngModel)]="connName" name="connName" placeholder="z. B. review-gate" /></label>
                  <label>Richtung
                    <select [(ngModel)]="connDirection" name="connDirection">
                      <option value="out">Agent → Connector</option>
                      <option value="in">Connector → Agent</option>
                      <option value="both">beide</option>
                    </select>
                  </label>
                  @if (connKind === 'agent-chain') {
                    <label>Ziel-Agent
                      <select [(ngModel)]="connTargetAgent" name="connTargetAgent">
                        <option value="">— select —</option>
                        @for (a of agents; track a.id) { @if (a.id !== connectorAgent()?.id) { <option [value]="a.id">{{ a.display_name || a.name }}</option> } }
                      </select>
                    </label>
                  }
                  @if (connKind === 'http') {
                    <label class="wide">URL / Endpoint <input [(ngModel)]="connURL" name="connURL" placeholder="https://…" /></label>
                  }
                  @if (connKind === 'gate') {
                    <label class="wide">Condition / expression <input [(ngModel)]="gateExpression" name="gateExpression" placeholder="e.g. tests == green && budget < 5" /></label>
                    <label>On true <input [(ngModel)]="gateTrue" name="gateTrue" placeholder="allow / next agent" /></label>
                    <label>On false <input [(ngModel)]="gateFalse" name="gateFalse" placeholder="deny / retry" /></label>
                    <label class="wide">Shell-Befehl (optional) <input [(ngModel)]="gateCommand" name="gateCommand" placeholder="npm run build" /></label>
                    <label>Arbeitsverzeichnis <input [(ngModel)]="gateCwd" name="gateCwd" placeholder="." /></label>
                    <label>Timeout (s) <input type="number" [(ngModel)]="gateTimeout" name="gateTimeout" min="1" /></label>
                    <label><input type="checkbox" [(ngModel)]="gateReview" name="gateReview" /> Review erforderlich</label>
                    <label><input type="checkbox" [(ngModel)]="gateHuman" name="gateHuman" /> Menschliche Freigabe</label>
                  }
                  @if (connKind === 'custom') {
                    <label class="wide">JSON contract <textarea rows="4" [(ngModel)]="connContract" name="connContract"></textarea></label>
                  }
                </div>
                <div class="settings-row">
                  <button class="primary" (click)="addConnector()">Connector hinzufügen</button>
                </div>
              </section>

              <section>
                <h4>Lokale Connectoren</h4>
                @for (c of connectorsFor(connectorAgent()?.id || ''); track c.id) {
                  <div class="custom-edge">
                    <span class="mono">{{ c.name }}</span>
                    <span class="muted">{{ c.kind }} · {{ c.direction }}{{ c.targetAgentId ? ' · → ' + c.targetAgentId : '' }}{{ c.contract ? ' · ' + contractSummary(c.contract) : '' }}</span>
                    <span class="spacer"></span>
                    <button class="small" (click)="toggleConnector(c.id)">{{ c.enabled ? 'disable' : 'enable' }}</button>
                    <button class="small danger" (click)="deleteConnector(c.id)">delete</button>
                  </div>
                }
                @if (!connectorsFor(connectorAgent()?.id || '').length) { <p class="muted">Noch keine lokalen Connectoren.</p> }
                @if (configError()) { <div class="notice error">{{ configError() }}</div> }
                @if (schemaError()) { <div class="notice error">{{ schemaError() }}</div> }
              </section>
            </div>
          </div>
        </div>
      }

      @if (connectOpen()) {
        <div class="settings-backdrop floating">
          <div class="settings-modal popover" [style.left.px]="popoverPos()?.x ?? 100" [style.top.px]="popoverPos()?.y ?? 100" (click)="$event.stopPropagation()">
            <header>
              <h3>Neue Verbindung · {{ connectSource()?.title }}</h3>
              <button class="ghost small" (click)="connectOpen.set(false)">✕</button>
            </header>
            <div class="settings-body">
              <section>
                <label>Verbindungstyp
                  <select [(ngModel)]="connectType" name="connectType">
                    @for (t of schema().connectionTypes; track t.id) { <option [value]="t.id">{{ t.label }} ({{ t.id }})</option> }
                  </select>
                </label>
                <p class="muted">Nach „Weiter“ nur noch die andere Node anklicken.</p>
                <div class="settings-row">
                  <button class="primary" (click)="continueConnect()">Weiter</button>
                  <button (click)="connectOpen.set(false)">Abbrechen</button>
                </div>
                @if (connectError()) { <div class="notice error">{{ connectError() }}</div> }
              </section>
            </div>
          </div>
        </div>
      }

      @if (editConnectorId()) {
        <div class="settings-backdrop floating">
          <div class="settings-modal popover" [style.left.px]="popoverPos()?.x ?? 100" [style.top.px]="popoverPos()?.y ?? 100" (click)="$event.stopPropagation()">
            <header>
              <h3>Node konfigurieren</h3>
              <button class="ghost small" (click)="editConnectorId.set(null)">✕</button>
            </header>
            <div class="settings-body">
              <section>
                <div class="settings-grid">
                  <label>Name <input [(ngModel)]="ecName" name="ecName" /></label>
                  <label>Art
                    <select [(ngModel)]="ecKind" name="ecKind">
                      <option value="gate">Deterministisches Gate</option>
                      <option value="http">HTTP API</option>
                      <option value="agent-chain">Agenten-Kette</option>
                      <option value="custom">Custom</option>
                    </select>
                  </label>
                  <label>Richtung
                    <select [(ngModel)]="ecDirection" name="ecDirection">
                      <option value="out">Agent → Node</option>
                      <option value="in">Node → Agent</option>
                      <option value="both">beide</option>
                    </select>
                  </label>
                  @if (ecKind === 'agent-chain') {
                    <label>Ziel-Agent
                      <select [(ngModel)]="ecTarget" name="ecTarget">
                        <option value="">— select —</option>
                        @for (a of agents; track a.id) { <option [value]="a.id">{{ a.display_name || a.name }}</option> }
                      </select>
                    </label>
                  }
                  @if (ecKind === 'http') {
                    <label class="wide">URL <input [(ngModel)]="ecURL" name="ecURL" /></label>
                  }
                  @if (ecKind === 'gate') {
                    <label class="wide">Condition <input [(ngModel)]="ecExpression" name="ecExpression" placeholder="tests == green" /></label>
                    <label>On true <input [(ngModel)]="ecTrue" name="ecTrue" /></label>
                    <label>On false <input [(ngModel)]="ecFalse" name="ecFalse" /></label>
                    <label class="wide">Shell-Befehl (optional) <input [(ngModel)]="ecCommand" name="ecCommand" placeholder="npm run build" /></label>
                    <label>Arbeitsverzeichnis <input [(ngModel)]="ecCwd" name="ecCwd" placeholder="." /></label>
                    <label>Timeout (s) <input type="number" [(ngModel)]="ecTimeout" name="ecTimeout" min="1" /></label>
                    <label><input type="checkbox" [(ngModel)]="ecReview" name="ecReview" /> Review erforderlich</label>
                    <label><input type="checkbox" [(ngModel)]="ecHuman" name="ecHuman" /> Menschliche Freigabe</label>
                  }
                  @if (ecKind === 'custom') {
                    <label class="wide">JSON contract <textarea rows="5" [(ngModel)]="ecContract" name="ecContract"></textarea></label>
                  }
                </div>
                <div class="settings-row">
                  <button class="primary" (click)="saveLocalEditor()">Save</button>
                  <button class="danger" (click)="deleteLocalEdited()">Delete</button>
                  <button (click)="editConnectorId.set(null)">Cancel</button>
                </div>
              </section>
            </div>
          </div>
        </div>
      }

      @if (contextMenu(); as menu) {
        <div class="context-menu" [style.left.px]="menu.x" [style.top.px]="menu.y" (click)="$event.stopPropagation()" (contextmenu)="$event.preventDefault()">
          @if (menu.node; as n) {
            @if (n.kind === 'ghost') {
              <button (click)="toggleGhost(n.id)">{{ n.active ? 'Platzhalter deaktivieren' : 'Platzhalter aktivieren' }}</button>
              <button class="danger" (click)="removeGhost(n.id)">Platzhalter löschen</button>
            } @else {
              <button (click)="saveTemplate()">Layout als Template speichern…</button>
              <button (click)="resetNodePosition(n.id)">Position zurücksetzen</button>
              @if (n.kind === 'agent') {
                <button (click)="openAgentById(n.id)">Agent öffnen</button>
                <button (click)="openConnectorDialogByNode(n.id)">Connectoren konfigurieren…</button>
                <button (click)="mainAgentSelected.emit(agentIdOf(n)); closeContext()">Als Hauptagent festlegen</button>
              }
              <button (click)="beginConnect(n)">Neue Verbindung…</button>
              <button class="danger" (click)="deleteCustomEdgesFor(n.id)">Eigene Kanten löschen</button>
            }
          } @else {
            <button (click)="saveTemplate()">Layout als Template speichern…</button>
            <button (click)="cleanup()">Aufräumen</button>
          }
          @if (menu.edge; as e) {
            <div class="menu-sep"></div>
            <button class="danger" (click)="deleteEdge(e)">Verbindung löschen</button>
          }
          @if (layoutTemplates().length) {
            <div class="menu-sep"></div>
            @for (t of layoutTemplates(); track t.id) {
              <button (click)="applyTemplateById(t.id)">Template: {{ t.name }}</button>
            }
          }
        </div>
      }
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
    .graph-body.drop-target { outline:2px dashed #58a6ff; outline-offset:-4px; }
    .node-palette { width:150px; border-right:1px solid #30363d; background:#0f141b; padding:.6rem .5rem; display:flex; flex-direction:column; gap:.25rem; overflow:auto; }
    .node-palette h4 { margin:.1rem 0 .4rem; font-size:.65rem; letter-spacing:.08em; color:#8b949e; text-transform:uppercase; }
    .node-palette button { display:flex; flex-direction:column; align-items:flex-start; background:#161b22; border:1px solid #30363d; border-radius:6px; padding:.35rem .45rem; color:#c9d1d9; cursor:pointer; text-align:left; }
    .node-palette button:hover { border-color:#58a6ff; }
    .node-palette button.gate { border-left:3px solid #d29922; }
    .node-palette b { font-size:.75rem; }
    .node-palette span { font-size:.63rem; color:#8b949e; }
    .node-palette p { font-size:.62rem; color:#8b949e; margin:.5rem 0 0; }
    .connect-banner { position:absolute; top:8px; left:50%; transform:translateX(-50%); z-index:20; background:#11263b; border:1px solid #1f6feb; border-radius:7px; padding:.4rem .7rem; font-size:.75rem; display:flex; align-items:center; gap:.5rem; }
    .node-actions { position:absolute; right:-10px; top:50%; transform:translateY(-50%); display:flex; flex-direction:column; gap:3px; opacity:0; transition:opacity .15s; z-index:8; }
    .gw-node:hover .node-actions { opacity:1; }
    .node-actions button { width:22px; height:22px; border-radius:50%; border:1px solid #58a6ff; background:#0d1117; color:#58a6ff; cursor:pointer; font-size:.85rem; line-height:1; padding:0; }
    .node-actions button:hover { background:#58a6ff; color:#0b0e14; }
    .node-actions button:last-child { border-color:#f85149; color:#f85149; }
    .node-actions button:last-child:hover { background:#f85149; color:#fff; }
    .edge-content { display:flex; align-items:center; gap:4px; }
    .edge-actions { display:flex; gap:3px; opacity:0; transition:opacity .15s; }
    .f-connection:hover .edge-actions { opacity:1; }
    .edge-actions button { width:20px; height:20px; border-radius:50%; border:1px solid #58a6ff; background:#0d1117; color:#58a6ff; cursor:pointer; font-size:.8rem; line-height:1; padding:0; }
    .edge-actions button:hover { background:#58a6ff; color:#0b0e14; }
    .edge-actions button:last-child { border-color:#f85149; color:#f85149; }
    .edge-actions button:last-child:hover { background:#f85149; color:#fff; }
    .edge-toolbar { position:fixed; z-index:120; background:#161b22; border:1px solid #58a6ff; border-radius:8px; padding:.35rem .6rem; display:flex; align-items:center; gap:.5rem; font-size:.75rem; box-shadow:0 6px 18px #000a; }
    .settings-backdrop.floating { position:fixed; inset:0; background:transparent; display:block; pointer-events:none; }
    .settings-backdrop.floating .settings-modal.popover { position:fixed; pointer-events:auto; width:420px; max-width:calc(100vw - 16px); max-height:76vh; overflow:auto; }
    .settings-backdrop.floating .settings-modal.popover.popover-wide { width:520px; }
    .settings-backdrop.floating .settings-modal.popover.popover-settings { width:680px; max-height:82vh; }
    .settings-modal.popover .settings-body { padding:.55rem; }
    .settings-modal.popover .settings-body section { padding:.5rem .6rem; }
    f-flow { flex:1; min-height:0; position:relative; background:#0b0e14; }
    f-canvas { background:#0b0e14; }
    .graph-minimap { position:absolute; right:12px; bottom:12px; width:190px; height:130px; background:#0d1117; border:1px solid #30363d; border-radius:8px; overflow:hidden; z-index:10; }
    .layout-spinner { position:absolute; top:50%; left:50%; transform:translate(-50%,-50%); z-index:50; display:flex; align-items:center; gap:.6rem; background:#161b22f2; border:1px solid #30363d; border-radius:10px; padding:.7rem 1rem; color:#c9d1d9; font-size:.78rem; box-shadow:0 8px 30px #000a; }
    .spinner-cancel { background:#21262d; border:1px solid #30363d; color:#c9d1d9; border-radius:5px; padding:.25rem .6rem; font-size:.72rem; cursor:pointer; }
    .spinner-cancel:hover { background:#30363d; }
    .spinner { width:12px; height:12px; border:2px solid #30363d; border-top-color:#58a6ff; border-radius:50%; animation:spin .7s linear infinite; }
    @keyframes spin { to { transform: rotate(360deg); } }
    .gw-node { position:relative; width:250px; padding:.6rem .7rem; border-radius:9px; background:#161b22; border:1px solid #30363d; color:#c9d1d9; cursor:pointer; box-shadow:0 4px 16px #0008; box-sizing:border-box; }
    .gw-node.selected { border-color:#58a6ff; box-shadow:0 0 0 2px #58a6ff55, 0 4px 16px #0008; }
    .gw-node.kind-agent { padding:0; width:170px; background:transparent; border:none; box-shadow:none; }
    .gw-node.kind-org { border-left:4px solid #58a6ff; background:#11263b; }
    .gw-node.kind-unit { border-left:4px solid #3fb950; background:#12261a; }
    .gw-node.kind-workspace { border-left:4px solid #3fb950; }
    .gw-node.kind-memory { border-left:4px solid #bc8cff; }
    .gw-node.kind-credential { border-left:4px solid #d29922; }
    .gw-node.kind-knowledge { border-left:4px solid #39c5cf; }
    .gw-node.kind-mcp { border-left:4px solid #bc8cff; }
    .gw-node.kind-gate { border-left:4px solid #d29922; }
    .gw-node.kind-http { border-left:4px solid #39c5cf; }
    .gw-node.kind-custom { border-left:4px solid #8b949e; }
    .gw-node.kind-ghost { width:190px; background:#1a0f12; border:1px dashed #f85149; opacity:.72; }
    .gw-node.kind-ghost.active { opacity:1; border-style:solid; box-shadow:0 0 0 2px #f8514933; }
    .ghost-card { padding:.5rem .6rem; }
    .ghost-kind { font-size:.6rem; color:#f85149; letter-spacing:.08em; }
    .ghost-title { font-weight:700; font-size:.82rem; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
    .ghost-sub { font-size:.65rem; color:#8b949e; }
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
    .figure { border:2px solid #484f58; }
    .figure.on { border-color:#3fb950; animation:pulse-green 1.6s ease-out infinite; }
    .figure.error { border-color:#f85149; animation:blink-red 1s steps(2, jump-none) infinite; }
    .figure.selected { border-color:#58a6ff; }
    .gw-node.kind-agent.active { border-color:#3fb950; animation:pulse-green 1.6s ease-out infinite; }
    .gw-node.kind-agent.inactive { border-color:#484f58; }
    .gw-node.kind-agent.error { border-color:#f85149; animation:blink-red 1s steps(2, jump-none) infinite; }
    @keyframes pulse-green { 0% { box-shadow:0 0 0 0 #3fb95088; } 70% { box-shadow:0 0 0 9px #3fb95000; } 100% { box-shadow:0 0 0 0 #3fb95000; } }
    @keyframes blink-red { 0%, 100% { opacity:1; } 50% { opacity:.35; } }
    .avatar { width:76px; height:90px; }
    .avatar-picker { border:1px solid #21262d; border-radius:6px; padding:.45rem; display:flex; flex-direction:column; gap:.4rem; }
    .avatar-preview { width:76px; height:90px; align-self:center; }
    .avatar-preview svg { width:76px; height:90px; }
    .avatar-grid { display:grid; grid-template-columns:repeat(5,1fr); gap:.25rem; }
    .avatar-choice { background:#0d1117; border:1px solid #30363d; border-radius:5px; padding:.15rem; color:#8b949e; font-size:.55rem; cursor:pointer; display:flex; flex-direction:column; align-items:center; }
    .avatar-choice.active { border-color:#58a6ff; color:#58a6ff; }
    .avatar-mini { width:24px; height:28px; display:block; }
    .avatar-mini svg { width:24px; height:28px; }
    .main-badge { font-size:.55rem; font-weight:700; letter-spacing:.06em; color:#0b0e14; background:#d29922; border-radius:999px; padding:.05rem .35rem; margin-top:.15rem; }
    .gw-node.main-agent .figure { border-color:#d29922; box-shadow:0 0 0 3px #d2992255, 0 0 18px #d2992266; }
    .gw-node.main-agent:not(.kind-agent) { border-color:#d29922; box-shadow:0 0 0 2px #d2992255; }
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
    .gw-port { position:absolute; width:7px; height:7px; border-radius:50%; background:#58a6ff88; border:1px solid #58a6ff; opacity:0; transition:opacity .15s; transform:translate(-50%,-50%); z-index:5; }
    .gw-node:hover .gw-port, .gw-node.selected .gw-port { opacity:1; }
    .small { padding:.2rem .5rem; font-size:.75rem; }
    .inspector { width:300px; border-left:1px solid #30363d; background:#161b22; padding:.9rem; display:flex; flex-direction:column; gap:.65rem; overflow:auto; }
    .inspector header { display:flex; align-items:flex-start; gap:.5rem; }
    .inspector h3 { margin:.1rem 0; font-size:1rem; flex:1; }
    .eyebrow { font-size:.62rem; letter-spacing:.08em; color:#8b949e; }
    .inspector label { display:flex; flex-direction:column; gap:.25rem; font-size:.75rem; color:#8b949e; }
    .inspector-row { display:flex; align-items:center; gap:.4rem; }
    .inspector-actions { display:flex; gap:.5rem; }
    .settings-backdrop { position:fixed; inset:0; background:#010409cc; display:flex; align-items:center; justify-content:center; z-index:90; }
    .settings-modal { width:min(980px,95vw); max-height:90vh; display:flex; flex-direction:column; background:#161b22; border:1px solid #30363d; border-radius:10px; overflow:hidden; }
    .settings-modal header { display:flex; align-items:center; gap:.5rem; padding:.7rem .9rem; border-bottom:1px solid #30363d; }
    .settings-modal header h3 { margin:0; font-size:.95rem; flex:1; }
    .settings-body { overflow:auto; padding:.8rem .9rem; display:flex; flex-direction:column; gap:.8rem; }
    .settings-body section { border:1px solid #21262d; border-radius:8px; padding:.7rem .8rem; background:#0f141b; }
    .settings-body h4 { margin:0 0 .5rem; font-size:.82rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.06em; }
    .settings-row { display:flex; align-items:center; gap:.5rem; margin-top:.4rem; flex-wrap:wrap; }
    .settings-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(190px,1fr)); gap:.5rem; }
    .settings-grid label { display:flex; flex-direction:column; gap:.2rem; font-size:.72rem; color:#8b949e; }
    .settings-grid label.wide { grid-column:1/-1; }
    .ports-table { width:100%; border-collapse:collapse; font-size:.75rem; }
    .ports-table th { text-align:left; color:#8b949e; font-weight:500; padding:.2rem; border-bottom:1px solid #30363d; }
    .ports-table td { padding:.2rem; border-bottom:1px solid #21262d; }
    .port-cell { display:flex; align-items:center; gap:.25rem; }
    .port-cell input { width:3.2rem; padding:.15rem .25rem; }
    .port-cell span { color:#8b949e; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; }
    .hint { font-size:.72rem; margin:.2rem 0 .5rem; }
    .file-button { display:inline-flex; align-items:center; border:1px solid #30363d; border-radius:6px; padding:.35rem .6rem; cursor:pointer; font-size:.75rem; }
    .custom-edge { display:flex; align-items:center; gap:.5rem; padding:.3rem 0; border-bottom:1px solid #21262d; font-size:.75rem; }
    .small-select { padding:.2rem .35rem; font-size:.72rem; }
    .danger { background:#da3633; border-color:#f85149; color:#fff; }
    .context-menu { position:fixed; z-index:120; min-width:230px; background:#161b22; border:1px solid #30363d; border-radius:8px; padding:.25rem; box-shadow:0 12px 32px #000a; display:flex; flex-direction:column; }
    .context-menu button { text-align:left; background:transparent; border:none; color:#c9d1d9; padding:.4rem .55rem; border-radius:5px; font-size:.78rem; }
    .context-menu button:hover { background:#1f6feb26; }
    .context-menu .danger { color:#ff7b72; }
    .menu-sep { height:1px; background:#30363d; margin:.25rem 0; }
    .notice { padding:.45rem .6rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; font-size:.78rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .ghost { background:transparent; border:1px solid #30363d; color:#c9d1d9; }
  `],
})
export class GraphComponent implements OnChanges {
  @Input() agents: Agent[] = [];
  @Input() activity: AgentActivity[] = [];
  @Input() workspaces: Workspace[] = [];
  @Input() grants: Grant[] = [];
  @Input() brokerGrants: BrokerGrant[] = [];
  @Input() paths: Record<string, AgentPaths> = {};
  @Input() selectedAgentId: string | null = null;
  @Input() mainAgentId: string | null = null;

  readonly agentSelected = output<string>();
  readonly mainAgentSelected = output<string>();
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
  readonly sides: Side[] = ['left', 'right', 'top', 'bottom'];

  schema = signal<GraphSchema>(this.loadSchema());
  layout = signal<LayoutAlgorithm>('mixed');
  settingsOpen = signal(false);
  customEdges = signal<CustomEdge[]>(this.loadCustomEdges());
  layoutNonce = signal(0);
  readonly computing = signal(false);
  private animTick = signal(0);
  private animated = new Map<string, { x: number; y: number }>();
  private animId: number | null = null;
  private layoutGen = 0;
  private pendingLayout: LayoutAlgorithm | null = null;
  private previousLayout: LayoutAlgorithm = 'mixed';
  private layoutCacheKey = '';
  private layoutCache = new Map<string, { x: number; y: number }>();
  private fullLayoutCacheKey = '';
  private fullLayoutCache = new Map<string, { x: number; y: number }>();
  schemaJson = '';
  schemaError = signal('');
  connSource = '';
  connSourcePort = '';
  connTarget = '';
  connTargetPort = '';
  connType = 'rw';
  connLabel = '';
  connTemplate = '';
  layoutTemplates = signal<LayoutTemplate[]>(this.loadTemplates());
  ghosts = signal<GhostNode[]>(this.loadGhosts());
  contextMenu = signal<{ x: number; y: number; node?: GraphNode; edge?: GraphEdge } | null>(null);
  newTemplateName = '';
  connectors = signal<ConnectorConfig[]>(this.loadConnectors());
  mcpServers = signal<AgentMCP[]>([]);
  skills = signal<AgentSkill[]>([]);
  configError = signal('');
  configBusy = signal(false);
  mcpName = '';
  mcpTransport: AgentMCP['transport'] = 'stdio';
  mcpCommand = '';
  mcpArgs = '';
  mcpURL = '';
  mcpContract = '';
  skillName = '';
  skillDescription = '';
  skillSource = '';
  skillConfig = '';
  gateExpression = '';
  gateTrue = '';
  gateFalse = '';
  gateCommand = '';
  gateCwd = '';
  gateTimeout = 300;
  gateReview = false;
  gateHuman = false;
  editAvatar = '';
  editAvatarSVG = '';
  avatarError = signal('');
  readonly figures = FIGURES;
  draggingPalette = signal<string | null>(null);
  private pendingDropPosition: { x: number; y: number } | null = null;
  private pendingDropKind: string | null = null;
  private draftConnectorId: string | null = null;
  editConnectorId = signal<string | null>(null);
  ecName = '';
  ecKind: ConnectorConfig['kind'] = 'gate';
  ecDirection: ConnectorConfig['direction'] = 'out';
  ecTarget = '';
  ecURL = '';
  ecExpression = '';
  ecTrue = '';
  ecFalse = '';
  ecCommand = '';
  ecCwd = '';
  ecTimeout = 300;
  ecReview = false;
  ecHuman = false;
  ecContract = '';
  connectOpen = signal(false);
  connectSource = signal<GraphNode | null>(null);
  connectType = 'custom';
  connectingSource = signal<GraphNode | null>(null);
  connectingType = signal('custom');
  connectError = signal('');
  selectedEdge = signal<GraphEdge | null>(null);
  popoverPos = signal<{ x: number; y: number } | null>(null);
  edgeToolbarPos = signal<{ x: number; y: number } | null>(null);
  connectorsOpen = signal(false);
  connectorAgent = signal<Agent | null>(null);
  connKind: ConnectorConfig['kind'] = 'mcp';
  connName = '';
  connTargetAgent = '';
  connURL = '';
  connContract = '';
  mcpImportJson = '';
  connDirection: ConnectorConfig['direction'] = 'out';
  private manualPositions = this.loadPositions();

  @ViewChild(FCanvasComponent) private canvas?: FCanvasComponent;
  private readonly api = inject(ApiService);
  private readonly sanitizer = inject(DomSanitizer);
  private readonly graphService = inject(GraphService);
  private readonly gateService = inject(GateService);
  private readonly paperclip = inject(PaperclipService);
  private saveTimer: ReturnType<typeof setTimeout> | null = null;
  readonly gateCount = computed(() => this.connectors().filter(c => c.kind === 'gate' && c.enabled).length);
  readonly paperclipWorkspace = signal('');
  readonly paperclipSync = signal(false);
  readonly paperclipSyncInfo = signal('');
  readonly gateSyncStatus = signal<GateSyncStatus | null>(null);
  readonly gateRuns = signal<GateRunView[]>([]);
  readonly gateRunError = signal('');
  readonly paperclipIssues = signal<{ identifier?: string; title?: string; status?: string }[]>([]);
  readonly openReviews = computed(() => this.paperclipIssues()
    .filter(i => /review/i.test(i.title || '') && i.status !== 'done' && i.status !== 'cancelled').length);

  constructor() {
    // The gateway is the shared source of truth; localStorage stays as a
    // fallback when the gateway has no document yet.
    this.paperclip.status().subscribe({
      next: (st) => this.paperclipIssues.set(st?.issues || []),
      error: () => { /* paperclip offline */ },
    });
    this.graphService.load().subscribe({
      next: (res) => {
        const doc = (res?.saved ? res.body : null) as Record<string, unknown> | null;
        if (!doc) return;
        const apply = <T>(key: string, value: T | undefined, target: { set: (v: T) => void }) => {
          if (value === undefined) return;
          try { localStorage.setItem(key, JSON.stringify(value)); } catch { /* ignore */ }
          target.set(value);
        };
        apply('dockweave.agentConnectors', doc['connectors'] as ConnectorConfig[] | undefined, this.connectors);
        apply('dockweave.customEdges', doc['customEdges'] as CustomEdge[] | undefined, this.customEdges);
        apply('dockweave.graphGhosts', doc['ghosts'] as GhostNode[] | undefined, this.ghosts);
        if (doc['schema']) apply('dockweave.graphSchema', doc['schema'] as GraphSchema, this.schema);
        if (doc['positions']) {
          this.manualPositions = doc['positions'] as Record<string, { x: number; y: number }>;
          try { localStorage.setItem('dockweave.graphPositions', JSON.stringify(this.manualPositions)); } catch { /* ignore */ }
        }
        const pc = doc['paperclip'] as { workspace?: string; syncGates?: boolean } | undefined;
        if (pc) {
          this.paperclipWorkspace.set(pc.workspace || '');
          this.paperclipSync.set(!!pc.syncGates);
          this.persistPaperclipSettings();
        }
      },
      error: () => { /* offline: keep the local copy */ },
    });
    try {
      this.paperclipWorkspace.set(localStorage.getItem('dockweave.paperclipWorkspace') || '');
      this.paperclipSync.set(localStorage.getItem('dockweave.paperclipSync') === '1');
    } catch { /* ignore */ }
    this.loadGateSyncStatus();
    // Gate runs are fetched lazily when the settings panel opens: the daemon
    // endpoint may not exist on an older daemon and a background 404 would only
    // add console noise.
  }

  setPaperclipWorkspace(value: string): void {
    this.paperclipWorkspace.set(value);
    this.persistPaperclipSettings();
    this.scheduleGraphSave();
  }

  setPaperclipSync(value: boolean): void {
    this.paperclipSync.set(value);
    this.persistPaperclipSettings();
    this.scheduleGraphSave();
  }

  private persistPaperclipSettings(): void {
    try {
      localStorage.setItem('dockweave.paperclipWorkspace', this.paperclipWorkspace());
      localStorage.setItem('dockweave.paperclipSync', this.paperclipSync() ? '1' : '0');
    } catch { /* ignore */ }
  }

  loadGateSyncStatus(): void {
    this.graphService.gateStatus().subscribe({
      next: (status) => this.gateSyncStatus.set(status),
      error: () => this.gateSyncStatus.set(null),
    });
  }

  loadGateHealth(): void {
    this.gateService.runs(this.paperclipWorkspace().trim(), 8).subscribe({
      next: (res) => { this.gateRuns.set(res?.runs ?? []); this.gateRunError.set(''); },
      error: () => { this.gateRuns.set([]); this.gateRunError.set('Gate-Historie nicht verfügbar'); },
    });
  }

  gateRunWhen(run: GateRunView): string {
    return run.started_at ? run.started_at.replace('T', ' ').slice(0, 19) : '';
  }

  gateSyncLabel(): string {
    const s = this.gateSyncStatus();
    if (!s || !s.enabled) return 'Sync inaktiv';
    if (s.error) return 'Status: ' + s.error;
    if (!s.exists) return 'Noch keine gates.json';
    if (!s.managed) return 'Manuelle gates.json (nicht von DockWeave)';
    if (s.drifted) return 'Drift: gates.json wurde seit dem Sync verändert';
    return 'Synchronisiert' + (s.hash ? ' · ' + s.hash.slice(0, 12) : '');
  }

  private describeGateSync(result: GraphGateSyncResult | undefined): string {
    if (!result) return '';
    if (result.error) return `Paperclip-Sync fehlgeschlagen: ${result.error}`;
    if (result.skipped) return result.reason ? `Paperclip-Sync übersprungen: ${result.reason}` : 'Paperclip-Sync übersprungen.';
    const flags = [result.gates ? `${result.gates} Gate(s)` : '', result.review ? 'Review' : '', result.human_approval ? 'Human Approval' : '']
      .filter(Boolean).join(', ');
    return `Paperclip-Gates geschrieben${flags ? `: ${flags}` : ''}${result.path ? ` → ${result.path}` : ''}`;
  }

  /** Debounced push of the whole graph document to the gateway. */
  private scheduleGraphSave(): void {
    if (this.saveTimer) clearTimeout(this.saveTimer);
    this.saveTimer = setTimeout(() => this.pushGraph(), 400);
  }

  private pushGraph(): void {
    const doc = {
      version: 1,
      schema: this.schema(),
      connectors: this.connectors(),
      customEdges: this.customEdges(),
      ghosts: this.ghosts(),
      positions: this.manualPositions,
      paperclip: {
        workspace: this.paperclipWorkspace().trim(),
        syncGates: this.paperclipSync(),
      },
      updatedAt: new Date().toISOString(),
    };
    this.graphService.save(doc).subscribe({
      next: (res) => {
        if (this.paperclipSync()) this.paperclipSyncInfo.set(this.describeGateSync(res.gates));
        this.loadGateSyncStatus();
        this.loadGateHealth();
      },
      error: () => { /* keep localStorage */ },
    });
  }

  dataVersion = signal(0);

  ngOnChanges(_changes: SimpleChanges): void {
    this.dataVersion.update(v => v + 1);
  }

  readonly nodes = computed(() => {
    this.dataVersion();
    this.layoutNonce();
    this.schema();
    const nodes = this.buildNodes();
    const real = new Set(nodes.map(n => n.id));
    const ghosts = this.ghosts()
      .filter(g => !real.has(g.id))
      .map(g => ({
        id: g.id, kind: 'ghost' as const, title: g.title, subtitle: g.active ? 'active placeholder' : 'template placeholder',
        detail: g.active ? 'enabled' : 'disabled', position: g.position, ghost: true, active: g.active, templateId: g.templateId,
      }));
    const all = [...nodes, ...this.connectorNodes(nodes), ...this.mcpNodes(nodes), ...this.skillNodes(nodes), ...ghosts];
    const algo = this.layout();
    if (algo !== 'force' && algo !== 'stress') return all;
    // Connector/MCP/skill nodes are attached at fixed offsets to their agent.
    // In a position-driven layout those offsets pile up, so run the same
    // crossing/clearance pass over the full set (it also separates the
    // satellites and keeps their edges away from unrelated nodes). The result
    // is cached by layout + positions so the periodic data refresh does not
    // re-run the expensive pass every few seconds.
    const key = algo + '|' + this.mode() + '|' +
      all.map(n => `${n.id}:${n.position.x},${n.position.y}`).join(';');
    let refined: GraphNode[];
    if (key === this.fullLayoutCacheKey) {
      refined = all.map(n => ({ ...n, position: this.fullLayoutCache.get(n.id) ?? n.position }));
    } else {
      refined = this.refineLayout(all);
      this.fullLayoutCacheKey = key;
      this.fullLayoutCache.clear();
      for (const n of refined) this.fullLayoutCache.set(n.id, n.position);
    }
    return refined.map(n =>
      this.manualPositions[n.id] ? { ...n, position: this.manualPositions[n.id] } : n);
  });
  // Per-edge connectors for position-driven layouts. Each edge gets its own
  // port on the node border, at the point where the straight line to the other
  // endpoint leaves the rectangle. Because the id is unique per edge, edges no
  // longer pile up on a single shared port and the connectors visibly slide
  // around the node as it moves.
  private readonly dynamicPorts = computed<{
    byNode: Map<string, GraphPort[]>;
    byEdge: Map<string, { sourcePort: GraphPort; targetPort: GraphPort }>;
  }>(() => {
    const byNode = new Map<string, GraphPort[]>();
    const byEdge = new Map<string, { sourcePort: GraphPort; targetPort: GraphPort }>();
    if (!this.isPositionDriven()) return { byNode, byEdge };
    const nodes = this.nodes();
    const byId = new Map(nodes.map(n => [n.id, n]));
    const clamp = (v: number): number => Math.max(0, Math.min(100, v));
    const portAt = (node: GraphNode, other: GraphNode, edgeId: string, type: 'in' | 'out'): GraphPort => {
      const box = this.toLayoutNode(node);
      const { side, point } = borderPoint(box, this.toLayoutNode(other));
      const left = side === 'left' ? 0 : side === 'right' ? 100 : clamp(((point.x - node.position.x) / box.w) * 100);
      const top = side === 'top' ? 0 : side === 'bottom' ? 100 : clamp(((point.y - node.position.y) / box.h) * 100);
      return { id: `${node.id}:dyn:${edgeId}:${type}`, type, side, left, top };
    };
    for (const e of this.buildEdges()) {
      const source = byId.get(e.source);
      const target = byId.get(e.target);
      if (!source || !target || source.id === target.id) continue;
      const sourcePort = portAt(source, target, e.id, 'out');
      const targetPort = portAt(target, source, e.id, 'in');
      byEdge.set(e.id, { sourcePort, targetPort });
      byNode.set(source.id, [...(byNode.get(source.id) ?? []), sourcePort]);
      byNode.set(target.id, [...(byNode.get(target.id) ?? []), targetPort]);
    }
    return { byNode, byEdge };
  });

  // Waypoints for edges whose straight line would cut through an unrelated
  // node. Only blocked edges are routed, so the common case stays a direct
  // line and the visibility search stays cheap.
  private readonly edgeRoutes = computed(() => {
    const routes = new Map<string, { x: number; y: number }[]>();
    if (!this.isPositionDriven()) return routes;
    if (!this.dynamicPorts().byEdge.size) return routes;
    const nodes = this.nodes();
    const byId = new Map(nodes.map(n => [n.id, n]));
    const rects = new Map<string, RouterRect>();
    for (const n of nodes) {
      const s = this.nodeSize(n);
      rects.set(n.id, { x: n.position.x, y: n.position.y, w: s.w, h: s.h });
    }
    for (const e of this.buildEdges()) {
      const source = byId.get(e.source);
      const target = byId.get(e.target);
      if (!source || !target || source.id === target.id) continue;
      const start = borderPoint(this.toLayoutNode(source), this.toLayoutNode(target)).point;
      const end = borderPoint(this.toLayoutNode(target), this.toLayoutNode(source)).point;
      const obstacles: RouterRect[] = [];
      for (const n of nodes) {
        if (n.id === source.id || n.id === target.id) continue;
        const r = rects.get(n.id);
        if (r) obstacles.push(r);
      }
      const waypoints = routeAroundNodes(start, end, obstacles);
      if (waypoints.length) routes.set(e.id, waypoints);
    }
    return routes;
  });

  readonly edges = computed(() => {
    this.dataVersion();
    this.schema();
    this.customEdges();
    this.layout();
    const base = this.buildEdges();
    const dyn = this.dynamicPorts().byEdge;
    if (!dyn.size) return base;
    const routes = this.edgeRoutes();
    // In position-driven layouts every edge gets its own connector placed
    // where the straight line to its partner leaves the node border.
    return base.map(e => {
      const d = dyn.get(e.id);
      if (!d) return e;
      const waypoints = routes.get(e.id);
      return {
        ...e,
        sourcePort: d.sourcePort.id,
        targetPort: d.targetPort.id,
        sourceSide: d.sourcePort.side,
        targetSide: d.targetPort.side,
        type: 'straight' as const,
        ...(waypoints ? { waypoints } : {}),
      };
    });
  });
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


  figureHtml(node: GraphNode): SafeHtml {
    return this.sanitizer.bypassSecurityTrustHtml(figureSvg(node.avatar, node.avatarSvg));
  }

  trustFigure(svg: string): SafeHtml {
    return this.sanitizer.bypassSecurityTrustHtml(svg);
  }

  avatarPreview(): SafeHtml {
    return this.sanitizer.bypassSecurityTrustHtml(figureSvg(this.editAvatar, this.editAvatarSVG));
  }

  chooseAvatar(id: string): void {
    this.editAvatar = id;
    this.editAvatarSVG = '';
    this.avatarError.set('');
  }

  importAvatar(ev: Event): void {
    const input = ev.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    file.text().then(text => {
      const clean = sanitizeSvg(text);
      if (!clean) {
        this.avatarError.set('SVG rejected (scripts/event handlers/foreignObject or >64 KiB)');
        return;
      }
      this.editAvatar = 'custom';
      this.editAvatarSVG = clean;
      this.avatarError.set('');
    }).catch(() => this.avatarError.set('could not read SVG'));
    input.value = '';
  }

  saveAvatar(): void {
    const a = this.editor();
    if (!a) return;
    this.busy.set(true);
    this.avatarError.set('');
    this.api.setAgentAvatar(a.id, this.editAvatar, this.editAvatarSVG).subscribe({
      next: updated => {
        this.busy.set(false);
        this.editor.set(updated);
        this.message.set('Avatar gespeichert');
        this.changed.emit();
      },
      error: e => { this.busy.set(false); this.avatarError.set(e.message ?? String(e)); },
    });
  }

  onPaletteDragStart(ev: DragEvent, kind: string): void {
    this.draggingPalette.set(kind);
    ev.dataTransfer?.setData('application/x-dockweave-node', kind);
    if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'copy';
  }

  onDragOver(ev: DragEvent): void {
    ev.preventDefault();
    if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'copy';
  }

  onPaletteDrop(ev: DragEvent): void {
    ev.preventDefault();
    const kind = ev.dataTransfer?.getData('application/x-dockweave-node') || this.draggingPalette();
    this.draggingPalette.set(null);
    if (!kind) return;
    const a = this.agents.find(x => x.id === this.selectedAgentId);
    if (!a) { this.configError.set('select an agent first'); return; }
    const pos = this.flowPoint(ev.clientX, ev.clientY);
    this.createPaletteNode(a, kind, pos);
  }

  private flowPoint(clientX: number, clientY: number): { x: number; y: number } {
    const flow = document.querySelector('f-flow') as HTMLElement | null;
    const canvas = document.querySelector('f-canvas') as HTMLElement | null;
    if (!flow) return { x: clientX, y: clientY };
    const rect = flow.getBoundingClientRect();
    if (!canvas) return { x: clientX - rect.left, y: clientY - rect.top };
    const tr = getComputedStyle(canvas).transform;
    const m = tr && tr !== 'none' ? tr.match(/matrix\(([^)]+)\)/) : null;
    const parts = m ? m[1].split(',').map(Number) : [1, 0, 0, 1, 0, 0];
    const scale = parts[0] || 1;
    const tx = parts[4] || 0;
    const ty = parts[5] || 0;
    return { x: (clientX - rect.left - tx) / scale, y: (clientY - rect.top - ty) / scale };
  }

  palette(kind: 'mcp' | 'skill' | 'gate' | 'http' | 'agent-chain' | 'custom'): void {
    const a = this.agents.find(x => x.id === this.selectedAgentId);
    if (!a) { this.configError.set('select an agent first'); return; }
    const flow = document.querySelector('f-flow') as HTMLElement | null;
    let pos = { x: 0, y: 120 };
    if (flow) {
      const r = flow.getBoundingClientRect();
      pos = this.flowPoint(r.left + r.width / 2, r.top + r.height / 2);
    }
    this.createPaletteNode(a, kind, pos);
  }

  private createPaletteNode(a: Agent, kind: string, pos: { x: number; y: number }): void {
    const mapped = kind === 'agent-chain' ? 'custom' : kind;
    const id = 'conn-' + Date.now();
    const name = kind === 'gate' ? 'New gate' : kind === 'http' ? 'New HTTP' : kind === 'mcp' ? 'New MCP server' : kind === 'skill' ? 'New skill' : kind === 'agent-chain' ? 'New chain' : 'New node';
    const cfg: ConnectorConfig = {
      id, agentId: a.id, kind: mapped as ConnectorConfig['kind'], name,
      direction: 'out', enabled: true, connected: false, draft: kind === 'mcp' || kind === 'skill',
      contract: kind === 'gate' ? JSON.stringify({ expression: 'true', on_true: 'allow', on_false: 'deny' }) : undefined,
    };
    const next = [...this.connectors(), cfg];
    this.connectors.set(next);
    this.persistConnectors(next);
    this.manualPositions['connector-' + id] = pos;
    this.persistPositions(this.manualPositions);
    this.layoutNonce.update(v => v + 1);
    this.configError.set('');
  }

  private finishDraft(prefix: 'mcp' | 'skill', createdId: string): void {
    const draft = this.draftConnectorId;
    const pos = this.pendingDropPosition || (draft ? this.manualPositions['connector-' + draft] : null);
    if (pos) this.manualPositions[prefix + '-' + createdId] = pos;
    if (draft) {
      delete this.manualPositions['connector-' + draft];
      const next = this.connectors().filter(c => c.id !== draft);
      this.connectors.set(next);
      this.persistConnectors(next);
      this.draftConnectorId = null;
    }
    this.persistPositions(this.manualPositions);
    this.pendingDropPosition = null;
    this.pendingDropKind = null;
    this.layoutNonce.update(v => v + 1);
  }

  handleNodeClick(node: GraphNode): void {
    const src = this.connectingSource();
    if (src) {
      if (src.id === node.id) { this.connectError.set('target must be a different node'); return; }
      this.createConnection(src, node, this.connectingType());
      return;
    }
    this.nodeClicked(node);
  }

  private positionPopoverForNode(node: GraphNode | null, width = 430): void {
    let x = 120, y = 90;
    if (node) {
      const el = document.querySelector(`[data-node-id="${node.id}"]`) as HTMLElement | null;
      if (el) {
        const r = el.getBoundingClientRect();
        x = r.right + 12;
        if (x + width > window.innerWidth - 8) x = Math.max(8, r.left - width - 12);
        y = Math.max(8, Math.min(window.innerHeight - 480, r.top));
      }
    }
    this.popoverPos.set({ x: Math.max(8, Math.min(x, window.innerWidth - width - 8)), y });
  }

  beginConnect(node: GraphNode): void {
    if (node.kind === 'ghost') return;
    this.positionPopoverForNode(node, 340);
    this.connectSource.set(node);
    this.connectType = this.schema().connectionTypes[0]?.id || 'custom';
    this.connectError.set('');
    this.connectOpen.set(true);
    this.closeContext();
  }

  continueConnect(): void {
    const src = this.connectSource();
    if (!src) return;
    this.connectingSource.set(src);
    this.connectingType.set(this.connectType);
    this.connectOpen.set(false);
    this.connectError.set('');
  }

  cancelConnect(): void {
    this.connectingSource.set(null);
    this.connectSource.set(null);
    this.connectOpen.set(false);
    this.connectError.set('');
  }

  private createConnection(source: GraphNode, target: GraphNode, kind: string): void {
    const sides = this.bestSides(source, target);
    const out = this.pickPort(source.id, 'out', sides.sourceSide);
    const inn = this.pickPort(target.id, 'in', sides.targetSide);
    if (!out || !inn) {
      this.connectError.set('source or target has no compatible port');
      this.cancelConnect();
      return;
    }
    const ctype = this.schema().connectionTypes.find(t => t.id === kind);
    const edge: CustomEdge = {
      id: 'custom-' + Date.now(), source: source.id, target: target.id,
      sourcePort: out.id, targetPort: inn.id, label: ctype?.label || kind,
      kind, sourceSide: out.side, targetSide: inn.side,
    };
    const next = [...this.customEdges(), edge];
    this.customEdges.set(next);
    this.persistCustomEdges(next);
    this.selectedEdge.set({ ...edge, type: 'bezier' });
    this.cancelConnect();
  }

  private bestSides(source: GraphNode, target: GraphNode): { sourceSide: Side; targetSide: Side } {
    const dx = target.position.x - source.position.x;
    const dy = target.position.y - source.position.y;
    if (Math.abs(dx) >= Math.abs(dy)) {
      return dx >= 0 ? { sourceSide: 'right', targetSide: 'left' } : { sourceSide: 'left', targetSide: 'right' };
    }
    return dy >= 0 ? { sourceSide: 'bottom', targetSide: 'top' } : { sourceSide: 'top', targetSide: 'bottom' };
  }

  selectEdge(edge: GraphEdge, ev?: MouseEvent): void {
    this.selectedEdge.set(edge);
    if (ev) this.edgeToolbarPos.set({ x: Math.min(window.innerWidth - 340, ev.clientX + 10), y: Math.min(window.innerHeight - 60, ev.clientY + 10) });
  }

  openEdgeContext(ev: MouseEvent, edge: GraphEdge): void {
    ev.preventDefault();
    ev.stopPropagation();
    this.selectedEdge.set(edge);
    this.edgeToolbarPos.set({ x: Math.min(window.innerWidth - 340, ev.clientX + 10), y: Math.min(window.innerHeight - 60, ev.clientY + 10) });
    this.contextMenu.set({ x: ev.clientX, y: ev.clientY, edge });
  }

  removeNode(node: GraphNode): void {
    if (node.kind === 'ghost') { this.removeGhost(node.id); return; }
    if (node.id.startsWith('connector-')) {
      const cid = node.id.slice('connector-'.length);
      const next = this.connectors().filter(c => c.id !== cid);
      this.connectors.set(next);
      this.persistConnectors(next);
      const edges = this.customEdges().filter(e => e.source !== node.id && e.target !== node.id);
      this.customEdges.set(edges);
      this.persistCustomEdges(edges);
      delete this.manualPositions[node.id];
      this.persistPositions(this.manualPositions);
      this.layoutNonce.update(v => v + 1);
      return;
    }
    if (node.id.startsWith('mcp-')) {
      const m = this.mcpServers().find(x => 'mcp-' + x.id === node.id);
      if (m) this.api.deleteMCP(m.agent_id, m.id).subscribe({ next: () => { this.loadAgentConfig(m.agent_id); this.changed.emit(); }, error: e => this.configError.set(e.message ?? String(e)) });
      return;
    }
    if (node.id.startsWith('skill-')) {
      const sk = this.skills().find(x => 'skill-' + x.id === node.id);
      if (sk) this.api.deleteSkill(sk.agent_id, sk.id).subscribe({ next: () => { this.loadAgentConfig(sk.agent_id); this.changed.emit(); }, error: e => this.configError.set(e.message ?? String(e)) });
      return;
    }
    this.configError.set('dieser Node stammt aus den Agentendaten und kann hier nicht entfernt werden');
  }

  insertNodeOnEdge(edge: GraphEdge): void {
    if (!this.customEdges().some(e => e.id === edge.id)) {
      this.configError.set('nur eigene Kanten können einen Zwischen-Node erhalten');
      return;
    }
    const source = this.nodes().find(n => n.id === edge.source);
    const target = this.nodes().find(n => n.id === edge.target);
    if (!source || !target) return;
    const id = 'conn-' + Date.now();
    const cfg: ConnectorConfig = {
      id, agentId: this.agents.find(x => x.id === this.selectedAgentId)?.id || source.id.replace(/^agent-/, ''),
      kind: 'custom', name: 'Junction', direction: 'out', enabled: true, connected: false,
    };
    const next = [...this.connectors(), cfg];
    this.connectors.set(next);
    this.persistConnectors(next);
    const mid = { x: (source.position.x + target.position.x) / 2, y: (source.position.y + target.position.y) / 2 };
    this.manualPositions['connector-' + id] = mid;
    this.persistPositions(this.manualPositions);
    const at = this.pickPort('connector-' + id, 'in', 'left');
    const ot = this.pickPort('connector-' + id, 'out', 'right');
    if (at && ot) {
      const e1: CustomEdge = { id: 'custom-' + Date.now() + '-a', source: edge.source, target: 'connector-' + id, sourcePort: edge.sourcePort, targetPort: at.id, label: edge.label, kind: edge.kind, sourceSide: edge.sourceSide, targetSide: at.side };
      const e2: CustomEdge = { id: 'custom-' + Date.now() + '-b', source: 'connector-' + id, target: edge.target, sourcePort: ot.id, targetPort: edge.targetPort, label: edge.label, kind: edge.kind, sourceSide: ot.side, targetSide: edge.targetSide };
      const edges = this.customEdges().filter(e => e.id !== edge.id);
      edges.push(e1, e2);
      this.customEdges.set(edges);
      this.persistCustomEdges(edges);
    }
    this.layoutNonce.update(v => v + 1);
  }

  deleteEdge(edge: GraphEdge): void {
    const custom = this.customEdges().filter(e => e.id !== edge.id);
    if (custom.length !== this.customEdges().length) {
      this.customEdges.set(custom);
      this.persistCustomEdges(custom);
    } else if (edge.id.startsWith('conn-')) {
      const cid = edge.id.slice('conn-'.length);
      const next = this.connectors().map(c => c.id === cid ? { ...c, connected: false } : c);
      this.connectors.set(next);
      this.persistConnectors(next);
    } else {
      this.configError.set('diese Verbindung stammt aus den Agentendaten und kann hier nicht gelöscht werden');
    }
    if (this.selectedEdge()?.id === edge.id) this.selectedEdge.set(null);
    this.closeContext();
    this.layoutNonce.update(v => v + 1);
  }

  nodeDoubleClicked(node: GraphNode): void {
    if (!node.id.startsWith('connector-')) {
      if (node.kind === 'agent') {
        const a = this.agents.find(x => x.id === this.agentIdOf(node));
        if (a) this.openConnectorDialog(a);
        return;
      }
      const a = this.agents.find(x => x.id === this.selectedAgentId);
      if (a && (node.kind === 'mcp' || node.kind === 'skill')) this.openConnectorDialog(a);
      return;
    }
    const cfg = this.connectors().find(c => 'connector-' + c.id === node.id);
    if (!cfg) return;
    if (cfg.draft && (cfg.kind === 'mcp' || cfg.kind === 'skill')) {
      const a = this.agents.find(x => x.id === cfg.agentId);
      if (!a) return;
      this.draftConnectorId = cfg.id;
      this.pendingDropPosition = this.manualPositions['connector-' + cfg.id] ?? null;
      this.openConnectorDialog(a);
      if (cfg.kind === 'mcp') this.mcpName = cfg.name; else this.skillName = cfg.name;
      return;
    }
    this.openLocalEditor(cfg);
  }

  openLocalEditor(c: ConnectorConfig): void {
    const node = this.nodes().find(n => `connector-${c.id}` === n.id) ?? null;
    this.positionPopoverForNode(node, 380);
    this.editConnectorId.set(c.id);
    this.ecName = c.name;
    this.ecKind = c.kind;
    this.ecDirection = c.direction;
    this.ecTarget = c.targetAgentId || '';
    this.ecURL = c.url || '';
    this.ecContract = c.contract || '';
    this.ecExpression = '';
    this.ecTrue = '';
    this.ecFalse = '';
    this.ecCommand = '';
    this.ecCwd = '';
    this.ecTimeout = 300;
    this.ecReview = false;
    this.ecHuman = false;
    if (c.kind === 'gate' && c.contract) {
      try {
        const g = JSON.parse(c.contract) as {
          expression?: string; on_true?: string; on_false?: string;
          command?: string; cwd?: string; timeout_sec?: number;
          review_required?: boolean; human_approval_required?: boolean;
        };
        this.ecExpression = g.expression || '';
        this.ecTrue = g.on_true || '';
        this.ecFalse = g.on_false || '';
        this.ecCommand = g.command || '';
        this.ecCwd = g.cwd || '';
        this.ecTimeout = g.timeout_sec || 300;
        this.ecReview = !!g.review_required;
        this.ecHuman = !!g.human_approval_required;
      } catch { /* ignore */ }
    }
  }

  saveLocalEditor(): void {
    const id = this.editConnectorId();
    if (!id) return;
    const next = this.connectors().map(c => {
      if (c.id !== id) return c;
      let contract = this.ecContract;
      if (this.ecKind === 'gate') {
        const gate: Record<string, unknown> = {
          expression: this.ecExpression.trim(),
          on_true: this.ecTrue.trim() || 'allow',
          on_false: this.ecFalse.trim() || 'deny',
        };
        if (this.ecCommand.trim()) gate['command'] = this.ecCommand.trim();
        if (this.ecCwd.trim()) gate['cwd'] = this.ecCwd.trim();
        if (this.ecTimeout) gate['timeout_sec'] = Number(this.ecTimeout) || 300;
        if (this.ecReview) gate['review_required'] = true;
        if (this.ecHuman) gate['human_approval_required'] = true;
        contract = JSON.stringify(gate, null, 2);
      }
      return { ...c, name: this.ecName.trim() || c.name, kind: this.ecKind, direction: this.ecDirection,
        targetAgentId: this.ecTarget || undefined, url: this.ecURL.trim() || undefined, contract };
    });
    this.connectors.set(next);
    this.persistConnectors(next);
    this.editConnectorId.set(null);
    this.layoutNonce.update(v => v + 1);
  }

  deleteLocalEdited(): void {
    const id = this.editConnectorId();
    if (!id) return;
    const next = this.connectors().filter(c => c.id !== id);
    this.connectors.set(next);
    this.persistConnectors(next);
    delete this.manualPositions['connector-' + id];
    this.persistPositions(this.manualPositions);
    this.editConnectorId.set(null);
    this.layoutNonce.update(v => v + 1);
  }

  gateSummary(raw: string): string {
    try {
      const c = JSON.parse(raw) as { expression?: string; on_true?: string; on_false?: string };
      return `${c.expression || 'condition'} · true→${c.on_true || 'allow'} · false→${c.on_false || 'deny'}`;
    } catch {
      return 'gate';
    }
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
    this.editAvatar = a.avatar || '';
    this.editAvatarSVG = a.avatar_svg || '';
    this.avatarError.set('');
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

  openAgentById(nodeId: string): void {
    this.closeContext();
    this.agentSelected.emit(nodeId.replace(/^agent-/, ''));
  }


  nodeKinds(): string[] {
    return Object.keys(this.schema().nodePorts).sort();
  }

  portCount(kind: string, side: Side, type: 'inputs' | 'outputs'): number {
    return this.schema().nodePorts[kind]?.[side]?.[type] ?? 0;
  }

  setPortCount(kind: string, side: Side, type: 'inputs' | 'outputs', value: number | string): void {
    const n = Math.max(0, Math.min(12, Number(value) || 0));
    const schema = structuredClone(this.schema());
    if (!schema.nodePorts[kind]) {
      schema.nodePorts[kind] = { left: { inputs: 0, outputs: 0 }, right: { inputs: 0, outputs: 0 }, top: { inputs: 0, outputs: 0 }, bottom: { inputs: 0, outputs: 0 } };
    }
    schema.nodePorts[kind][side][type] = n;
    this.schema.set(schema);
    this.persistSchema(schema);
    this.layoutNonce.update(v => v + 1);
  }

  setLayout(algo: LayoutAlgorithm): void {
    this.previousLayout = this.layout();
    this.layout.set(algo);
    this.pendingLayout = algo;
    const gen = ++this.layoutGen;
    this.computing.set(true);
    try {
      // Snapshot the current rendered positions as the animation start.
      const from = new Map<string, { x: number; y: number }>();
      for (const n of this.nodes()) from.set(n.id, { ...(this.animated.get(n.id) ?? n.position) });
      // Yield one frame so the spinner paints, then run the layout.
      setTimeout(() => {
        if (gen !== this.layoutGen) { this.computing.set(false); return; } // cancelled
        try {
          this.layoutNonce.update(v => v + 1);
          const target = this.nodes(); // force evaluation while the spinner is visible
          this.animateTo(from, target);
          this.fit();
        } catch (e) {
          console.error('[graph] setLayout failed', e);
        } finally {
          this.computing.set(false);
          this.pendingLayout = null;
        }
      }, 16);
    } catch (e) {
      console.error('[graph] setLayout snapshot failed', e);
      this.computing.set(false);
      this.pendingLayout = null;
    }
  }

  // animatedPos is bound to every node position: during an animation it
  // returns the interpolated value, otherwise the node's target position.
  animatedPos(node: GraphNode): { x: number; y: number } {
    this.animTick();
    return this.animated.get(node.id) ?? node.position;
  }

  // Animate nodes from their previous positions to the new layout with an
  // ease-out, so the user sees them arrange themselves.
  private animateTo(from: Map<string, { x: number; y: number }>, target: GraphNode[]): void {
    if (this.animId !== null) cancelAnimationFrame(this.animId);
    const start = performance.now();
    const dur = 480;
    const step = (now: number) => {
      const t = Math.min(1, (now - start) / dur);
      const e = 1 - Math.pow(1 - t, 3); // easeOutCubic
      this.animated.clear();
      for (const n of target) {
        const f = from.get(n.id) ?? n.position;
        this.animated.set(n.id, {
          x: f.x + (n.position.x - f.x) * e,
          y: f.y + (n.position.y - f.y) * e,
        });
      }
      this.animTick.update(v => v + 1);
      if (t < 1) {
        this.animId = requestAnimationFrame(step);
      } else {
        this.animated.clear();
        this.animTick.update(v => v + 1);
        this.animId = null;
        // Fit once more against the final positions: the early fit in
        // setLayout() measures the animation start, which would leave
        // position-driven layouts (force/stress) zoomed far out.
        this.fit();
      }
    };
    this.animId = requestAnimationFrame(step);
  }

  cancelLayout(): void {
    this.layoutGen++; // invalidate any pending layout run
    if (this.pendingLayout) {
      this.layout.set(this.previousLayout);
      this.layoutNonce.update(v => v + 1);
    }
    this.pendingLayout = null;
    this.computing.set(false);
    this.fit();
  }

  cleanup(): void {
    this.manualPositions = {};
    this.persistPositions({});
    this.layoutNonce.update(v => v + 1);
    setTimeout(() => this.fit(), 100);
  }

  moveNode(id: string, pos: { x: number; y: number }): void {
    this.manualPositions[id] = { x: pos.x, y: pos.y };
    this.persistPositions(this.manualPositions);
  }

  openContext(ev: MouseEvent, node?: GraphNode): void {
    ev.preventDefault();
    ev.stopPropagation();
    this.contextMenu.set({ x: ev.clientX, y: ev.clientY, node });
  }

  closeContext(): void {
    this.contextMenu.set(null);
  }

  saveTemplate(name?: string): void {
    const chosen = (name ?? '').trim();
    if (!chosen) {
      this.newTemplateName = `Layout ${new Date().toLocaleString()}`;
      this.settingsOpen.set(true);
      this.closeContext();
      return;
    }
    const nodes = this.nodes()
      .filter(n => n.kind !== 'ghost')
      .map(n => ({ id: n.id, kind: n.kind, title: n.title, position: { x: n.position.x, y: n.position.y } }));
    if (!nodes.length) return;
    const tpl: LayoutTemplate = { id: `tpl-${Date.now()}`, name: chosen, createdAt: new Date().toISOString(), nodes };
    const next = [tpl, ...this.layoutTemplates()];
    this.layoutTemplates.set(next);
    this.persistTemplates(next);
    this.newTemplateName = '';
    this.message.set(`Template "${chosen}" saved`);
  }

  applyTemplateById(id: string): void {
    const tpl = this.layoutTemplates().find(t => t.id === id);
    if (tpl) this.applyTemplate(tpl);
    this.closeContext();
  }

  applyTemplate(tpl: LayoutTemplate): void {
    const real = this.nodes().filter(n => n.kind !== 'ghost');
    const byId = new Map(real.map(n => [n.id, n]));
    const tplById = new Map(tpl.nodes.map(n => [n.id, n]));
    const maxY = tpl.nodes.reduce((m, n) => Math.max(m, n.position.y), 0);
    let extra = 0;
    const positions: Record<string, { x: number; y: number }> = {};
    for (const n of real) {
      const p = tplById.get(n.id);
      if (p) {
        positions[n.id] = { ...p.position };
      } else {
        positions[n.id] = { x: (extra % 4) * 320, y: maxY + 260 + Math.floor(extra / 4) * 210 };
        extra++;
      }
    }
    this.manualPositions = positions;
    this.persistPositions(positions);
    const missing = tpl.nodes.filter(n => !byId.has(n.id)).map(n => ({
      id: n.id, kind: n.kind, title: n.title, position: n.position, templateId: tpl.id, active: false,
    }));
    this.ghosts.set(missing);
    this.persistGhosts(missing);
    this.layout.set('free');
    this.layoutNonce.update(v => v + 1);
    this.message.set(`Template "${tpl.name}" applied${missing.length ? ` · ${missing.length} placeholder(s)` : ''}`);
    setTimeout(() => this.fit(), 120);
  }

  deleteTemplate(id: string): void {
    const next = this.layoutTemplates().filter(t => t.id !== id);
    this.layoutTemplates.set(next);
    this.persistTemplates(next);
  }

  toggleGhost(id: string): void {
    const next = this.ghosts().map(g => g.id === id ? { ...g, active: !g.active } : g);
    this.ghosts.set(next);
    this.persistGhosts(next);
    this.closeContext();
  }

  removeGhost(id: string): void {
    const next = this.ghosts().filter(g => g.id !== id);
    this.ghosts.set(next);
    this.persistGhosts(next);
    this.closeContext();
  }

  resetNodePosition(id: string): void {
    delete this.manualPositions[id];
    this.persistPositions(this.manualPositions);
    this.layoutNonce.update(v => v + 1);
    this.closeContext();
  }

  deleteCustomEdgesFor(id: string): void {
    const next = this.customEdges().filter(e => e.source !== id && e.target !== id);
    this.customEdges.set(next);
    this.persistCustomEdges(next);
    this.closeContext();
  }


  openConnectorDialogByNode(nodeId: string): void {
    const a = this.agents.find(x => x.id === nodeId.replace(/^agent-/, ''));
    if (a) this.openConnectorDialog(a);
  }

  openConnectorDialog(a: Agent): void {
    this.closeContext();
    const el = document.querySelector(`[data-agent-id="${a.id}"]`) as HTMLElement | null;
    let x = 140, y = 90;
    if (el) {
      const r = el.getBoundingClientRect();
      x = Math.max(8, Math.min(r.right + 12, window.innerWidth - 470));
      y = Math.max(8, Math.min(window.innerHeight - 560, r.top));
    }
    this.popoverPos.set({ x, y });
    this.connectorAgent.set(a);
    this.connKind = 'mcp';
    this.connName = '';
    this.connTargetAgent = '';
    this.connURL = '';
    this.connContract = '';
    this.connDirection = 'out';
    this.mcpName = '';
    this.mcpCommand = '';
    this.mcpArgs = '';
    this.mcpURL = '';
    this.mcpContract = '';
    this.mcpImportJson = '';
    this.skillName = '';
    this.skillDescription = '';
    this.skillSource = '';
    this.skillConfig = '';
    this.gateExpression = '';
    this.gateTrue = '';
    this.gateFalse = '';
    this.gateCommand = '';
    this.gateCwd = '';
    this.gateTimeout = 300;
    this.gateReview = false;
    this.gateHuman = false;
    this.connectorsOpen.set(true);
    this.loadAgentConfig(a.id);
  }

  addConnector(): void {
    const a = this.connectorAgent();
    if (!a) return;
    const name = this.connName.trim();
    if (!name) { this.schemaError.set('connector name is required'); return; }
    if (this.connKind === 'agent-chain' && !this.connTargetAgent) {
      this.schemaError.set('target agent is required for an agent chain');
      return;
    }
    let contractRaw = this.connContract.trim();
    if (this.connKind === 'gate') {
      const contract: Record<string, unknown> = {
        expression: this.gateExpression.trim(),
        on_true: this.gateTrue.trim() || 'allow',
        on_false: this.gateFalse.trim() || 'deny',
      };
      if (this.gateCommand.trim()) contract['command'] = this.gateCommand.trim();
      if (this.gateCwd.trim()) contract['cwd'] = this.gateCwd.trim();
      if (this.gateTimeout) contract['timeout_sec'] = Number(this.gateTimeout) || 300;
      if (this.gateReview) contract['review_required'] = true;
      if (this.gateHuman) contract['human_approval_required'] = true;
      contractRaw = JSON.stringify(contract, null, 2);
    }
    if (contractRaw) {
      const parsed = this.parseJsonSafe(contractRaw);
      if (parsed === null) { this.schemaError.set('contract is not valid JSON'); return; }
      if (this.connKind === 'mcp') {
        const contract = this.contractFromMcpResponse(parsed) ?? this.contractFromMcpServer(name, parsed);
        if (!contract) { this.schemaError.set('not a recognizable MCP contract'); return; }
        const errors = this.validateMcpContract(contract);
        if (errors.length) { this.schemaError.set(errors.join('; ')); return; }
        contractRaw = JSON.stringify(contract, null, 2);
      }
    }
    const cfg: ConnectorConfig = {
      id: `conn-${Date.now()}`, agentId: a.id, kind: this.connKind, name,
      targetAgentId: this.connTargetAgent || undefined, url: this.connURL.trim() || undefined,
      contract: contractRaw || undefined, direction: this.connDirection, enabled: true,
    };
    const next = [...this.connectors(), cfg];
    this.connectors.set(next);
    this.persistConnectors(next);
    if (this.pendingDropPosition) {
      this.manualPositions[`connector-${cfg.id}`] = this.pendingDropPosition;
      this.persistPositions(this.manualPositions);
      this.pendingDropPosition = null;
      this.pendingDropKind = null;
      this.layoutNonce.update(v => v + 1);
    }
    this.connName = '';
    this.connContract = '';
    this.connURL = '';
    this.schemaError.set('');
  }

  deleteConnector(id: string): void {
    const next = this.connectors().filter(c => c.id !== id);
    this.connectors.set(next);
    this.persistConnectors(next);
  }

  toggleConnector(id: string): void {
    const next = this.connectors().map(c => c.id === id ? { ...c, enabled: !c.enabled } : c);
    this.connectors.set(next);
    this.persistConnectors(next);
  }

  connectorsFor(agentId: string): ConnectorConfig[] {
    return this.connectors().filter(c => c.agentId === agentId);
  }


  importMcpServers(): void {
    const parsed = this.parseJsonSafe(this.mcpImportJson);
    if (!parsed) { this.schemaError.set('invalid JSON'); return; }
    const servers = (parsed as Record<string, unknown>)['mcpServers'];
    if (!servers || typeof servers !== 'object') {
      this.schemaError.set('expected {"mcpServers": {"name": {...}}}');
      return;
    }
    const a = this.connectorAgent();
    if (!a) { this.configError.set('select an agent first'); return; }
    let added = 0;
    for (const [name, value] of Object.entries(servers as Record<string, unknown>)) {
      const c = this.contractFromMcpServer(name, value);
      if (!c) continue;
      this.api.addMCP(a.id, {
        name: c.name, transport: c.transport, command: c.command, args: c.args, url: c.url,
        env_keys: c.envKeys || [], contract: JSON.stringify(c), enabled: true,
      }).subscribe({
        next: () => { added++; this.loadAgentConfig(a.id); },
        error: e => this.configError.set(e.message ?? String(e)),
      });
    }
    this.configError.set(added || true ? 'MCP import gestartet' : 'no valid mcpServers entries');
  }

  importMcpResponse(): void {
    const parsed = this.parseJsonSafe(this.mcpImportJson);
    if (!parsed) { this.schemaError.set('invalid JSON'); return; }
    const contract = this.contractFromMcpResponse(parsed);
    if (!contract) { this.schemaError.set('could not derive an MCP contract (expected initialize/tools/list result)'); return; }
    this.mcpName = contract.name;
    this.mcpTransport = contract.transport;
    this.mcpURL = contract.url || '';
    this.mcpCommand = contract.command || '';
    this.mcpArgs = (contract.args || []).join('\n');
    this.mcpContract = JSON.stringify(contract, null, 2);
    this.configError.set('');
  }

  validateContract(): void {
    const parsed = this.parseJsonSafe(this.mcpContract || this.connContract);
    if (!parsed) { this.configError.set('contract is not valid JSON'); return; }
    const contract = this.contractFromMcpResponse(parsed) ?? this.contractFromMcpServer(this.mcpName || 'mcp', parsed);
    if (!contract) { this.configError.set('not a recognizable MCP contract'); return; }
    const errors = this.validateMcpContract(contract);
    this.configError.set(errors.length ? errors.join('; ') : '');
    if (!errors.length) this.mcpContract = JSON.stringify(contract, null, 2);
  }

  private parseJsonSafe(raw: string): unknown | null {
    try { return JSON.parse(raw); } catch { return null; }
  }

  private contractFromMcpServer(name: string, value: unknown): McpServerContract | null {
    if (!value || typeof value !== 'object') return null;
    const v = value as Record<string, unknown>;
    const url = typeof v['url'] === 'string' ? v['url'] : undefined;
    const command = typeof v['command'] === 'string' ? v['command'] : undefined;
    const args = Array.isArray(v['args']) ? v['args'].filter((a): a is string => typeof a === 'string') : [];
    const env = v['env'] && typeof v['env'] === 'object' ? Object.keys(v['env'] as Record<string, unknown>) : [];
    const transport: McpServerContract['transport'] = url ? 'http' : 'stdio';
    return { name: name || command || 'mcp', transport, command, args, url, envKeys: env, tools: [] };
  }

  private contractFromMcpResponse(value: unknown): McpServerContract | null {
    if (!value || typeof value !== 'object') return null;
    const root = value as Record<string, unknown>;
    if (typeof root['name'] === 'string' && typeof root['transport'] === 'string') {
      return root as unknown as McpServerContract;
    }
    const result = (root['result'] && typeof root['result'] === 'object' ? root['result'] : root) as Record<string, unknown>;
    const serverInfo = (result['serverInfo'] && typeof result['serverInfo'] === 'object' ? result['serverInfo'] : {}) as Record<string, unknown>;
    const toolsRaw = Array.isArray(result['tools']) ? result['tools'] : Array.isArray(root['tools']) ? root['tools'] : [];
    const tools: McpToolContract[] = [];
    for (const t of toolsRaw) {
      if (!t || typeof t !== 'object') continue;
      const tool = t as Record<string, unknown>;
      const name = typeof tool['name'] === 'string' ? tool['name'] : '';
      if (!name) continue;
      const schema = tool['inputSchema'];
      tools.push({
        name,
        description: typeof tool['description'] === 'string' ? tool['description'] : undefined,
        inputSchema: schema && typeof schema === 'object' ? schema as Record<string, unknown> : { type: 'object' },
      });
    }
    const capabilities = result['capabilities'] && typeof result['capabilities'] === 'object' ? result['capabilities'] as Record<string, unknown> : undefined;
    const protocolVersion = typeof result['protocolVersion'] === 'string' ? result['protocolVersion'] : undefined;
    const name = typeof serverInfo['name'] === 'string' ? serverInfo['name'] : (this.connName || 'mcp');
    if (!tools.length && !capabilities && !protocolVersion && !serverInfo['name']) return null;
    return {
      name, transport: this.connURL ? 'http' : 'stdio', url: this.connURL || undefined,
      protocolVersion, capabilities, tools, args: [], envKeys: [],
    };
  }

  private validateMcpContract(contract: McpServerContract): string[] {
    const errors: string[] = [];
    if (!contract.name) errors.push('name is required');
    if (!['stdio', 'http', 'sse'].includes(contract.transport)) errors.push('transport must be stdio|http|sse');
    if (contract.transport === 'stdio' && !contract.command) errors.push('stdio requires command');
    if ((contract.transport === 'http' || contract.transport === 'sse') && !contract.url) errors.push(`${contract.transport} requires url`);
    for (const tool of contract.tools ?? []) {
      if (!tool.name) errors.push('tool.name is required');
      if (!tool.inputSchema || typeof tool.inputSchema !== 'object') {
        errors.push(`tool ${tool.name || '?'}: inputSchema must be an object`);
        continue;
      }
      const t = tool.inputSchema['type'];
      if (t !== undefined && typeof t !== 'string') errors.push(`tool ${tool.name}: inputSchema.type must be a string`);
      const props = tool.inputSchema['properties'];
      if (props !== undefined && (!props || typeof props !== 'object')) errors.push(`tool ${tool.name}: inputSchema.properties must be an object`);
      const required = tool.inputSchema['required'];
      if (required !== undefined && (!Array.isArray(required) || required.some(r => typeof r !== 'string'))) {
        errors.push(`tool ${tool.name}: inputSchema.required must be a string array`);
      }
    }
    return errors;
  }


  applyDefaults(): void {
    const a = this.connectorAgent();
    if (!a) return;
    this.configBusy.set(true);
    this.api.applyTypeDefaults(a.id).subscribe({
      next: r => {
        this.configBusy.set(false);
        this.configError.set('');
        this.message.set(`${r.mcp_added} MCP, ${r.skills_added} Skills aus Typ-Defaults übernommen`);
        this.loadAgentConfig(a.id);
        this.changed.emit();
      },
      error: e => { this.configBusy.set(false); this.configError.set(e.message ?? String(e)); },
    });
  }

  agentProjects(agentId: string): string[] {
    const names = this.grants
      .filter(g => g.agent_id === agentId && g.status !== 'revoked')
      .map(g => this.workspaces.find(w => w.id === g.workspace_id)?.name)
      .filter((n): n is string => !!n);
    return [...new Set(names)];
  }

  private loadAgentConfig(agentId: string): void {
    this.configError.set('');
    this.mcpServers.set([]);
    this.skills.set([]);
    this.api.listMCP(agentId).subscribe({
      next: list => this.mcpServers.set(list ?? []),
      error: e => this.configError.set(e.message ?? String(e)),
    });
    this.api.listSkills(agentId).subscribe({
      next: list => this.skills.set(list ?? []),
      error: e => this.configError.set(e.message ?? String(e)),
    });
  }

  addMcpServer(): void {
    const a = this.connectorAgent();
    if (!a) return;
    if (!this.mcpName.trim()) { this.configError.set('MCP name is required'); return; }
    let contract = this.mcpContract.trim();
    if (contract) {
      const parsed = this.parseJsonSafe(contract);
      if (parsed === null) { this.configError.set('contract is not valid JSON'); return; }
      const normal = this.contractFromMcpResponse(parsed) ?? this.contractFromMcpServer(this.mcpName, parsed);
      if (!normal) { this.configError.set('not a recognizable MCP contract'); return; }
      const errors = this.validateMcpContract(normal);
      if (errors.length) { this.configError.set(errors.join('; ')); return; }
      contract = JSON.stringify(normal, null, 2);
    }
    this.configBusy.set(true);
    this.api.addMCP(a.id, {
      name: this.mcpName.trim(), transport: this.mcpTransport,
      command: this.mcpCommand.trim(), args: this.mcpArgs.split('\n').map(s => s.trim()).filter(Boolean),
      url: this.mcpURL.trim(), contract, env_keys: [], enabled: true,
    }).subscribe({
      next: created => {
        this.configBusy.set(false);
        this.finishDraft('mcp', created.id);
        this.mcpName = ''; this.mcpCommand = ''; this.mcpArgs = ''; this.mcpURL = ''; this.mcpContract = '';
        this.loadAgentConfig(a.id);
      },
      error: e => { this.configBusy.set(false); this.configError.set(e.message ?? String(e)); },
    });
  }

  toggleMcp(m: AgentMCP): void {
    const a = this.connectorAgent();
    if (!a) return;
    this.api.updateMCP(a.id, m.id, { ...m, enabled: !m.enabled }).subscribe({
      next: () => this.loadAgentConfig(a.id),
      error: e => this.configError.set(e.message ?? String(e)),
    });
  }

  deleteMcp(m: AgentMCP): void {
    const a = this.connectorAgent();
    if (!a) return;
    this.api.deleteMCP(a.id, m.id).subscribe({
      next: () => { this.loadAgentConfig(a.id); this.changed.emit(); },
      error: e => this.configError.set(e.message ?? String(e)),
    });
  }

  addSkill(): void {
    const a = this.connectorAgent();
    if (!a) return;
    if (!this.skillName.trim()) { this.configError.set('skill name is required'); return; }
    if (this.skillConfig.trim()) {
      const parsed = this.parseJsonSafe(this.skillConfig);
      if (parsed === null) { this.configError.set('skill config is not valid JSON'); return; }
    }
    this.configBusy.set(true);
    this.api.addSkill(a.id, {
      name: this.skillName.trim(), description: this.skillDescription.trim(),
      source_path: this.skillSource.trim(), config: this.skillConfig.trim(), enabled: true,
    }).subscribe({
      next: created => {
        this.configBusy.set(false);
        this.finishDraft('skill', created.id);
        this.skillName = ''; this.skillDescription = ''; this.skillSource = ''; this.skillConfig = '';
        this.loadAgentConfig(a.id);
      },
      error: e => { this.configBusy.set(false); this.configError.set(e.message ?? String(e)); },
    });
  }

  toggleSkill(s: AgentSkill): void {
    const a = this.connectorAgent();
    if (!a) return;
    this.api.updateSkill(a.id, s.id, { ...s, enabled: !s.enabled }).subscribe({
      next: () => this.loadAgentConfig(a.id),
      error: e => this.configError.set(e.message ?? String(e)),
    });
  }

  deleteSkill(s: AgentSkill): void {
    const a = this.connectorAgent();
    if (!a) return;
    this.api.deleteSkill(a.id, s.id).subscribe({
      next: () => { this.loadAgentConfig(a.id); this.changed.emit(); },
      error: e => this.configError.set(e.message ?? String(e)),
    });
  }

  private mcpNodes(base: GraphNode[]): GraphNode[] {
    const byId = new Map(base.map(n => [n.id, n]));
    const out: GraphNode[] = [];
    const count = new Map<string, number>();
    for (const m of this.mcpServers()) {
      const agent = byId.get(`agent-${m.agent_id}`);
      if (!agent) continue;
      const i = count.get(m.agent_id) ?? 0;
      count.set(m.agent_id, i + 1);
      out.push({
        id: `mcp-${m.id}`, kind: 'mcp', title: m.name, subtitle: `MCP · ${m.transport}`,
        detail: m.url || (m.contract ? this.contractSummary(m.contract) : (m.command || '')),
        position: { x: agent.position.x + 320, y: agent.position.y + 200 + i * 130 },
      });
    }
    return out;
  }

  private skillNodes(base: GraphNode[]): GraphNode[] {
    const byId = new Map(base.map(n => [n.id, n]));
    const out: GraphNode[] = [];
    const count = new Map<string, number>();
    for (const sk of this.skills()) {
      const agent = byId.get(`agent-${sk.agent_id}`);
      if (!agent) continue;
      const i = count.get(sk.agent_id) ?? 0;
      count.set(sk.agent_id, i + 1);
      out.push({
        id: `skill-${sk.id}`, kind: 'skill', title: sk.name, subtitle: 'skill',
        detail: sk.description || sk.source_path || '',
        position: { x: agent.position.x - 320, y: agent.position.y + 200 + i * 130 },
      });
    }
    return out;
  }

  private dbEdges(): GraphEdge[] {
    const edges: GraphEdge[] = [];
    for (const m of this.mcpServers()) {
      const src = this.pickPort(`agent-${m.agent_id}`, 'out', 'right');
      const tgt = this.pickPort(`mcp-${m.id}`, 'in', 'left');
      if (src && tgt) edges.push({ id: `mcp-edge-${m.id}`, source: `agent-${m.agent_id}`, target: `mcp-${m.id}`, sourcePort: src.id, targetPort: tgt.id, label: m.name, kind: 'mcp', sourceSide: src.side, targetSide: tgt.side, type: 'bezier' });
    }
    for (const sk of this.skills()) {
      const src = this.pickPort(`agent-${sk.agent_id}`, 'out', 'left');
      const tgt = this.pickPort(`skill-${sk.id}`, 'in', 'right');
      if (src && tgt) edges.push({ id: `skill-edge-${sk.id}`, source: `agent-${sk.agent_id}`, target: `skill-${sk.id}`, sourcePort: src.id, targetPort: tgt.id, label: 'skill', kind: 'custom', sourceSide: src.side, targetSide: tgt.side, type: 'bezier' });
    }
    return edges;
  }

  private loadConnectors(): ConnectorConfig[] {
    try { return JSON.parse(localStorage.getItem('dockweave.agentConnectors') || '[]'); } catch { return []; }
  }

  private persistConnectors(v: ConnectorConfig[]): void {
    try { localStorage.setItem('dockweave.agentConnectors', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  private connectorNodes(base: GraphNode[]): GraphNode[] {
    const byId = new Map(base.map(n => [n.id, n]));
    const out: GraphNode[] = [];
    const used = new Map<string, number>();
    for (const c of this.connectors()) {
      if (!c.enabled || c.kind === 'agent-chain') continue;
      if ((c.kind === 'mcp' || c.kind === 'skill') && !c.draft) continue;
      const agent = byId.get(`agent-${c.agentId}`);
      if (!agent) continue;
      const idx = used.get(c.agentId) ?? 0;
      used.set(c.agentId, idx + 1);
      out.push({
        id: `connector-${c.id}`,
        kind: c.kind === 'custom' ? 'custom' : c.kind,
        title: c.name,
        subtitle: c.kind,
        detail: c.kind === 'gate' && c.contract ? this.gateSummary(c.contract) : (c.url || (c.contract ? this.contractSummary(c.contract) : '')),
        position: { x: agent.position.x + 320, y: agent.position.y + idx * 130 },
      });
    }
    return out;
  }

  contractSummary(raw: string): string {
    try {
      const c = JSON.parse(raw) as McpServerContract;
      const n = c.tools?.length ?? 0;
      return `JSON contract · ${n} tool${n === 1 ? '' : 's'}`;
    } catch {
      return 'JSON contract';
    }
  }

  private connectorEdges(): GraphEdge[] {
    const edges: GraphEdge[] = [];
    for (const c of this.connectors()) {
      if (!c.enabled || c.kind === 'mcp' || c.kind === 'skill' || c.connected === false) continue;
      if (c.kind === 'agent-chain') {
        if (!c.targetAgentId) continue;
        const src = this.pickPort(`agent-${c.agentId}`, 'out', 'right');
        const tgt = this.pickPort(`agent-${c.targetAgentId}`, 'in', 'left');
        if (src && tgt) edges.push({
          id: `chain-${c.id}`, source: `agent-${c.agentId}`, target: `agent-${c.targetAgentId}`,
          sourcePort: src.id, targetPort: tgt.id, label: c.name || 'chain', kind: 'chain',
          sourceSide: src.side, targetSide: tgt.side, type: 'bezier',
        });
        continue;
      }
      const connId = `connector-${c.id}`;
      if (c.direction === 'in') {
        const src = this.pickPort(connId, 'out', 'right');
        const tgt = this.pickPort(`agent-${c.agentId}`, 'in', 'left');
        if (src && tgt) edges.push({ id: `conn-${c.id}`, source: connId, target: `agent-${c.agentId}`, sourcePort: src.id, targetPort: tgt.id, label: c.name, kind: c.kind, sourceSide: src.side, targetSide: tgt.side, type: 'bezier' });
      } else {
        const src = this.pickPort(`agent-${c.agentId}`, 'out', 'right');
        const tgt = this.pickPort(connId, 'in', 'left');
        if (src && tgt) edges.push({ id: `conn-${c.id}`, source: `agent-${c.agentId}`, target: connId, sourcePort: src.id, targetPort: tgt.id, label: c.name, kind: c.kind, sourceSide: src.side, targetSide: tgt.side, type: 'bezier' });
      }
    }
    return edges;
  }

  private loadTemplates(): LayoutTemplate[] {
    try { return JSON.parse(localStorage.getItem('dockweave.layoutTemplates') || '[]'); } catch { return []; }
  }

  private persistTemplates(v: LayoutTemplate[]): void {
    try { localStorage.setItem('dockweave.layoutTemplates', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  private loadGhosts(): GhostNode[] {
    try { return JSON.parse(localStorage.getItem('dockweave.graphGhosts') || '[]'); } catch { return []; }
  }

  private persistGhosts(v: GhostNode[]): void {
    try { localStorage.setItem('dockweave.graphGhosts', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  openSettings(): void {
    this.popoverPos.set({ x: Math.max(8, window.innerWidth - 700), y: 64 });
    this.schemaJson = JSON.stringify(this.schema(), null, 2);
    this.schemaError.set('');
    this.settingsOpen.set(true);
    this.loadGateSyncStatus();
    this.loadGateHealth();
  }

  applySchemaJson(): void {
    try {
      const parsed = JSON.parse(this.schemaJson) as GraphSchema;
      if (!parsed.nodePorts || !parsed.connectionTypes) throw new Error('nodePorts and connectionTypes are required');
      this.schema.set(parsed);
      this.persistSchema(parsed);
      this.schemaError.set('');
      this.layoutNonce.update(v => v + 1);
    } catch (e) {
      this.schemaError.set(e instanceof Error ? e.message : String(e));
    }
  }

  resetSchema(): void {
    this.schema.set(structuredClone(DEFAULT_GRAPH_SCHEMA));
    this.persistSchema(this.schema());
    this.schemaJson = JSON.stringify(this.schema(), null, 2);
    this.schemaError.set('');
    this.layoutNonce.update(v => v + 1);
  }

  downloadSchema(): void {
    const blob = new Blob([JSON.stringify(this.schema(), null, 2)], { type: 'application/json' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = 'dockweave-agent-graph.schema.json';
    a.click();
    URL.revokeObjectURL(a.href);
  }

  loadSchemaFile(ev: Event): void {
    const input = ev.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    file.text().then(text => { this.schemaJson = text; this.applySchemaJson(); }).catch(() => this.schemaError.set('read failed'));
    input.value = '';
  }

  private loadSchema(): GraphSchema {
    try {
      const raw = localStorage.getItem('dockweave.graphSchema');
      if (raw) return JSON.parse(raw);
    } catch { /* ignore */ }
    return structuredClone(DEFAULT_GRAPH_SCHEMA);
  }

  private persistSchema(v: GraphSchema): void {
    try { localStorage.setItem('dockweave.graphSchema', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  private loadCustomEdges(): CustomEdge[] {
    try { return JSON.parse(localStorage.getItem('dockweave.customEdges') || '[]'); } catch { return []; }
  }

  private persistCustomEdges(v: CustomEdge[]): void {
    try { localStorage.setItem('dockweave.customEdges', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  private loadPositions(): Record<string, { x: number; y: number }> {
    try { return JSON.parse(localStorage.getItem('dockweave.graphPositions') || '{}'); } catch { return {}; }
  }

  private persistPositions(v: Record<string, { x: number; y: number }>): void {
    try { localStorage.setItem('dockweave.graphPositions', JSON.stringify(v)); } catch { /* ignore */ }
    this.scheduleGraphSave();
  }

  // Derive a node kind from its deterministic id prefix, without touching the
  // `nodes` computed. Needed because port construction is called from the edge
  // builders, which are themselves called from the layout inside `nodes` — a
  // lookup through this.nodes() there would create a circular dependency.
  private kindOfId(id: string): NodeKind | null {
    if (id === 'org-root') return 'org';
    if (id === 'knowledge') return 'knowledge';
    if (id.startsWith('unit-')) return 'unit';
    if (id.startsWith('agent-')) return 'agent';
    if (id.startsWith('workspace-')) return 'workspace';
    if (id.startsWith('memory-')) return 'memory';
    if (id.startsWith('credential-')) return 'credential';
    if (id.startsWith('mcp-')) return 'mcp';
    if (id.startsWith('skill-')) return 'skill';
    if (id.startsWith('connector-')) {
      const c = this.connectors().find(x => 'connector-' + x.id === id);
      if (c) return (c.kind === 'custom' ? 'custom' : c.kind) as NodeKind;
      return 'custom';
    }
    if (id.startsWith('ghost-')) return 'ghost';
    return null;
  }

  private buildPorts(id: string, kind: NodeKind): GraphPort[] {
    if (kind === 'ghost') return [];
    const spec = this.schema().nodePorts[kind] ?? DEFAULT_GRAPH_SCHEMA.nodePorts[kind];
    if (!spec) return [];
    const out: GraphPort[] = [];
    for (const side of this.sides) {
      const counts = spec[side];
      for (let i = 0; i < counts.inputs; i++) {
        const pos = this.portPosition(side, i, counts.inputs);
        out.push({ id: `${id}:in:${side}:${i}`, type: 'in', side, ...pos });
      }
      for (let i = 0; i < counts.outputs; i++) {
        const pos = this.portPosition(side, i, counts.outputs);
        out.push({ id: `${id}:out:${side}:${i}`, type: 'out', side, ...pos });
      }
    }
    return out;
  }

  portsForId(nodeId: string, type?: 'in' | 'out'): GraphPort[] {
    const kind = this.kindOfId(nodeId);
    if (!kind) return [];
    const all = this.buildPorts(nodeId, kind);
    return type ? all.filter(p => p.type === type) : all;
  }

  portsFor(node: GraphNode): GraphPort[] {
    const schemaPorts = this.buildPorts(node.id, node.kind);
    const dynamic = this.dynamicPorts().byNode.get(node.id) ?? [];
    return dynamic.length ? [...schemaPorts, ...dynamic] : schemaPorts;
  }

  private portPosition(side: Side, index: number, count: number): { left: number; top: number } {
    const pct = ((index + 1) / (count + 1)) * 100;
    switch (side) {
      case 'left': return { left: 0, top: pct };
      case 'right': return { left: 100, top: pct };
      case 'top': return { left: pct, top: 0 };
      default: return { left: pct, top: 100 };
    }
  }

  private pickPort(nodeId: string, type: 'in' | 'out', preferred: Side): { id: string; side: Side } | null {
    const ports = this.portsForId(nodeId, type);
    const match = ports.find(p => p.side === preferred) ?? ports[0];
    return match ? { id: match.id, side: match.side } : null;
  }

  private pickPortAt(nodeId: string, type: 'in' | 'out', preferred: Side, index: number): { id: string; side: Side } | null {
    const ports = this.portsForId(nodeId, type).filter(p => p.side === preferred);
    const pool = ports.length ? ports : this.portsForId(nodeId, type);
    if (!pool.length) return null;
    const p = pool[index % pool.length];
    return { id: p.id, side: p.side };
  }

  applyConnectionTemplate(): void {
    const t = this.schema().templates.find(x => x.id === this.connTemplate);
    if (!t) return;
    this.connType = t.type;
    this.connLabel = t.label;
  }

  addCustomEdge(): void {
    if (!this.connSource || !this.connTarget || !this.connSourcePort || !this.connTargetPort) {
      this.schemaError.set('source, target and both ports are required');
      return;
    }
    const edge: CustomEdge = {
      id: `custom-${Date.now()}`,
      source: this.connSource,
      target: this.connTarget,
      sourcePort: this.connSourcePort,
      targetPort: this.connTargetPort,
      label: this.connLabel || this.connType,
      kind: this.connType,
      sourceSide: (this.portsForId(this.connSource).find(p => p.id === this.connSourcePort)?.side ?? 'right') as Side,
      targetSide: (this.portsForId(this.connTarget).find(p => p.id === this.connTargetPort)?.side ?? 'left') as Side,
    };
    const next = [...this.customEdges(), edge];
    this.customEdges.set(next);
    this.persistCustomEdges(next);
    this.schemaError.set('');
  }

  deleteCustomEdge(id: string): void {
    const next = this.customEdges().filter(e => e.id !== id);
    this.customEdges.set(next);
    this.persistCustomEdges(next);
  }

  // --- Layout constants: generous spacing so nodes and edges never crowd. ---
  private nodeSize(n: GraphNode): { w: number; h: number } {
    if (n.kind === 'agent') return { w: 170, h: 150 };
    if (n.kind === 'ghost') return { w: 190, h: 110 };
    return { w: 250, h: 110 };
  }

  // Layouts where nodes are free in 2D: ports must follow the neighbour
  // direction and edges may need routed detours.
  private isPositionDriven(): boolean {
    const algo = this.layout();
    return algo === 'force' || algo === 'stress' || algo === 'free' || algo === 'star';
  }

  private toLayoutNode(n: GraphNode): LayoutNode {
    const s = this.nodeSize(n);
    return { id: n.id, x: n.position.x, y: n.position.y, w: s.w, h: s.h, kind: n.kind };
  }

  // Edges and their available port sides, restricted to the node set being
  // optimised. Connector/MCP/skill nodes only take part when the full node set
  // is passed in, which the force/stress pass does in `nodes`.
  private optimizerEdges(ids: Set<string>): LayoutEdge[] {
    const sides = (id: string, type: 'in' | 'out'): Side[] =>
      [...new Set(this.portsForId(id, type).map(p => p.side))];
    return this.buildEdges()
      .filter(e => ids.has(e.source) && ids.has(e.target))
      .map(e => ({
        id: e.id,
        source: e.source,
        target: e.target,
        sourceSides: sides(e.source, 'out'),
        targetSides: sides(e.target, 'in'),
        label: e.label,
      }));
  }

  // Crossing/label/clearance refinement shared by force and stress. Both
  // layouts only optimise distances, so edges still cross unnecessarily;
  // this pass trades a little edge length for fewer crossings and edges that
  // cut through unrelated nodes.
  private refineLayout(laid: GraphNode[]): GraphNode[] {
    if (laid.length < 3) return laid;
    const ids = new Set(laid.map(n => n.id));
    const input = laid.map(n => this.toLayoutNode(n));
    const edges = this.optimizerEdges(ids);
    if (edges.length < 2) return laid;
    // perimeter: the renderer attaches each edge at the border point facing
    // its partner, so measure the same geometry here.
    const result = optimizeLayout(input, edges, { perimeter: true });
    return laid.map(n => {
      const pos = result.positions.get(n.id);
      return pos ? { ...n, position: pos } : n;
    });
  }

  private applyLayout(nodes: GraphNode[]): GraphNode[] {
    const algo = this.layout();
    const key = this.mode() + '|' + this.layout() + '|' + this.schema().version + '|' +
      nodes.map(n => n.kind + ':' + n.id).sort().join(',');
    let laid: GraphNode[];
    try {
      if (algo !== 'free' && this.layoutCacheKey === key) {
        // Structure unchanged: reuse positions so periodic data refreshes do not
        // re-run an expensive layout.
        laid = nodes.map(n => ({ ...n, position: this.layoutCache.get(n.id) ?? n.position }));
      } else if (algo === 'free') {
        laid = nodes;
      } else if (algo === 'hierarchy') {
        laid = this.layeredLayout(nodes);
      } else if (algo === 'mixed') {
        laid = this.mode() === 'organization' ? this.layeredLayout(nodes) : this.columnLayout(nodes);
      } else if (algo === 'columns') {
        laid = this.columnLayout(nodes);
      } else if (algo === 'grid') {
        laid = this.gridLayout(nodes);
      } else if (algo === 'force') {
        laid = this.forceLayout(nodes);
      } else if (algo === 'stress') {
        laid = this.stressLayout(nodes);
      } else if (algo === 'star') {
        laid = this.starLayout(nodes);
      } else {
        laid = this.radialLayout(nodes);
      }
    } catch (e) {
      // A layout must never break the whole graph: fall back to a grid and
      // surface the error in the console instead of hanging.
      console.error('[graph] layout failed, falling back to grid', e);
      laid = this.gridLayout(nodes);
    }
    if (algo !== 'free') {
      this.layoutCacheKey = key;
      this.layoutCache.clear();
      for (const n of laid) this.layoutCache.set(n.id, n.position);
    }
    const sane = laid.every(n =>
      Number.isFinite(n.position.x) && Number.isFinite(n.position.y) &&
      Math.abs(n.position.x) < 1e7 && Math.abs(n.position.y) < 1e7);
    if (!sane) laid = this.gridLayout(nodes);
    return laid.map(n => this.manualPositions[n.id] ? { ...n, position: this.manualPositions[n.id] } : n);
  }

  // Top-down layered layout: root -> units -> agents (organization) or
  // left-to-right columns (resources). Edges always point downwards/rightwards.
  private layeredLayout(nodes: GraphNode[]): GraphNode[] {
    const GX = 280, GY = 130;
    if (this.mode() === 'organization') {
      const root = nodes.filter(n => n.kind === 'org');
      const units = nodes.filter(n => n.kind === 'unit');
      const agents = nodes.filter(n => n.kind === 'agent');
      const byUnit = new Map<string, GraphNode[]>();
      for (const a of agents) byUnit.set(a.unit || 'Unassigned', [...(byUnit.get(a.unit || 'Unassigned') || []), a]);

      const out: GraphNode[] = [];
      // center the org root over all units
      const unitOrder = [...units].sort((a, b) => a.title.localeCompare(b.title));
      const totalW = Math.max(1, unitOrder.reduce((acc, u) => acc + this.maxColWidth(byUnit.get(u.title) || []), 0)
        + (unitOrder.length - 1) * GX);
      const rootY = 0;
      out.push(...root.map(r => ({ ...r, position: { x: totalW / 2, y: rootY } })));

      let x = 0;
      const unitY = 170;
      for (const u of unitOrder) {
        const members = (byUnit.get(u.title) || []).sort((a, b) => a.title.localeCompare(b.title));
        const colW = this.maxColWidth(members);
        out.push({ ...u, position: { x: x + colW / 2, y: unitY } });
        members.forEach((a, j) => {
          out.push({ ...a, position: { x: x + colW / 2, y: unitY + 130 + j * (this.nodeSize(a).h + GY) } });
        });
        x += colW + GX;
      }
      return out;
    }
    // resources: agents column on the left, resources to the right
    return this.columnLayout(nodes);
  }

  private maxColWidth(members: GraphNode[]): number {
    return Math.max(this.nodeSize({ kind: 'agent' } as GraphNode).w, members.length ? 250 : 170);
  }

  // Columns by kind, ordered with the barycenter heuristic (Sugiyama) so that
  // edges between the agent column and the resource columns cross as little as
  // possible.
  private columnLayout(nodes: GraphNode[]): GraphNode[] {
    const order: NodeKind[] = ['org', 'unit', 'agent', 'workspace', 'memory', 'credential', 'knowledge'];
    const groups = new Map<NodeKind, GraphNode[]>();
    for (const kind of order) groups.set(kind, []);
    for (const n of nodes) groups.get(n.kind)?.push(n);

    // adjacency among these nodes, used for barycenter ordering
    const ids = new Set(nodes.map(n => n.id));
    const edges = this.buildEdges().filter(e => ids.has(e.source) && ids.has(e.target));
    const adj = new Map<string, string[]>();
    for (const n of nodes) adj.set(n.id, []);
    for (const e of edges) { adj.get(e.source)?.push(e.target); adj.get(e.target)?.push(e.source); }

    // stable anchor order: agents sorted by name define the row indices
    const rank = new Map<string, number>();
    const agents = (groups.get('agent') || []).sort((a, b) => a.title.localeCompare(b.title));
    agents.forEach((a, i) => rank.set(a.id, i));

    const ordered: GraphNode[][] = [];
    for (const kind of order) {
      const list = groups.get(kind) || [];
      if (kind === 'org' || kind === 'unit' || kind === 'knowledge') {
        list.sort((a, b) => a.title.localeCompare(b.title));
      } else {
        list.sort((a, b) => {
          const ba = this.barycenter(a.id, adj, rank);
          const bb = this.barycenter(b.id, adj, rank);
          if (ba !== bb) return ba - bb;
          return a.title.localeCompare(b.title);
        });
      }
      ordered.push(list);
    }

    const out: GraphNode[] = [];
    let col = 0;
    const GX = 440, GY = 120;
    for (const list of ordered) {
      let y = 0;
      for (const n of list) {
        out.push({ ...n, position: { x: col * GX, y } });
        y += this.nodeSize(n).h + GY;
      }
      if (list.length) col++;
    }
    return out;
  }

  // Mean rank of the node's agent neighbours; used to order resource rows so
  // that edges to the agent column do not cross.
  private barycenter(id: string, adj: Map<string, string[]>, rank: Map<string, number>): number {
    const neigh = adj.get(id) || [];
    let sum = 0, count = 0;
    for (const m of neigh) {
      const r = rank.get(m);
      if (r !== undefined) { sum += r; count++; }
    }
    return count ? sum / count : Number.MAX_SAFE_INTEGER;
  }

  // Node-size aware grid with a natural aspect ratio.
  private gridLayout(nodes: GraphNode[]): GraphNode[] {
    const sorted = [...nodes].sort((a, b) => a.kind.localeCompare(b.kind) || a.title.localeCompare(b.title));
    const maxW = Math.max(...sorted.map(n => this.nodeSize(n).w));
    const maxH = Math.max(...sorted.map(n => this.nodeSize(n).h));
    const cw = maxW + 140, ch = maxH + 120;
    const cols = Math.max(1, Math.round(Math.sqrt(sorted.length * (cw / ch))));
    return sorted.map((n, i) => ({ ...n, position: { x: (i % cols) * cw, y: Math.floor(i / cols) * ch } }));
  }

  // Radial with a radius that guarantees no node overlap and a BFS ordering
  // that keeps connected nodes adjacent (fewer crossing edges).
  private radialLayout(nodes: GraphNode[]): GraphNode[] {
    const ids = new Set(nodes.map(n => n.id));
    const edges = this.buildEdges().filter(e => ids.has(e.source) && ids.has(e.target));
    const adj = new Map<string, string[]>();
    for (const n of nodes) adj.set(n.id, []);
    for (const e of edges) { adj.get(e.source)?.push(e.target); adj.get(e.target)?.push(e.source); }
    const order: string[] = [];
    const visited = new Set<string>();
    const start = nodes.reduce((best, n) => (adj.get(n.id)!.length > (adj.get(best?.id || '')?.length ?? -1) ? n : best), nodes[0]).id;
    const queue = [start]; visited.add(start);
    while (queue.length) {
      const cur = queue.shift()!;
      order.push(cur);
      for (const nxt of adj.get(cur) || []) if (!visited.has(nxt)) { visited.add(nxt); queue.push(nxt); }
    }
    for (const n of nodes) if (!visited.has(n.id)) { visited.add(n.id); order.push(n.id); }

    // circumference must fit every node
    const arc = nodes.reduce((acc, n) => acc + Math.max(this.nodeSize(n).w, this.nodeSize(n).h) + 40, 0);
    const r = Math.max(300, arc / (2 * Math.PI));
    const byId = new Map(nodes.map(n => [n.id, n]));
    return order.map((id, i) => {
      const a = (i / order.length) * 2 * Math.PI - Math.PI / 2;
      const n = byId.get(id)!;
      return { ...n, position: { x: Math.cos(a) * r, y: Math.sin(a) * r } };
    });
  }

  // Hub-and-spoke ("star") layout: the highest-degree nodes become hub centres
  // and their neighbours orbit them on a ring. Ring radii are sized from the
  // node dimensions and the hub circle is enlarged until no two rings can
  // overlap, so this mode is overlap-free by construction.
  private starLayout(nodes: GraphNode[]): GraphNode[] {
    if (nodes.length < 3) {
      return nodes.map((n, i) => ({ ...n, position: { x: i * 280, y: 0 } }));
    }
    const ids = new Set(nodes.map(n => n.id));
    const edges = this.buildEdges().filter(e => ids.has(e.source) && ids.has(e.target) && e.source !== e.target);
    const adj = new Map<string, Set<string>>();
    for (const n of nodes) adj.set(n.id, new Set());
    for (const e of edges) {
      adj.get(e.source)?.add(e.target);
      adj.get(e.target)?.add(e.source);
    }
    const degree = (id: string): number => adj.get(id)?.size ?? 0;
    const size = (n: GraphNode) => this.nodeSize(n);
    const half = (n: GraphNode): number => Math.max(size(n).w, size(n).h) / 2;

    // Hub selection: clearly above-average degree, capped for readability.
    const ranked = [...nodes].filter(n => degree(n.id) >= 3)
      .sort((a, b) => degree(b.id) - degree(a.id) || a.title.localeCompare(b.title));
    const avg = nodes.reduce((s, n) => s + degree(n.id), 0) / Math.max(1, nodes.length);
    const maxHubs = Math.max(1, Math.min(8, Math.round(Math.sqrt(nodes.length))));
    let hubs = ranked.filter(n => degree(n.id) >= Math.max(3, avg * 1.15)).slice(0, maxHubs);
    if (!hubs.length) hubs = ranked.slice(0, 1);
    if (!hubs.length) return this.radialLayout(nodes);
    const hubIds = new Set(hubs.map(h => h.id));

    // Assign every other node to the graph-nearest hub (multi-source BFS).
    // This also places nodes that are not directly attached to a hub (e.g. a
    // memory store whose agent sits near a hub) with the right star instead of
    // dumping them all onto the first hub.
    const ring = new Map<string, GraphNode[]>(hubs.map(h => [h.id, []]));
    const owner = new Map<string, string>();
    const hop = new Map<string, number>();
    const queue: string[] = [];
    for (const h of hubs) { owner.set(h.id, h.id); hop.set(h.id, 0); queue.push(h.id); }
    for (let qi = 0; qi < queue.length; qi++) {
      const cur = queue[qi];
      const d = hop.get(cur)!;
      for (const nb of adj.get(cur) ?? []) {
        if (hop.has(nb)) continue;
        hop.set(nb, d + 1);
        owner.set(nb, owner.get(cur)!);
        queue.push(nb);
      }
    }
    for (const n of nodes) {
      if (hubIds.has(n.id)) continue;
      const hub = owner.get(n.id) ?? hubs[0].id;
      ring.get(hub)!.push(n);
    }

    // Ring radius: the circumference must fit every satellite with a gap.
    const GAP = 72;
    const ringRadius = new Map<string, number>();
    for (const h of hubs) {
      const members = ring.get(h.id)!;
      if (!members.length) { ringRadius.set(h.id, 0); continue; }
      const circumference = members.reduce((s, m) => s + 2 * half(m) + GAP, 0);
      ringRadius.set(h.id, Math.max(
        circumference / (2 * Math.PI),
        half(h) + Math.max(...members.map(half)) + 90,
      ));
    }

    // Hub centres on a circle large enough that neighbouring rings never touch.
    const H = hubs.length;
    let hubRing = 0;
    if (H > 1) {
      let maxPair = 0;
      for (let i = 0; i < H; i++) for (let j = i + 1; j < H; j++) {
        maxPair = Math.max(maxPair, ringRadius.get(hubs[i].id)! + ringRadius.get(hubs[j].id)! + 140);
      }
      hubRing = Math.max(320, maxPair / (2 * Math.sin(Math.PI / H)));
    }
    const hubCenter = new Map<string, { x: number; y: number }>();
    hubs.forEach((h, i) => {
      const a = (i / H) * 2 * Math.PI - Math.PI / 2;
      hubCenter.set(h.id, { x: Math.cos(a) * hubRing, y: Math.sin(a) * hubRing });
    });

    const out: GraphNode[] = [];
    const place = (n: GraphNode, cx: number, cy: number): void => {
      const s = size(n);
      out.push({ ...n, position: { x: Math.round(cx - s.w / 2), y: Math.round(cy - s.h / 2) } });
    };
    for (const h of hubs) {
      const c = hubCenter.get(h.id)!;
      place(h, c.x, c.y);
    }
    for (const h of hubs) {
      const c = hubCenter.get(h.id)!;
      const members = ring.get(h.id)!;
      const r = ringRadius.get(h.id)!;
      if (!members.length) continue;
      // Order satellites by the direction of the other hub they also touch.
      // Mapping that order onto evenly spaced ring slots keeps the long
      // cross-star edges short and reduces spokes crossing each other.
      const withAngle = members.map(m => {
        let sx = 0, sy = 0, count = 0;
        for (const nb of adj.get(m.id) ?? []) {
          if (nb === h.id) continue;
          const other = hubCenter.get(nb);
          if (other) { sx += other.x - c.x; sy += other.y - c.y; count++; }
        }
        return { m, has: count > 0, angle: count ? Math.atan2(sy, sx) : 0 };
      });
      withAngle.sort((a, b) => {
        if (a.has && b.has) return a.angle - b.angle;
        if (a.has !== b.has) return a.has ? -1 : 1;
        return a.m.title.localeCompare(b.m.title);
      });
      const outward = Math.atan2(c.y, c.x) || 0;
      const step = (2 * Math.PI) / members.length;
      withAngle.forEach((entry, i) => {
        const a = outward + i * step;
        place(entry.m, c.x + Math.cos(a) * r, c.y + Math.sin(a) * r);
      });
    }
    return out;
  }

  // Fruchterman-Reingold force layout with node-size aware collision
  // resolution. Connected nodes attract, all nodes repel; a final pass
  // separates overlaps.
  private forceLayout(nodes: GraphNode[]): GraphNode[] {
    if (nodes.length < 2) return nodes.map(n => ({ ...n, position: { x: 0, y: 0 } }));
    const ids = new Set(nodes.map(n => n.id));
    const edges = this.buildEdges().filter(e => ids.has(e.source) && ids.has(e.target));
    const area = Math.max(1200, Math.sqrt(nodes.length) * 420);
    const k = area / Math.max(1, Math.sqrt(nodes.length));
    const pos = new Map<string, { x: number; y: number }>();
    const sizes = new Map<string, { w: number; h: number }>();
    for (const n of nodes) {
      const ang = Math.random() * 2 * Math.PI;
      const rr = 120 + Math.random() * 200;
      pos.set(n.id, { x: Math.cos(ang) * rr, y: Math.sin(ang) * rr });
      sizes.set(n.id, this.nodeSize(n));
    }
    const adj = new Map<string, Set<string>>();
    for (const n of nodes) adj.set(n.id, new Set());
    for (const e of edges) { adj.get(e.source)?.add(e.target); adj.get(e.target)?.add(e.source); }

    const temp = (t: number) => Math.max(1, 40 * (1 - t));
    for (let it = 0; it < 140; it++) {
      const t = it / 140;
      const disp = new Map<string, { x: number; y: number }>();
      for (const n of nodes) disp.set(n.id, { x: 0, y: 0 });
      // repulsion
      const arr = nodes;
      for (let i = 0; i < arr.length; i++) for (let j = i + 1; j < arr.length; j++) {
        const a = arr[i], b = arr[j];
        let dx = pos.get(a.id)!.x - pos.get(b.id)!.x;
        let dy = pos.get(a.id)!.y - pos.get(b.id)!.y;
        let dist = Math.hypot(dx, dy) || 0.01;
        const f = (k * k) / dist;
        dx = (dx / dist) * f; dy = (dy / dist) * f;
        disp.get(a.id)!.x += dx; disp.get(a.id)!.y += dy;
        disp.get(b.id)!.x -= dx; disp.get(b.id)!.y -= dy;
      }
      // attraction
      for (const e of edges) {
        const a = pos.get(e.source), b = pos.get(e.target);
        if (!a || !b) continue;
        const dx = a.x - b.x, dy = a.y - b.y;
        const dist = Math.hypot(dx, dy) || 0.01;
        const f = (dist * dist) / k;
        disp.get(e.source)!.x -= (dx / dist) * f; disp.get(e.source)!.y -= (dy / dist) * f;
        disp.get(e.target)!.x += (dx / dist) * f; disp.get(e.target)!.y += (dy / dist) * f;
      }
      for (const n of nodes) {
        const d = disp.get(n.id)!;
        const cur = pos.get(n.id)!;
        // Weak gravity toward the origin keeps disconnected or low-degree
        // nodes (e.g. an unmounted workspace) from being repelled into the
        // far distance, which would otherwise blow up the fit-to-screen zoom.
        d.x -= cur.x * 0.06;
        d.y -= cur.y * 0.06;
        const dl = Math.hypot(d.x, d.y) || 0.01;
        const step = Math.min(dl, temp(t));
        pos.set(n.id, { x: cur.x + (d.x / dl) * step, y: cur.y + (d.y / dl) * step });
      }
    }
    // collision resolution: separate overlapping nodes by their radii
    for (let pass = 0; pass < 8; pass++) {
      let moved = false;
      for (let i = 0; i < nodes.length; i++) for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        const sa = sizes.get(a.id)!, sb = sizes.get(b.id)!;
        const minDX = (sa.w + sb.w) / 2 + 40;
        const minDY = (sa.h + sb.h) / 2 + 40;
        const pa = pos.get(a.id)!, pb = pos.get(b.id)!;
        const dx = pa.x - pb.x, dy = pa.y - pb.y;
        if (Math.abs(dx) < minDX && Math.abs(dy) < minDY) {
          moved = true;
          if (Math.abs(dx) * minDY > Math.abs(dy) * minDX) {
            const push = Math.sign(dx || 1) * (minDX - Math.abs(dx));
            pa.x += push / 2; pb.x -= push / 2;
          } else {
            const push = Math.sign(dy || 1) * (minDY - Math.abs(dy));
            pa.y += push / 2; pb.y -= push / 2;
          }
        }
      }
      if (!moved) break;
    }
    const laid = nodes.map(n => ({ ...n, position: { x: Math.round(pos.get(n.id)!.x), y: Math.round(pos.get(n.id)!.y) } }));
    return this.refineLayout(laid);
  }

  // Constrained stress majorization (Gansner-Koren-North 2004, Dwyer 2009,
  // Wang et al. 2018): minimizes the weighted squared discrepancy to graph
  // distances and then projects onto non-overlap separation constraints.
  private stressLayout(nodes: GraphNode[]): GraphNode[] {
    if (nodes.length < 2) return nodes.map(n => ({ ...n, position: { x: 0, y: 0 } }));
    const ids = new Set(nodes.map(n => n.id));
    const edges = this.buildEdges().filter(e => ids.has(e.source) && ids.has(e.target));
    const n = nodes.length;
    const idx = new Map(nodes.map((nd, i) => [nd.id, i]));
    const sizes = nodes.map(nd => this.nodeSize(nd));

    const adj: number[][] = nodes.map(() => []);
    for (const e of edges) {
      const a = idx.get(e.source), b = idx.get(e.target);
      if (a != null && b != null) { adj[a].push(b); adj[b].push(a); }
    }
    // unweighted shortest-path distances (hops) via BFS
    const hop: number[][] = nodes.map(() => new Array(n).fill(-1));
    for (let s = 0; s < n; s++) {
      const dist = new Array(n).fill(-1); dist[s] = 0;
      const q = [s];
      for (let qi = 0; qi < q.length; qi++) {
        const u = q[qi];
        for (const v of adj[u]) if (dist[v] < 0) { dist[v] = dist[u] + 1; q.push(v); }
      }
      hop[s] = dist;
    }

    const U = 230; // ideal unit distance (node size + gap)
    const w: number[][] = nodes.map(() => new Array(n).fill(0));
    const d: number[][] = nodes.map(() => new Array(n).fill(0));
    for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
      const h = hop[i][j];
      if (h > 0) {
        d[i][j] = d[j][i] = h * U;
        w[i][j] = w[j][i] = 1 / ((h * U) * (h * U));
      } else {
        // disconnected components: mild mutual repulsion
        d[i][j] = d[j][i] = U * 3;
        w[i][j] = w[j][i] = 1 / ((U * 3) * (U * 3));
      }
    }

    let x = new Float64Array(n), y = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      const a = 2 * Math.PI * i / n;
      x[i] = Math.cos(a) * (n * U / (2 * Math.PI));
      y[i] = Math.sin(a) * (n * U / (2 * Math.PI));
    }

    const iterations = Math.min(60, 20 + n * 3);
    for (let it = 0; it < iterations; it++) {
      for (const isX of [true, false]) {
        const pos = isX ? x : y;
        const b = new Float64Array(n);
        for (let i = 0; i < n; i++) {
          for (let j = 0; j < n; j++) {
            if (i === j || w[i][j] === 0) continue;
            const dist = Math.hypot(x[i] - x[j], y[i] - y[j]) || 0.01;
            b[i] += w[i][j] * d[i][j] * (pos[i] - pos[j]) / dist;
          }
        }
        const sol = this.solveLaplacian(w, b, n);
        for (let i = 0; i < n; i++) if (isX) x[i] = sol[i]; else y[i] = sol[i];
      }
      // separation projection (hard non-overlap, with gap)
      for (let pass = 0; pass < 6; pass++) {
        let moved = false;
        for (let i = 0; i < n; i++) for (let j = i + 1; j < n; j++) {
          const minX = (sizes[i].w + sizes[j].w) / 2 + 44;
          const minY = (sizes[i].h + sizes[j].h) / 2 + 44;
          const dx = x[i] - x[j], dy = y[i] - y[j];
          if (Math.abs(dx) < minX && Math.abs(dy) < minY) {
            moved = true;
            if (Math.abs(dx) * minY > Math.abs(dy) * minX) {
              const push = Math.sign(dx || 1) * (minX - Math.abs(dx));
              x[i] += push / 2; x[j] -= push / 2;
            } else {
              const push = Math.sign(dy || 1) * (minY - Math.abs(dy));
              y[i] += push / 2; y[j] -= push / 2;
            }
          }
        }
        if (!moved) break;
      }
    }
    const laid = nodes.map((nd, i) => ({ ...nd, position: { x: Math.round(x[i]), y: Math.round(y[i]) } }));
    return this.refineLayout(laid);
  }

  // Solve the pinned weighted Laplacian system L x = b (node 0 fixed at 0)
  // with conjugate gradient. The reduced system is symmetric positive
  // definite, so CG converges quickly for our small graphs.
  private solveLaplacian(w: number[][], b: Float64Array, n: number): Float64Array {
    const sol = new Float64Array(n);
    if (n <= 1) return sol;
    const m = n - 1;
    const diag = new Float64Array(m);
    for (let u = 0; u < m; u++) {
      const i = u + 1;
      let s = 0;
      for (let j = 0; j < n; j++) if (j !== i) s += w[i][j];
      diag[u] = s;
    }
    const matvec = (v: Float64Array, out: Float64Array) => {
      for (let u = 0; u < m; u++) {
        const i = u + 1;
        let acc = diag[u] * v[u];
        for (let k = 0; k < m; k++) if (k !== u) acc -= w[i][k + 1] * v[k];
        out[u] = acc;
      }
    };
    const rhs = new Float64Array(m);
    for (let u = 0; u < m; u++) rhs[u] = b[u + 1];
    const x = new Float64Array(m);
    const r = new Float64Array(m); r.set(rhs);
    const p = new Float64Array(m); p.set(rhs);
    const Ap = new Float64Array(m);
    let rr = 0; for (let u = 0; u < m; u++) rr += r[u] * r[u];
    const tol = 1e-12;
    for (let it = 0; it < 300 && rr > tol; it++) {
      matvec(p, Ap);
      let pAp = 0; for (let u = 0; u < m; u++) pAp += p[u] * Ap[u];
      if (pAp <= 0) break;
      const alpha = rr / pAp;
      for (let u = 0; u < m; u++) x[u] += alpha * p[u];
      for (let u = 0; u < m; u++) r[u] -= alpha * Ap[u];
      let rrNew = 0; for (let u = 0; u < m; u++) rrNew += r[u] * r[u];
      const beta = rrNew / rr;
      for (let u = 0; u < m; u++) p[u] = r[u] + beta * p[u];
      rr = rrNew;
    }
    for (let u = 0; u < m; u++) sol[u + 1] = x[u];
    return sol;
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
    const nodes = this.mode() === 'organization' ? this.buildOrgNodes() : this.buildResourceNodes();
    return this.applyLayout(nodes);
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
          state: a.state, role: a.role, unit: a.org_unit, avatar: a.avatar, avatarSvg: a.avatar_svg,
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
        position: { x: 0, y: i * 190 }, state: a.state, role: a.role, unit: a.org_unit, avatar: a.avatar, avatarSvg: a.avatar_svg,
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
    const base = this.mode() === 'organization' ? this.buildOrgEdges() : this.buildResourceEdges();
    const connectors = [...this.connectorEdges(), ...this.dbEdges()];
    const custom = this.customEdges().map(e => ({
      id: e.id, source: e.source, target: e.target, sourcePort: e.sourcePort, targetPort: e.targetPort,
      label: e.label, kind: e.kind, sourceSide: e.sourceSide, targetSide: e.targetSide,
      type: 'bezier' as const,
    }));
    return [...base, ...connectors, ...custom];
  }

  private buildOrgEdges(): GraphEdge[] {
    const edges: GraphEdge[] = [];
    const units = [...new Set(this.agents.map(a => this.effective(a).org_unit || 'Unassigned'))];
    units.forEach((u, ui) => {
      const orgOut = this.pickPortAt('org-root', 'out', 'bottom', ui);
      const unitIn = this.pickPort(`unit-${slug(u)}`, 'in', 'top');
      if (orgOut && unitIn) {
        edges.push({ id: `org-unit-${slug(u)}`, source: 'org-root', target: `unit-${slug(u)}`, sourcePort: orgOut.id, targetPort: unitIn.id, label: 'unit', kind: 'org', sourceSide: orgOut.side, targetSide: unitIn.side, type: 'segment' });
      }
      const members = this.agents.map(x => this.effective(x)).filter(x => (x.org_unit || 'Unassigned') === u);
      members.forEach((a, j) => {
        const out = this.pickPortAt(`unit-${slug(u)}`, 'out', 'bottom', j);
        const inn = this.pickPort(`agent-${a.id}`, 'in', 'top');
        if (!out || !inn) return;
        edges.push({
          id: `unit-agent-${a.id}`, source: `unit-${slug(u)}`, target: `agent-${a.id}`,
          sourcePort: out.id, targetPort: inn.id,
          label: a.role || 'member', kind: 'unit', sourceSide: out.side, targetSide: inn.side, type: 'segment',
        });
      });
    });
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
      const out = this.pickPort(`agent-${g.agent_id}`, 'out', 'left');
      const inn = this.pickPort(`workspace-${ws.id}`, 'in', 'right');
      if (out && inn) edges.push({ id: `grant-${g.id}`, source: `agent-${g.agent_id}`, target: `workspace-${ws.id}`, sourcePort: out.id, targetPort: inn.id, label: g.mode, kind: g.mode === 'rw' ? 'rw' : 'ro', sourceSide: out.side, targetSide: inn.side, type: 'bezier' });
    }
    let ki = 0;
    for (const a of this.agents) {
      const p = this.paths[a.id];
      if (p?.memory_id) {
        const out = this.pickPort(`agent-${a.id}`, 'out', 'right');
        const inn = this.pickPort(`memory-${a.id}`, 'in', 'left');
        if (out && inn) edges.push({ id: `memory-edge-${a.id}`, source: `agent-${a.id}`, target: `memory-${a.id}`, sourcePort: out.id, targetPort: inn.id, label: 'memory', kind: 'memory', sourceSide: out.side, targetSide: inn.side, type: 'bezier' });
      }
      if (p?.knowledge_dir) {
        const out = this.pickPort(`agent-${a.id}`, 'out', 'bottom');
        const inn = this.pickPortAt('knowledge', 'in', 'top', ki++);
        if (out && inn) edges.push({ id: `knowledge-edge-${a.id}`, source: `agent-${a.id}`, target: 'knowledge', sourcePort: out.id, targetPort: inn.id, label: 'knowledge', kind: 'knowledge', sourceSide: out.side, targetSide: inn.side, type: 'bezier' });
      }
    }
    for (const g of this.brokerGrants) {
      const agentId = agentNameToId.get(g.agent);
      if (!agentId) continue;
      const out = this.pickPort(`agent-${agentId}`, 'out', 'right');
      const inn = this.pickPort(`credential-${g.id}`, 'in', 'left');
      if (out && inn) edges.push({ id: `credential-edge-${g.id}`, source: `agent-${agentId}`, target: `credential-${g.id}`, sourcePort: out.id, targetPort: inn.id, label: 'credential', kind: 'credential', sourceSide: out.side, targetSide: inn.side, type: 'bezier' });
    }
    return edges;
  }
}

function slug(s: string): string {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'unassigned';
}
