import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DomSanitizer, SafeResourceUrl } from '@angular/platform-browser';
import { PaperclipService } from '../../core/services/paperclip.service';
import { Store } from '../../core/store';
import {
  PaperclipAgent, PaperclipCompany, PaperclipIssue, PaperclipStatus,
} from '../../core/domain/integrations';

@Component({
  selector: 'dw-paperclip-panel',
  standalone: true,
  imports: [FormsModule],
  template: `
    <div class="panel">
      <div class="head">
        <div>
          <h3>Paperclip</h3>
          <p class="hint">Original-Oberfläche eingebettet, plus Schnellaktionen aus DockWeave: Task anlegen und Agent wecken.</p>
        </div>
        <a class="open" [href]="configUrl()" target="_blank" rel="noopener">Original öffnen ↗</a>
      </div>

      @if (error()) { <div class="notice error">{{ error() }}</div> }
      @if (message()) { <div class="notice">{{ message() }}</div> }

      <div class="grid">
        <section>
          <h4>Company</h4>
          <select [ngModel]="companyId()" (ngModelChange)="selectCompany($event)" name="pcCompany">
            @for (c of companies(); track c.id) { <option [value]="c.id">{{ c.name }}</option> }
          </select>

          <h4>Agenten</h4>
          <div class="list">
            @for (a of agents(); track a.id) {
              <div class="row">
                <span class="mono">{{ a.name }}</span>
                <span class="muted">{{ a.adapterType || 'process' }} · {{ a.status || a.lifecycleState || '–' }}</span>
                <span class="spacer"></span>
                <button class="small" (click)="wake(a)" [disabled]="busy()">wake</button>
              </div>
            }
            @if (!agents().length) { <p class="muted">Keine Agenten.</p> }
          </div>

          <h4>DockWeave-Agenten → Paperclip</h4>
          <div class="actions">
            <button class="small primary" (click)="syncAll()" [disabled]="busy() || !companyId()">Alle synchronisieren</button>
            <span class="muted">legt <code>DW-&lt;name&gt;</code> an bzw. aktualisiert es</span>
          </div>
          <div class="list">
            @for (a of dockweaveAgents(); track a.id) {
              <div class="row">
                <span class="mono">{{ a.name }}</span>
                <span class="muted">{{ a.type }} · {{ a.state }}</span>
                <span class="spacer"></span>
                @if (paperclipAgentFor(a.name); as pa) { <span class="badge">{{ pa.name }}</span> }
                <button class="small" (click)="ensureTarget(a.name)" [disabled]="busy()">
                  {{ paperclipAgentFor(a.name) ? 'aktualisieren' : 'anlegen' }}
                </button>
              </div>
            }
          </div>

          <h4>Ziel-Agent (DockWeave)</h4>
          @if (primaryAgent(); as pa) {
            <div class="target-row">
              <span class="mono">{{ pa.name }}</span>
              <span class="muted">→ {{ targetOf(pa) || 'nicht gesetzt' }}</span>
              <span class="spacer"></span>
              <select [(ngModel)]="targetModel" name="pcTarget">
                @for (a of dockweaveAgents(); track a.id) { <option [value]="a.name">{{ a.name }}</option> }
              </select>
              <button class="small primary" (click)="assignTarget()" [disabled]="busy() || !targetModel">zuweisen</button>
            </div>
            <p class="hint">Gilt für den nächsten Wake. Der DockWeave-Agent muss laufen.</p>
          }

          <h4>Task anlegen</h4>
          <label class="field">Titel <input [(ngModel)]="issueTitle" name="pcTitle" placeholder="Antworte nur mit …" /></label>
          <label class="field">Beschreibung <textarea rows="2" [(ngModel)]="issueDescription" name="pcDesc"></textarea></label>
          <label class="field">Agent
            <select [(ngModel)]="assigneeId" name="pcAssignee">
              @for (a of agents(); track a.id) { <option [value]="a.id">{{ a.name }}</option> }
            </select>
          </label>
          <div class="actions">
            <button class="primary" (click)="createIssue()" [disabled]="busy() || !issueTitle.trim() || !companyId()">Task anlegen</button>
          </div>

          <h4>Tasks</h4>
          <div class="list">
            @for (i of issues(); track i.id) {
              <div class="row">
                <span class="mono">{{ i.identifier || i.id }}</span>
                <span class="muted">{{ i.status }}</span>
                <span class="title">{{ i.title }}</span>
              </div>
            }
            @if (!issues().length) { <p class="muted">Keine Tasks.</p> }
          </div>
        </section>

        <section class="embed">
          @if (safeUrl(); as url) {
            <iframe [src]="url" title="Paperclip" referrerpolicy="no-referrer"></iframe>
          } @else {
            <div class="empty">Paperclip-URL nicht konfiguriert. <a [href]="configUrl()" target="_blank" rel="noopener">Original öffnen</a></div>
          }
        </section>
      </div>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .panel { padding:1rem 1.2rem; height:100%; box-sizing:border-box; overflow:auto; }
    .head { display:flex; align-items:flex-start; gap:1rem; flex-wrap:wrap; margin-bottom:.6rem; }
    .head h3 { margin:0; font-size:1rem; color:#58a6ff; }
    .hint { color:#8b949e; font-size:.76rem; max-width:70ch; margin:.2rem 0 0; }
    .open { color:#58a6ff; font-size:.75rem; }
    .grid { display:grid; grid-template-columns:minmax(340px, .9fr) minmax(480px, 1.6fr); gap:1rem; align-items:stretch; }
    @media (max-width:1100px) { .grid { grid-template-columns:1fr; } }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; }
    section.embed { padding:0; overflow:hidden; min-height:640px; }
    iframe { width:100%; height:100%; min-height:640px; border:0; background:#fff; display:block; }
    .empty { padding:1rem; color:#8b949e; }
    h4 { margin:1rem 0 .4rem; font-size:.78rem; text-transform:uppercase; letter-spacing:.05em; color:#8b949e; }
    h4:first-child { margin-top:0; }
    .list { border-top:1px solid #21262d; }
    .row { display:flex; align-items:center; gap:.5rem; padding:.35rem 0; border-bottom:1px solid #21262d; font-size:.78rem; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; font-size:.72rem; }
    .title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .spacer { flex:1; }
    .field { display:flex; flex-direction:column; gap:.2rem; font-size:.72rem; color:#8b949e; margin:.4rem 0; }
    .target-row { display:flex; align-items:center; gap:.5rem; flex-wrap:wrap; margin:.3rem 0; }
    .badge { padding:.05rem .35rem; border-radius:4px; background:#1f6feb33; color:#58a6ff; border:1px solid #1f6feb; font-size:.65rem; }
    code { font-size:.72rem; }
    .hint { color:#8b949e; font-size:.72rem; margin:.1rem 0 .4rem; }
    .actions { display:flex; gap:.5rem; margin:.5rem 0; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .small { padding:.15rem .45rem; font-size:.72rem; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin:.4rem 0; font-size:.78rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
  `],
})
export class PaperclipPanelComponent implements OnInit {
  private readonly svc = inject(PaperclipService);
  private readonly sanitizer = inject(DomSanitizer);
  readonly store = inject(Store);

