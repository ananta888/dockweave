import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DatePipe } from '@angular/common';
import { Store } from '../core/store';
import { CredentialService } from '../core/services/credential.service';
import {
  CredentialGrant, CredentialLimits, CredentialMint, CredentialSecret, CredentialSession, CredentialStatus,
} from '../core/domain/credentials';
import { BrokerRequestEntry } from '../core/models';

@Component({
  selector: 'dw-credentials',
  standalone: true,
  imports: [FormsModule, DatePipe],
  template: `
    <div class="cred">
      <div class="head">
        <div>
          <h2>Credentials</h2>
          <p class="hint">Vault + Broker: Secrets sind write-only, Agenten sehen nur kurzlebige <code>dws_</code>-Tokens. Werte werden nie zurückgegeben.</p>
        </div>
        <button class="small" (click)="loadAll()" [disabled]="busy()">Refresh</button>
      </div>
      @if (error()) { <div class="notice error">{{ error() }}</div> }
      @if (message()) { <div class="notice">{{ message() }}</div> }

      @if (status(); as st) {
        <div class="cards">
          <div class="card"><strong>{{ st.status }}</strong><span>Broker</span></div>
          <div class="card"><strong>{{ st.secrets }}</strong><span>Secrets</span></div>
          <div class="card"><strong>{{ st.grants }}</strong><span>Grants</span></div>
          <div class="card"><strong>{{ st.sessions }}</strong><span>Sessions</span></div>
          <div class="card wide"><strong>{{ st.providers.join(', ') }}</strong><span>Provider</span></div>
        </div>
      }

      <div class="cols">
        <section>
          <h3>Secrets</h3>
          <div class="list">
            @for (s of secrets(); track s.name) {
              <div class="row">
                <span class="mono">{{ s.name }}</span>
                <span class="muted">{{ s.provider }}</span>
                <span class="spacer"></span>
                <span class="muted">{{ s.updated_at | date:'short' }}</span>
                <button class="small danger" (click)="removeSecret(s)" [disabled]="busy()">delete</button>
              </div>
            }
            @if (!secrets().length) { <p class="muted">Noch keine Secrets.</p> }
          </div>
          <div class="form-grid">
            <label>Name <input [(ngModel)]="secretName" name="secName" placeholder="deepseek-main" /></label>
            <label>Provider
              <select [(ngModel)]="secretProvider" name="secProvider">
                @for (p of providers(); track p) { <option [value]="p">{{ p }}</option> }
              </select>
            </label>
            <label class="wide">Wert (write-only)
              <input type="password" [(ngModel)]="secretValue" name="secValue" autocomplete="new-password" placeholder="API-Key / Secret" />
            </label>
          </div>
          <button class="primary" (click)="saveSecret()" [disabled]="busy() || !secretName.trim() || !secretValue">Speichern</button>
        </section>

        <section>
          <h3>Grants</h3>
          <div class="list">
            @for (g of grants(); track g.id) {
              <div class="row">
                <span class="mono">{{ g.agent }}</span>
                <span class="muted">{{ g.credential }} · {{ g.provider }}</span>
                <span class="spacer"></span>
                <span class="muted">{{ limits(g) }}</span>
                <button class="small danger" (click)="removeGrant(g)" [disabled]="busy()">delete</button>
              </div>
            }
            @if (!grants().length) { <p class="muted">Noch keine Grants.</p> }
          </div>
          <div class="form-grid">
            <label>Agent
              <select [(ngModel)]="grantAgent" name="grAgent">
                @for (a of agents(); track a.id) { <option [value]="a.name">{{ a.name }}</option> }
              </select>
            </label>
            <label>Credential
              <select [(ngModel)]="grantCredential" name="grCred">
                @for (s of secrets(); track s.name) { <option [value]="s.name">{{ s.name }}</option> }
              </select>
            </label>
            <label class="wide">Modelle (Komma, * = alle) <input [(ngModel)]="grantModels" name="grModels" /></label>
            <label>RPM <input type="number" [(ngModel)]="grantRPM" name="grRPM" /></label>
            <label>Concurrent <input type="number" [(ngModel)]="grantConcurrent" name="grConc" /></label>
            <label>Tokens/Tag <input type="number" [(ngModel)]="grantTokensDay" name="grTokens" /></label>
            <label>USD/Tag <input type="number" [(ngModel)]="grantUSDDay" name="grUSD" /></label>
          </div>
          <button class="primary" (click)="addGrant()" [disabled]="busy() || !grantAgent || !grantCredential">Grant anlegen</button>
        </section>
      </div>

      <section>
        <h3>Provider aktivieren (opencode)</h3>
        <p class="hint">Schreibt eine OpenAI-kompatible Provider-Config mit einem kurzlebigen <code>dws_</code>-Token ins Agenten-Memory. Der echte DeepSeek-Key bleibt im Vault.</p>
        <div class="form-grid">
          <label>Agent
            <select [(ngModel)]="providerAgent" name="pvAgent">
              @for (a of opencodeAgents(); track a.id) { <option [value]="a.name">{{ a.name }}</option> }
            </select>
          </label>
          <label>Credential
            <select [(ngModel)]="providerCredential" name="pvCred">
              @for (s of secrets(); track s.name) { @if (s.provider === 'deepseek') { <option [value]="s.name">{{ s.name }}</option> } }
            </select>
          </label>
          <label>Modell
            <select [(ngModel)]="providerModel" name="pvModel">
              <option value="deepseek-chat">deepseek-chat</option>
              <option value="deepseek-reasoner">deepseek-reasoner</option>
            </select>
          </label>
          <label>TTL <input [(ngModel)]="providerTTL" name="pvTTL" /></label>
        </div>
        <button class="primary" (click)="enableDeepSeek()" [disabled]="busy() || !providerAgent || !providerCredential">DeepSeek in opencode aktivieren</button>
        @if (providerResult(); as pr) {
          <div class="notice">Aktiviert: {{ pr.provider }}/{{ pr.model }} · Session {{ pr.session_id }} · gültig bis {{ pr.expires_at }}. Agent neu starten (oder Reload), damit OpenCode die Config lädt.</div>
        }
      </section>

      <section>
        <h3>Sessions</h3>
        <div class="list">
          @for (s of sessions(); track s.id) {
            <div class="row">
              <span class="mono">{{ s.id }}</span>
              <span class="muted">{{ s.agent }} · grant {{ s.grant_id }}</span>
              <span class="spacer"></span>
              <span class="muted">bis {{ s.expires_at | date:'short' }}</span>
              <button class="small danger" (click)="revoke(s)" [disabled]="busy()">revoke</button>
            </div>
          }
          @if (!sessions().length) { <p class="muted">Keine aktiven Sessions.</p> }
        </div>
        <div class="form-grid">
          <label>Agent
            <select [(ngModel)]="mintAgent" name="miAgent">
              @for (a of agents(); track a.id) { <option [value]="a.name">{{ a.name }}</option> }
            </select>
          </label>
          <label>Credential
            <select [(ngModel)]="mintCredential" name="miCred">
              @for (s of secrets(); track s.name) { <option [value]="s.name">{{ s.name }}</option> }
            </select>
          </label>
          <label>TTL <input [(ngModel)]="mintTTL" name="miTTL" placeholder="8h" /></label>
        </div>
        <button class="primary" (click)="mint()" [disabled]="busy() || !mintAgent || !mintCredential">Session minten</button>
        @if (minted(); as m) {
          <div class="token">
            <p><strong>Token (nur einmal sichtbar):</strong></p>
            <textarea readonly rows="2">{{ m.token }}</textarea>
            <button class="small" (click)="copyToken(m.token)">kopieren</button>
          </div>
        }
      </section>

      <section>
        <div class="head">
          <h3>Letzte Broker-Requests</h3>
          <button class="small" (click)="loadRequests()" [disabled]="busy()">Refresh</button>
        </div>
        <table>
          <thead><tr><th>Zeit</th><th>Agent</th><th>Provider</th><th>Status</th><th>Pfad</th><th>Tokens</th></tr></thead>
          <tbody>
            @for (r of requests(); track r.time) {
              <tr>
                <td>{{ r.time | date:'short' }}</td>
                <td>{{ r.agent }}</td>
                <td>{{ r.provider }}</td>
                <td>{{ r.status }}</td>
                <td class="mono">{{ r.path }}</td>
                <td>{{ r.prompt_tokens }}+{{ r.completion_tokens }}</td>
              </tr>
            }
            @if (!requests().length) { <tr><td colspan="6" class="muted">Keine Requests.</td></tr> }
          </tbody>
        </table>
      </section>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .cred { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    .head { display:flex; align-items:flex-start; gap:1rem; margin-bottom:.6rem; }
    .head h2 { margin:0 0 .2rem; font-size:1.05rem; }
    .head h3 { margin:0; }
    .hint { color:#8b949e; font-size:.76rem; max-width:70ch; margin:.1rem 0 .4rem; }
    .cards { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:.6rem; margin-bottom:1rem; }
    .card { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:.55rem .7rem; display:flex; flex-direction:column; }
    .card.wide { grid-column:1/-1; }
    .card strong { font-size:.95rem; }
    .card span { color:#8b949e; font-size:.68rem; }
    .cols { display:grid; grid-template-columns:1fr 1fr; gap:1rem; }
    @media (max-width:1000px) { .cols { grid-template-columns:1fr; } .cards { grid-template-columns:repeat(2,1fr); } }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; margin-bottom:1rem; }
    h3 { margin:0 0 .5rem; font-size:.82rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.05em; }
    .list { border-top:1px solid #21262d; }
    .row { display:flex; align-items:center; gap:.5rem; padding:.35rem 0; border-bottom:1px solid #21262d; font-size:.78rem; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; font-size:.72rem; }
    .spacer { flex:1; }
    .form-grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(150px,1fr)); gap:.5rem; margin:.7rem 0; }
    .form-grid label { display:flex; flex-direction:column; gap:.2rem; font-size:.7rem; color:#8b949e; }
    .form-grid label.wide { grid-column:1/-1; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .danger { background:#da3633; border-color:#f85149; color:#fff; }
    .small { padding:.15rem .45rem; font-size:.72rem; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin-bottom:.6rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .token { margin-top:.7rem; background:#0d1117; border:1px solid #30363d; border-radius:6px; padding:.6rem; }
    .token textarea { width:100%; box-sizing:border-box; font-family:ui-monospace,monospace; font-size:.72rem; }
    table { width:100%; border-collapse:collapse; font-size:.74rem; }
    th,td { text-align:left; padding:.3rem .4rem; border-bottom:1px solid #21262d; }
    th { color:#8b949e; font-weight:500; }
    code { font-size:.72rem; }
  `],
})
export class CredentialsComponent implements OnInit {
  private readonly svc = inject(CredentialService);
  readonly store = inject(Store);

