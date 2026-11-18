import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';
import { ApiService } from '../api.service';
import { AgentUsage } from '../domain/usage';

@Injectable({ providedIn: 'root' })
export class UsageService {
  private readonly api = inject(ApiService);
  usage(): Observable<AgentUsage[]> { return this.api.usage(); }
}
