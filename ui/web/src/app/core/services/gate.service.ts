import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

export interface GateRunView {
  id: string;
  workspace: string;
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
  warnings?: string[];
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
}