  status = signal<CredentialStatus | null>(null);
  secrets = signal<CredentialSecret[]>([]);
  grants = signal<CredentialGrant[]>([]);
  sessions = signal<CredentialSession[]>([]);
  requests = signal<BrokerRequestEntry[]>([]);
  minted = signal<CredentialMint | null>(null);
  busy = signal(false);
  error = signal('');
  message = signal('');

  secretName = '';
  secretProvider = 'deepseek';
  secretValue = '';

  grantAgent = '';
  grantCredential = '';
  grantModels = '*';
  grantRPM = 60;
  grantConcurrent = 4;
  grantTokensDay = 0;
  grantUSDDay = 0;

  mintAgent = '';
  mintCredential = '';
  mintTTL = '8h';

  providerAgent = '';
  providerCredential = 'deepseek-main';
  providerModel = 'deepseek-chat';
  providerTTL = '8h';
  providerResult = signal<{ provider: string; model: string; session_id: string; expires_at: string } | null>(null);

  readonly agents = computed(() => this.store.agents());
  readonly opencodeAgents = computed(() => this.store.agents().filter(a => a.type === 'opencode'));
  readonly providers = computed(() => {
    const p = this.status()?.providers ?? [];
    return p.length ? p : ['deepseek', 'openai', 'openrouter', 'anthropic', 'gemini', 'opencode', 'codex-chatgpt'];
  });

