import { Injectable, computed, inject, signal } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { ApiService } from './api.service';
import {
  Agent, AgentActivity, AgentPaths, BrokerGrant, BrokerRequestEntry, BrokerStatus,
  EgressStatus, Event, FileConflict, Grant, MountState, Workspace,
} from './models';

@Injectable({ providedIn: 'root' })
export class Store {
  private readonly api = inject(ApiService);

  readonly agents = signal<Agent[]>([]);
  readonly activity = signal<AgentActivity[]>([]);
  readonly workspaces = signal<Workspace[]>([]);
  readonly grants = signal<Grant[]>([]);
  readonly mounts = signal<MountState[]>([]);
  readonly events = signal<Event[]>([]);
  readonly conflicts = signal<FileConflict[]>([]);
  readonly broker = signal<BrokerStatus | null>(null);
  readonly brokerGrants = signal<BrokerGrant[]>([]);
  readonly brokerRequests = signal<BrokerRequestEntry[]>([]);
  readonly paths = signal<Record<string, AgentPaths>>({});
  readonly egress = signal<Record<string, EgressStatus>>({});
  readonly providers = signal<Record<string, string>>({});
  readonly selectedId = signal<string | null>(null);
  readonly error = signal<string>('');
  readonly loading = signal(false);

  readonly selected = computed(() => {
    const id = this.selectedId();
    return this.agents().find(a => a.id === id) ?? null;
  });

  readonly selectedActivity = computed(() => {
    const id = this.selectedId();
    return this.activity().find(a => a.agent_id === id) ?? null;
  });

  readonly selectedGrants = computed(() => {
    const id = this.selectedId();
    return this.grants().filter(g => g.agent_id === id);
  });

  readonly selectedBrokerGrants = computed(() => {
    const name = this.selected()?.name;
    return this.brokerGrants().filter(g => g.agent === name);
  });

  select(id: string): void { this.selectedId.set(id); }

  async refresh(): Promise<void> {
    this.loading.set(true);
    const results = await Promise.allSettled([
      firstValueFrom(this.api.agents()),
      firstValueFrom(this.api.activity()),
      firstValueFrom(this.api.workspaces()),
      firstValueFrom(this.api.grants()),
      firstValueFrom(this.api.mounts()),
      firstValueFrom(this.api.events(120)),
      firstValueFrom(this.api.conflicts()),
      firstValueFrom(this.api.brokerStatus()),
      firstValueFrom(this.api.brokerGrants()),
      firstValueFrom(this.api.brokerRequests()),
      firstValueFrom(this.api.providers()),
    ]);
    const [agents, activity, workspaces, grants, mounts, events, conflicts, broker, bgrants, breqs, providers] = results;
    if (agents.status === 'fulfilled') {
      this.agents.set(agents.value);
      if (!this.selectedId() && agents.value.length) {
        const running = agents.value.find(a => a.state === 'running');
        this.select((running ?? agents.value[0]).id);
      }
      if (this.selectedId() && !agents.value.some(a => a.id === this.selectedId())) {
        this.select(agents.value[0]?.id ?? null);
      }
    } else {
      this.error.set('Daemon nicht erreichbar: ' + (agents.reason?.message ?? agents.reason));
    }
    if (activity.status === 'fulfilled') this.activity.set(activity.value ?? []);
    if (workspaces.status === 'fulfilled') this.workspaces.set(workspaces.value ?? []);
    if (grants.status === 'fulfilled') this.grants.set(grants.value ?? []);
    if (mounts.status === 'fulfilled') this.mounts.set(mounts.value ?? []);
    if (events.status === 'fulfilled') this.events.set(events.value ?? []);
    if (conflicts.status === 'fulfilled') this.conflicts.set(conflicts.value ?? []);
    this.broker.set(broker.status === 'fulfilled' ? broker.value : null);
    this.brokerGrants.set(bgrants.status === 'fulfilled' ? (bgrants.value ?? []) : []);
    this.brokerRequests.set(breqs.status === 'fulfilled' ? (breqs.value ?? []) : []);
    if (providers.status === 'fulfilled') this.providers.set(providers.value);

    const agentsNow = this.agents();
    const pathMap: Record<string, AgentPaths> = {};
    const egressMap: Record<string, EgressStatus> = {};
    await Promise.all(agentsNow.map(async a => {
      try { pathMap[a.id] = await firstValueFrom(this.api.paths(a.id)); } catch { /* ignore */ }
      try { egressMap[a.id] = await firstValueFrom(this.api.egress(a.id)); } catch { /* ignore */ }
    }));
    this.paths.set(pathMap);
    this.egress.set(egressMap);
    this.loading.set(false);
  }

  activityFor(id: string): AgentActivity | null {
    return this.activity().find(a => a.agent_id === id) ?? null;
  }

  mountsFor(id: string): MountState[] {
    return this.mounts().filter(m => m.agent_id === id && m.status === 'mounted');
  }

  grantsFor(id: string): Grant[] {
    return this.grants().filter(g => g.agent_id === id);
  }

  brokerGrantsFor(name: string): BrokerGrant[] {
    return this.brokerGrants().filter(g => g.agent === name);
  }

  egressFor(id: string): EgressStatus | null {
    return this.egress()[id] ?? null;
  }

  pathsFor(id: string): AgentPaths | null {
    return this.paths()[id] ?? null;
  }
}
