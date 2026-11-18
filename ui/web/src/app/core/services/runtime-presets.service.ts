import { Injectable } from '@angular/core';
import { RuntimeKind, RuntimePreset } from '../domain/integrations';

// Single source of truth for runtime defaults. Components ask for a preset
// instead of duplicating image/command knowledge.
@Injectable({ providedIn: 'root' })
export class RuntimePresetsService {
  private readonly all: RuntimePreset[] = [
    {
      id: 'docker',
      label: 'docker (direct)',
      description: 'Harness läuft direkt im DockWeave-Container.',
      image: 'node:22-bookworm',
      command: '',
      agentType: 'opencode',
    },
    {
      id: 'herdr',
      label: 'herdr (Terminal-Runtime)',
      description: 'Herdr besitzt Panes/Sessions; Codex läuft im fokussierten Pane.',
      image: 'dockweave/herdr-agent',
      command: 'codex --dangerously-bypass-approvals-and-sandbox',
      agentType: 'codex',
    },
    {
      id: 'paperclip',
      label: 'paperclip (Heartbeat-Runtime)',
      description: 'Der Agent wird von Paperclip in Heartbeats geweckt.',
      image: 'node:22-bookworm',
      command: '',
      agentType: 'opencode',
    },
  ];

  presets(): readonly RuntimePreset[] {
    return this.all;
  }

  get(id: RuntimeKind): RuntimePreset | undefined {
    return this.all.find(p => p.id === id);
  }
}
