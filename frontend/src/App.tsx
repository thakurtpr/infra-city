// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useMemo, useState } from 'react';
import CityScene, { CityMode } from './three/CityScene';
import { api, connectEvents, Edge, Node } from './api/client';

export default function App() {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [metrics, setMetrics] = useState({ requestsPerSec: 0, errorRate: 0, p95LatencyMs: 0, pods: 0, services: 0, flows: 0 });
  const [incidents, setIncidents] = useState<{ id: string; title: string; rootNode: string; severity: string }[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [blast, setBlast] = useState<{ downstream: string[]; upstream: string[] } | null>(null);
  const [explain, setExplain] = useState('');
  const [mode, setMode] = useState<CityMode>('live');
  const [query, setQuery] = useState('');
  const [flyTo, setFlyTo] = useState<string | null>(null);
  const [live, setLive] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const [g, m, inc] = await Promise.all([api.graph(), api.metrics(), api.incidents()]);
      setNodes(g.nodes); setEdges(g.edges); setMetrics(m); setIncidents(inc);
    } catch (e) { console.warn('backend unreachable (make dev running?)', e); }
  }, []);

  useEffect(() => { refresh(); const t = setInterval(refresh, 5000); return () => clearInterval(t); }, [refresh]);

  // live WS: patch graph incrementally, no page refresh
  useEffect(() => {
    if (!live) return;
    return connectEvents((ev) => {
      if (ev.resource) {
        setNodes((prev) => {
          const i = prev.findIndex((n) => n.id === ev.resource!.id);
          if (i >= 0) { const c = [...prev]; c[i] = ev.resource!; return c; }
          return [...prev, ev.resource!];
        });
      }
      if (ev.edge) {
        setEdges((prev) => {
          const i = prev.findIndex((e) => e.id === ev.edge!.id);
          if (i >= 0) { const c = [...prev]; c[i] = ev.edge!; return c; }
          return [...prev, ev.edge!];
        });
      }
    });
  }, [live]);

  const selNode = useMemo(() => nodes.find((n) => n.id === selected) ?? null, [nodes, selected]);

  const inspect = async (id: string | null) => {
    setSelected(id); setBlast(null); setExplain('');
    if (!id) return;
    try {
      const [b, ex] = await Promise.all([api.blast(id), api.explain(id)]);
      setBlast(b); setExplain(ex.explanation);
    } catch { /* backend may be mid-restart */ }
  };

  const search = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!query.trim()) return;
    const res = await api.search(query);
    if (res.length) { setFlyTo(res[0].id); inspect(res[0].id); }
  };

  return (
    <div style={{ position: 'relative', height: '100%' }}>
      <CityScene nodes={nodes} edges={edges} selected={selected} blast={blast} mode={mode} onSelect={inspect} flyTo={flyTo} />

      {/* top bar: search + modes */}
      <div style={bar}>
        <strong style={{ letterSpacing: 1 }}>INFRACITY</strong>
        <form onSubmit={search} style={{ display: 'flex', gap: 8 }}>
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="search: payments, svc:checkout, ns:payments…"
            style={input} />
        </form>
        <div style={{ display: 'flex', gap: 6 }}>
          {(['live', 'heatmap', 'security', 'cost'] as CityMode[]).map((m) => (
            <button key={m} onClick={() => setMode(m)} style={mode === m ? btnActive : btn}>{m}</button>
          ))}
          <button onClick={() => setLive(!live)} style={live ? btnActive : btn}>{live ? '● live' : '○ paused'}</button>
        </div>
      </div>

      {/* command center */}
      <div style={panel}>
        <div style={h}>COMMAND CENTER</div>
        <Stat label="req/s" value={metrics.requestsPerSec > 0 ? fmt(metrics.requestsPerSec) : '— L4 only'} />
        <Stat label="error" value={metrics.requestsPerSec > 0 ? (metrics.errorRate * 100).toFixed(2) + '%' : '—'} alert={metrics.errorRate > 0.02} />
        <Stat label="p95" value={metrics.p95LatencyMs > 0 ? fmt(metrics.p95LatencyMs) + 'ms' : '—'} alert={metrics.p95LatencyMs > 500} />
        <Stat label="pods" value={String(metrics.pods)} />
        <Stat label="services" value={String(metrics.services)} />
        <Stat label="flows" value={String(metrics.flows)} />
        <div style={h}>INCIDENTS ({incidents.length})</div>
        {incidents.length === 0 && <div style={{ opacity: 0.6 }}>all clear</div>}
        {incidents.map((i) => (
          <button key={i.id} onClick={() => inspect(i.rootNode)} style={incBtn}>
            {(i.severity === 'critical' ? '🔴 ' : '🟡 ') + i.title}
          </button>
        ))}
        <div style={{ opacity: 0.55, fontSize: 11, marginTop: 8 }}>click building to inspect · drag to orbit · right-drag to slide · scroll to zoom · WASD/arrows to glide · double-click to fly there</div>
      </div>

      {/* inspector */}
      {selNode && (
        <div style={inspector}>
          <button onClick={() => inspect(null)} style={btn}>✕</button>
          <h3 style={{ margin: '4px 0' }}>{selNode.name}</h3>
          <div style={{ opacity: 0.7, fontSize: 12 }}>{selNode.type} · {selNode.namespace ?? 'cluster-scoped'}</div>
          {selNode.metrics && (
            <table style={{ fontSize: 12, marginTop: 8 }}>
              <tbody>
                <Row k="CPU" v={(selNode.metrics.cpuPct ?? 0).toFixed(0) + '%'} />
                <Row k="MEM" v={(selNode.metrics.memPct ?? 0).toFixed(0) + '%'} />
                <Row k="req/s" v={fmt(selNode.metrics.reqPerSec ?? 0)} />
                <Row k="err" v={((selNode.metrics.errRate ?? 0) * 100).toFixed(2) + '%'} />
                <Row k="p95" v={fmt(selNode.metrics.latencyMsP95 ?? 0) + 'ms'} />
                {selNode.metrics.replicas ? <Row k="replicas" v={`${selNode.metrics.readyReplicas}/${selNode.metrics.replicas}`} /> : null}
              </tbody>
            </table>
          )}
          {blast && (
            <div style={{ fontSize: 12, marginTop: 8 }}>
              <div>↓ downstream: <b>{blast.downstream.length}</b> · ↑ upstream: <b>{blast.upstream.length}</b></div>
            </div>
          )}
          {explain && <p style={{ fontSize: 12, opacity: 0.9 }}>🤖 {explain}</p>}
        </div>
      )}
    </div>
  );
}

