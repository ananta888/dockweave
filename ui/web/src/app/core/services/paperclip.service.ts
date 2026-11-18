import { Injectable, inject } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, catchError, throwError } from 'rxjs';
import {
  PaperclipAgent, PaperclipConfig, PaperclipIssue, PaperclipIssueDraft, PaperclipStatus,
} from '../domain/integrations';

// The DockWeave web gateway adapts the local Paperclip CLI under /paperclip.
// This service is the only place that knows those endpoints.
@Injectable({ providedIn: 'root' })
export class PaperclipService {
  private readonly http = inject(HttpClient);

  private req<T>(obs: Observable<T>): Observable<T> {
    return obs.pipe(catchError((err: HttpErrorResponse) => {
      const msg = err.error?.error || err.error?.message || err.message || 'paperclip request failed';
      return throwError(() => new Error(typeof msg === 'string' ? msg : JSON.stringify(msg)));
    }));
  }

  config(): Observable<PaperclipConfig> {
    return this.req(this.http.get<PaperclipConfig>('/paperclip/config'));
  }

  status(companyId?: string): Observable<PaperclipStatus> {
    const q = companyId ? `?company=${encodeURIComponent(companyId)}` : '';
    return this.req(this.http.get<PaperclipStatus>(`/paperclip/status${q}`));
  }

  createIssue(draft: PaperclipIssueDraft): Observable<PaperclipIssue> {
    return this.req(this.http.post<PaperclipIssue>('/paperclip/issues', draft));
  }

  ensureAgent(companyId: string, agent: string): Observable<{ action: string; agent: PaperclipAgent }> {
    return this.req(this.http.post<{ action: string; agent: PaperclipAgent }>('/paperclip/agents', { company_id: companyId, agent }));
  }

  sync(companyId: string, agents: string[]): Observable<{ created: string[]; updated: string[]; errors: { agent: string; error: string }[] }> {
    return this.req(this.http.post<{ created: string[]; updated: string[]; errors: { agent: string; error: string }[] }>('/paperclip/sync', { company_id: companyId, agents }));
  }

  setTarget(agentId: string, model: string): Observable<unknown> {
    return this.req(this.http.post(`/paperclip/agents/${encodeURIComponent(agentId)}/target`, { model }));
  }

  wake(agentId: string, companyId: string): Observable<unknown> {
    return this.req(this.http.post(`/paperclip/agents/${encodeURIComponent(agentId)}/wake`, { company_id: companyId }));
  }
}
