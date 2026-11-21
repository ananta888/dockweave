import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

export interface GraphDocument {
  version: number;
  revision?: number;
  schema?: unknown;
  connectors?: unknown[];
  customEdges?: unknown[];
  ghosts?: unknown[];
  positions?: Record<string, { x: number; y: number }>;
  paperclip?: {
    /** Host path of the Paperclip workspace (gates.json lives in .paperclip/). */
    workspace?: string;
    syncGates?: boolean;
  };
  [key: string]: unknown;
}

export interface GraphGateSyncResult {
  written?: boolean;
  skipped?: boolean;
  reason?: string;
  error?: string;
  workspace?: string;
  path?: string;
  gates?: number;
  review?: boolean;
  human_approval?: boolean;
  warnings?: string[];
}

export interface GraphDocumentResponse {
  saved: boolean;
  body: GraphDocument | null;
  revision?: number;
  gates?: GraphGateSyncResult;
}

export interface GateSyncStatus {
  enabled?: boolean;
  exists?: boolean;
  managed?: boolean;
  drifted?: boolean;
  hash?: string;
  generatedAt?: string;
  path?: string;
  workspace?: string;
  error?: string;
}

// The graph document (layout, connectors, deterministic gates) is stored by the
// web gateway instead of the browser, so wrappers and adapters can read it.
@Injectable({ providedIn: 'root' })
export class GraphService {
  private readonly http = inject(HttpClient);

  load(): Observable<GraphDocumentResponse> {
    return this.http.get<GraphDocumentResponse>('/graph');
  }

  save(doc: GraphDocument): Observable<GraphDocumentResponse> {
    return this.http.put<GraphDocumentResponse>('/graph', doc);
  }

  gateStatus(): Observable<GateSyncStatus> {
    return this.http.get<GateSyncStatus>('/graph/gates/status');
  }
}