  configUrl = signal('');
  status = signal<PaperclipStatus | null>(null);
  companyId = signal('');
  busy = signal(false);
  error = signal('');
  message = signal('');
  issueTitle = '';
  issueDescription = '';
  assigneeId = '';
  targetModel = '';

  readonly companies = computed<PaperclipCompany[]>(() => this.status()?.companies ?? []);
  readonly agents = computed<PaperclipAgent[]>(() => this.status()?.agents ?? []);
  readonly issues = computed<PaperclipIssue[]>(() => this.status()?.issues ?? []);
  readonly dockweaveAgents = computed(() => this.store.agents());
  readonly primaryAgent = computed<PaperclipAgent | null>(() => this.agents()[0] ?? null);
  readonly safeUrl = computed<SafeResourceUrl | null>(() => {
    const u = this.configUrl();
    return u ? this.sanitizer.bypassSecurityTrustResourceUrl(u) : null;
  });

  ngOnInit(): void {
    this.svc.config().subscribe({
      next: c => { this.configUrl.set(c.url); this.load(); },
      error: e => this.error.set(e.message ?? String(e)),
    });
  }

  load(): void {
    this.busy.set(true);
    this.error.set('');
    this.svc.status(this.companyId() || undefined).subscribe({
      next: s => {
        this.status.set(s);
        if (!this.companyId() && s.company_id) this.companyId.set(s.company_id);
        if (!this.assigneeId && s.agents?.length) this.assigneeId = s.agents[0].id;
        if (!this.targetModel && s.agents?.length) this.targetModel = this.targetOf(s.agents[0]);
        this.busy.set(false);
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  selectCompany(id: string): void {
    this.companyId.set(id);
    this.assigneeId = '';
    this.load();
  }

  createIssue(): void {
    if (!this.issueTitle.trim() || !this.companyId()) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.createIssue({
      company_id: this.companyId(),
      title: this.issueTitle.trim(),
      description: this.issueDescription.trim(),
      assignee_agent_id: this.assigneeId,
    }).subscribe({
      next: () => {
        this.busy.set(false);
        this.message.set('Task angelegt.');
        this.issueTitle = '';
        this.issueDescription = '';
        this.load();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  paperclipAgentFor(dockweaveName: string): PaperclipAgent | undefined {
    return this.agents().find(a => a.adapterConfig?.args?.[0] === dockweaveName);
  }

  ensureTarget(dockweaveName: string): void {
    if (!this.companyId()) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.ensureAgent(this.companyId(), dockweaveName).subscribe({
      next: r => {
        this.busy.set(false);
        this.message.set(`${r.action === 'created' ? 'Angelegt' : 'Aktualisiert'}: DW-${dockweaveName}.`);
        this.load();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  syncAll(): void {
    const agents = this.dockweaveAgents().filter(a => a.state === 'running').map(a => a.name);
    if (!this.companyId() || !agents.length) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.sync(this.companyId(), agents).subscribe({
      next: r => {
        this.busy.set(false);
        const errs = r.errors?.length ? `, ${r.errors.length} Fehler` : '';
        this.message.set(`Sync: ${r.created.length} angelegt, ${r.updated.length} aktualisiert${errs}.`);
        this.load();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  targetOf(a: PaperclipAgent | null): string {
    return a?.adapterConfig?.args?.[0] ?? '';
  }

  assignTarget(): void {
    const a = this.primaryAgent();
    if (!a || !this.targetModel) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.setTarget(a.id, this.targetModel).subscribe({
      next: () => {
        this.busy.set(false);
        this.message.set(`Ziel von ${a.name} → ${this.targetModel}. Gilt für den nächsten Wake.`);
        this.load();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  wake(a: PaperclipAgent): void {
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.wake(a.id, this.companyId()).subscribe({
      next: () => { this.busy.set(false); this.message.set(`${a.name} geweckt.`); this.load(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }
}
