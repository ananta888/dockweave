import { Injectable, inject } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { Observable, catchError, throwError } from 'rxjs';
import {
  Agent, AgentActivity, AgentPaths, BrokerGrant, BrokerRequestEntry, BrokerStatus,
  AgentMCP, AgentSkill, AgentTypeMCP, AgentTypeSkill, BrokerUsage, DirListing, EgressStatus, Event, FileConflict, Grant, MemoryInfo, MemorySnapshot,
  MountState, ProviderStatus, Workspace, SkillCatalog, SkillCatalogEntry,
} from './models';

export interface AgentSpec {
  name: string;
  type: string;
  image: string;
  command?: string[];
  env_file?: string;
  agent_ro?: boolean;
  uid?: number;
  gid?: number;
  cpu_limit?: number;
  memory_mb?: number;
  network_mode?: string;
  network_profile?: string;
  runtime?: string;
}

@Injectable({ providedIn: 'root' })
export class ApiService {
  private readonly http = inject(HttpClient);
  private readonly base = '/api/v1';

  private req<T>(obs: Observable<T>): Observable<T> {
    return obs.pipe(catchError((err: HttpErrorResponse) => {
      const msg = err.error?.error?.message || err.error?.error || err.message || 'request failed';
      return throwError(() => new Error(msg));
    }));
  }

  health(): Observable<Record<string, unknown>> { return this.req(this.http.get<Record<string, unknown>>(`${this.base}/health`)); }

