import { Component, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { DecimalPipe, JsonPipe } from '@angular/common';
import {
  BudgetActual, FocusSession, Objective, ProductivityService, Task, TodayPlan,
} from '../core/services/productivity.service';

@Component({
  selector: 'dw-productivity',
  standalone: true,
  imports: [FormsModule, JsonPipe, DecimalPipe],
  template: `
    <div class="prod">
      <header>
        <div>
          <h2>Productivity</h2>
          <p class="hint">Tagesplan, Fokus, Budgets und Review. Deterministisch – ohne LLM.</p>
        </div>
        <div class="actions">
          <span class="muted">{{ plan()?.day }}</span>
          <button class="small" (click)="rollover()">Rollover</button>
          <button class="small" (click)="load()">Refresh</button>
        </div>
      </header>

      @if (error()) { <div class="notice error">{{ error() }}</div> }
      @if (message()) { <div class="notice">{{ message() }}</div> }

      <div class="grid">
        <section class="card focus-card">
          <h3>Focus</h3>
          @if (focusTask(); as ft) {
            <p class="eyebrow">{{ focus()?.status }} · {{ elapsed() }}</p>
            <p class="title">{{ ft.title }}</p>
            <p class="muted">Task {{ ft.id }} · Priorität {{ ft.priority }}</p>
            <div class="row">
              @if (focus()?.status === 'paused') {
                <button (click)="act('resume')">Weiter</button>
              } @else {
                <button (click)="act('pause')">Pause</button>
              }
              <button class="primary" (click)="act('complete')">Fertig</button>
              <button (click)="act('skip')">Überspringen</button>
            </div>
          } @else {
            <p class="muted">Keine aktive Session. Wähle eine Aufgabe und starte den Fokus.</p>
            <button class="primary" [disabled]="!selected()" (click)="startSelected()">Fokus starten</button>
          }
          <p class="muted">Agenten aktiv: {{ plan()?.active_agents ?? 0 }} (nicht durch Fokus begrenzt)</p>
        </section>

        <section class="card">
          <div class="card-head">
            <h3>Tagesplan</h3>
            <span class="muted">{{ tasks().length }} offen</span>
          </div>
          <div class="queue">
            @for (t of tasks(); track t.id) {
              <div class="task" [class.selected]="selected()?.id === t.id" (click)="select(t)">
                <span class="prio">p{{ t.priority }}</span>
                <span class="status">{{ t.status }}</span>
                <span class="name">{{ t.title }}</span>
                @if (t.objective_ids?.length) { <span class="goal">●</span> }
                <span class="spacer"></span>
                <button class="small" (click)="switchTo(t); $event.stopPropagation()">Fokus</button>
                <button class="small" (click)="markDone(t); $event.stopPropagation()">Done</button>
              </div>
            } @empty {
              <p class="muted">Nichts geplant für heute.</p>
            }
          </div>
        </section>

        <section class="card">
          <h3>Aufgabe anlegen</h3>
          <label>Titel <input [(ngModel)]="newTitle" name="title" placeholder="Was ist der nächste Schritt?" /></label>
          <label>Priorität <input type="number" [(ngModel)]="newPriority" name="priority" min="0" max="100" /></label>
          <label>Ziel
            <select [(ngModel)]="newObjective" name="objective">
              <option value="">– kein Ziel –</option>
              @for (o of objectives(); track o.id) { <option [value]="o.id">{{ o.title }}</option> }
            </select>
          </label>
          <button class="primary" [disabled]="!newTitle.trim()" (click)="addTask()">Anlegen</button>
        </section>

        <section class="card">
          <h3>Ziele</h3>
          <div class="queue">
            @for (o of objectives(); track o.id) {
              <div class="task static"><span class="status">{{ o.kind }}</span><span class="name">{{ o.title }}</span></div>
            } @empty { <p class="muted">Noch keine Ziele.</p> }
          </div>
          <label>Neues Ziel <input [(ngModel)]="newObjectiveTitle" name="objtitle" placeholder="z. B. Q4: Produktivität" /></label>
          <label>Art
            <select [(ngModel)]="newObjectiveKind" name="objkind">
              <option value="quarter">Quartal</option>
              <option value="month">Monat</option>
              <option value="week">Woche</option>
              <option value="project">Projekt</option>
            </select>
          </label>
          <button [disabled]="!newObjectiveTitle.trim()" (click)="addObjective()">Ziel anlegen</button>
        </section>

        <section class="card">
          <h3>Budgets (Ist / Soll)</h3>
          @for (b of budgets(); track b.kind + b.scope) {
            <div class="budget">
              <span class="status">{{ b.scope }}</span>
              <span class="name">{{ b.kind }}</span>
              <span class="muted">{{ b.actual | number:'1.0-2' }} / {{ b.target | number:'1.0-2' }}</span>
              @if (b.exceeded) { <span class="over">über</span> }
            </div>
          } @empty { <p class="muted">Keine Budgets gesetzt.</p> }
          <p class="muted">Verifizierte Ergebnisse: {{ verified() }}</p>
        </section>

        <section class="card">
          <h3>Review (deterministisch)</h3>
          <div class="row">
            <button class="small" (click)="loadReport('weekly')">Woche</button>
            <button class="small" (click)="loadReport('monthly')">Monat</button>
          </div>
          @if (reportData(); as rep) {
            <pre class="report">{{ rep | json }}</pre>
          } @else { <p class="muted">Noch kein Report geladen.</p> }
        </section>
      </div>
    </div>
  `,
  styles: [`
    .prod { padding: 1rem 1.2rem; overflow: auto; height: 100%; box-sizing: border-box; }
    header { display: flex; align-items: flex-start; gap: 1rem; margin-bottom: .8rem; }
    h2 { margin: 0 0 .2rem; font-size: 1.05rem; }
    h3 { margin: 0 0 .5rem; font-size: .82rem; text-transform: uppercase; letter-spacing: .05em; color: #8b949e; }
    .hint { color: #8b949e; font-size: .76rem; margin: 0; }
    .actions { margin-left: auto; display: flex; gap: .4rem; align-items: center; }
    .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 1rem; }
    .card { background: #161b22; border: 1px solid #30363d; border-radius: 8px; padding: 1rem; }
    .card-head { display: flex; align-items: baseline; gap: .5rem; }
    .queue { display: flex; flex-direction: column; gap: .2rem; margin-bottom: .6rem; max-height: 320px; overflow: auto; }
    .task { display: flex; align-items: center; gap: .5rem; padding: .3rem .4rem; border: 1px solid transparent; border-radius: 5px; font-size: .78rem; cursor: pointer; }
    .task:hover { background: #1f2630; }
    .task.selected { border-color: #1f6feb; background: #11263b; }
    .task.static { cursor: default; }
    .prio { color: #d29922; font-family: ui-monospace, monospace; font-size: .72rem; }
    .status { color: #8b949e; font-size: .68rem; text-transform: uppercase; width: 82px; }
    .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .goal { color: #3fb950; }
    .spacer { flex: 1; }
    .muted { color: #8b949e; font-size: .72rem; }
    .eyebrow { color: #58a6ff; font-size: .7rem; text-transform: uppercase; margin: 0 0 .2rem; }
    .title { font-size: 1rem; margin: 0 0 .2rem; }
    .row { display: flex; gap: .4rem; flex-wrap: wrap; margin: .5rem 0; }
    label { display: flex; flex-direction: column; gap: .2rem; font-size: .7rem; color: #8b949e; margin: .4rem 0; }
    input, select { background: #0d1117; border: 1px solid #30363d; color: #c9d1d9; border-radius: 5px; padding: .3rem .4rem; font-size: .78rem; }
    button { background: #21262d; border: 1px solid #30363d; color: #c9d1d9; border-radius: 5px; padding: .3rem .6rem; cursor: pointer; font-size: .76rem; }
    button.primary { background: #238636; border-color: #2ea043; color: #fff; }
    button.small { padding: .15rem .45rem; font-size: .7rem; }
    button:disabled { opacity: .5; cursor: default; }
    .notice { padding: .5rem .7rem; background: #11263b; border: 1px solid #1f6feb; border-radius: 6px; margin-bottom: .6rem; font-size: .78rem; }
    .notice.error { background: #3d1114; border-color: #f85149; }
    .budget { display: flex; gap: .5rem; align-items: baseline; font-size: .78rem; padding: .2rem 0; border-bottom: 1px solid #21262d; }
    .over { color: #f85149; font-size: .68rem; }
    .report { background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: .5rem; font-size: .7rem; max-height: 220px; overflow: auto; white-space: pre-wrap; }
  `],
})
export class ProductivityComponent implements OnInit, OnDestroy {
  private readonly svc = inject(ProductivityService);
  readonly plan = signal<TodayPlan | null>(null);
  readonly objectives = signal<Objective[]>([]);
  readonly budgets = signal<BudgetActual[]>([]);
  readonly verified = signal(0);
  readonly reportData = signal<Record<string, unknown> | null>(null);
  readonly selected = signal<Task | null>(null);
  readonly error = signal('');
  readonly message = signal('');
  readonly now = signal(Date.now());
  private timer?: ReturnType<typeof setInterval>;

  newTitle = '';
  newPriority = 50;
  newObjective = '';
  newObjectiveTitle = '';
  newObjectiveKind = 'quarter';

  readonly tasks = computed(() => (this.plan()?.tasks ?? []).filter(t => t.assignee_kind === 'human' && !['done', 'verified', 'cancelled'].includes(t.status)));
  readonly focus = computed(() => this.plan()?.focus ?? null);
  readonly focusTask = computed(() => {
    const f = this.focus();
    if (!f) return null;
    return (this.plan()?.tasks ?? []).find(t => t.id === f.task_id) ?? ({ id: f.task_id, title: f.task_id } as unknown as Task);
  });
  readonly elapsed = computed(() => {
    const f = this.focus();
    if (!f) return '';
    const end = f.ended_at ? Date.parse(f.ended_at) : this.now();
    const secs = Math.max(0, Math.floor((end - Date.parse(f.started_at)) / 1000) - (f.paused_seconds || 0));
    const m = Math.floor(secs / 60);
    return `${m}m ${secs % 60}s`;
  });

  ngOnInit(): void {
    this.load();
    this.timer = setInterval(() => this.now.set(Date.now()), 1000);
  }

  ngOnDestroy(): void {
    if (this.timer) clearInterval(this.timer);
  }

  load(): void {
    this.error.set('');
    const day = new Date().toISOString().slice(0, 10);
    this.svc.plan(day).subscribe({
      next: p => {
        this.plan.set(p);
        if (p.focus) {
          this.selected.set((p.tasks ?? []).find(t => t.id === p.focus!.task_id) ?? null);
        }
      },
      error: e => this.error.set(e.message ?? String(e)),
    });
    this.svc.objectives().subscribe({ next: o => this.objectives.set(o), error: () => {} });
    this.svc.budgetActuals().subscribe({
      next: b => { this.budgets.set(b.budgets ?? []); this.verified.set(b.verified_results); },
      error: () => {},
    });
  }

  select(t: Task): void { this.selected.set(t); }

  startSelected(): void {
    const t = this.selected();
    if (!t) return;
    this.svc.focus('start', t.id).subscribe({ next: () => this.done('Fokus gestartet'), error: e => this.fail(e) });
  }

  act(action: 'pause' | 'resume' | 'complete' | 'skip'): void {
    this.svc.focus(action).subscribe({ next: () => this.done('Fokus ' + action), error: e => this.fail(e) });
  }

  switchTo(t: Task): void {
    this.svc.switchFocus(t.id).subscribe({ next: () => this.done('Gewechselt zu ' + t.title), error: e => this.fail(e) });
  }

  markDone(t: Task): void {
    this.svc.updateTask({ ...t, status: 'done' }).subscribe({ next: () => this.done('Erledigt: ' + t.title), error: e => this.fail(e) });
  }

  addTask(): void {
    const body: Partial<Task> = {
      title: this.newTitle.trim(), day: new Date().toISOString().slice(0, 10),
      priority: this.newPriority, assignee_kind: 'human', status: 'todo',
      objective_ids: this.newObjective ? [this.newObjective] : [],
    };
    this.svc.addTask(body).subscribe({
      next: () => { this.newTitle = ''; this.done('Aufgabe angelegt'); },
      error: e => this.fail(e),
    });
  }

  addObjective(): void {
    this.svc.addObjective({ title: this.newObjectiveTitle.trim(), kind: this.newObjectiveKind, status: 'active' }).subscribe({
      next: () => { this.newObjectiveTitle = ''; this.done('Ziel angelegt'); },
      error: e => this.fail(e),
    });
  }

  rollover(): void {
    const to = new Date().toISOString().slice(0, 10);
    const from = new Date(Date.now() - 86400000).toISOString().slice(0, 10);
    this.svc.rollover(from, to).subscribe({
      next: r => this.done(`${r.moved ?? 0} Aufgaben übernommen`),
      error: e => this.fail(e),
    });
  }

  loadReport(kind: 'weekly' | 'monthly'): void {
    this.svc.report(kind).subscribe({ next: r => this.reportData.set(r), error: e => this.fail(e) });
  }

  private done(msg: string): void {
    this.message.set(msg);
    this.load();
  }

  private fail(e: unknown): void {
    this.error.set((e as Error)?.message ?? String(e));
  }
}
