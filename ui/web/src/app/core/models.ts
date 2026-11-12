export interface Agent {
  id: string;
  name: string;
  display_name?: string;
  org_unit?: string;
  role?: string;
  type: string;
  image: string;
  command?: string[];
  env_file?: string;
  agent_ro: boolean;
  uid: number;
  gid: number;
  agent_dir?: string;
  notes_dir?: string;
  cpu_limit?: number;
  memory_mb?: number;
  network_mode: string;
  network_profile?: string;
  state: string;
  container_id?: string;
  created_at: string;
  updated_at: string;
}

export interface AgentActivity {
  agent_id: string;
  state: string;
  activity: string;
  cpu_percent: number;
  memory_bytes: number;
  workspace?: string;
  open_files?: string[];
  processes: number;
  available: boolean;
  unavailable_reason?: string;
  timestamp: string;
}

export interface Workspace {
  id: string;
  name: string;
  host_path: string;
  created_at: string;
}

export interface Grant {
  id: string;
  workspace_id: string;
  agent_id: string;
  mode: string;
  exclusive: boolean;
  status: string;
  expires_at?: string;
  created_at: string;
}

export interface MountState {
  id: string;
  agent_id: string;
  grant_id: string;
  host_path: string;
  target_path: string;
  mode: string;
  status: string;
  last_error?: string;
  updated_at: string;
}

export interface Event {
  id: string;
  type: string;
  actor: string;
  agent_id?: string;
  workspace_id?: string;
  message: string;
  timestamp: string;
}

export interface FileConflict {
  path: string;
  agent_ids: string[];
  writers: string[];
  detected_at: string;
}

export interface BrokerStatus {
  status: string;
  secrets: number;
  grants: number;
  sessions: number;
  providers: string[];
  egress: string;
}

export interface BrokerGrant {
  id: string;
  agent: string;
  credential: string;
  provider: string;
  models?: string[];
  limits: Record<string, number>;
  created_at: string;
  expires_at?: string;
}

export interface BrokerRequestEntry {
  time: string;
  agent: string;
  provider: string;
  credential?: string;
  method: string;
  path: string;
  model?: string;
  status: number;
  denied?: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  duration_ms: number;
}

export interface BrokerUsage {
  grant_id: string;
  day: string;
  month: string;
  requests_day: number;
  requests_month: number;
  tokens_day: number;
  tokens_month: number;
  cost_day_usd: number;
  cost_month_usd: number;
}

export interface EgressStatus {
  agent: string;
  profile: string;
  enforced: boolean;
  network?: string;
  gateway?: string;
  broker_base_url?: string;
  warning?: string;
}

export interface AgentPaths {
  agent_dir: string;
  agent_dir_custom: boolean;
  memory_id?: string;
  memory_dir: string;
  memory_custom: boolean;
  workspace_dir: string;
  knowledge_dir?: string;
  notes_dir: string;
  notes_dir_custom: boolean;
}

export interface MemorySnapshot {
  name: string;
  size_bytes: number;
  created_at: string;
}

export interface MemoryInfo {
  id: string;
  owner_agent_id?: string;
  path?: string;
  created_at: string;
  attached_at?: string;
}

export interface ProviderStatus {
  [name: string]: string;
}

export interface DirEntry {
  name: string;
  path: string;
}

export interface DirListing {
  path: string;
  parent?: string;
  roots: DirEntry[];
  entries: DirEntry[];
}

export interface AgentMCP {
  id: string;
  agent_id: string;
  name: string;
  transport: 'stdio' | 'http' | 'sse';
  command?: string;
  args?: string[];
  url?: string;
  env_keys?: string[];
  contract?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AgentSkill {
  id: string;
  agent_id: string;
  name: string;
  description?: string;
  source_path?: string;
  config?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AgentTypeMCP {
  id: string;
  agent_type: string;
  name: string;
  transport: 'stdio' | 'http' | 'sse';
  command?: string;
  args?: string[];
  url?: string;
  env_keys?: string[];
  contract?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AgentTypeSkill {
  id: string;
  agent_type: string;
  name: string;
  description?: string;
  source_path?: string;
  config?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}
