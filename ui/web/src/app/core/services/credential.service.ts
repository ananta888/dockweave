import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiService } from '../api.service';
import {
  CredentialGrant, CredentialMint, CredentialSecret, CredentialSession, CredentialStatus,
} from '../domain/credentials';
import { BrokerRequestEntry } from '../models';

// Application service for the DockWeave credential vault/broker. Secret
// values are write-only; the API never returns them.
@Injectable({ providedIn: 'root' })
export class CredentialService {
  private readonly api = inject(ApiService);

  status(): Observable<CredentialStatus> { return this.api.credentialStatus(); }
  secrets(): Observable<CredentialSecret[]> { return this.api.credentialSecrets(); }
  putSecret(name: string, provider: string, value: string): Observable<unknown> {
    return this.api.putCredentialSecret(name, provider, value);
  }
  deleteSecret(name: string): Observable<unknown> { return this.api.deleteCredentialSecret(name); }

  grants(agent = ''): Observable<CredentialGrant[]> { return this.api.credentialGrants(agent); }
  addGrant(g: Partial<CredentialGrant>): Observable<CredentialGrant> { return this.api.addCredentialGrant(g); }
  deleteGrant(id: string): Observable<unknown> { return this.api.deleteCredentialGrant(id); }

  sessions(agent = ''): Observable<CredentialSession[]> { return this.api.credentialSessions(agent); }
  mint(body: { agent: string; credential: string; ttl: string; grant_id?: string }): Observable<CredentialMint> {
    return this.api.mintCredentialSession(body);
  }
  revokeSession(id: string): Observable<unknown> { return this.api.revokeCredentialSession(id); }

  requests(): Observable<BrokerRequestEntry[]> { return this.api.credentialRequests(); }

  enableDeepSeek(agentId: string, body: { credential: string; model: string; ttl: string }) {
    return this.api.enableDeepSeek(agentId, body);
  }
}
