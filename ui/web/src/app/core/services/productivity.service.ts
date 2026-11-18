import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

export interface Task {
  id: string;
  title: string;
  description?: string;
  objective_ids?: string[];
  assignee_kind: 'human' | 'agent';
  agent_id?: string;
  day?: string;
  status: string;
  priority: number;
  effort_minutes?: number;
  order_index: number;
}

export interface FocusSession {
  id: string;
  task_id: string;
  human_id: string;
  status: string;
  started_at: string;
  ended_at?: string;
  paused_seconds: number;
}

export interface TodayPlan {
  day: string;
  tasks: Task[];
  focus?: FocusSession;
  active_agents: number;
}

export interface Objective {
  id: string;
  kind: string;
  title: string;
  status: string;
  period_start?: string;
  period_end?: string;
}

export interface BudgetActual {
  scope: string;
  kind: string;
  period: string;
  target: number;
  actual: number;
  exceeded: boolean;
}

// Thin client for the productivity API. The gateway proxies /api/ to the daemon.
@Injectable({ providedIn: 'root' })
export class ProductivityService {
  private readonly http = inject(HttpClient);

  plan(day: string, human = 'local'): Observable<TodayPlan> {
    return this.http.get<TodayPlan>(`/api/v1/tasks/today?day=${day}&human=${human}`);
  }

  objectives(): Observable<Objective[]> {
    return this.http.get<Objective[]>('/api/v1/objectives');
  }

  addObjective(o: Partial<Objective>): Observable<Objective> {
    return this.http.post<Objective>('/api/v1/objectives', o);
  }

  addTask(t: Partial<Task>): Observable<Task> {
    return this.http.post<Task>('/api/v1/tasks', t);
  }

  updateTask(t: Task): Observable<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${t.id}`, t);
  }

  focus(action: 'start' | 'pause' | 'resume' | 'complete' | 'skip', taskId?: string, human = 'local'): Observable<unknown> {
    if (action === 'start') {
      return this.http.post(`/api/v1/focus/start`, { task_id: taskId, human });
    }
    const q = `?human=${encodeURIComponent(human)}`;
    return this.http.post(`/api/v1/focus/${action}${q}`, {});
  }

  switchFocus(taskId: string, human = 'local'): Observable<FocusSession> {
    return this.http.post<FocusSession>('/api/v1/focus/switch', { task_id: taskId, human });
  }

  rollover(from: string, to: string): Observable<{ moved: number }> {
    return this.http.post<{ moved: number }>('/api/v1/tasks/rollover', { from, to });
  }

  budgetActuals(): Observable<{ budgets: BudgetActual[]; verified_results: number }> {
    return this.http.get<{ budgets: BudgetActual[]; verified_results: number }>('/api/v1/budgets/actuals');
  }

  report(kind: 'weekly' | 'monthly'): Observable<Record<string, unknown>> {
    return this.http.get<Record<string, unknown>>(`/api/v1/reviews/report?kind=${kind}`);
  }

  evidence(taskId: string): Observable<unknown[]> {
    return this.http.get<unknown[]>(`/api/v1/tasks/${taskId}/evidence`);
  }
}