const fmt = (n: number) => n >= 1000 ? (n / 1000).toFixed(1) + 'k' : n.toFixed(0);
const Stat = ({ label, value, alert }: { label: string; value: string; alert?: boolean }) => (
  <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}>
    <span style={{ opacity: 0.65 }}>{label}</span>
    <b style={{ color: alert ? '#ff6b6b' : '#fff' }}>{value}</b>
  </div>
);
const Row = ({ k, v }: { k: string; v: string }) => (<tr><td style={{ opacity: 0.6, paddingRight: 8 }}>{k}</td><td><b>{v}</b></td></tr>);

const bar: React.CSSProperties = { position: 'absolute', top: 12, left: 12, right: 12, display: 'flex', gap: 12, alignItems: 'center', justifyContent: 'space-between', background: 'rgba(10,16,34,.85)', padding: '8px 12px', borderRadius: 10, border: '1px solid #22305e', backdropFilter: 'blur(6px)' };
const panel: React.CSSProperties = { position: 'absolute', left: 12, top: 70, width: 230, background: 'rgba(10,16,34,.88)', padding: 12, borderRadius: 10, border: '1px solid #22305e', display: 'flex', flexDirection: 'column', gap: 4 };
const inspector: React.CSSProperties = { position: 'absolute', right: 12, top: 70, width: 300, background: 'rgba(10,16,34,.92)', padding: 12, borderRadius: 10, border: '1px solid #22305e' };
const h: React.CSSProperties = { fontSize: 11, letterSpacing: 1.5, opacity: 0.6, marginTop: 6 };
const input: React.CSSProperties = { background: '#0d1530', color: '#dbe4ff', border: '1px solid #2a3a6e', borderRadius: 6, padding: '6px 10px', width: 320, outline: 'none' };
const btn: React.CSSProperties = { background: '#16204a', color: '#dbe4ff', border: '1px solid #2a3a6e', borderRadius: 6, padding: '6px 10px', cursor: 'pointer', fontSize: 12 };
const btnActive: React.CSSProperties = { ...btn, background: '#2b4bd8', borderColor: '#5b7cff' };
const incBtn: React.CSSProperties = { ...btn, textAlign: 'left', marginTop: 4 };
