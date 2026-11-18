import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { DatePipe, DecimalPipe } from '@angular/common';
import { UsageService } from '../core/services/usage.service';
import { AgentUsage, UsageWindow } from '../core/domain/usage';

@Component({
  selector: 'dw-usage',
  standalone: true,
  imports: [DatePipe, DecimalPipe],
  template: `
    <div class="usage-view">
      <div class="head">
        <div>
          <h2>Usage</h2>
          <p class="hint">Abo-/Plan-Verbrauch der Harnesse, soweit lokal verfügbar. Codex liest die Plan-Fenster aus dem Rollout, Claude den lokalen Usage-Cache, OpenCode die Session-Kosten/Tokens.</p>
        </div>
        <div class="actions">
          <span class="muted">aktualisiert {{ updatedAt() | date:'mediumTime' }}</span>
          <button class="small" (click)="load()" [disabled]="busy()">Refresh</button>
        </div>
      </div>
      @if (error()) { <div class="notice error">{{ error() }}</div> }
      @if (!usage().length && !busy()) { <div class="notice">Keine Agenten gefunden.</div> }

      <div class="grid">
        @for (u of usage(); track u.agent_id) {
          <section class="card" [class.unavailable]="!u.available">
            <header>
              <h3>{{ u.display_name || u.agent }}</h3>
              <span class="badge">{{ u.harness }}{{ u.runtime ? ' · ' + u.runtime : '' }}</span>
            </header>
            <p class="source">{{ u.source }}{{ u.fetched_at ? ' · ' + (u.fetched_at | date:'medium') : '' }}</p>

            @if (!u.available) {
              <p class="note">{{ u.note || 'nicht verfügbar' }}</p>
            } @else {
              @if (u.five_hour; as w) {
                <div class="window">
                  <div class="label"><span>{{ label(w, '5h') }}</span><span>{{ w.used_percent | number:'1.0-1' }}%</span></div>
                  <div class="bar"><div [style.width.%]="clamp(w.used_percent)" [class.warm]="w.used_percent >= 60 && w.used_percent < 80" [class.hot]="w.used_percent >= 80"></div></div>
                  @if (w.resets_at) { <div class="reset">Reset {{ w.resets_at | date:'short' }}</div> }
                </div>
              }
              @if (u.seven_day; as w) {
                <div class="window">
                  <div class="label"><span>{{ label(w, '7d') }}</span><span>{{ w.used_percent | number:'1.0-1' }}%</span></div>
                  <div class="bar"><div [style.width.%]="clamp(w.used_percent)" [class.warm]="w.used_percent >= 60 && w.used_percent < 80" [class.hot]="w.used_percent >= 80"></div></div>
                  @if (w.resets_at) { <div class="reset">Reset {{ w.resets_at | date:'short' }}</div> }
                </div>
              }
              @if (u.credits; as c) {
                <p class="credits">Credits: <strong>{{ c.unlimited ? 'unlimited' : (c.has_credits ? (c.balance || 'vorhanden') : 'keine') }}</strong></p>
              }
              @if (u.tokens; as t) {
                <p class="tokens"><strong>{{ t.total | number }}</strong> Tokens · in {{ t.input | number }} / out {{ t.output | number }} / reasoning {{ t.reasoning | number }}</p>
                <p class="muted">Cache read {{ t.cache_read | number }} · write {{ t.cache_write | number }}</p>
              }
              @if (u.cost_usd !== undefined && u.cost_usd > 0) {
                <p class="cost">{{ u.cost_usd | number:'1.2-2' }} USD</p>
              }
              @if (u.sessions) { <p class="muted">{{ u.sessions }} Sessions</p> }
              @if (u.note) { <p class="note">{{ u.note }}</p> }
            }
          </section>
        }
      </div>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .usage-view { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    .head { display:flex; align-items:flex-start; gap:1rem; margin-bottom:.8rem; }
    .head h2 { margin:0 0 .2rem; font-size:1.05rem; }
    .hint { color:#8b949e; font-size:.76rem; max-width:75ch; margin:.1rem 0 .4rem; }
    .actions { display:flex; align-items:center; gap:.5rem; }
    .grid { display:grid; grid-template-columns:repeat(auto-fit,minmax(320px,1fr)); gap:1rem; }
    .card { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; }
    .card.unavailable { opacity:.72; }
    header { display:flex; align-items:center; gap:.5rem; margin-bottom:.2rem; }
    h3 { margin:0; font-size:.95rem; }
    .badge { padding:.05rem .4rem; border-radius:4px; background:#30363d; color:#c9d1d9; font-size:.65rem; text-transform:uppercase; }
    .source { color:#8b949e; font-size:.68rem; margin:0 0 .7rem; }
    .window { margin:.6rem 0; }
    .label { display:flex; justify-content:space-between; font-size:.75rem; margin-bottom:.2rem; }
    .bar { height:.5rem; background:#0d1117; border:1px solid #21262d; border-radius:999px; overflow:hidden; }
    .bar > div { height:100%; background:#1f6feb; border-radius:999px; transition:width .3s; }
    .bar > div.warm { background:#d29922; }
    .bar > div.hot { background:#f85149; }
    .reset { color:#8b949e; font-size:.68rem; margin-top:.15rem; }
    .credits, .tokens, .cost, .note { font-size:.76rem; margin:.45rem 0 0; }
    .tokens strong { color:#58a6ff; }
    .cost { color:#3fb950; font-weight:600; }
    .muted { color:#8b949e; font-size:.7rem; margin:.15rem 0 0; }
    .note { color:#8b949e; font-size:.7rem; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin-bottom:.6rem; font-size:.78rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
    .small { padding:.2rem .5rem; font-size:.72rem; }
  `],
})
export class UsageComponent implements OnInit, OnDestroy {
  private readonly svc = inject(UsageService);
  usage = signal<AgentUsage[]>([]);
  busy = signal(false);
  error = signal('');
  updatedAt = signal<Date | null>(null);
  private timer?: ReturnType<typeof setInterval>;

  ngOnInit(): void {
    this.load();
    this.timer = setInterval(() => this.load(), 60_000);
  }

  ngOnDestroy(): void {
    if (this.timer) clearInterval(this.timer);
  }

  load(): void {
    this.busy.set(true);
    this.error.set('');
    this.svc.usage().subscribe({
      next: u => { this.usage.set(u ?? []); this.busy.set(false); this.updatedAt.set(new Date()); },
      error: e => { this.busy.set(false); this.error.set(e.message ?? String(e)); },
    });
  }

  clamp(v: number): number {
    return Math.max(0, Math.min(100, v));
  }

  label(w: UsageWindow, fallback: string): string {
    if (!w.window_minutes) return fallback;
    if (w.window_minutes >= 10080) return '7 Tage';
    if (w.window_minutes >= 300) return '5 Stunden';
    return `${w.window_minutes} min`;
  }
}
