import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

/** One evaluated gate -> agent edge (see docs/adr/0002-pipeline-enforcement.md). */
export interface AdmissionEdge {
  edge_id: string;
  gate_id: string;
  upstream_agent_id?: string;
  workspace: string;
  satisfied: boolean;
  /** ok | override | no_run | failed | error | stale | expired | tamper | fingerprint_unavailable | unknown_gate | denied | internal */
  code: string;
  message?: string;
  run_id?: string;
  run_status?: string;
  run_finished_at?: string;
  action: string;
  reason?: string;
  override_id?: string;
}

export interface Admission {
  agent_id: string;
  agent?: string;
  allowed: boolean;
  action: string;
  edges: AdmissionEdge[];
  revision: number;
  checked_at: string;
}

export interface PipelineSyncResult {
  pushed: boolean;
  edges: number;
  warnings?: string[];
  error?: string;
  /** Daemon error code of a failed push (stale_revision, revision_conflict, forbidden, unavailable). */
  code?: string;
  /** Gate connector whose contract stopped the sync (enforcement unchanged). */
  invalid_connector?: { connector_id: string; target_agent_id?: string; message: string };
}

export interface ExpressionCheck {
  ok: boolean;
  error?: string;
  uses_budget?: boolean;
  warning?: string;
}

// Pipeline status (daemon, proxied under /api/) and expression validation
// (gateway). Admission is read-only: it never consumes overrides.
@Injectable({ providedIn: 'root' })
export class PipelineService {
  private readonly http = inject(HttpClient);

  admissions(): Observable<{ revision: number; admissions: Admission[] }> {
    return this.http.get<{ revision: number; admissions: Admission[] }>('/api/v1/pipelines/admission');
  }

  validateExpression(expression: string, budgetConfigured: boolean): Observable<ExpressionCheck> {
    return this.http.post<ExpressionCheck>('/graph/validate-expression', {
      expression, budget_configured: budgetConfigured,
    });
  }
}
