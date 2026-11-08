import { Component, Input, computed } from '@angular/core';
import { DatePipe, DecimalPipe } from '@angular/common';
import { Agent, AgentActivity, BrokerGrant, BrokerRequestEntry, BrokerStatus, Event, FileConflict, MountState } from '../core/models';

@Component({
  selector: 'dw-analysis',
  standalone: true,
  imports: [DatePipe, DecimalPipe],
  template: `
    <div class="analysis">
      <div class="cards">
        <div class="card"><div class="label">Agents</div><div class="value">{{ agents.length }}</div></div>
        <div class="card"><div class="label">Running</div><div class="value">{{ running() }}</div></div>
        <div class="card"><div class="label">Active mounts</div><div class="value">{{ mounts.length }}</div></div>
        <div class="card"><div class="label">Conflicts</div><div class="value" [class.bad]="conflicts.length">{{ conflicts.length }}</div></div>
        <div class="card"><div class="label">Broker</div><div class="value small">{{ broker?.status || 'unavailable' }}</div></div>
        <div class="card"><div class="label">Secrets / grants</div><div class="value small">{{ broker ? broker.secrets + ' / ' + broker.grants : '—' }}</div></div>
      </div>

      <section>
        <h3>Activity</h3>
        <table>
          <thead><tr><th>Agent</th><th>State</th><th>Activity</th><th>CPU</th><th>Memory</th><th>Processes</th></tr></thead>
          <tbody>
            @for (a of agents; track a.id) {
              @let act = activityFor(a.id);
              <tr>
                <td class="mono">{{ a.name }}</td>
                <td>{{ act?.state || a.state }}</td>
                <td><span class="badge" [class.warn]="act?.activity === 'WRITING'" [class.ok]="act?.activity === 'READING'">{{ act?.activity || 'UNKNOWN' }}</span></td>
                <td>
                  <div class="bar"><span [style.width.%]="Math.min(100, act?.cpu_percent || 0)"></span></div>
                  {{ (act?.cpu_percent || 0) | number:'1.0-0' }}%
                </td>
                <td>{{ ((act?.memory_bytes || 0) / 1048576) | number:'1.0-0' }} MiB</td>
                <td>{{ act?.processes || 0 }}</td>
              </tr>
            }
          </tbody>
        </table>
      </section>

      <div class="cols">
        <section>
          <h3>File conflicts</h3>
          @for (c of conflicts; track c.path) {
            <div class="row">
              <span class="mono">{{ c.path }}</span>
              <span class="muted">agents: {{ c.agent_ids.join(', ') }} · writers: {{ c.writers.join(', ') || '—' }}</span>
            </div>
          }
          @if (!conflicts.length) { <p class="muted">No conflicts detected.</p> }
        </section>

        <section>
          <h3>Broker traffic (secret-free)</h3>
          <table>
            <thead><tr><th>Time</th><th>Agent</th><th>Provider</th><th>Status</th><th>Tokens</th></tr></thead>
            <tbody>
              @for (r of brokerRequests.slice(0, 20); track $index) {
                <tr [class.bad-row]="r.denied">
                  <td>{{ r.time | date:'HH:mm:ss' }}</td>
                  <td class="mono">{{ r.agent }}</td>
                  <td>{{ r.provider }}</td>
                  <td>{{ r.status }}</td>
                  <td>{{ r.prompt_tokens }}+{{ r.completion_tokens }}</td>
                </tr>
              }
            </tbody>
          </table>
          @if (!brokerRequests.length) { <p class="muted">No broker requests logged.</p> }
        </section>
      </div>

      <section>
        <h3>Events</h3>
        <table>
          <thead><tr><th>Time</th><th>Type</th><th>Actor</th><th>Message</th></tr></thead>
          <tbody>
            @for (e of events.slice(0, 60); track e.id) {
              <tr>
                <td class="nowrap">{{ e.timestamp | date:'MM-dd HH:mm:ss' }}</td>
                <td class="mono">{{ e.type }}</td>
                <td>{{ e.actor }}</td>
                <td class="msg">{{ e.message }}</td>
              </tr>
            }
          </tbody>
        </table>
      </section>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .analysis { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    .cards { display:grid; grid-template-columns:repeat(auto-fit,minmax(150px,1fr)); gap:.7rem; margin-bottom:1rem; }
    .card { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:.8rem 1rem; }
    .label { color:#8b949e; font-size:.75rem; text-transform:uppercase; letter-spacing:.05em; }
    .value { font-size:1.8rem; font-weight:600; color:#58a6ff; }
    .value.small { font-size:1rem; color:#c9d1d9; }
    .value.bad { color:#f85149; }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; margin-bottom:1rem; }
    h3 { margin:0 0 .8rem; font-size:.95rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.06em; }
    .cols { display:grid; grid-template-columns:1fr 1fr; gap:1rem; }
    @media (max-width:900px) { .cols { grid-template-columns:1fr; } }
    table { width:100%; border-collapse:collapse; font-size:.82rem; }
    th { text-align:left; color:#8b949e; font-weight:500; border-bottom:1px solid #30363d; padding:.35rem .4rem; }
    td { padding:.35rem .4rem; border-bottom:1px solid #21262d; vertical-align:top; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; }
    .nowrap { white-space:nowrap; }
    .msg { word-break:break-word; }
    .badge { background:#21262d; border-radius:999px; padding:.1rem .5rem; font-size:.72rem; }
    .badge.ok { color:#3fb950; } .badge.warn { color:#d29922; }
    .bad-row { background:#3d111433; }
    .bar { display:inline-block; width:80px; height:6px; background:#21262d; border-radius:3px; overflow:hidden; margin-right:.4rem; vertical-align:middle; }
    .bar span { display:block; height:100%; background:#58a6ff; }
    .row { padding:.4rem 0; border-bottom:1px solid #21262d; display:flex; flex-direction:column; gap:.15rem; }
    .row:last-child { border-bottom:none; }
  `],
})
export class AnalysisComponent {
  @Input() agents: Agent[] = [];
  @Input() activity: AgentActivity[] = [];
  @Input() conflicts: FileConflict[] = [];
  @Input() events: Event[] = [];
  @Input() mounts: MountState[] = [];
  @Input() broker: BrokerStatus | null = null;
  @Input() brokerGrants: BrokerGrant[] = [];
  @Input() brokerRequests: BrokerRequestEntry[] = [];

  readonly Math = Math;
  readonly running = computed(() => this.agents.filter(a => a.state === 'running').length);

  activityFor(id: string): AgentActivity | undefined {
    return this.activity.find(a => a.agent_id === id);
  }
}
