import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

export interface GateReviewView {
  issue_id: string;
  verdict: 'PASS' | 'FAIL' | string;
  actor?: string;
  note?: string;
  evidence_id?: string;
  recorded_at?: string;
  conflict?: boolean;
  existing?: string;
}

export interface GateRunView {
  id: string;
  workspace: string;
  issue_id?: string;
  image?: string;
  image_digest?: string;
  status: 'passed' | 'failed' | 'error' | 'skipped' | string;
  gates: number;
  passed: number;
  duration_ms: number;
  gates_hash?: string;
  tamper?: boolean;
  actor?: string;
  reason?: string;
  results?: { id: string; command?: string; status: string; exit?: number; duration_ms?: number; output?: string; reason?: string }[];
  diff?: { changed?: string[]; out_of_scope?: string[]; new_dependencies?: string[]; secrets?: string[] };
  budget?: {
    grant_id?: string;
    requests_day?: number;
    tokens_day?: number;
    cost_day_usd?: number;
    max_requests_day?: number;
    max_tokens_day?: number;
    max_cost_day_usd?: number;
    exceeded?: boolean;
    reason?: string;
  };
  warnings?: string[];
  upstream_agent_id?: string;
  started_at?: string;
  finished_at?: string;
}

// Thin client for the daemon's sandboxed gate runs (proxied by the gateway).
@Injectable({ providedIn: 'root' })
export class GateService {
  private readonly http = inject(HttpClient);

  runs(workspace = '', limit = 10): Observable<{ runs: GateRunView[] }> {
    const params = new URLSearchParams({ limit: String(limit) });
    if (workspace) params.set('workspace', workspace);
    return this.http.get<{ runs: GateRunView[] }>(`/api/v1/gates/runs?${params.toString()}`);
  }

  reviews(limit = 10): Observable<{ reviews: GateReviewView[] }> {
    return this.http.get<{ reviews: GateReviewView[] }>(`/api/v1/gates/reviews?limit=${limit}`);
  }
}
