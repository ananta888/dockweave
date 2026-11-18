import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiService } from '../api.service';
import { HerdrPrepareResult, HerdrStatus } from '../domain/integrations';

// Application service for the herdr runtime. Components never talk to HTTP
// directly; they depend on this domain-facing API.
@Injectable({ providedIn: 'root' })
export class HerdrService {
  private readonly api = inject(ApiService);

  status(agentId: string): Observable<HerdrStatus> {
    return this.api.herdrStatus(agentId);
  }

  prompt(agentId: string, prompt: string): Observable<{ content: string }> {
    return this.api.herdrPrompt(agentId, prompt);
  }

  prepare(agentId: string, options: { credential?: string; model?: string; ttl?: string } = {}): Observable<HerdrPrepareResult> {
    return this.api.herdrPrepare(agentId, options);
  }
}
