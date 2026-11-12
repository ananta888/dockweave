import { Component, OnInit, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ApiService } from '../core/api.service';
import { AgentTypeMCP, AgentTypeSkill } from '../core/models';

@Component({
  selector: 'dw-agent-types',
  standalone: true,
  imports: [FormsModule],
  template: `
    <div class="types-view">
      <div class="type-bar">
        @for (t of types; track t) {
          <button [class.active]="type() === t" (click)="select(t)">{{ t }}</button>
        }
        <span class="spacer"></span>
        <button class="primary" (click)="importDefaults()" [disabled]="busy()">Standardwerte einlesen</button>
      </div>
      <p class="hint">Diese Defaults gelten für alle Agenten des Typs. Per-Agent-Einträge mit gleichem Namen überschreiben sie; die Konfiguration wird beim Start in das Memory-Home materialisiert.</p>
      @if (message()) { <div class="notice">{{ message() }}</div> }
      @if (error()) { <div class="notice error">{{ error() }}</div> }

      <div class="cols">
        <section>
          <h3>MCP Server · {{ type() }}</h3>
          @for (m of mcp(); track m.id) {
            <div class="row">
              <span class="mono">{{ m.name }}</span>
              <span class="muted">{{ m.transport }}{{ m.url ? ' · ' + m.url : '' }}</span>
              <span class="spacer"></span>
              <button class="small" (click)="toggleMCP(m)">{{ m.enabled ? 'disable' : 'enable' }}</button>
              <button class="small danger" (click)="delMCP(m)">delete</button>
            </div>
          }
          @if (!mcp().length) { <p class="muted">Noch keine Defaults. „Standardwerte einlesen“ scannt bestehende Agenten/Homes.</p> }
          <div class="form-grid">
            <label>Name <input [(ngModel)]="mcpName" name="tmcpName" placeholder="filesystem" /></label>
            <label>Transport
              <select [(ngModel)]="mcpTransport" name="tmcpTransport">
                <option value="stdio">stdio</option><option value="http">http</option><option value="sse">sse</option>
              </select>
            </label>
            @if (mcpTransport === 'stdio') {
              <label>Command <input [(ngModel)]="mcpCommand" name="tmcpCommand" /></label>
              <label class="wide">Args (one per line) <textarea rows="2" [(ngModel)]="mcpArgs" name="tmcpArgs"></textarea></label>
            } @else {
              <label class="wide">URL <input [(ngModel)]="mcpURL" name="tmcpURL" /></label>
            }
          </div>
          <button class="primary" (click)="addMCP()" [disabled]="busy()">MCP Default hinzufügen</button>
        </section>

        <section>
          <h3>Skills · {{ type() }}</h3>
          @for (sk of skills(); track sk.id) {
            <div class="row">
              <span class="mono">{{ sk.name }}</span>
              <span class="muted">{{ sk.description || sk.source_path }}</span>
              <span class="spacer"></span>
              <button class="small" (click)="toggleSkill(sk)">{{ sk.enabled ? 'disable' : 'enable' }}</button>
              <button class="small danger" (click)="delSkill(sk)">delete</button>
            </div>
          }
          @if (!skills().length) { <p class="muted">Noch keine Skill-Defaults.</p> }
          <div class="form-grid">
            <label>Name <input [(ngModel)]="skillName" name="tskillName" /></label>
            <label class="wide">Beschreibung <input [(ngModel)]="skillDescription" name="tskillDescription" /></label>
            <label class="wide">Quelle <input [(ngModel)]="skillSource" name="tskillSource" placeholder="/pfad/zum/skill" /></label>
          </div>
          <button class="primary" (click)="addSkill()" [disabled]="busy()">Skill Default hinzufügen</button>
        </section>
      </div>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .types-view { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    .type-bar { display:flex; align-items:center; gap:.35rem; margin-bottom:.5rem; flex-wrap:wrap; }
    .type-bar button { background:transparent; border:1px solid #30363d; color:#8b949e; padding:.35rem .7rem; border-radius:6px; cursor:pointer; }
    .type-bar button.active { border-color:#58a6ff; color:#58a6ff; background:#1f6feb1a; }
    .spacer { flex:1; }
    .hint { color:#8b949e; font-size:.78rem; margin:.2rem 0 .8rem; }
    .cols { display:grid; grid-template-columns:1fr 1fr; gap:1rem; }
    @media (max-width:900px) { .cols { grid-template-columns:1fr; } }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; }
    h3 { margin:0 0 .6rem; font-size:.9rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.05em; }
    .row { display:flex; align-items:center; gap:.5rem; border-bottom:1px solid #21262d; padding:.35rem 0; font-size:.8rem; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; }
    .small { padding:.15rem .45rem; font-size:.72rem; }
    .danger { background:#da3633; border-color:#f85149; color:#fff; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .form-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(180px,1fr)); gap:.5rem; margin:.7rem 0 .5rem; }
    .form-grid label { display:flex; flex-direction:column; gap:.2rem; font-size:.72rem; color:#8b949e; }
    .form-grid label.wide { grid-column:1/-1; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin-bottom:.6rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
  `],
})
export class AgentTypesComponent implements OnInit {
  readonly types = ['opencode', 'claude', 'codex', 'gemini', 'antigravity', 'custom'];
  type = signal('opencode');
  mcp = signal<AgentTypeMCP[]>([]);
  skills = signal<AgentTypeSkill[]>([]);
  busy = signal(false);
  message = signal('');
  error = signal('');

