import { Component, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Store } from '../../core/store';
import { HerdrService } from '../../core/services/herdr.service';
import { ApiService } from '../../core/api.service';
import { HerdrStatus } from '../../core/domain/integrations';
import { TerminalComponent } from '../terminal.component';

@Component({
  selector: 'dw-herdr-panel',
  standalone: true,
  imports: [FormsModule, TerminalComponent],
  template: `
    <div class="panel">
      <div class="head">
        <div>
          <h3>Herdr</h3>
          <p class="hint">Herdr besitzt Panes und Sessions. Der Prompt geht über die native herdr-API; die Antwort kommt aus dem Codex-Rollout. Die TUI bleibt live sichtbar.</p>
        </div>
        <label class="agent-pick">Agent
          <select [ngModel]="selectedId()" (ngModelChange)="selectAgent($event)">
            @for (a of herdrAgents(); track a.id) { <option [value]="a.id">{{ a.name }}</option> }
          </select>
        </label>
        <button class="small" (click)="refresh()" [disabled]="busy()">Refresh</button>
      </div>

      @if (error()) { <div class="notice error">{{ error() }}</div> }
      @if (message()) { <div class="notice">{{ message() }}</div> }
      @if (!herdrAgents().length) {
        <div class="notice">Kein Agent mit Runtime <code>herdr</code>. Lege einen Agenten an (Preset „herdr“) oder stelle einen Agenten im Config-Tab um.</div>
      }

      @if (herdrAgents().length) {
      <div class="grid">
        <section>
          <h4>Status</h4>
          @if (status(); as st) {
            <div class="cards">
              <div class="card"><strong>{{ st.agents.length }}</strong><span>herdr-Agenten</span></div>
              <div class="card"><strong>{{ st.panes.length }}</strong><span>Panes</span></div>
              <div class="card"><strong>{{ focusedAgent(st) }}</strong><span>fokussiert</span></div>
            </div>
            <div class="list">
              @for (a of st.agents; track a.name) {
                <div class="row">
                  <span class="mono">{{ a.name }}</span>
                  <span class="muted">{{ a.agent || '?' }} · {{ a.agent_status || 'unknown' }}</span>
                  <span class="spacer"></span>
                  <span class="badge" [class.on]="a.focused">{{ a.focused ? 'focused' : (a.interactive_ready ? 'ready' : '…') }}</span>
                </div>
              }
            </div>
          } @else { <p class="muted">Noch kein Status geladen.</p> }

          <div class="actions">
            <button class="primary" (click)="prepare()" [disabled]="busy()">Broker-Zugang vorbereiten</button>
            <span class="muted">schreibt Codex-Config + dws_-Token ins Agenten-Memory; danach Agent neu starten.</span>
          </div>

          <h4>Prompt an herdr</h4>
          <textarea rows="3" [(ngModel)]="prompt" name="herdrPrompt" placeholder="Aufgabe an den Agenten …"></textarea>
          <div class="actions">
            <button class="primary" (click)="send()" [disabled]="busy() || !prompt.trim()">Senden</button>
          </div>
          @if (answer()) { <pre class="answer">{{ answer() }}</pre> }
        </section>

        <section class="term">
          @if (selected(); as a) {
            <dw-terminal [agentId]="a.id" [agentName]="a.name" [running]="a.state === 'running'"
                         (start)="startAgent(a.id)" />
          }
        </section>
      </div>
      }
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .panel { padding:1rem 1.2rem; height:100%; box-sizing:border-box; overflow:auto; }
    .head { display:flex; align-items:flex-start; gap:1rem; flex-wrap:wrap; margin-bottom:.6rem; }
    .head h3 { margin:0; font-size:1rem; color:#58a6ff; }
    .hint { color:#8b949e; font-size:.76rem; max-width:70ch; margin:.2rem 0 0; }
    .agent-pick { display:flex; flex-direction:column; gap:.2rem; font-size:.7rem; color:#8b949e; }
    .grid { display:grid; grid-template-columns:minmax(340px, 1fr) minmax(420px, 1.4fr); gap:1rem; align-items:start; }
    @media (max-width:1100px) { .grid { grid-template-columns:1fr; } }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; }
    section.term { padding:0; overflow:hidden; min-height:520px; }
    h4 { margin:1rem 0 .4rem; font-size:.78rem; text-transform:uppercase; letter-spacing:.05em; color:#8b949e; }
    h4:first-child { margin-top:0; }
    .cards { display:grid; grid-template-columns:repeat(3,1fr); gap:.5rem; margin-bottom:.6rem; }
    .card { background:#0d1117; border:1px solid #21262d; border-radius:6px; padding:.5rem; display:flex; flex-direction:column; }
    .card strong { font-size:1.1rem; }
    .card span { color:#8b949e; font-size:.68rem; }
    .list { border-top:1px solid #21262d; }
    .row { display:flex; align-items:center; gap:.5rem; padding:.35rem 0; border-bottom:1px solid #21262d; font-size:.78rem; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; font-size:.72rem; }
    .spacer { flex:1; }
    .badge { padding:.05rem .35rem; border-radius:4px; background:#30363d; color:#c9d1d9; font-size:.65rem; text-transform:uppercase; }
    .badge.on { background:#1f6feb33; color:#58a6ff; border:1px solid #1f6feb; }
    .actions { display:flex; align-items:center; gap:.5rem; flex-wrap:wrap; margin:.6rem 0; }
    textarea { width:100%; box-sizing:border-box; }
    .answer { background:#0d1117; border:1px solid #30363d; border-radius:6px; padding:.6rem; white-space:pre-wrap; font-size:.78rem; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin:.4rem 0; font-size:.78rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .small { padding:.2rem .5rem; font-size:.72rem; }
  `],
})
export class HerdrPanelComponent implements OnInit {
  readonly store = inject(Store);
  private readonly herdr = inject(HerdrService);
  private readonly api = inject(ApiService);

