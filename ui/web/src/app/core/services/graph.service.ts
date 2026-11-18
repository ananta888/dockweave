import { Injectable, inject } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { Observable } from 'rxjs';

export interface GraphDocument {
  version: number;
  schema?: unknown;
  connectors?: unknown[];
  customEdges?: unknown[];
  ghosts?: unknown[];
  positions?: Record<string, { x: number; y: number }>;
  [key: string]: unknown;
}

export interface GraphDocumentResponse {
  saved: boolean;
  body: GraphDocument | null;
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
}
