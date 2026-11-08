import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { KeyValuePipe, NgFor } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ApiService } from './core/api.service';
import { Store } from './core/store';
import { TerminalComponent } from './components/terminal.component';
import { ConfigComponent } from './components/config.component';
import { AnalysisComponent } from './components/analysis.component';
import { Agent } from './core/models';

type View = 'agent' | 'analysis' | 'workspaces';

@Component({
  selector: 'app-root',
  imports: [FormsModule, KeyValuePipe, NgFor, TerminalComponent, ConfigComponent, AnalysisComponent],
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnInit, OnDestroy {
  readonly store = inject(Store);
  private readonly api = inject(ApiService);
  private timer?: ReturnType<typeof setInterval>;

  view = signal<View>('agent');
  agentTab = signal<'chat' | 'config'>('chat');
  newOpen = signal(false);
  creating = signal(false);
  createError = signal('');
  workspaceName = '';
  workspacePath = '';
  workspaceError = signal('');

  newAgent = {
    name: '', type: 'opencode', image: 'node:22-bookworm',
    exec: 'npm install -g --prefix "$HOME/.local" opencode-ai && exec "$HOME/.local/bin/opencode" --auto',
    env_file: '', profile: '', uid: 1000, gid: 1000,
  };

  ngOnInit(): void {
    void this.store.refresh();
    this.timer = setInterval(() => void this.store.refresh(), 5000);
  }

  ngOnDestroy(): void {
    if (this.timer) clearInterval(this.timer);
  }

  select(a: Agent): void {
    this.store.select(a.id);
    this.view.set('agent');
    this.agentTab.set('chat');
  }

  startSelected(): void {
    const a = this.store.selected();
    if (!a) return;
    this.api.startAgent(a.id).subscribe({
      next: () => void this.store.refresh(),
      error: e => this.store.error.set(e.message),
    });
  }

  create(): void {
    const f = this.newAgent;
    if (!f.name.trim() || !f.image.trim()) return;
    this.creating.set(true);
    this.createError.set('');
    const spec = {
      name: f.name.trim(), type: f.type, image: f.image.trim(),
      command: f.exec.trim() ? ['sh', '-c', f.exec.trim()] : [],
      env_file: f.env_file.trim(), uid: f.uid, gid: f.gid,
      agent_ro: true, network_mode: 'bridge', network_profile: f.profile,
    };
    this.api.createAgent(spec).subscribe({
      next: a => {
        this.creating.set(false);
        this.newOpen.set(false);
        this.newAgent.name = '';
        void this.store.refresh().then(() => this.store.select(a.id));
      },
      error: e => { this.creating.set(false); this.createError.set(e.message); },
    });
  }

  registerWorkspace(): void {
    if (!this.workspaceName.trim() || !this.workspacePath.trim()) return;
    this.workspaceError.set('');
    this.api.registerWorkspace(this.workspaceName.trim(), this.workspacePath.trim()).subscribe({
      next: () => { this.workspaceName = ''; this.workspacePath = ''; void this.store.refresh(); },
      error: e => this.workspaceError.set(e.message),
    });
  }

  unregisterWorkspace(id: string, name: string): void {
    if (!confirm(`Unregister workspace ${name}?`)) return;
    this.api.unregisterWorkspace(id).subscribe({
      next: () => void this.store.refresh(),
      error: e => this.workspaceError.set(e.message),
    });
  }

  stateClass(state: string): string {
    return state === 'running' ? 'on' : state === 'error' ? 'err' : 'off';
  }

}