  mcpName = '';
  mcpTransport: AgentTypeMCP['transport'] = 'stdio';
  mcpCommand = '';
  mcpArgs = '';
  mcpURL = '';
  skillName = '';
  skillDescription = '';
  skillSource = '';

  private readonly api = inject(ApiService);

  ngOnInit(): void {
    this.load();
  }

  select(t: string): void {
    this.type.set(t);
    this.load();
  }

  load(): void {
    this.error.set('');
    const t = this.type();
    this.api.listTypeMCP(t).subscribe({
      next: m => this.mcp.set(m ?? []),
      error: e => this.error.set(e.message ?? String(e)),
    });
    this.api.listTypeSkills(t).subscribe({
      next: s => this.skills.set(s ?? []),
      error: e => this.error.set(e.message ?? String(e)),
    });
  }

  importDefaults(): void {
    this.busy.set(true);
    this.error.set('');
    this.api.importTypeDefaults(this.type()).subscribe({
      next: r => {
        this.busy.set(false);
        this.message.set(`Import: ${r.mcp_added} MCP, ${r.skills_added} Skills hinzugefügt`);
        this.load();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  addMCP(): void {
    if (!this.mcpName.trim()) return;
    this.busy.set(true);
    this.api.addTypeMCP(this.type(), {
      name: this.mcpName.trim(), transport: this.mcpTransport, command: this.mcpCommand.trim(),
      args: this.mcpArgs.split('\n').map(s => s.trim()).filter(Boolean), url: this.mcpURL.trim(), enabled: true,
    }).subscribe({
      next: () => { this.busy.set(false); this.mcpName = ''; this.mcpCommand = ''; this.mcpArgs = ''; this.mcpURL = ''; this.load(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  toggleMCP(m: AgentTypeMCP): void {
    this.api.updateTypeMCP(this.type(), m.id, { ...m, enabled: !m.enabled }).subscribe({
      next: () => this.load(), error: e => this.error.set(e.message ?? String(e)),
    });
  }

  delMCP(m: AgentTypeMCP): void {
    if (!confirm(`MCP-Default ${m.name} löschen?`)) return;
    this.api.deleteTypeMCP(this.type(), m.id).subscribe({ next: () => this.load(), error: e => this.error.set(e.message ?? String(e)) });
  }

  addSkill(): void {
    if (!this.skillName.trim()) return;
    this.busy.set(true);
    this.api.addTypeSkill(this.type(), {
      name: this.skillName.trim(), description: this.skillDescription.trim(), source_path: this.skillSource.trim(), enabled: true,
    }).subscribe({
      next: () => { this.busy.set(false); this.skillName = ''; this.skillDescription = ''; this.skillSource = ''; this.load(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  toggleSkill(sk: AgentTypeSkill): void {
    this.api.updateTypeSkill(this.type(), sk.id, { ...sk, enabled: !sk.enabled }).subscribe({
      next: () => this.load(), error: e => this.error.set(e.message ?? String(e)),
    });
  }

  delSkill(sk: AgentTypeSkill): void {
    if (!confirm(`Skill-Default ${sk.name} löschen?`)) return;
    this.api.deleteTypeSkill(this.type(), sk.id).subscribe({ next: () => this.load(), error: e => this.error.set(e.message ?? String(e)) });
  }
}
