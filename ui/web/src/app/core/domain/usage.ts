export interface UsageWindow {
  used_percent: number;
  window_minutes?: number;
  resets_at?: string;
}

export interface UsageCredits {
  has_credits: boolean;
  unlimited: boolean;
  balance?: string;
}

export interface TokenUsage {
  input: number;
  output: number;
  reasoning: number;
  cache_read: number;
  cache_write: number;
  total: number;
}

export interface AgentUsage {
  agent_id: string;
  agent: string;
  display_name?: string;
  harness: string;
  runtime?: string;
  source: string;
  available: boolean;
  note?: string;
  five_hour?: UsageWindow;
  seven_day?: UsageWindow;
  credits?: UsageCredits;
  cost_usd?: number;
  tokens?: TokenUsage;
  sessions?: number;
  fetched_at?: string;
}