  ngOnInit(): void {
    this.loadAll();
    this.loadRequests();
  }

  loadAll(): void {
    this.busy.set(true);
    this.error.set('');
    this.svc.status().subscribe({
      next: st => {
        this.status.set(st);
        this.applyDefaults();
        this.svc.secrets().subscribe({ next: s => this.secrets.set(s ?? []), error: e => this.error.set(e.message) });
        this.svc.grants().subscribe({ next: g => this.grants.set(g ?? []), error: e => this.error.set(e.message) });
        this.svc.sessions().subscribe({
          next: s => { this.sessions.set(s ?? []); this.busy.set(false); },
          error: e => { this.busy.set(false); this.error.set(e.message); },
        });
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  loadRequests(): void {
    this.svc.requests().subscribe({ next: r => this.requests.set((r ?? []).slice(-30).reverse()), error: e => this.error.set(e.message) });
  }

  private applyDefaults(): void {
    if (!this.grantAgent && this.agents().length) this.grantAgent = this.agents()[0].name;
    if (!this.mintAgent && this.agents().length) this.mintAgent = this.agents()[0].name;
    if (!this.providerAgent && this.opencodeAgents().length) this.providerAgent = this.opencodeAgents()[0].name;
  }

  saveSecret(): void {
    if (!this.secretName.trim() || !this.secretValue) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.putSecret(this.secretName.trim(), this.secretProvider, this.secretValue).subscribe({
      next: () => {
        this.busy.set(false);
        this.message.set(`Secret ${this.secretName} gespeichert (Wert wird nicht angezeigt).`);
        this.secretName = '';
        this.secretValue = '';
        this.loadAll();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  removeSecret(s: CredentialSecret): void {
    if (!confirm(`Secret ${s.name} löschen? Grants darauf werden ungültig.`)) return;
    this.busy.set(true);
    this.svc.deleteSecret(s.name).subscribe({
      next: () => { this.busy.set(false); this.message.set(`${s.name} gelöscht.`); this.loadAll(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  addGrant(): void {
    const secret = this.secrets().find(s => s.name === this.grantCredential);
    const limits: CredentialLimits = {};
    if (this.grantRPM > 0) limits.max_requests_per_minute = this.grantRPM;
    if (this.grantConcurrent > 0) limits.max_concurrent = this.grantConcurrent;
    if (this.grantTokensDay > 0) limits.max_tokens_per_day = this.grantTokensDay;
    if (this.grantUSDDay > 0) limits.max_cost_usd_per_day = this.grantUSDDay;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.svc.addGrant({
      agent: this.grantAgent,
      credential: this.grantCredential,
      provider: secret?.provider ?? '',
      models: this.grantModels.split(',').map(m => m.trim()).filter(Boolean),
      limits,
    }).subscribe({
      next: g => { this.busy.set(false); this.message.set(`Grant ${g.id} für ${g.agent} angelegt.`); this.loadAll(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  removeGrant(g: CredentialGrant): void {
    if (!confirm(`Grant ${g.id} (${g.agent} → ${g.credential}) löschen?`)) return;
    this.busy.set(true);
    this.svc.deleteGrant(g.id).subscribe({
      next: () => { this.busy.set(false); this.message.set('Grant gelöscht.'); this.loadAll(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  mint(): void {
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.minted.set(null);
    this.svc.mint({ agent: this.mintAgent, credential: this.mintCredential, ttl: this.mintTTL }).subscribe({
      next: m => { this.busy.set(false); this.minted.set(m); this.loadAll(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  enableDeepSeek(): void {
    if (!this.providerAgent || !this.providerCredential) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.providerResult.set(null);
    this.svc.enableDeepSeek(this.providerAgent, {
      credential: this.providerCredential, model: this.providerModel, ttl: this.providerTTL,
    }).subscribe({
      next: r => {
        this.busy.set(false);
        this.providerResult.set(r);
        this.message.set('Provider aktiviert; Agent neu starten, damit OpenCode die Config lädt.');
        this.loadAll();
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  revoke(s: CredentialSession): void {
    this.busy.set(true);
    this.svc.revokeSession(s.id).subscribe({
      next: () => { this.busy.set(false); this.message.set(`Session ${s.id} widerrufen.`); this.loadAll(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  copyToken(token: string): void {
    void navigator.clipboard.writeText(token);
  }

  limits(g: CredentialGrant): string {
    const l = g.limits || {};
    const parts: string[] = [];
    if (l.max_requests_per_minute) parts.push(`${l.max_requests_per_minute} rpm`);
    if (l.max_concurrent) parts.push(`${l.max_concurrent} conc`);
    if (l.max_tokens_per_day) parts.push(`${l.max_tokens_per_day} tok/d`);
    if (l.max_cost_usd_per_day) parts.push(`$${l.max_cost_usd_per_day}/d`);
    return parts.join(' · ') || 'keine Limits';
  }
}
