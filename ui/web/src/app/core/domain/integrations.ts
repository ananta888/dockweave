// Domain types for the DockWeave integrations (herdr, paperclip).
// These are intentionally independent of the HTTP transport so the UI
// components depend on the domain, not on endpoint details.

export type RuntimeKind = 'docker' | 'herdr' | 'paperclip';

export interface RuntimePreset {
  readonly id: RuntimeKind;
  readonly label: string;
  readonly description: string;
  readonly image: string;
  readonly command: string;
  readonly agentType: string;
}

export interface HerdrAgent {
  name: string;
  agent?: string;
  pane_id?: string;
  agent_status?: string;
  interactive_ready?: boolean;
  focused?: boolean;
  cwd?: string;
  terminal_title?: string;
}

export interface HerdrPane {
  pane_id: string;
  cwd?: string;
  focused?: boolean;
  workspace_id?: string;
}

export interface HerdrStatus {
  agent: string;
  agents: HerdrAgent[];
  panes: HerdrPane[];
}

export interface HerdrPrepareResult {
  ok: boolean;
  grant_id: string;
  expires_at: string;
  config: string;
  token_file: string;
}

export interface PaperclipCompany {
  id: string;
  name: string;
  issuePrefix?: string;
}

export interface PaperclipAgent {
  id: string;
  name: string;
  status?: string;
  adapterType?: string;
  lifecycleState?: string;
  adapterConfig?: {
    args?: string[];
    command?: string;
    cwd?: string;
    timeoutSec?: number;
  };
}

export interface PaperclipIssue {
  id: string;
  identifier?: string;
  title?: string;
  status?: string;
}

export interface PaperclipStatus {
  url: string;
  company_id: string;
  companies: PaperclipCompany[];
  agents: PaperclipAgent[];
  issues: PaperclipIssue[];
}

export interface PaperclipConfig {
  url: string;
}

export interface PaperclipIssueDraft {
  company_id: string;
  title: string;
  description: string;
  assignee_agent_id: string;
}
