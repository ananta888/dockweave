import { Injectable, inject } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, catchError, throwError } from 'rxjs';

// Provider-neutral ticket API of the daemon (ADR 0003), proxied by the
// gateway under /api/v1/tickets. The UI never talks to Paperclip directly.

export type TicketStatus = 'backlog' | 'todo' | 'in_progress' | 'in_review' | 'blocked' | 'done' | 'cancelled';
export const TICKET_STATUSES: TicketStatus[] = ['backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'cancelled'];
export type TicketPriority = '' | 'critical' | 'high' | 'medium' | 'low';

export interface TicketActor { kind: 'human' | 'agent' | 'system'; id?: string; name?: string }
export interface TicketRef { provider: string; id: string }

export interface Ticket {
  ref: TicketRef;
  identifier?: string;
  title: string;
  description?: string;
  status: TicketStatus;
  priority?: TicketPriority;
  assignee?: TicketActor;
  parent?: TicketRef;
  blocked_by?: TicketRef[];
  workspace_id?: string;
  created_by?: TicketActor;
  checked_out_by?: string;
  created_at: string;
  updated_at: string;
  version: string;
  extra?: Record<string, unknown>;
}

export interface TicketComment { id: string; ticket: TicketRef; author: TicketActor; body: string; created_at: string; origin?: string }

export interface Capabilities { threads: boolean; inbox: boolean; children: boolean; approvals: boolean; wake: boolean; authorship: boolean }
export interface ProviderView { name: string; health: { ok: boolean; detail?: string; checked: string }; capabilities: Capabilities }
export interface ProviderStatus { configured: boolean; available: string[]; error?: string; primary?: ProviderView; mirror?: ProviderView }

export interface Me { actor: TicketActor; roles: string[]; permissions: string[]; source: 'groups' | 'peer'; config_error?: string }

export interface SyncState {
  configured: boolean;
  hint?: string;
  status?: { primary: string; mirror: string; linked: number; last_success?: string; lag_seconds: number; conflicts_total: number; last_report?: { errors?: string[] } };
}

export interface DispatchOutcome { kind: string; ticket?: TicketRef; status?: TicketStatus; detail?: string }

export interface TicketFilter { status?: TicketStatus[]; assignee?: string; q?: string; parent?: string; limit?: number }

/** Key stored with gate runs, reviews, approvals and live work (dispatch.IssueKey). */
export function issueKey(r: TicketRef): string {
  return r.provider === 'paperclip' ? r.id : `${r.provider}:${r.id}`;
}

export function refString(r: TicketRef): string { return `${r.provider}:${r.id}`; }

@Injectable({ providedIn: 'root' })
export class TicketService {
  private readonly http = inject(HttpClient);
  private readonly base = '/api/v1/tickets';

  private req<T>(obs: Observable<T>): Observable<T> {
    return obs.pipe(catchError((err: HttpErrorResponse) => {
      const e = err.error?.error;
      const msg = (typeof e === 'object' && e?.message) || (typeof e === 'string' && e) || err.message || 'request failed';
      const perm = err.error?.permission;
      return throwError(() => new Error(perm ? `${msg} (${perm})` : String(msg)));
    }));
  }

  provider(): Observable<ProviderStatus> { return this.req(this.http.get<ProviderStatus>(`${this.base}/provider`)); }
  me(): Observable<Me> { return this.req(this.http.get<Me>(`${this.base}/me`)); }

  list(f: TicketFilter = {}): Observable<{ provider: string; tickets: Ticket[] }> {
    const q = new URLSearchParams();
    if (f.status?.length) q.set('status', f.status.join(','));
    if (f.assignee) q.set('assignee', f.assignee);
    if (f.q) q.set('q', f.q);
    if (f.parent) q.set('parent', f.parent);
    if (f.limit) q.set('limit', String(f.limit));
    const qs = q.toString();
    return this.req(this.http.get<{ provider: string; tickets: Ticket[] }>(this.base + (qs ? `?${qs}` : '')));
  }

  get(id: string): Observable<Ticket> { return this.req(this.http.get<Ticket>(`${this.base}/${encodeURIComponent(id)}`)); }
  create(t: Partial<Ticket> & { title: string }): Observable<Ticket> { return this.req(this.http.post<Ticket>(this.base, t)); }
  update(id: string, u: Record<string, unknown>): Observable<Ticket> {
    return this.req(this.http.patch<Ticket>(`${this.base}/${encodeURIComponent(id)}`, u));
  }
  comments(id: string): Observable<{ comments: TicketComment[] }> {
    return this.req(this.http.get<{ comments: TicketComment[] }>(`${this.base}/${encodeURIComponent(id)}/comments`));
  }
  comment(id: string, body: string): Observable<TicketComment> {
    return this.req(this.http.post<TicketComment>(`${this.base}/${encodeURIComponent(id)}/comments`, { body }));
  }
  run(agent: string): Observable<DispatchOutcome> { return this.req(this.http.post<DispatchOutcome>(`${this.base}/run`, { agent })); }
  syncStatus(): Observable<SyncState> { return this.req(this.http.get<SyncState>(`${this.base}/sync`)); }
  syncRun(initial = false, dryRun = false): Observable<{ report: unknown; complete: boolean }> {
    return this.req(this.http.post<{ report: unknown; complete: boolean }>(`${this.base}/sync`, { initial, dry_run: dryRun }));
  }
  approve(key: string, note = ''): Observable<unknown> {
    return this.req(this.http.post('/api/v1/gates/approvals', { issue_id: key, note }));
  }
  approval(key: string): Observable<{ actor: string; approved_at: string }> {
    return this.req(this.http.get<{ actor: string; approved_at: string }>(`/api/v1/gates/approvals/${encodeURIComponent(key)}`));
  }
}
