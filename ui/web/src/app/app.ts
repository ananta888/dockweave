import { Component, OnDestroy, OnInit, computed, effect, inject, signal } from '@angular/core';
import { Subscription } from 'rxjs';
import { DomSanitizer, SafeHtml } from '@angular/platform-browser';
import { KeyValuePipe, NgFor } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { ApiService } from './core/api.service';
import { Store } from './core/store';
import { TerminalComponent } from './components/terminal.component';
import { ConfigComponent } from './components/config.component';
import { AnalysisComponent } from './components/analysis.component';
import { DirPickerComponent } from './components/dir-picker.component';
import { GraphComponent } from './components/graph.component';
import { AgentTypesComponent } from './components/agent-types.component';
import { figureSvg } from './core/figures';
import { UiBridgeService } from './core/ui-bridge.service';
import { Agent } from './core/models';

type View = 'agent' | 'graph' | 'analysis' | 'workspaces' | 'types';

@Component({
  selector: 'app-root',
  imports: [FormsModule, KeyValuePipe, NgFor, TerminalComponent, ConfigComponent, AnalysisComponent, DirPickerComponent, GraphComponent, AgentTypesComponent],
  templateUrl: './app.html',
  styleUrl: './app.scss',
})
export class App implements OnInit, OnDestroy {
  readonly store = inject(Store);
  private readonly api = inject(ApiService);
  private readonly sanitizer = inject(DomSanitizer);
  private readonly uiBridge = inject(UiBridgeService);
  private uiSub?: Subscription;
  uiHint = signal<{ text: string; severity: string; x: number; y: number } | null>(null);
  private hintTimer?: ReturnType<typeof setTimeout>;
  private timer?: ReturnType<typeof setInterval>;

  view = signal<View>('agent');
  actionBusy = signal(false);
  action = signal<'start' | 'stop' | 'restart' | 'remove' | ''>('');
  agentTab = signal<'chat' | 'config'>('chat');
  newOpen = signal(false);
  mainAgentId = signal<string | null>(localStorage.getItem('dockweave.mainAgent'));
  chatMinimized = signal(false);
  chatVisible = signal(true);
  creating = signal(false);
  createError = signal('');
  workspaceName = '';
  workspacePath = '';
  workspaceError = signal('');
  readonly mainAgent = computed(() => this.store.agents().find(a => a.id === this.mainAgentId()) ?? null);
  wsBrowserOpen = signal(false);

  newAgent = {
    name: '', type: 'opencode', image: 'node:22-bookworm',
    exec: 'rm -f "$HOME/.local/bin/opencode" "$HOME/.local/bin/opencode2" && npm install -g --prefix "$HOME/.local" @opencode/cli@2.2.6 && exec "$HOME/.local/bin/opencode" --auto',
    env_file: '', profile: '', uid: 1000, gid: 1000,
  };

  constructor() {
    effect(() => {
      this.uiBridge.sendState({
        view: this.view(),
        selectedAgentId: this.store.selectedId(),
        mainAgentId: this.mainAgentId(),
        chatVisible: this.chatVisible(),
        agents: this.store.agents().map(a => ({ id: a.id, name: a.display_name || a.name, type: a.type, state: a.state, avatar: a.avatar })),
      });
    });
  }

  ngOnInit(): void {
    void this.store.refresh();
    this.timer = setInterval(() => void this.store.refresh(), 5000);
    this.uiBridge.connect();
    this.uiSub = this.uiBridge.commands.subscribe(cmd => this.handleUiCommand(cmd));
  }

  ngOnDestroy(): void {
    if (this.timer) clearInterval(this.timer);
    clearTimeout(this.hintTimer);
    this.uiSub?.unsubscribe();
  }

  private handleUiCommand(cmd: Record<string, unknown>): void {
    const action = String(cmd['action'] || '');
    const agentId = String(cmd['agent_id'] || '');
    if (action === 'select_agent' && agentId) this.selectById(agentId);
    else if (action === 'select_view') {
      const v = String(cmd['view'] || '');
      if (['agent', 'graph', 'analysis', 'workspaces', 'types'].includes(v)) this.view.set(v as View);
    } else if (action === 'set_main_agent' && agentId) this.setMainAgentById(agentId);
    else if (action === 'show_hint') this.showUiHint(cmd);
  }

  private showUiHint(cmd: Record<string, unknown>): void {
    const text = String(cmd['text'] || '');
    if (!text) return;
    const severity = String(cmd['severity'] || 'info');
    let selector = String(cmd['selector'] || '');
    if (!selector && cmd['agent_id']) selector = `[data-agent-id="${String(cmd['agent_id'])}"]`;
    if (!selector && cmd['node_id']) selector = `[data-node-id="${String(cmd['node_id'])}"]`;
    let x = window.innerWidth - 340;
    let y = 80;
    if (selector) {
      const el = document.querySelector(selector);
      if (el) {
        const r = el.getBoundingClientRect();
        x = Math.min(window.innerWidth - 320, Math.max(8, r.right + 10));
        y = Math.max(8, r.top);
      }
    }
    this.uiHint.set({ text, severity, x, y });
    clearTimeout(this.hintTimer);
    const ms = Number(cmd['duration_ms'] || 10000);
    this.hintTimer = setTimeout(() => this.uiHint.set(null), Math.max(2000, Math.min(ms, 60000)));
  }

  select(a: Agent): void {
    this.store.select(a.id);
    this.view.set('agent');
    this.agentTab.set('chat');
  }

  selectById(id: string): void {
    this.store.select(id);
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

  setMainAgent(a: Agent): void {
    this.mainAgentId.set(a.id);
    localStorage.setItem('dockweave.mainAgent', a.id);
    this.chatVisible.set(true);
    this.chatMinimized.set(false);
  }

  setMainAgentById(id: string): void {
    const a = this.store.agents().find(x => x.id === id);
    if (a) this.setMainAgent(a);
  }

  mainAvatar(a: Agent): SafeHtml {
    return this.sanitizer.bypassSecurityTrustHtml(figureSvg(a.avatar, a.avatar_svg));
  }

  startMainAgent(a: Agent): void {
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
