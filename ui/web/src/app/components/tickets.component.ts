import { Component, Input, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { Agent, LiveWork, Workspace } from '../core/models';
import { GateReviewView, GateRunView, GateService } from '../core/services/gate.service';
import { ProductivityService } from '../core/services/productivity.service';
import {
  KnownHuman, Me, ProviderStatus, SyncState, TICKET_STATUSES, Ticket, TicketActor, TicketComment, TicketService, TicketStatus, issueKey, refString,
} from '../core/services/ticket.service';

const STATUS_TEXT: Record<TicketStatus, string> = {
  backlog: 'Backlog', todo: 'Offen', in_progress: 'In Arbeit', in_review: 'Im Review', blocked: 'Blockiert', done: 'Erledigt', cancelled: 'Abgebrochen',
};

// Tickets for agents and humans (ADR 0003 D-7): provider-neutral, actions
// only where /api/v1/tickets/me grants them, capabilities hide what the
// backend cannot do.
@Component({
  selector: 'dw-tickets',
  standalone: true,
  imports: [FormsModule],
  template: `
    <div class="tickets">
      <header>
        <div>
          <h2>Tickets</h2>
          @if (provider(); as p) {
            @if (p.configured && p.primary; as pr) {
              <span class="chip provider" [class.bad]="!pr.health.ok" [attr.title]="pr.health.detail || ''">
                {{ pr.name }} · {{ pr.health.ok ? 'erreichbar' : 'nicht erreichbar' }}
              </span>
              @if (p.mirror; as mi) {
                <span class="chip mirror" [class.bad]="!mi.health.ok">Spiegel {{ mi.name }} · {{ mi.health.ok ? 'ok' : 'nicht erreichbar' }}</span>
              }
              @if (sync()?.status; as st) {
                <span class="chip sync" [class.bad]="(st.last_report?.errors?.length ?? 0) > 0">
                  Sync: {{ st.linked }} verknüpft · Verzug {{ st.lag_seconds }}s · {{ st.conflicts_total }} Konflikte
                </span>
              }
            }
          }
        </div>
        <span class="spacer"></span>
        @if (me(); as m) { <span class="muted who" [attr.title]="m.permissions.join(', ')">{{ m.actor.name || m.actor.id }} · {{ m.roles.join(', ') || 'keine Rolle' }}</span> }
        @if (can('sync.run') && sync()?.configured) { <button class="small" (click)="runSync()">Sync jetzt</button> }
        <button class="small" (click)="reload()">Refresh</button>
      </header>

      @if (me()?.config_error; as ce) { <div class="notice error" role="alert">Rollen-Konfiguration ungültig: {{ ce }}</div> }
      @if (error()) { <div class="notice error" role="alert">{{ error() }}</div> }
      @if (message()) { <div class="notice" role="status">{{ message() }}</div> }

      @if (provider() && !provider()!.configured) {
        <div class="card unconfigured">
          <h3>Ticketing ist nicht eingerichtet</h3>
          <p>{{ provider()!.error }}</p>
          <p class="muted">Vorschlag: <code>dockweave ticket provider detect</code>, dann <code>TICKET_PROVIDER</code> in <code>/etc/dockweave/dockweaved.conf</code> setzen und den Daemon neu starten.</p>
        </div>
      } @else {
        <div class="toolbar">
          <input class="search" [(ngModel)]="query" name="q" placeholder="Suchen…" (keyup.enter)="reload()" />
          <select [(ngModel)]="statusFilter" name="statusFilter" (change)="reload()">
            <option value="">alle Status</option>
            @for (s of statuses; track s) { <option [value]="s">{{ statusText(s) }}</option> }
          </select>
          <select [(ngModel)]="assigneeFilter" name="assigneeFilter" (change)="reload()">
            <option value="">alle Zuständigen</option>
            @for (a of agents; track a.id) { <option [value]="'agent:' + a.id">{{ a.display_name || a.name }}</option> }
          </select>
          <div class="seg">
            <button class="small" [class.active]="mode() === 'list'" (click)="mode.set('list')">Liste</button>
            <button class="small" [class.active]="mode() === 'board'" (click)="mode.set('board')">Board</button>
          </div>
          @if (can('ticket.create')) { <button class="small primary" (click)="creating.set(!creating())">Neues Ticket</button> }
        </div>

        @if (creating()) {
          <form class="card create" (submit)="$event.preventDefault(); create()">
            <label>Titel <input [(ngModel)]="newTitle" name="newTitle" required /></label>
            <label>Beschreibung <textarea rows="3" [(ngModel)]="newDescription" name="newDescription"></textarea></label>
            <div class="row">
              <label>Priorität
                <select [(ngModel)]="newPriority" name="newPriority">
                  <option value="">–</option><option value="critical">kritisch</option><option value="high">hoch</option><option value="medium">mittel</option><option value="low">niedrig</option>
                </select>
              </label>
              @if (can('ticket.assign')) {
                <label>Zuständig
                  <select [(ngModel)]="newAssignee" name="newAssignee">
                    <option value="">niemand</option>
                    <optgroup label="Agenten">
                      @for (a of agents; track a.id) { <option [value]="'agent:' + a.id">{{ a.display_name || a.name }}</option> }
                    </optgroup>
                    @if (caps()?.human_assignee && humans().length) {
                      <optgroup label="Menschen">
                        @for (h of humans(); track h.id) { <option [value]="'human:' + h.id">{{ h.name || h.id }}</option> }
                      </optgroup>
                    }
                  </select>
                </label>
              }
              @if (caps()?.workspaces && workspaces.length) {
                <label>Workspace
                  <select [(ngModel)]="newWorkspace" name="newWorkspace">
                    <option value="">Standard (DISPATCH_DEFAULT_WORKSPACE)</option>
                    @for (w of workspaces; track w.id) { <option [value]="w.id">{{ w.name }}</option> }
                  </select>
                </label>
              }
            </div>
            <button class="primary" type="submit" [disabled]="!newTitle.trim()">Anlegen</button>
          </form>
        }

        <div class="body">
          <section class="lane-wrap">
            @if (mode() === 'list') {
              <div class="list">
                @for (t of tickets(); track refKey(t)) {
                  <div class="ticket-row" [class.selected]="isSelected(t)" (click)="select(t)" [attr.data-ticket]="refKey(t)">
                    <span class="ident mono">{{ t.identifier || t.ref.id }}</span>
                    <span class="title">{{ t.title }}</span>
                    @if (liveFor(t)) { <span class="live" title="Agent arbeitet gerade">● läuft</span> }
                    <span class="spacer"></span>
                    @if (t.assignee) { <span class="muted">{{ assigneeName(t) }}</span> }
                    <span class="status st-{{ t.status }}">{{ statusText(t.status) }}</span>
                  </div>
                } @empty { <p class="muted">Keine Tickets.</p> }
              </div>
            } @else {
              <div class="board">
                @for (col of boardColumns; track col) {
                  <div class="col" [attr.data-col]="col">
                    <h4>{{ statusText(col) }} <span class="muted">{{ byStatus()[col].length }}</span></h4>
                    @for (t of byStatus()[col]; track refKey(t)) {
                      <div class="card mini" [class.selected]="isSelected(t)" (click)="select(t)" [attr.data-ticket]="refKey(t)">
                        <span class="ident mono">{{ t.identifier || t.ref.id }}</span>
                        <div>{{ t.title }}</div>
                        @if (t.assignee) { <div class="muted">{{ assigneeName(t) }}</div> }
                        @if (liveFor(t)) { <span class="live">● läuft</span> }
                      </div>
                    }
                  </div>
                }
              </div>
            }
          </section>

          @if (selected(); as t) {
            <aside class="detail">
              <div class="detail-head">
                <span class="ident mono">{{ t.identifier || t.ref.id }}</span>
                <span class="muted">{{ refKey(t) }}</span>
                <button class="small ghost" (click)="selected.set(null)">✕</button>
              </div>
              @if (can('ticket.edit')) {
                <input class="title-edit" [(ngModel)]="editTitle" name="editTitle" (blur)="saveField('title', editTitle)" />
                <textarea rows="4" [(ngModel)]="editDescription" name="editDescription" (blur)="saveField('description', editDescription)"></textarea>
              } @else {
                <h3>{{ t.title }}</h3>
                <p class="desc">{{ t.description }}</p>
              }
              <div class="meta">
                <label>Status
                  <select [ngModel]="t.status" name="detailStatus" [disabled]="!can('ticket.edit')" (ngModelChange)="saveField('status', $event)">
                    @for (s of statuses; track s) { <option [value]="s">{{ statusText(s) }}</option> }
                  </select>
                </label>
                <label>Zuständig
                  <select [ngModel]="assigneeValue(t)" name="detailAssignee" [disabled]="!can('ticket.assign')" (ngModelChange)="assign($event)">
                    <option value="">niemand</option>
                    @if (t.assignee?.kind === 'human' && !knownHuman(t.assignee?.id)) { <option [value]="assigneeValue(t)">{{ assigneeName(t) }}</option> }
                    <optgroup label="Agenten">
                      @for (a of agents; track a.id) { <option [value]="'agent:' + a.id">{{ a.display_name || a.name }}</option> }
                    </optgroup>
                    @if (caps()?.human_assignee && humans().length) {
                      <optgroup label="Menschen">
                        @for (h of humans(); track h.id) { <option [value]="'human:' + h.id">{{ h.name || h.id }}</option> }
                      </optgroup>
                    }
                  </select>
                </label>
                @if (caps()?.workspaces) {
                  <label>Workspace
                    <select [ngModel]="t.workspace_id || ''" name="detailWorkspace" [disabled]="!can('ticket.edit')" (ngModelChange)="setWorkspace($event)">
                      <option value="">Standard</option>
                      @for (w of workspaces; track w.id) { <option [value]="w.id">{{ w.name }}</option> }
                    </select>
                  </label>
                }
                @if (t.checked_out_by) { <span class="chip">gesperrt von {{ agentName(t.checked_out_by) }}</span> }
                @if (liveFor(t); as lw) { <span class="chip live-chip">● Agent arbeitet ({{ agentName(lw.agent_id || '') }})</span> }
                @if (t.extra?.['verified']) { <span class="chip ok">nachgewiesen</span> }
              </div>
              @if (t.parent || t.blocked_by?.length) {
                <div class="rel muted">
                  @if (t.parent) { <div>Eltern: {{ t.parent.id }}</div> }
                  @if (t.blocked_by?.length) { <div>Blockiert durch: {{ blockedText(t) }}</div> }
                </div>
              }

              <div class="actions">
                @if (can('agent.wake') && t.assignee?.kind === 'agent') {
                  <button class="small" (click)="runNow(t)">Heartbeat jetzt</button>
                }
                @if (can('ticket.approve')) {
                  @if (approval(); as ap) { <span class="chip ok">freigegeben von {{ ap.actor }}</span> }
                  @else { <button class="small" (click)="approve(t)">Freigeben</button> }
                }
                <button class="small" (click)="toDailyPlan(t)">In Tagesplan</button>
                @if (can('ticket.delete') && caps()?.delete) {
                  <button class="small danger" (click)="remove(t)">Löschen</button>
                }
              </div>

              <h4>Gates</h4>
              @for (r of runsFor(t); track r.id) {
                <div class="gate-run"><span class="mono">{{ r.status }}</span> <span class="muted">{{ r.passed }}/{{ r.gates }}{{ r.tamper ? ' · TAMPER' : '' }} · {{ when(r.started_at || '') }}</span></div>
              } @empty { <p class="muted">Keine Gate-Läufe zu diesem Ticket.</p> }
              @if (reviewFor(t); as rv) { <div class="chip" [class.ok]="rv.verdict === 'PASS'" [class.bad]="rv.verdict === 'FAIL'">Review {{ rv.verdict }}{{ rv.note ? ': ' + rv.note : '' }}</div> }

              @if (caps()?.threads) {
                <h4>Verlauf</h4>
                <div class="thread">
                  @for (c of comments(); track c.id) {
                    <div class="comment" [class.agent]="c.author.kind === 'agent'">
                      <div class="author">{{ actorText(c.author) }} <span class="muted">{{ when(c.created_at) }}</span></div>
                      <pre>{{ c.body }}</pre>
                    </div>
                  } @empty { <p class="muted">Noch keine Kommentare.</p> }
                </div>
                @if (can('ticket.comment')) {
                  <form class="reply" (submit)="$event.preventDefault(); addComment(t)">
                    <textarea rows="2" [(ngModel)]="reply" name="reply" placeholder="Antworten…"></textarea>
                    <button class="small primary" type="submit" [disabled]="!reply.trim()">Senden</button>
                  </form>
                }
              }
            </aside>
          }
        </div>
      }
    </div>
  `,
  styles: [`
    .tickets { padding: 1rem 1.2rem; height: 100%; box-sizing: border-box; display: flex; flex-direction: column; gap: .6rem; overflow: hidden; }
    header { display: flex; align-items: center; gap: .6rem; flex-wrap: wrap; }
    h2 { margin: 0 .6rem .2rem 0; font-size: 1.05rem; display: inline; }
    .spacer { flex: 1; }
    .chip { display: inline-block; font-size: .7rem; padding: .1rem .45rem; border-radius: 999px; border: 1px solid #30363d; color: #8b949e; margin-right: .3rem; }
    .chip.ok, .provider:not(.bad) { border-color: #238636; color: #3fb950; }
    .chip.bad { border-color: #da3633; color: #f85149; }
    .live, .live-chip { color: #3fb950; font-size: .72rem; }
    .muted { color: #8b949e; font-size: .78rem; }
    .mono, .ident { font-family: ui-monospace, monospace; font-size: .75rem; color: #79c0ff; }
    .toolbar { display: flex; gap: .5rem; align-items: center; flex-wrap: wrap; }
    .search { min-width: 14rem; }
    .seg button.active { border-color: #58a6ff; color: #58a6ff; }
    .card { border: 1px solid #30363d; border-radius: 8px; padding: .6rem .7rem; background: #161b22; }
    .create { display: flex; flex-direction: column; gap: .4rem; max-width: 40rem; }
    .row { display: flex; gap: .6rem; }
    .body { display: flex; gap: .8rem; min-height: 0; flex: 1; }
    .lane-wrap { flex: 1; min-width: 0; overflow: auto; }
    .ticket-row { display: flex; gap: .6rem; align-items: center; padding: .4rem .5rem; border-bottom: 1px solid #21262d; cursor: pointer; }
    .ticket-row.selected, .mini.selected { background: #1f6feb22; }
    .status { font-size: .7rem; padding: .05rem .4rem; border-radius: 4px; border: 1px solid #30363d; }
    .st-done { color: #3fb950; } .st-blocked { color: #f85149; } .st-in_progress { color: #d29922; } .st-in_review { color: #bc8cff; }
    .board { display: flex; gap: .6rem; align-items: flex-start; overflow-x: auto; padding-bottom: .4rem; }
    .col { min-width: 12rem; flex: 1; }
    .col h4 { font-size: .8rem; margin: .2rem 0 .4rem; }
    .mini { margin-bottom: .4rem; cursor: pointer; font-size: .82rem; }
    .detail { width: 26rem; flex-shrink: 0; overflow: auto; border-left: 1px solid #30363d; padding-left: .8rem; display: flex; flex-direction: column; gap: .5rem; }
    .detail-head { display: flex; gap: .5rem; align-items: center; }
    .detail-head button { margin-left: auto; }
    .title-edit { font-weight: 700; font-size: .95rem; }
    .meta { display: flex; flex-wrap: wrap; gap: .5rem; align-items: center; }
    .actions { display: flex; gap: .4rem; flex-wrap: wrap; align-items: center; }
    h4 { margin: .4rem 0 .2rem; font-size: .82rem; }
    .thread { display: flex; flex-direction: column; gap: .4rem; }
    .comment { border: 1px solid #21262d; border-radius: 6px; padding: .35rem .5rem; }
    .comment.agent { border-color: #23863655; }
    .comment pre { white-space: pre-wrap; margin: .2rem 0 0; font-size: .78rem; font-family: inherit; }
    .author { font-size: .74rem; font-weight: 600; }
    .reply { display: flex; flex-direction: column; gap: .3rem; }
    .notice { padding: .4rem .6rem; border: 1px solid #30363d; border-radius: 6px; font-size: .82rem; }
    .notice.error { border-color: #f85149; color: #f85149; }
    button.danger { border-color: #da3633; color: #f85149; }
    @media (max-width: 900px) { .body { flex-direction: column; } .detail { width: auto; border-left: 0; padding-left: 0; } }
  `],
})
export class TicketsComponent implements OnInit, OnDestroy {
  private readonly api = inject(TicketService);
  private readonly gates = inject(GateService);
  private readonly productivity = inject(ProductivityService);

  @Input() agents: Agent[] = [];
  @Input() live: LiveWork[] | null = null;
  @Input() workspaces: Workspace[] = [];

  readonly statuses = TICKET_STATUSES;
  readonly boardColumns: TicketStatus[] = ['todo', 'in_progress', 'in_review', 'blocked', 'done'];
  readonly provider = signal<ProviderStatus | null>(null);
  readonly me = signal<Me | null>(null);
  readonly sync = signal<SyncState | null>(null);
  readonly tickets = signal<Ticket[]>([]);
  readonly selected = signal<Ticket | null>(null);
  readonly comments = signal<TicketComment[]>([]);
  readonly runs = signal<GateRunView[]>([]);
  readonly reviews = signal<GateReviewView[]>([]);
  readonly approval = signal<{ actor: string } | null>(null);
  readonly humans = signal<KnownHuman[]>([]);
  readonly error = signal('');
  readonly message = signal('');
  readonly mode = signal<'list' | 'board'>('list');
  readonly creating = signal(false);

  query = '';
  statusFilter = '';
  assigneeFilter = '';
  newTitle = '';
  newDescription = '';
  newPriority = '';
  newAssignee = '';
  newWorkspace = '';
  editTitle = '';
  editDescription = '';
  reply = '';
  private timer?: ReturnType<typeof setInterval>;

  readonly caps = computed(() => this.provider()?.primary?.capabilities ?? null);
  readonly byStatus = computed(() => {
    const out = {} as Record<TicketStatus, Ticket[]>;
    for (const s of TICKET_STATUSES) out[s] = [];
    for (const t of this.tickets()) (out[t.status] ?? out.todo).push(t);
    return out;
  });

  ngOnInit(): void {
    this.reload();
    this.timer = setInterval(() => this.refreshQuiet(), 10000);
  }

  ngOnDestroy(): void { if (this.timer) clearInterval(this.timer); }

  statusText(s: TicketStatus): string { return STATUS_TEXT[s] ?? s; }
  when(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('de-DE', { dateStyle: 'short', timeStyle: 'short' });
  }
  refKey(t: Ticket): string { return refString(t.ref); }
  isSelected(t: Ticket): boolean { const s = this.selected(); return !!s && refString(s.ref) === refString(t.ref); }
  can(p: string): boolean { return !!this.me()?.permissions?.includes(p); }

  agentName(id: string): string {
    const a = this.agents.find(x => x.id === id);
    return a ? (a.display_name || a.name) : id;
  }
  assigneeValue(t: Ticket): string { return t.assignee && t.assignee.kind !== 'system' ? `${t.assignee.kind}:${t.assignee.id}` : ''; }
  knownHuman(id?: string): boolean { return !!id && this.humans().some(h => h.id === id); }
  humanName(id: string): string { const h = this.humans().find(x => x.id === id); return h?.name || id; }

  assigneeName(t: Ticket): string {
    const a = t.assignee;
    if (!a) return '';
    return a.kind === 'agent' ? this.agentName(a.id || '') : (a.name || this.humanName(a.id || ''));
  }
  actorText(a: TicketActor): string {
    if (a.kind === 'agent') return this.agentName(a.id || '') + ' (Agent)';
    if (a.kind === 'system') return a.name || 'System';
    return a.name || a.id || 'Mensch';
  }
  blockedText(t: Ticket): string {
    return (t.blocked_by || []).map(b => this.tickets().find(x => refString(x.ref) === refString(b))?.identifier || b.id).join(', ');
  }
  liveFor(t: Ticket): LiveWork | undefined {
    const key = issueKey(t.ref);
    return (this.live || []).find(w => w.kind === 'endpoint_job' && w.issue_id === key);
  }
  runsFor(t: Ticket): GateRunView[] { const key = issueKey(t.ref); return this.runs().filter(r => r.issue_id === key).slice(0, 5); }
  reviewFor(t: Ticket): GateReviewView | undefined { const key = issueKey(t.ref); return this.reviews().find(r => r.issue_id === key); }

  private fail = (e: Error) => { this.error.set(e.message); };

  reload(): void {
    this.error.set('');
    this.api.provider().subscribe({ next: p => this.provider.set(p), error: this.fail });
    this.api.me().subscribe({ next: m => this.me.set(m), error: () => this.me.set(null) });
    this.api.syncStatus().subscribe({ next: s => this.sync.set(s), error: () => this.sync.set(null) });
    this.api.humans().subscribe({ next: h => this.humans.set(h.humans ?? []), error: () => this.humans.set([]) });
    this.loadTickets();
    this.gates.runs('', 50).subscribe({ next: r => this.runs.set(r?.runs ?? []), error: () => this.runs.set([]) });
    this.gates.reviews(50).subscribe({ next: r => this.reviews.set(r?.reviews ?? []), error: () => this.reviews.set([]) });
  }

  private refreshQuiet(): void {
    this.loadTickets();
    const s = this.selected();
    if (s) this.loadThread(s);
  }

  private loadTickets(): void {
    this.api.list({
      q: this.query.trim() || undefined,
      status: this.statusFilter ? [this.statusFilter as TicketStatus] : undefined,
      assignee: this.assigneeFilter || undefined,
      limit: 500,
    }).subscribe({
      next: r => {
        this.tickets.set(r.tickets ?? []);
        const s = this.selected();
        if (s) {
          const fresh = (r.tickets ?? []).find(t => refString(t.ref) === refString(s.ref));
          if (fresh && fresh.version !== s.version) this.setSelected(fresh);
        }
      },
      error: (e: Error) => { this.tickets.set([]); if (this.provider()?.configured !== false) this.error.set(e.message); },
    });
  }

  select(t: Ticket): void {
    this.setSelected(t);
    this.loadThread(t);
    this.approval.set(null);
    this.api.approval(issueKey(t.ref)).subscribe({ next: a => this.approval.set(a), error: () => this.approval.set(null) });
  }

  private setSelected(t: Ticket): void {
    this.selected.set(t);
    this.editTitle = t.title;
    this.editDescription = t.description || '';
  }

  private loadThread(t: Ticket): void {
    if (!this.caps()?.threads) { this.comments.set([]); return; }
    this.api.comments(refString(t.ref)).subscribe({ next: r => this.comments.set(r.comments ?? []), error: () => this.comments.set([]) });
  }

  create(): void {
    const body: Record<string, unknown> = { title: this.newTitle.trim(), description: this.newDescription };
    if (this.newPriority) body['priority'] = this.newPriority;
    const asg = this.actorFrom(this.newAssignee);
    if (asg) body['assignee'] = asg;
    if (this.newWorkspace) body['workspace_id'] = this.newWorkspace;
    this.api.create(body as never).subscribe({
      next: t => {
        this.newTitle = ''; this.newDescription = ''; this.newPriority = ''; this.newAssignee = ''; this.newWorkspace = '';
        this.creating.set(false);
        this.message.set(`Ticket ${t.identifier || t.ref.id} angelegt.`);
        this.loadTickets();
        this.select(t);
      },
      error: this.fail,
    });
  }

  saveField(field: 'title' | 'description' | 'status', value: string): void {
    const t = this.selected();
    if (!t || (field === 'title' && (!value.trim() || value === t.title)) || (field === 'description' && value === (t.description || '')) || (field === 'status' && value === t.status)) return;
    this.api.update(refString(t.ref), { [field]: value, expected_version: t.version }).subscribe({
      next: u => { this.setSelected(u); this.loadTickets(); },
      error: (e: Error) => { this.fail(e); this.loadTickets(); },
    });
  }

  private actorFrom(v: string): TicketActor | null {
    if (!v) return null;
    const i = v.indexOf(':');
    const kind = v.slice(0, i) as 'agent' | 'human';
    const id = v.slice(i + 1);
    return kind === 'human' ? { kind, id, name: this.humanName(id) } : { kind, id };
  }

  setWorkspace(id: string): void {
    const t = this.selected();
    if (!t || id === (t.workspace_id || '')) return;
    this.api.update(refString(t.ref), { workspace_id: id }).subscribe({ next: x => { this.setSelected(x); this.loadTickets(); }, error: this.fail });
  }

  remove(t: Ticket): void {
    if (!confirm(`Ticket ${t.identifier || t.ref.id} „${t.title}“ wirklich löschen? Kommentare werden mitgelöscht.`)) return;
    this.api.remove(refString(t.ref)).subscribe({
      next: () => { this.selected.set(null); this.message.set(`Ticket ${t.identifier || t.ref.id} gelöscht.`); this.loadTickets(); },
      error: this.fail,
    });
  }

  assign(value: string): void {
    const t = this.selected();
    if (!t || value === this.assigneeValue(t)) return;
    const a = this.actorFrom(value);
    const u = a ? { assignee: a } : { clear_assignee: true };
    this.api.update(refString(t.ref), u).subscribe({ next: x => { this.setSelected(x); this.loadTickets(); }, error: this.fail });
  }

  addComment(t: Ticket): void {
    const body = this.reply.trim();
    if (!body) return;
    this.api.comment(refString(t.ref), body).subscribe({ next: () => { this.reply = ''; this.loadThread(t); }, error: this.fail });
  }

  runNow(t: Ticket): void {
    const agent = t.assignee?.id;
    if (!agent) return;
    this.message.set('Heartbeat läuft…');
    this.api.run(agent).subscribe({
      next: o => { this.message.set(`Heartbeat ${this.agentName(agent)}: ${o.kind}${o.status ? ' → ' + o.status : ''}${o.detail ? ' (' + o.detail + ')' : ''}`); this.refreshQuiet(); },
      error: this.fail,
    });
  }

  approve(t: Ticket): void {
    this.api.approve(issueKey(t.ref)).subscribe({
      next: () => { this.message.set('Freigabe erteilt.'); this.select(t); this.loadTickets(); },
      error: this.fail,
    });
  }

  runSync(): void {
    this.api.syncRun().subscribe({
      next: r => { this.message.set(r.complete ? 'Sync abgeschlossen.' : 'Sync unvollständig – siehe Status.'); this.reload(); },
      error: this.fail,
    });
  }

  toDailyPlan(t: Ticket): void {
    const today = new Date().toISOString().slice(0, 10);
    this.productivity.addTask({
      title: `[${t.identifier || t.ref.id}] ${t.title}`, description: `Ticket ${refString(t.ref)}`,
      assignee_kind: 'human', status: 'todo', priority: 50, day: today,
    }).subscribe({ next: () => this.message.set('In den heutigen Tagesplan übernommen.'), error: this.fail });
  }
}