  selectedId = signal('');
  prompt = '';
  status = signal<HerdrStatus | null>(null);
  answer = signal('');
  busy = signal(false);
  error = signal('');
  message = signal('');

  readonly herdrAgents = computed(() => this.store.agents().filter(a => a.runtime === 'herdr'));
  readonly selected = computed(() => this.herdrAgents().find(a => a.id === this.selectedId()) ?? this.herdrAgents()[0] ?? null);

  ngOnInit(): void {
    const first = this.herdrAgents()[0];
    if (first) {
      this.selectedId.set(first.id);
      this.refresh();
    }
  }

  selectAgent(id: string): void {
    this.selectedId.set(id);
    this.status.set(null);
    this.answer.set('');
    this.refresh();
  }

  startAgent(id: string): void {
    this.error.set('');
    this.api.startAgent(id).subscribe({
      next: () => { void this.store.refresh(); this.refresh(); },
      error: e => this.error.set(e.message ?? String(e)),
    });
  }

  refresh(): void {
    const a = this.selected();
    if (!a) return;
    this.busy.set(true);
    this.error.set('');
    this.herdr.status(a.id).subscribe({
      next: s => { this.status.set(s); this.busy.set(false); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  prepare(): void {
    const a = this.selected();
    if (!a) return;
    this.busy.set(true);
    this.error.set('');
    this.message.set('');
    this.herdr.prepare(a.id, { credential: 'codex-main', model: 'gpt-6.1-sol', ttl: '8h' }).subscribe({
      next: r => {
        this.busy.set(false);
        this.message.set(`Broker-Zugang vorbereitet (Grant ${r.grant_id}, gültig bis ${r.expires_at}). Agent neu starten.`);
      },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  send(): void {
    const a = this.selected();
    const text = this.prompt.trim();
    if (!a || !text) return;
    this.busy.set(true);
    this.error.set('');
    this.answer.set('');
    this.herdr.prompt(a.id, text).subscribe({
      next: r => { this.busy.set(false); this.answer.set(r.content); this.prompt = ''; this.refresh(); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  focusedAgent(st: HerdrStatus): string {
    return st.agents.find(a => a.focused)?.name ?? '–';
  }
}