  agents(): Observable<Agent[]> { return this.req(this.http.get<Agent[]>(`${this.base}/agents`)); }
  agent(id: string): Observable<Agent> { return this.req(this.http.get<Agent>(`${this.base}/agents/${id}`)); }
  createAgent(spec: AgentSpec): Observable<Agent> { return this.req(this.http.post<Agent>(`${this.base}/agents`, spec)); }
  updateAgent(id: string, body: Record<string, unknown>): Observable<Agent> {
    return this.req(this.http.patch<Agent>(`${this.base}/agents/${id}`, body));
  }
  startAgent(id: string): Observable<Agent> { return this.req(this.http.post<Agent>(`${this.base}/agents/${id}/start`, {})); }
  stopAgent(id: string): Observable<unknown> { return this.req(this.http.post(`${this.base}/agents/${id}/stop`, {})); }
  restartAgent(id: string): Observable<Agent> { return this.req(this.http.post<Agent>(`${this.base}/agents/${id}/restart`, {})); }
  removeAgent(id: string, prune = false): Observable<unknown> {
    return this.req(this.http.delete(`${this.base}/agents/${id}?prune=${prune}`));
  }
  paths(id: string): Observable<AgentPaths> { return this.req(this.http.get<AgentPaths>(`${this.base}/agents/${id}/paths`)); }
  setAgentDir(id: string, path: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/agent-dir`, { path }));
  }
  setNotesDir(id: string, path: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/notes-dir`, { path }));
  }
  setEnvFile(id: string, path: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/env-file`, { path }));
  }
  setAgentAvatar(id: string, avatar: string, svg: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/avatar`, { avatar, svg }));
  }
  setAgentOrganization(id: string, displayName: string, orgUnit: string, role: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/organization`, {
      display_name: displayName, org_unit: orgUnit, role,
    }));
  }
  setNetworkProfile(id: string, profile: string): Observable<Agent> {
    return this.req(this.http.put<Agent>(`${this.base}/agents/${id}/network-profile`, { profile }));
  }
  egress(id: string): Observable<EgressStatus> {
    return this.req(this.http.get<EgressStatus>(`${this.base}/agents/${id}/egress`));
  }
  session(id: string): Observable<Record<string, unknown>> {
    return this.req(this.http.get<Record<string, unknown>>(`${this.base}/agents/${id}/session`));
  }

  activity(): Observable<AgentActivity[]> { return this.req(this.http.get<AgentActivity[]>(`${this.base}/activity`)); }
  conflicts(): Observable<FileConflict[]> { return this.req(this.http.get<FileConflict[]>(`${this.base}/conflicts`)); }
  events(limit = 100): Observable<Event[]> { return this.req(this.http.get<Event[]>(`${this.base}/events?limit=${limit}`)); }
  providers(): Observable<ProviderStatus> { return this.req(this.http.get<ProviderStatus>(`${this.base}/monitor/providers`)); }

  workspaces(): Observable<Workspace[]> { return this.req(this.http.get<Workspace[]>(`${this.base}/workspaces`)); }
  registerWorkspace(name: string, path: string): Observable<Workspace> {
    return this.req(this.http.post<Workspace>(`${this.base}/workspaces`, { name, path }));
  }
  unregisterWorkspace(id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/workspaces/${id}`)); }

  grants(): Observable<Grant[]> { return this.req(this.http.get<Grant[]>(`${this.base}/grants`)); }
  createGrant(workspace: string, agent: string, mode: string): Observable<Grant> {
    return this.req(this.http.post<Grant>(`${this.base}/grants`, { workspace, agent, mode }));
  }
  revokeGrant(id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/grants/${id}`)); }
  mounts(agent = ''): Observable<MountState[]> {
    return this.req(this.http.get<MountState[]>(`${this.base}/mounts${agent ? `?agent=${encodeURIComponent(agent)}` : ''}`));
  }

  brokerStatus(): Observable<BrokerStatus> { return this.req(this.http.get<BrokerStatus>(`${this.base}/broker/status`)); }
  brokerGrants(agent = ''): Observable<BrokerGrant[]> {
    return this.req(this.http.get<BrokerGrant[]>(`${this.base}/broker/grants${agent ? `?agent=${encodeURIComponent(agent)}` : ''}`));
  }
  brokerRequests(): Observable<BrokerRequestEntry[]> { return this.req(this.http.get<BrokerRequestEntry[]>(`${this.base}/broker/requests`)); }
  brokerUsage(grant: string): Observable<BrokerUsage> {
    return this.req(this.http.get<BrokerUsage>(`${this.base}/broker/usage?grant=${encodeURIComponent(grant)}`));
  }

  memories(): Observable<MemoryInfo[]> { return this.req(this.http.get<MemoryInfo[]>(`${this.base}/memories`)); }
  listTypeMCP(agentType: string): Observable<AgentTypeMCP[]> { return this.req(this.http.get<AgentTypeMCP[]>(`${this.base}/agent-types/${agentType}/mcp`)); }
  addTypeMCP(agentType: string, m: Partial<AgentTypeMCP>): Observable<AgentTypeMCP> { return this.req(this.http.post<AgentTypeMCP>(`${this.base}/agent-types/${agentType}/mcp`, m)); }
  updateTypeMCP(agentType: string, id: string, m: Partial<AgentTypeMCP>): Observable<AgentTypeMCP> { return this.req(this.http.patch<AgentTypeMCP>(`${this.base}/agent-types/${agentType}/mcp/${id}`, m)); }
  deleteTypeMCP(agentType: string, id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/agent-types/${agentType}/mcp/${id}`)); }
  listTypeSkills(agentType: string): Observable<AgentTypeSkill[]> { return this.req(this.http.get<AgentTypeSkill[]>(`${this.base}/agent-types/${agentType}/skills`)); }
  addTypeSkill(agentType: string, sk: Partial<AgentTypeSkill>): Observable<AgentTypeSkill> { return this.req(this.http.post<AgentTypeSkill>(`${this.base}/agent-types/${agentType}/skills`, sk)); }
  updateTypeSkill(agentType: string, id: string, sk: Partial<AgentTypeSkill>): Observable<AgentTypeSkill> { return this.req(this.http.patch<AgentTypeSkill>(`${this.base}/agent-types/${agentType}/skills/${id}`, sk)); }
  deleteTypeSkill(agentType: string, id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/agent-types/${agentType}/skills/${id}`)); }
  importTypeDefaults(agentType: string): Observable<{ mcp_added: number; skills_added: number }> { return this.req(this.http.post<{ mcp_added: number; skills_added: number }>(`${this.base}/agent-types/${agentType}/import`, {})); }
  applyTypeDefaults(agent: string): Observable<{ mcp_added: number; skills_added: number }> { return this.req(this.http.post<{ mcp_added: number; skills_added: number }>(`${this.base}/agents/${agent}/apply-type-defaults`, {})); }
  listMCP(agent: string): Observable<AgentMCP[]> { return this.req(this.http.get<AgentMCP[]>(`${this.base}/agents/${agent}/mcp`)); }
  addMCP(agent: string, m: Partial<AgentMCP>): Observable<AgentMCP> { return this.req(this.http.post<AgentMCP>(`${this.base}/agents/${agent}/mcp`, m)); }
  updateMCP(agent: string, id: string, m: Partial<AgentMCP>): Observable<AgentMCP> { return this.req(this.http.patch<AgentMCP>(`${this.base}/agents/${agent}/mcp/${id}`, m)); }
  deleteMCP(agent: string, id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/agents/${agent}/mcp/${id}`)); }
  listSkills(agent: string): Observable<AgentSkill[]> { return this.req(this.http.get<AgentSkill[]>(`${this.base}/agents/${agent}/skills`)); }
  addSkill(agent: string, sk: Partial<AgentSkill>): Observable<AgentSkill> { return this.req(this.http.post<AgentSkill>(`${this.base}/agents/${agent}/skills`, sk)); }
  updateSkill(agent: string, id: string, sk: Partial<AgentSkill>): Observable<AgentSkill> { return this.req(this.http.patch<AgentSkill>(`${this.base}/agents/${agent}/skills/${id}`, sk)); }
  deleteSkill(agent: string, id: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/agents/${agent}/skills/${id}`)); }
  getSkillCatalog(): Observable<SkillCatalog> { return this.req(this.http.get<SkillCatalog>(`${this.base}/skills/catalog`)); }
  installCatalogSkill(repo: string, path: string, ref: string): Observable<SkillCatalogEntry> { return this.req(this.http.post<SkillCatalogEntry>(`${this.base}/skills/install`, { repo, path, ref })); }
  deleteCatalogSkill(name: string): Observable<unknown> { return this.req(this.http.delete(`${this.base}/skills/${encodeURIComponent(name)}`)); }
  listDirs(path = ''): Observable<DirListing> {
    return this.req(this.http.get<DirListing>(`${this.base}/fs/dirs${path ? `?path=${encodeURIComponent(path)}` : ''}`));
  }
  registerMemory(path: string): Observable<MemoryInfo> {
    return this.req(this.http.post<MemoryInfo>(`${this.base}/memories`, { path }));
  }
  snapshots(id: string): Observable<MemorySnapshot[]> {
    return this.req(this.http.get<MemorySnapshot[]>(`${this.base}/agents/${id}/memory/snapshots`));
  }
  snapshot(id: string): Observable<MemorySnapshot> {
    return this.req(this.http.post<MemorySnapshot>(`${this.base}/agents/${id}/memory/snapshot`, {}));
  }
  restoreSnapshot(id: string, name: string): Observable<unknown> {
    return this.req(this.http.post(`${this.base}/agents/${id}/memory/restore`, { snapshot: name }));
  }
  deleteSnapshot(id: string, name: string): Observable<unknown> {
    return this.req(this.http.delete(`${this.base}/agents/${id}/memory/snapshots/${encodeURIComponent(name)}`));
  }
  attachMemory(id: string, memoryId: string): Observable<MemoryInfo> {
    return this.req(this.http.post<MemoryInfo>(`${this.base}/agents/${id}/memory/attach`, { memory_id: memoryId }));
  }
  detachMemory(id: string): Observable<MemoryInfo> {
    return this.req(this.http.post<MemoryInfo>(`${this.base}/agents/${id}/memory/detach`, {}));
  }
}
