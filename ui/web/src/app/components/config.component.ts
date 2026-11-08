import { Component, Input, OnChanges, SimpleChanges, inject, signal } from '@angular/core';
import { NgFor } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ApiService } from '../core/api.service';
import { Store } from '../core/store';
import { Agent, AgentPaths, BrokerGrant, EgressStatus, Grant, MemoryInfo, MemorySnapshot, Workspace } from '../core/models';

@Component({
  selector: 'dw-config',
  standalone: true,
  imports: [NgFor, FormsModule],
  template: `
    <div class="config">

      <section>
        <h3>Folders · effective host paths</h3>

        <div class="folder">
          <div class="folder-head">
            <span class="mono">/agent</span>
            <span class="pill" [class.def]="!paths?.agent_dir_custom">{{ paths?.agent_dir_custom ? 'custom' : 'default' }}</span>
          </div>
          <div class="fullpath">{{ paths?.agent_dir || '—' }}</div>
          <div class="folder-edit">
            <input [(ngModel)]="agentDirInput" placeholder="custom host path (empty = default)" />
            <button class="small" (click)="applyAgentDir()" [disabled]="busy()">Apply</button>
            <button class="small" (click)="resetAgentDir()" [disabled]="busy() || !paths?.agent_dir_custom">Reset</button>
          </div>
        </div>

        <div class="folder">
          <div class="folder-head">
            <span class="mono">/memory</span>
            <span class="pill" [class.def]="!paths?.memory_custom">{{ paths?.memory_custom ? 'custom' : 'default' }}</span>
            <span class="muted">{{ paths?.memory_id || 'not attached' }}</span>
          </div>
          <div class="fullpath">{{ paths?.memory_dir || '—' }}</div>
          <div class="folder-edit">
            <select [(ngModel)]="memoryId">
              <option value="">select existing memory…</option>
              @for (m of memories(); track m.id) {
                <option [value]="m.id">{{ m.id }} · {{ m.path || 'default path' }}{{ m.owner_agent_id ? ' · owned by ' + m.owner_agent_id : '' }}</option>
              }
            </select>
            <button class="small" (click)="attachMemory()" [disabled]="busy() || !memoryId.trim()">Attach</button>
            <button class="small" (click)="detachMemory()" [disabled]="busy() || !paths?.memory_id">Detach</button>
          </div>
          <div class="folder-edit">
            <input [(ngModel)]="newMemoryPath" placeholder="new host folder → register and attach" />
            <button class="small" (click)="registerAndAttachMemory()" [disabled]="busy() || !newMemoryPath.trim()">Register &amp; attach</button>
          </div>
          <div class="muted hint">Memory must be attached/detached while the agent is stopped.</div>
        </div>

        <div class="folder">
          <div class="folder-head">
            <span class="mono">/workspace</span>
            <span class="pill def">managed</span>
          </div>
          <div class="fullpath">{{ paths?.workspace_dir || '—' }}</div>
          <div class="muted hint">Contains the project mounts. Configure it in “Project access” below (one workspace = one subdirectory).</div>
        </div>

        <div class="folder">
          <div class="folder-head">
            <span class="mono">/notes</span>
            <span class="pill" [class.def]="!paths?.notes_dir_custom">{{ paths?.notes_dir_custom ? 'custom' : 'default' }}</span>
          </div>
          <div class="fullpath">{{ paths?.notes_dir || '—' }}</div>
          <div class="folder-edit">
            <input [(ngModel)]="notesDirInput" placeholder="custom host path (empty = default)" />
            <button class="small" (click)="applyNotesDir()" [disabled]="busy()">Apply</button>
            <button class="small" (click)="resetNotesDir()" [disabled]="busy() || !paths?.notes_dir_custom">Reset</button>
          </div>
        </div>

        <div class="folder">
          <div class="folder-head">
            <span class="mono">/knowledge</span>
            <span class="pill def">global</span>
          </div>
          <div class="fullpath">{{ paths?.knowledge_dir || '—' }}</div>
          <div class="muted hint">Shared knowledge store; configured daemon-wide, not per agent.</div>
        </div>
      </section>

      <section>
        <h3>Runtime</h3>
        <div class="grid">
          <label>Type
            <select [(ngModel)]="type">
              @for (t of types; track t) { <option [value]="t">{{ t }}</option> }
            </select>
          </label>
          <label>Image <input [(ngModel)]="image" /></label>
          <label>UID <input type="number" [(ngModel)]="uid" /></label>
          <label>GID <input type="number" [(ngModel)]="gid" /></label>
          <label>Egress profile
            <select [(ngModel)]="profile">
              <option value="">unrestricted-legacy</option>
              <option value="offline">offline</option>
              <option value="broker-only">broker-only</option>
              <option value="restricted-development">restricted-development (fail closed)</option>
              <option value="unrestricted-legacy">unrestricted-legacy</option>
            </select>
          </label>
          <label>Env file <input [(ngModel)]="envFile" placeholder="/path/to/agent.env" /></label>
          <label class="wide">Command (one argv element per line; empty = image default)
            <textarea rows="4" [(ngModel)]="command"></textarea>
          </label>
        </div>
        <div class="actions">
          <button class="primary" (click)="save()" [disabled]="busy()">Save</button>
          <button (click)="start()" [disabled]="busy()">Start</button>
          <button (click)="stop()" [disabled]="busy()">Stop</button>
          <button (click)="restart()" [disabled]="busy()">Restart</button>
          <span class="spacer"></span>
          <button class="danger" (click)="remove()" [disabled]="busy()">Remove</button>
        </div>
        @if (egress) {
          <div class="notice" [class.warn]="!egress.enforced">
            egress: <strong>{{ egress.profile || 'unrestricted-legacy' }}</strong>
            · enforced={{ egress.enforced }}
            @if (egress.broker_base_url) { · broker {{ egress.broker_base_url }} }
            @if (egress.warning) { <div class="muted">{{ egress.warning }}</div> }
          </div>
        }
      </section>

      <section>
        <h3>Project access</h3>
        <div class="row" *ngFor="let w of workspaces">
          <span class="mono">{{ w.name }}</span>
          <span class="muted">{{ w.host_path }}</span>
          <span class="spacer"></span>
          @if (grantFor(w.id); as g) {
            <span class="pill">{{ g.mode }}</span>
            <button class="danger small" (click)="revoke(g.id)">unassign</button>
          } @else {
            <select [(ngModel)]="grantMode" class="small-select">
              <option value="rw">rw</option>
              <option value="ro">ro</option>
            </select>
            <button class="small" (click)="assign(w.name)">assign</button>
          }
        </div>
        @if (!workspaces.length) { <p class="muted">No workspaces registered. Use the Workspaces view.</p> }
      </section>

      <section>
        <h3>Memory snapshots</h3>
        <div class="row">
          <button class="small" (click)="snapshot()">create snapshot</button>
          <span class="muted">{{ snapshots().length }} snapshot(s)</span>
        </div>
        @for (s of snapshots(); track s.name) {
          <div class="row">
            <span class="mono">{{ s.name }}</span>
            <span class="muted">{{ s.size_bytes }} bytes · {{ s.created_at }}</span>
            <span class="spacer"></span>
            <button class="small" (click)="restore(s.name)">restore</button>
            <button class="small danger" (click)="delSnapshot(s.name)">delete</button>
          </div>
        }
      </section>

      <section>
        <h3>Credential grants</h3>
        @for (g of brokerGrants; track g.id) {
          <div class="row">
            <span class="mono">{{ g.credential }}</span>
            <span class="muted">{{ g.provider }} · models: {{ (g.models || []).join(', ') || 'none' }}</span>
          </div>
        }
        @if (!brokerGrants.length) {
          <p class="muted">No credential grants. Manage them with <code>dockweave credential</code> or the broker admin socket.</p>
        }
      </section>

      @if (message()) { <div class="notice">{{ message() }}</div> }
      @if (error()) { <div class="notice error">{{ error() }}</div> }
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .config { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; margin-bottom:1rem; }
    h3 { margin:0 0 .8rem; font-size:.95rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.06em; }
    .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(220px,1fr)); gap:.8rem; }
    label { display:flex; flex-direction:column; gap:.25rem; font-size:.8rem; color:#8b949e; }
    label.wide { grid-column:1/-1; }
    textarea { resize:vertical; }
    .actions { display:flex; gap:.5rem; margin-top:1rem; }
    .spacer { flex:1; }
    .row { display:flex; gap:.6rem; align-items:center; padding:.35rem 0; border-bottom:1px solid #21262d; font-size:.85rem; }
    .row:last-child { border-bottom:none; }
    .pill { background:#1f6feb33; color:#58a6ff; border-radius:999px; padding:.1rem .5rem; font-size:.75rem; }
    .small-select { width:5rem; }
    .small-input { width:14rem; }
    .notice { margin-top:.8rem; padding:.6rem .8rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; font-size:.85rem; }
    .notice.warn { background:#2d210b; border-color:#d29922; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .muted { color:#8b949e; }
    .mono { font-family:ui-monospace, monospace; }
    .folder { border:1px solid #21262d; border-radius:6px; padding:.55rem .7rem; margin-bottom:.55rem; }
    .folder-head { display:flex; align-items:center; gap:.6rem; }
    .folder-head .mono { color:#c9d1d9; font-weight:600; }
    .fullpath { font-family:ui-monospace, monospace; color:#79c0ff; background:#0d1117; border:1px solid #21262d; border-radius:4px; padding:.3rem .45rem; margin:.35rem 0; word-break:break-all; }
    .folder-edit { display:flex; gap:.4rem; margin-top:.35rem; }
    .folder-edit input { flex:1; }
    .pill.def { background:#21262d; color:#8b949e; }
    .hint { font-size:.75rem; margin-top:.3rem; }
  `],
})
export class ConfigComponent implements OnChanges {
  @Input({ required: true }) agent!: Agent;
  @Input() paths: AgentPaths | null = null;
  @Input() egress: EgressStatus | null = null;
  @Input() workspaces: Workspace[] = [];
  @Input() grants: Grant[] = [];
  @Input() brokerGrants: BrokerGrant[] = [];

