import { Component, OnDestroy, OnInit, inject, signal } from '@angular/core';
import { KeyValuePipe, NgFor } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ApiService } from './core/api.service';
import { Store } from './core/store';
import { TerminalComponent } from './components/terminal.component';
import { ConfigComponent } from './components/config.component';
import { AnalysisComponent } from './components/analysis.component';
import { DirPickerComponent } from './components/dir-picker.component';
import { Agent } from './core/models';

type View = 'agent' | 'analysis' | 'workspaces';

@Component({
  selector: 'app-root',
  imports: [FormsModule, KeyValuePipe, NgFor, TerminalComponent, ConfigComponent, AnalysisComponent, DirPickerComponent],
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnInit, OnDestroy {
  readonly store = inject(Store);
  private readonly api = inject(ApiService);
  private timer?: ReturnType<typeof setInterval>;

  view = signal<View>('agent');
  actionBusy = signal(false);
  action = signal<'start' | 'stop' | 'restart' | 'remove' | ''>('');
  agentTab = signal<'chat' | 'config'>('chat');
  newOpen = signal(false);
  creating = signal(false);
  createError = signal('');
  workspaceName = '';
  workspacePath = '';
  workspaceError = signal('');
  wsBrowserOpen = signal(false);

  newAgent = {
    name: '', type: 'opencode', image: 'node:22-bookworm',
    exec: 'rm -f "$HOME/.local/bin/opencode" "$HOME/.local/bin/opencode2" && npm install -g --prefix "$HOME/.local" @opencode/cli@2.2.6 && exec "$HOME/.local/bin/opencode" --auto',
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

  private agentAction(action: 'start' | 'stop' | 'restart' | 'remove'): void {
    const a = this.store.selected();
    if (!a || this.actionBusy()) return;
    if (action === 'remove') {
      if (!confirm(`Delete agent ${a.name}?\n\nThe identity and container are removed. Host data (/agent, /memory, workspace) is kept.`)) return;
    }
    this.action.set(action);
    this.actionBusy.set(true);
    const done = () => { this.actionBusy.set(false); this.action.set(''); };
    const fail = (e: { message?: string }) => { done(); this.store.error.set(e.message ?? 'action failed'); };
    if (action === 'remove') {
      this.api.removeAgent(a.id).subscribe({ next: () => { void this.store.refresh(); done(); }, error: fail });
      return;
    }
    const req = action === 'start' ? this.api.startAgent(a.id)
      : action === 'stop' ? this.api.stopAgent(a.id)
      : this.api.restartAgent(a.id);
    req.subscribe({ next: () => { void this.store.refresh(); done(); }, error: fail });
  }

  actionLabel(): string {
    switch (this.action()) {
      case 'start': return 'Starting agent…';
      case 'stop': return 'Stopping agent…';
      case 'restart': return 'Restarting agent…';
      case 'remove': return 'Deleting agent…';
      default: return '';
    }
  }

  startSelected(): void { this.agentAction('start'); }
  stopSelected(): void { this.agentAction('stop'); }
  restartSelected(): void { this.agentAction('restart'); }
  removeSelected(): void { this.agentAction('remove'); }

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

  openWorkspaceBrowser(): void { this.wsBrowserOpen.set(true); }

  onWorkspacePicked(path: string): void { this.workspacePath = path; this.wsBrowserOpen.set(false); }

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
