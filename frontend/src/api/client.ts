// SPDX-License-Identifier: Apache-2.0
// Typed client for the InfraCity backend (REST + WS with reconnect).
export interface Node {
  id: string; type: string; cluster: string; namespace?: string; name: string;
  status?: string; labels?: Record<string, string>; metadata?: Record<string, string>;
  nodeName?: string; ip?: string; role?: string;
  metrics?: {
    cpuPct?: number; memPct?: number; reqPerSec?: number; errRate?: number;
    latencyMsP95?: number; bytesPerSec?: number; replicas?: number; readyReplicas?: number; restarts?: number;
  };
}

export interface Edge {
  id: string; source: string; destination: string; type: string;
  protocol?: string; dstPort?: number; requestsPerSec?: number;
  latencyMs?: number; bytesPerSec?: number; errorsPerSec?: number; connections?: number;
}

export interface GraphData { nodes: Node[]; edges: Edge[]; }
export interface Incident { id: string; title: string; rootNode: string; severity: string; affected: string[]; description?: string; }
export interface WSEvent { type: string; resource?: Node; edge?: Edge; message?: string; timestamp: string; }

const base = '';

async function get<T>(path: string): Promise<T> {
  const r = await fetch(base + path);
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json() as Promise<T>;
}

export const api = {
  graph: (cluster = '') => get<GraphData>(`/api/graph${cluster ? `?cluster=${encodeURIComponent(cluster)}` : ''}`),
  clusters: () => get<{ id: string; environment?: string; region?: string; provider?: string; nodeCount: number; podCount: number; health: string }[]>('/api/clusters'),
  metrics: () => get<{ requestsPerSec: number; errorRate: number; p95LatencyMs: number; pods: number; services: number; flows: number }>('/api/metrics'),
  incidents: () => get<Incident[]>('/api/incidents'),
  dependencies: () => get<{ source: string; target: string; confidence: number }[]>('/api/dependencies'),
  blast: (id: string) => get<{ root: string; downstream: string[]; upstream: string[] }>(`/api/blast/${encodeURIComponent(id)}?depth=4`),
  explain: (target: string) => get<{ target: string; explanation: string; upstream: string[] }>(`/api/explain?target=${encodeURIComponent(target)}`),
  search: (q: string) => get<Node[]>(`/api/search?q=${encodeURIComponent(q)}`),
  snapshots: () => get<{ id: string; timestamp: string; label?: string }[]>('/api/snapshots'),
  snapshot: (id: string) => get<GraphData>(`/api/snapshots/${id}`),
  self: () => get<Record<string, unknown>>('/api/self'),
};

export function connectEvents(onEvent: (e: WSEvent) => void): () => void {
  let closed = false;
  let ws: WebSocket | null = null;
  let backoff = 1000;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  function open() {
    if (closed) return;
    ws = new WebSocket(`${proto}://${location.host}/ws/events`);
    ws.onmessage = (m) => {
      try { onEvent(JSON.parse(m.data)); } catch { /* ignore hello frames */ }
    };
    ws.onclose = () => {
      if (closed) return;
      setTimeout(open, Math.min(backoff *= 1.5, 10000));
    };
    ws.onerror = () => ws?.close();
  }
  open();
  return () => { closed = true; ws?.close(); };
}