  readonly types = ['codex', 'opencode', 'claude', 'antigravity', 'gemini', 'custom'];
  busy = signal(false);
  message = signal('');
  error = signal('');
  snapshots = signal<MemorySnapshot[]>([]);
  memories = signal<MemoryInfo[]>([]);

  type = 'custom';
  image = '';
  uid = 0;
  gid = 0;
  command = '';
  profile = '';
  envFile = '';
  agentDirInput = '';
  notesDirInput = '';
  grantMode = 'rw';
  memoryId = '';
  newMemoryPath = '';

  private readonly api = inject(ApiService);
  private readonly store = inject(Store);

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['agent'] && this.agent) {
      this.type = this.agent.type;
      this.image = this.agent.image;
      this.uid = this.agent.uid;
      this.gid = this.agent.gid;
      this.command = (this.agent.command ?? []).join('\n');
      this.profile = this.agent.network_profile ?? '';
      this.envFile = this.agent.env_file ?? '';
      this.loadSnapshots();
      this.loadMemories();
    }
    if (changes['paths'] && this.paths) {
      this.agentDirInput = this.paths.agent_dir_custom ? this.paths.agent_dir : '';
      this.notesDirInput = this.paths.notes_dir_custom ? this.paths.notes_dir : '';
      this.memoryId = this.paths.memory_id ?? '';
    }
  }

  private loadMemories(): void {
    this.api.memories().subscribe({
      next: m => this.memories.set(m),
      error: () => this.memories.set([]),
    });
  }

  private loadSnapshots(): void {
    this.api.snapshots(this.agent.id).subscribe({
      next: s => this.snapshots.set(s),
      error: () => this.snapshots.set([]),
    });
  }

  private run<T>(fn: () => Promise<T>, ok: string): Promise<T | null> {
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    return fn().then(v => { this.message.set(ok); return v; })
      .catch(e => { this.error.set(e.message ?? String(e)); return null; })
      .finally(() => { this.busy.set(false); void this.store.refresh(); });
  }

  save(): void {
    const cmd = this.command.split('\n').map(s => s.trim()).filter(Boolean);
    void this.run(() => new Promise<void>((resolve, reject) => {
      this.api.updateAgent(this.agent.id, {
        type: this.type, image: this.image, command: cmd, uid: this.uid, gid: this.gid,
      }).subscribe({
        next: () => {
          this.api.setNetworkProfile(this.agent.id, this.profile).subscribe({
            next: () => {
              this.api.setEnvFile(this.agent.id, this.envFile).subscribe({
                next: () => resolve(), error: reject,
              });
            }, error: reject,
          });
        }, error: reject,
      });
    }), 'Configuration saved (restart to apply)');
  }

  start(): void { void this.run(() => new Promise<void>((res, rej) => this.api.startAgent(this.agent.id).subscribe({ next: () => res(), error: rej })), 'Agent started'); }
  stop(): void { void this.run(() => new Promise<void>((res, rej) => this.api.stopAgent(this.agent.id).subscribe({ next: () => res(), error: rej })), 'Agent stopped'); }
  restart(): void { void this.run(() => new Promise<void>((res, rej) => this.api.restartAgent(this.agent.id).subscribe({ next: () => res(), error: rej })), 'Agent restarted'); }
  remove(): void {
    if (!confirm(`Remove agent ${this.agent.name}? Host data is kept.`)) return;
    void this.run(() => new Promise<void>((res, rej) => this.api.removeAgent(this.agent.id).subscribe({ next: () => res(), error: rej })), 'Agent removed');
  }

  grantFor(workspaceId: string): Grant | undefined {
    return this.grants.find(g => g.workspace_id === workspaceId && g.agent_id === this.agent.id && g.status !== 'revoked');
  }

  assign(workspaceName: string): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.createGrant(workspaceName, this.agent.id, this.grantMode).subscribe({ next: () => res(), error: rej })), 'Workspace assigned');
  }

  revoke(id: string): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.revokeGrant(id).subscribe({ next: () => res(), error: rej })), 'Grant revoked');
  }

  applyAgentDir(): void {
    const p = this.agentDirInput.trim();
    void this.run(() => new Promise<void>((res, rej) => this.api.setAgentDir(this.agent.id, p).subscribe({ next: () => res(), error: rej })), 'Agent folder updated (restart to apply)');
  }

  resetAgentDir(): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.setAgentDir(this.agent.id, '').subscribe({ next: () => res(), error: rej })), 'Agent folder reset to default (restart to apply)');
  }

  applyNotesDir(): void {
    const p = this.notesDirInput.trim();
    void this.run(() => new Promise<void>((res, rej) => this.api.setNotesDir(this.agent.id, p).subscribe({ next: () => res(), error: rej })), 'Notes folder updated (restart to apply)');
  }

  resetNotesDir(): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.setNotesDir(this.agent.id, '').subscribe({ next: () => res(), error: rej })), 'Notes folder reset to default (restart to apply)');
  }

  registerAndAttachMemory(): void {
    const path = this.newMemoryPath.trim();
    if (!path) return;
    void this.run(() => new Promise<void>((res, rej) => {
      this.api.registerMemory(path).subscribe({
        next: mem => {
          this.api.attachMemory(this.agent.id, mem.id).subscribe({
            next: () => { this.newMemoryPath = ''; this.loadMemories(); res(); }, error: rej,
          });
        }, error: rej,
      });
    }), 'Memory folder registered and attached');
  }

  attachMemory(): void {
    if (!this.memoryId.trim()) return;
    void this.run(() => new Promise<void>((res, rej) => this.api.attachMemory(this.agent.id, this.memoryId.trim()).subscribe({ next: () => res(), error: rej })), 'Memory attached');
  }

  detachMemory(): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.detachMemory(this.agent.id).subscribe({ next: () => res(), error: rej })), 'Memory detached');
  }

  snapshot(): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.snapshot(this.agent.id).subscribe({ next: () => { this.loadSnapshots(); res(); }, error: rej })), 'Snapshot created');
  }

  restore(name: string): void {
    void this.run(() => new Promise<void>((res, rej) => this.api.restoreSnapshot(this.agent.id, name).subscribe({ next: () => res(), error: rej })), 'Snapshot restored');
  }

  delSnapshot(name: string): void {
    if (!confirm(`Delete snapshot ${name}?`)) return;
    void this.run(() => new Promise<void>((res, rej) => this.api.deleteSnapshot(this.agent.id, name).subscribe({ next: () => { this.loadSnapshots(); res(); }, error: rej })), 'Snapshot deleted');
  }
}
