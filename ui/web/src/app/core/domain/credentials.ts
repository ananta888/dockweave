export interface CredentialSecret {
  name: string;
  provider: string;
  created_at: string;
  updated_at: string;
}

export interface CredentialLimits {
  max_requests_per_minute?: number;
  max_concurrent?: number;
  max_requests_per_day?: number;
  max_tokens_per_day?: number;
  max_tokens_per_month?: number;
  max_cost_usd_per_day?: number;
  max_cost_usd_per_month?: number;
  prompt_usd_per_1k?: number;
  completion_usd_per_1k?: number;
}

export interface CredentialGrant {
  id: string;
  agent: string;
  credential: string;
  provider: string;
  models?: string[];
  limits: CredentialLimits;
  expires_at?: string;
  created_at: string;
  updated_at: string;
}

export interface CredentialSession {
  id: string;
  agent: string;
  grant_id: string;
  created_at: string;
  expires_at: string;
  revoked_at?: string;
}

export interface CredentialMint {
  token: string;
  session: CredentialSession;
}

export interface CredentialStatus {
  status: string;
  secrets: number;
  grants: number;
  sessions: number;
  providers: string[];
  egress: string;
}
