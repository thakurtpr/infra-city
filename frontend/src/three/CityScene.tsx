// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef } from 'react';
import * as THREE from 'three';
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';
import type { Edge, Node } from '../api/client';
import { esc, fmtK, shortId } from '../utils/format';

export type CityMode = 'live' | 'heatmap' | 'security' | 'cost';

interface Props {
  nodes: Node[];
  edges: Edge[];
  selected: string | null;
  blast: { downstream: string[]; upstream: string[] } | null;
  mode: CityMode;
  onSelect: (id: string | null) => void;
  flyTo: string | null;
}

interface CityState {
  scene: THREE.Scene;
  camera: THREE.PerspectiveCamera;
  renderer: THREE.WebGLRenderer;
  controls: OrbitControls;
  city: THREE.Group;               // everything rebuilt per data change
  selectables: THREE.Object3D[];   // raycast targets (each carries userData.id)
  byId: Map<string, Node>;
  nodePos: Map<string, THREE.Vector3>; // tower-top anchor per node
  edgeList: Edge[];                // rolled observed flows (packet layer)
  wiringList: Edge[];              // declared/inferred wiring (dashed layer)
  roads: THREE.LineSegments[];     // raycast targets for road hover (userData.kind/edges)
  packets: THREE.Points | null;
  packetT: number[];
  packetEdge: number[];
  districts: { name: string; el: HTMLDivElement; pos: THREE.Vector3 }[];
  labelLayer: HTMLDivElement;
  tooltip: HTMLDivElement;
  chip: HTMLDivElement;
  hovered: string | null;
  fitted: boolean;
  userDrove: boolean;
  flight: { target: THREE.Vector3; pos: THREE.Vector3 } | null;
  keys: Set<string>;
}

// District layout constants (compact so the city reads as one city, not islands).
const DISTRICT = 96;
const GAP = 26;
const PITCH = DISTRICT + GAP;

export default function CityScene({ nodes, edges, selected, blast, mode, onSelect, flyTo }: Props) {
  const mountRef = useRef<HTMLDivElement>(null);
  const stateRef = useRef<CityState | null>(null);
  const propsRef = useRef({ nodes, edges, selected, blast, mode, onSelect });
  propsRef.current = { nodes, edges, selected, blast, mode, onSelect };

  // ---- init once ----
  useEffect(() => {
    const mount = mountRef.current!;
    const scene = new THREE.Scene();
    scene.background = new THREE.Color(0x060a16);
    scene.fog = new THREE.Fog(0x060a16, 320, 760);

    const camera = new THREE.PerspectiveCamera(52, mount.clientWidth / mount.clientHeight, 0.5, 2500);
    camera.position.set(170, 150, 170);

    const renderer = new THREE.WebGLRenderer({ antialias: true });
    renderer.setSize(mount.clientWidth, mount.clientHeight);
    renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    mount.appendChild(renderer.domElement);

    // HTML overlay layers (crisp labels that can never clip like sprites)
    const labelLayer = document.createElement('div');
    Object.assign(labelLayer.style, { position: 'absolute', inset: '0', overflow: 'hidden', pointerEvents: 'none' } as CSSStyleDeclaration);
    mount.appendChild(labelLayer);
    const tooltip = document.createElement('div');
    styleTooltip(tooltip);
    mount.appendChild(tooltip);
    const chip = document.createElement('div');
    styleChip(chip);
    mount.appendChild(chip);

    const controls = new OrbitControls(camera, renderer.domElement);
    controls.enableDamping = true;
    controls.dampingFactor = 0.08;
    controls.maxPolarAngle = Math.PI / 2.08;
    controls.minDistance = 20;
    controls.maxDistance = 600;

    // --- lighting: dusk city ---
    scene.add(new THREE.HemisphereLight(0x8fa3ff, 0x0a0f22, 0.75));
    const moon = new THREE.DirectionalLight(0xbfd0ff, 1.0);
    moon.position.set(160, 220, 80);
    scene.add(moon);
    const warm = new THREE.PointLight(0xffb46b, 0.5, 600);
    warm.position.set(0, 60, 0);
    scene.add(warm);

    // --- ground + stars ---
    const ground = new THREE.Mesh(
      new THREE.PlaneGeometry(1200, 1200),
      new THREE.MeshStandardMaterial({ color: 0x080d1d, roughness: 1 })
    );
    ground.rotation.x = -Math.PI / 2;
    ground.position.y = -0.6;
    scene.add(ground);
    scene.add(stars());

    const st: CityState = {
      scene, camera, renderer, controls,
      city: new THREE.Group(), selectables: [], byId: new Map(),
      nodePos: new Map(), edgeList: [], wiringList: [], roads: [], packets: null, packetT: [], packetEdge: [],
      districts: [], labelLayer, tooltip, chip, hovered: null, fitted: false, userDrove: false,
      flight: null, keys: new Set<string>(),
    };
    scene.add(st.city);
    stateRef.current = st;
    controls.addEventListener('start', () => { st.userDrove = true; st.flight = null; });

    // WASD / arrow-key glide: slide across the city without touching the mouse.
    // (Skipped while typing in the search box.)
    const inField = () => {
      const a = document.activeElement;
      return a && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA');
    };
    window.addEventListener('keydown', (e) => {
      if (inField()) return;
      const k = e.key.toLowerCase();
      if (['w', 'a', 's', 'd', 'arrowup', 'arrowdown', 'arrowleft', 'arrowright'].includes(k)) {
        st.keys.add(k);
        st.flight = null;
        e.preventDefault();
      }
    });
    window.addEventListener('keyup', (e) => { st.keys.delete(e.key.toLowerCase()); });

    // click-to-select (drag-safe: ignore if pointer moved)
    const ray = new THREE.Raycaster();
    const ptr = new THREE.Vector2();
    let down = { x: 0, y: 0 };
    renderer.domElement.addEventListener('pointerdown', (e) => { down = { x: e.clientX, y: e.clientY }; });
    renderer.domElement.addEventListener('pointerup', (e) => {
      if (Math.hypot(e.clientX - down.x, e.clientY - down.y) > 6) return;
      const hit = pick(e, ray, ptr, st);
      propsRef.current.onSelect(hit);
    });
    // double-click to travel: fly the camera to a building top or ground point.
    const groundMath = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);
    renderer.domElement.addEventListener('dblclick', (e) => {
      const r = renderer.domElement.getBoundingClientRect();
      ptr.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
      ray.setFromCamera(ptr, camera);
      const hits = ray.intersectObjects(st.selectables, false);
      let p: THREE.Vector3 | null = null;
      if (hits.length) p = hits[0].point.clone();
      else {
        const gp = new THREE.Vector3();
        if (ray.ray.intersectPlane(groundMath, gp)) p = gp;
      }
      if (!p) return;
      flyToPoint(st, p);
      const bhit = pick(e, ray, ptr, st);
      if (bhit) propsRef.current.onSelect(bhit);
    });
    renderer.domElement.addEventListener('pointermove', (e) => {
      const hit = pick(e, ray, ptr, st);
      st.hovered = hit;
      if (hit) {
        renderer.domElement.style.cursor = 'pointer';
        const n = st.byId.get(hit);
        tooltip.style.display = 'block';
        tooltip.style.left = e.clientX - mount.getBoundingClientRect().left + 14 + 'px';
        tooltip.style.top = e.clientY - mount.getBoundingClientRect().top + 10 + 'px';
        tooltip.innerHTML = `<b>${esc(n?.name ?? hit)}</b><br><span>${n?.type ?? ''}${n?.namespace ? ' · ' + esc(n.namespace) : ''}</span>`;
        return;
      }
      const road = pickRoad(e, ray, ptr, st);
      if (road) {
        renderer.domElement.style.cursor = 'crosshair';
        tooltip.style.display = 'block';
        tooltip.style.left = e.clientX - mount.getBoundingClientRect().left + 14 + 'px';
        tooltip.style.top = e.clientY - mount.getBoundingClientRect().top + 10 + 'px';
        tooltip.innerHTML = roadTooltip(st, road);
      } else {
        renderer.domElement.style.cursor = 'grab';
        tooltip.style.display = 'none';
      }
    });

    // legend: dashed dim = declared wiring, glowing = observed traffic
    const legend = document.createElement('div');
    Object.assign(legend.style, {
      position: 'absolute', bottom: '12px', left: '50%', transform: 'translateX(-50%)',
      display: 'flex', gap: '16px', fontSize: '11px', letterSpacing: '1px', color: '#8fa3ff',
      background: 'rgba(8,13,30,.8)', border: '1px solid #22305e', borderRadius: '8px',
      padding: '6px 14px', pointerEvents: 'none', whiteSpace: 'nowrap',
    } as CSSStyleDeclaration);
    legend.innerHTML = `<span><span style="color:#5a70c0">┄┄</span> declared wiring</span><span><span style="color:#35e0ff">━━</span> observed traffic</span>`;
    mount.appendChild(legend);

    const onResize = () => {
      camera.aspect = mount.clientWidth / mount.clientHeight;
      camera.updateProjectionMatrix();
      renderer.setSize(mount.clientWidth, mount.clientHeight);
    };
    window.addEventListener('resize', onResize);

    let raf = 0;
    const clock = new THREE.Clock();
    const animate = () => {
      raf = requestAnimationFrame(animate);
      const dt = Math.min(clock.getDelta(), 0.05);
      glideKeys(st, dt);
      stepFlight(st, dt);
      controls.update();
      animatePackets(st, dt);
      updateLabels(st);
      renderer.render(scene, camera);
    };
    animate();
    return () => {
      cancelAnimationFrame(raf);
      window.removeEventListener('resize', onResize);
      mount.removeChild(renderer.domElement);
      mount.removeChild(labelLayer);
      mount.removeChild(tooltip);
      mount.removeChild(chip);
      mount.removeChild(legend);
      stateRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- rebuild / highlight / fly ----
  useEffect(() => {
    const st = stateRef.current;
    if (st) rebuildCity(st, nodes, edges, mode);
  }, [nodes, edges, mode]);

  useEffect(() => {
    const st = stateRef.current;
    if (st) applyHighlight(st, selected, blast);
  }, [selected, blast]);

  useEffect(() => {
    const st = stateRef.current;
    if (st && flyTo) {
      const p = st.nodePos.get(flyTo);
      if (p) flyToPoint(st, p.clone());
    }
  }, [flyTo]);

  return <div ref={mountRef} style={{ position: 'absolute', inset: 0 }} />;
}

// ============================ construction ============================

function rebuildCity(st: CityState, nodes: Node[], edges: Edge[], mode: CityMode) {
  // dispose previous city
  st.scene.remove(st.city);
  st.city.traverse((o) => {
    const mesh = o as THREE.Mesh;
    if (mesh.geometry) mesh.geometry.dispose();
    const mat = (mesh as THREE.Mesh).material as THREE.Material | THREE.Material[] | undefined;
    if (Array.isArray(mat)) mat.forEach((m) => disposeMat(m));
    else if (mat) disposeMat(mat);
  });
  for (const d of st.districts) d.el.remove();
  st.districts = [];
  st.city = new THREE.Group();
  st.scene.add(st.city);
  st.selectables = [];
  st.byId = new Map(nodes.map((n) => [n.id, n]));
  st.nodePos.clear();
  st.packets = null;
  st.packetT = [];
  st.packetEdge = [];
  st.edgeList = [];
  st.wiringList = [];
  st.roads = [];

  const namespaces = [...new Set(nodes.filter((n) => n.namespace).map((n) => n.namespace!))].sort();
  const cols = Math.max(1, Math.ceil(Math.sqrt(namespaces.length)));
  const originX = -((cols - 1) * PITCH) / 2;

  namespaces.forEach((ns, i) => {
    const cx = originX + (i % cols) * PITCH;
    const cz = (Math.floor(i / cols) - (Math.ceil(namespaces.length / cols) - 1) / 2) * PITCH;
    buildDistrict(st, ns, cx, cz, nodes, mode);
  });

  // global (cluster-scoped leftovers: node block)
  const clusterNodes = nodes.filter((n) => !n.namespace && n.type === 'node');
  clusterNodes.forEach((n, i) => {
    const p = nodeBlock(st, n, -originX + 40 + i * 30, 6, -(namespaces.length ? 120 : 0), mode);
    void p;
  });

  buildTraffic(st, edges);
  for (const d of st.districts) st.labelLayer.appendChild(d.el);

  // auto-fit on first load only (never yank the camera after user takes over)
  if (!st.fitted && namespaces.length) {
    st.fitted = true;
    const w = cols * PITCH;
    st.controls.target.set(0, 0, 0);
    st.camera.position.set(w * 0.62, w * 0.58, w * 0.62);
  }
}

function buildDistrict(st: CityState, ns: string, cx: number, cz: number, nodes: Node[], mode: CityMode) {
  const local = nodes.filter((n) => n.namespace === ns && isVisual(n)).sort((a, b) => (a.id < b.id ? -1 : 1));
  const inhabited = local.length > 0;

  // plate (quiet zones with no workloads render faint — no streets, no lamps)
  const plate = new THREE.Mesh(
    new THREE.BoxGeometry(DISTRICT, 1.2, DISTRICT),
    new THREE.MeshStandardMaterial({
      color: inhabited ? 0x0d1734 : 0x090e20, roughness: 0.95,
      transparent: !inhabited, opacity: inhabited ? 1 : 0.55,
    })
  );
  plate.position.set(cx, -0.6, cz);
  st.city.add(plate);

  // glowing border
  const border = new THREE.LineLoop(
    new THREE.EdgesGeometry(new THREE.PlaneGeometry(DISTRICT, DISTRICT)),
    new THREE.LineBasicMaterial({ color: 0x3d5bff, transparent: true, opacity: inhabited ? 0.55 : 0.2 })
  );
  border.rotation.x = -Math.PI / 2;
  border.position.set(cx, 0.08, cz);
  st.city.add(border);

  if (!inhabited) {
    pushDistrictLabel(st, ns, cx, cz, true);
    return;
  }

  // streets: 3x3 grid of avenues
  const streetMat = new THREE.LineBasicMaterial({ color: 0x27407c, transparent: true, opacity: 0.8 });
  const pts: number[] = [];
  for (const off of [-DISTRICT / 4, 0, DISTRICT / 4]) {
    pts.push(cx + off, 0.06, cz - DISTRICT / 2, cx + off, 0.06, cz + DISTRICT / 2);
    pts.push(cx - DISTRICT / 2, 0.06, cz + off, cx + DISTRICT / 2, 0.06, cz + off);
  }
  const sg = new THREE.BufferGeometry();
  sg.setAttribute('position', new THREE.BufferAttribute(new Float32Array(pts), 3));
  st.city.add(new THREE.LineSegments(sg, streetMat));

  // street lamps (instanced, warm glow dots)
  const lampGeo = new THREE.SphereGeometry(0.5, 6, 6);
  const lampMat = new THREE.MeshBasicMaterial({ color: 0xffc873 });
  const lampPos: THREE.Matrix4[] = [];
  const m4 = new THREE.Matrix4();
  for (const ox of [-DISTRICT / 4, 0, DISTRICT / 4]) {
    for (const oz of [-DISTRICT / 3, 0, DISTRICT / 3]) {
      m4.makeTranslation(cx + ox + 3, 2.2, cz + oz);
      lampPos.push(m4.clone());
      // pole
      const pole = new THREE.Mesh(new THREE.CylinderGeometry(0.12, 0.12, 2.2, 5), new THREE.MeshStandardMaterial({ color: 0x1a2440 }));
      pole.position.set(cx + ox + 3, 1.1, cz + oz);
      st.city.add(pole);
    }
  }
  const lamps = new THREE.InstancedMesh(lampGeo, lampMat, lampPos.length);
  lampPos.forEach((m, i) => lamps.setMatrixAt(i, m));
  st.city.add(lamps);

  // buildings for this namespace, placed on block grid (skip avenue lines)
  const slots = blockSlots();
  local.forEach((n, i) => {
    const s = slots[i % slots.length];
    const jitter = ((hashStr(n.id) % 100) / 100 - 0.5) * 4;
    buildNode(st, n, cx + s[0] + jitter, cz + s[1] + jitter, mode);
  });

  pushDistrictLabel(st, ns, cx, cz, false);
}

function pushDistrictLabel(st: CityState, ns: string, cx: number, cz: number, dim: boolean) {
  // district label (HTML — always crisp, never clipped)
  const el = document.createElement('div');
  el.textContent = ns.toUpperCase().replace(/-/g, '‑');
  Object.assign(el.style, {
    position: 'absolute', transform: 'translate(-50%,-50%)', whiteSpace: 'nowrap',
    fontSize: '13px', fontWeight: '700', letterSpacing: '3px',
    color: dim ? '#5a6a9e' : '#aebfff',
    textShadow: '0 0 12px rgba(80,110,255,.8), 0 2px 6px #000',
  } as CSSStyleDeclaration);
  st.districts.push({ name: ns, el, pos: new THREE.Vector3(cx, 1.5, cz - DISTRICT / 2 - 7) });
}

// block-centre slots avoiding the avenue lines
function blockSlots(): [number, number][] {
  const q = DISTRICT / 4;
  const off = q / 2;
  const out: [number, number][] = [];
  for (const gx of [-q - off, -off, off, q + off]) {
    for (const gz of [-q - off, -off, off, q + off]) {
      void gx; void gz;
      out.push([gx, gz]);
    }
  }
  // 16 slots; overflow wraps with slight ring offset handled by caller jitter
  return out;
}

function isVisual(n: Node) {
  return ['deployment', 'statefulset', 'daemonset', 'database', 'service', 'gateway', 'pod'].includes(n.type);
}

function activityOf(n: Node): number {
  // 0..1 traffic activity from whatever signal exists (RPS preferred, conns fallback)
  const m = n.metrics;
  if (!m) return 0.15;
  const rps = m.reqPerSec ?? 0;
  if (rps > 0) return Math.min(1, rps / 1200);
  return 0.15;
}

function heightOf(n: Node): number {
  const m = n.metrics;
  const base = 9 + (hashStr(n.id) % 13); // deterministic variety 9..21
  const repBoost = Math.min(12, ((m?.replicas ?? 0) + (m?.readyReplicas ?? 0)) * 1.5);
  const cpuBoost = ((m?.cpuPct ?? 0) / 100) * 14;
  const rpsBoost = Math.min(10, (m?.reqPerSec ?? 0) / 250);
  return Math.min(46, Math.max(7, base + repBoost + cpuBoost + rpsBoost));
}

function facadeColor(n: Node, mode: CityMode): number {
  const err = n.metrics?.errRate ?? 0;
  if (err > 0.05) return 0x7a1f24;
  if (mode === 'heatmap') {
    const cpu = Math.min(1, (n.metrics?.cpuPct ?? 20) / 100);
    return new THREE.Color().setHSL(0.62 - cpu * 0.62, 0.75, 0.32).getHex();
  }
  if (mode === 'cost') {
    const t = Math.min(1, (n.metrics?.costPerMonth ?? 0) / 150);
    return new THREE.Color().setHSL(0.33 - t * 0.33, 0.8, 0.32).getHex();
  }
  if (mode === 'security') return 0x1c3f8f;
  switch (n.type) {
    case 'database': return n.role === 'redis' ? 0x6e1f22 : n.role === 'kafka' ? 0x3d2a86 : 0x173f6e;
    case 'gateway': return 0x6b4d12;
    case 'service': return 0x0f4a3f;
    case 'pod': return 0x232c4d;
    default: return 0x1b2a66;
  }
}

function windowLitRatio(n: Node): number {
  return 0.25 + activityOf(n) * 0.6;
}

// Canvas facade: dark concrete + seeded lit windows. Lit ratio follows traffic.
function facadeTexture(n: Node): THREE.CanvasTexture {
  const seed = hashStr(n.id);
  const cv = document.createElement('canvas');
  cv.width = 64; cv.height = 128;
  const ctx = cv.getContext('2d')!;
  ctx.fillStyle = '#0a1026';
  ctx.fillRect(0, 0, 64, 128);
  const rnd = mulberry(seed);
  const lit = windowLitRatio(n);
  const err = (n.metrics?.errRate ?? 0) > 0.05;
  for (let y = 4; y < 124; y += 8) {
    for (let x = 4; x < 60; x += 8) {
      const on = rnd() < lit;
      ctx.fillStyle = !on ? '#141d3d' : err ? '#ff5b5b' : rnd() < 0.8 ? '#ffd489' : '#9be9ff';
      ctx.fillRect(x, y, 5, 4);
    }
  }
  const tex = new THREE.CanvasTexture(cv);
  tex.colorSpace = THREE.SRGBColorSpace;
  return tex;
}

function buildNode(st: CityState, n: Node, x: number, z: number, mode: CityMode) {
  const g = new THREE.Group();
  g.position.set(x, 0, z);
  const col = facadeColor(n, mode);

  if (n.type === 'service') {
    // plaza / interchange: glowing disc
    const disc = new THREE.Mesh(
      new THREE.CylinderGeometry(6.5, 7.5, 1.6, 24),
      new THREE.MeshStandardMaterial({ color: col, emissive: 0x27c2a5, emissiveIntensity: 0.25 + activityOf(n), roughness: 0.6 })
    );
    disc.position.y = 0.8;
    disc.userData = { id: n.id };
    g.add(disc);
    st.selectables.push(disc);
    st.nodePos.set(n.id, new THREE.Vector3(x, 2.5, z));
  } else if (n.type === 'gateway') {
    // golden gate: twin pylons + beam
    const mat = new THREE.MeshStandardMaterial({ color: col, emissive: 0xffc233, emissiveIntensity: 0.55, roughness: 0.5 });
    for (const dx of [-5, 5]) {
      const pylon = new THREE.Mesh(new THREE.BoxGeometry(2.4, 20, 2.4), mat);
      pylon.position.set(dx, 10, 0);
      pylon.userData = { id: n.id };
      g.add(pylon);
      st.selectables.push(pylon);
    }
    const beam = new THREE.Mesh(new THREE.BoxGeometry(13, 2, 2.6), mat);
    beam.position.y = 20;
    beam.userData = { id: n.id };
    g.add(beam);
    st.selectables.push(beam);
    st.nodePos.set(n.id, new THREE.Vector3(x, 21, z));
  } else if (n.type === 'database') {
    // data tower: cylinder silo with glowing cap
    const h = heightOf(n) * 0.8;
    const silo = new THREE.Mesh(
      new THREE.CylinderGeometry(4.2, 4.6, h, 20),
      new THREE.MeshStandardMaterial({ color: col, emissive: 0x35e0ff, emissiveIntensity: 0.2 + activityOf(n) * 0.8, roughness: 0.45, metalness: 0.35 })
    );
    silo.position.y = h / 2;
    silo.userData = { id: n.id };
    g.add(silo);
    st.selectables.push(silo);
    const cap = new THREE.Mesh(
      new THREE.CylinderGeometry(4.4, 4.4, 0.7, 20),
      new THREE.MeshBasicMaterial({ color: dbCap(n) })
    );
    cap.position.y = h + 0.35;
    g.add(cap);
    st.nodePos.set(n.id, new THREE.Vector3(x, h + 1, z));
  } else if (n.type === 'pod') {
    // townhouse block
    const h = 3.5 + (hashStr(n.id) % 3);
    const tex = facadeTexture(n);
    const house = new THREE.Mesh(
      new THREE.BoxGeometry(4.4, h, 4.4),
      new THREE.MeshStandardMaterial({ color: 0xffffff, map: tex, emissive: 0xffffff, emissiveMap: tex, emissiveIntensity: 0.9, roughness: 0.8 })
    );
    house.position.y = h / 2;
    house.userData = { id: n.id };
    g.add(house);
    st.selectables.push(house);
    st.nodePos.set(n.id, new THREE.Vector3(x, h, z));
  } else {
    // office tower with lit windows + rooftop beacon
    const h = heightOf(n);
    const w = 6 + (hashStr(n.id + 'w') % 3);
    const tex = facadeTexture(n);
    const tower = new THREE.Mesh(
      new THREE.BoxGeometry(w, h, w),
      new THREE.MeshStandardMaterial({ color: 0xffffff, map: tex, emissive: 0xffffff, emissiveMap: tex, emissiveIntensity: 1.0, roughness: 0.7 })
    );
    tower.position.y = h / 2;
    tower.userData = { id: n.id };
    g.add(tower);
    st.selectables.push(tower);
    // setback crown
    const crown = new THREE.Mesh(
      new THREE.BoxGeometry(w * 0.7, 1.6, w * 0.7),
      new THREE.MeshStandardMaterial({ color: col, emissive: 0x8fa3ff, emissiveIntensity: 0.5 })
    );
    crown.position.y = h + 0.8;
    g.add(crown);
    // beacon for hot services
    if (activityOf(n) > 0.55 || (n.metrics?.errRate ?? 0) > 0.05) {
      const beacon = new THREE.Mesh(
        new THREE.SphereGeometry(0.7, 8, 8),
        new THREE.MeshBasicMaterial({ color: (n.metrics?.errRate ?? 0) > 0.05 ? 0xff4444 : 0x9be9ff })
      );
      beacon.position.y = h + 2.4;
      g.add(beacon);
    }
    st.nodePos.set(n.id, new THREE.Vector3(x, h + 2, z));
  }

  // foundation pad
  const pad = new THREE.Mesh(
    new THREE.BoxGeometry(11, 0.35, 11),
    new THREE.MeshStandardMaterial({ color: 0x131f45, roughness: 0.9 })
  );
  pad.position.y = 0.17;
  g.add(pad);

  st.city.add(g);
}

function nodeBlock(st: CityState, n: Node, x: number, y: number, z: number, mode: CityMode) {
  void y; void mode;
  // worker-node infrastructure block: wide rail-yard slab
  const slab = new THREE.Mesh(
    new THREE.BoxGeometry(26, 2.2, 18),
    new THREE.MeshStandardMaterial({ color: 0x14204a, emissive: 0x2b4bd8, emissiveIntensity: 0.18, roughness: 0.85 })
  );
  slab.position.set(x, 1.1, z);
  slab.userData = { id: n.id };
  st.city.add(slab);
  st.selectables.push(slab);
  st.nodePos.set(n.id, new THREE.Vector3(x, 3, z));
  const el = document.createElement('div');
  el.textContent = n.name.toUpperCase();
  Object.assign(el.style, {
    position: 'absolute', transform: 'translate(-50%,-50%)', whiteSpace: 'nowrap',
    fontSize: '11px', fontWeight: '700', letterSpacing: '2px', color: '#8fa3ff',
    textShadow: '0 0 10px rgba(80,110,255,.8), 0 2px 6px #000',
  } as CSSStyleDeclaration);
  st.districts.push({ name: n.name, el, pos: new THREE.Vector3(x, 4.5, z) });
  st.labelLayer.appendChild(el);
  return slab;
}

function dbCap(n: Node): number {
  switch (n.role) {
    case 'redis': return 0xff6b6b;
    case 'kafka': return 0xa88bff;
    default: return 0x35c2ff;
  }
}

// ---- connectivity: dashed wiring layer + glowing observed-flow layer ----
//
// Layer 1 (wiring): targets/routes/depends edges — statically known from K8s
//   (EndpointSlices, ingress rules) or inferred. Always drawable, no traffic needed.
// Layer 2 (flows): observed network edges, rolled up pod→workload so one road
//   represents frontend ⇒ api instead of N pod-to-pod lines.
function buildTraffic(st: CityState, edges: Edge[]) {
  ensureExternalAnchors(st, edges);

  // ---- layer 1: wiring ----
  const wiring = edges.filter(
    (e) =>
      (e.type === 'targets' || e.type === 'routes' || e.type === 'depends') &&
      st.nodePos.has(e.source) &&
      st.nodePos.has(e.destination)
  );
  st.wiringList = wiring;
  if (wiring.length) {
    const pos = new Float32Array(wiring.length * 6);
    wiring.forEach((e, i) => {
      const a = st.nodePos.get(e.source)!;
      const b = st.nodePos.get(e.destination)!;
      // sag wiring slightly below the bright flow arcs so layers stay distinct
      pos.set([a.x, a.y - 1, a.z, b.x, b.y - 1, b.z], i * 6);
    });
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    const mat = new THREE.LineDashedMaterial({
      color: 0x5a70c0, dashSize: 2.2, gapSize: 1.8, transparent: true, opacity: 0.45,
    });
    const lines = new THREE.LineSegments(g, mat);
    lines.computeLineDistances();
    lines.userData = { kind: 'wiring', edges: wiring };
    st.city.add(lines);
    st.roads.push(lines);
  }

  // ---- layer 2: observed flows, rolled up to workload roads ----
  const rolled = rollupFlows(st, edges.filter((e) => e.type === 'network'));
  const net = rolled.filter((e) => st.nodePos.has(e.source) && st.nodePos.has(e.destination));
  st.edgeList = net;
  if (!net.length) return;
  const pos = new Float32Array(net.length * 6);
  const col = new Float32Array(net.length * 6);
  const c = new THREE.Color();
  net.forEach((e, i) => {
    const a = st.nodePos.get(e.source)!;
    const b = st.nodePos.get(e.destination)!;
    pos.set([a.x, a.y, a.z, b.x, b.y, b.z], i * 6);
    const err = (e.errorsPerSec ?? 0) / Math.max(1, e.requestsPerSec ?? 1);
    c.set(err > 0.05 ? 0xff4d4d : e.latencyMs && e.latencyMs > 500 ? 0xffb020 : 0x35e0ff);
    const boost = Math.min(1, ((e.requestsPerSec ?? 0) + (e.connections ?? 0) * 8) / 800) * 0.7 + 0.35;
    col.set([c.r * boost, c.g * boost, c.b * boost, c.r * boost, c.g * boost, c.b * boost], i * 6);
  });
  const g = new THREE.BufferAttribute(pos, 3);
  const gg = new THREE.BufferGeometry();
  gg.setAttribute('position', g);
  gg.setAttribute('color', new THREE.BufferAttribute(col, 3));
  const flowLines = new THREE.LineSegments(gg, new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, opacity: 0.9 }));
  flowLines.userData = { kind: 'observed', edges: net };
  st.city.add(flowLines);
  st.roads.push(flowLines);

  const P = Math.min(Math.max(net.length * 6, 60), 4000);
  const pp = new Float32Array(P * 3);
  st.packetT = Array.from({ length: P }, () => Math.random());
  st.packetEdge = Array.from({ length: P }, (_, k) => k % net.length);
  const pg = new THREE.BufferGeometry();
  pg.setAttribute('position', new THREE.BufferAttribute(pp, 3));
  st.packets = new THREE.Points(pg, new THREE.PointsMaterial({
    color: 0xaef3ff, size: 1.9, transparent: true, opacity: 0.95,
    blending: THREE.AdditiveBlending, depthWrite: false,
  }));
  st.city.add(st.packets);
}

// External endpoints (internet, kube-dns, cloud IPs) never exist as Nodes, so
// give each a "port pylon" on a ring outside the city — otherwise every flow
// touching the outside world is silently dropped from the render.
function ensureExternalAnchors(st: CityState, edges: Edge[]) {
  const ids = new Set<string>();
  for (const e of edges) {
    if (e.type !== 'network') continue;
    for (const end of [e.source, e.destination]) {
      if (end.startsWith('external/') && !st.nodePos.has(end)) ids.add(end);
    }
  }
  if (!ids.size) return;
  // city extent from placed anchors
  let maxR = 120;
  for (const p of st.nodePos.values()) {
    maxR = Math.max(maxR, Math.hypot(p.x, p.z));
  }
  const R = maxR + 55;
  const sorted = [...ids].sort();
  sorted.forEach((id, i) => {
    const a = (i / Math.max(1, sorted.length)) * Math.PI * 2 + 0.4;
    const x = Math.cos(a) * R;
    const z = Math.sin(a) * R;
    const pylon = new THREE.Mesh(
      new THREE.CylinderGeometry(1.6, 2.4, 12, 10),
      new THREE.MeshStandardMaterial({ color: 0x2a2350, emissive: 0xa88bff, emissiveIntensity: 0.7, roughness: 0.5 })
    );
    pylon.position.set(x, 6, z);
    st.city.add(pylon);
    const lamp = new THREE.Mesh(new THREE.SphereGeometry(1.1, 10, 10), new THREE.MeshBasicMaterial({ color: 0xc9b8ff }));
    lamp.position.set(x, 13, z);
    st.city.add(lamp);
    const pad = new THREE.Mesh(
      new THREE.CylinderGeometry(5, 6, 0.6, 16),
      new THREE.MeshStandardMaterial({ color: 0x131f45, roughness: 0.9 })
    );
    pad.position.set(x, 0.3, z);
    st.city.add(pad);
    st.nodePos.set(id, new THREE.Vector3(x, 13.5, z));
  });
}

// Roll pod-level flows up to workload roads. A pod rolls up to the service
// that targets it (EndpointSlice truth); else to the deployment/statefulset
// whose name prefixes it; else it stays itself.
function rollupFlows(st: CityState, flows: Edge[]): Edge[] {
  const byTarget = new Map<string, string>(); // podID -> serviceID
  for (const e of st.wiringList) {
    if (e.type === 'targets' && !byTarget.has(e.destination)) byTarget.set(e.destination, e.source);
  }
  const agg = new Map<string, Edge>();
  for (const e of flows) {
    const src = workloadFor(st, e.source, byTarget);
    const dst = workloadFor(st, e.destination, byTarget);
    if (src === dst) continue;
    const key = `${src}|${dst}|${e.dstPort ?? 0}|${e.protocol ?? ''}`;
    const cur = agg.get(key);
    if (!cur) {
      agg.set(key, { ...e, id: `rolled:${key}`, source: src, destination: dst });
    } else {
      cur.requestsPerSec = (cur.requestsPerSec ?? 0) + (e.requestsPerSec ?? 0);
      cur.bytesPerSec = (cur.bytesPerSec ?? 0) + (e.bytesPerSec ?? 0);
      cur.errorsPerSec = (cur.errorsPerSec ?? 0) + (e.errorsPerSec ?? 0);
      cur.connections = (cur.connections ?? 0) + (e.connections ?? 0);
      cur.latencyMs = Math.max(cur.latencyMs ?? 0, e.latencyMs ?? 0);
    }
  }
  return [...agg.values()];
}

function workloadFor(st: CityState, id: string, byTarget: Map<string, string>): string {
  if (id.startsWith('external/')) return id;
  const n = st.byId.get(id);
  if (!n) return id;
  if (n.type !== 'pod') return id;
  const svc = byTarget.get(id);
  if (svc) return svc;
  // name-prefix fallback: <workload>-<rs-hash>-<pod-hash>
  const cands = [...st.byId.values()].filter(
    (c) =>
      c.namespace === n.namespace &&
      (c.type === 'deployment' || c.type === 'statefulset' || c.type === 'daemonset') &&
      n.name.startsWith(c.name + '-')
  );
  if (cands.length) return cands[0].id;
  return id;
}

// ---- camera travel: smooth flight + keyboard glide ----
function flyToPoint(st: CityState, p: THREE.Vector3) {
  st.userDrove = true;
  const dir = st.camera.position.clone().sub(st.controls.target);
  if (dir.lengthSq() < 1e-4) dir.set(1, 0.9, 1);
  dir.setLength(Math.min(120, Math.max(40, dir.length() * 0.45)));
  st.flight = { target: p.clone(), pos: p.clone().add(dir) };
}

function stepFlight(st: CityState, dt: number) {
  if (!st.flight) return;
  const k = 1 - Math.exp(-4.5 * dt);
  st.controls.target.lerp(st.flight.target, k);
  st.camera.position.lerp(st.flight.pos, k);
  if (st.camera.position.distanceTo(st.flight.pos) < 0.6) st.flight = null;
}

function glideKeys(st: CityState, dt: number) {
  if (!st.keys.size) return;
  const fwd = st.controls.target.clone().sub(st.camera.position);
  fwd.y = 0;
  if (fwd.lengthSq() < 1e-4) fwd.set(0, 0, -1);
  fwd.normalize();
  const right = new THREE.Vector3(-fwd.z, 0, fwd.x);
  const mv = new THREE.Vector3();
  if (st.keys.has('w') || st.keys.has('arrowup')) mv.add(fwd);
  if (st.keys.has('s') || st.keys.has('arrowdown')) mv.sub(fwd);
  if (st.keys.has('d') || st.keys.has('arrowright')) mv.add(right);
  if (st.keys.has('a') || st.keys.has('arrowleft')) mv.sub(right);
  if (mv.lengthSq() < 1e-6) return;
  const speed = Math.max(35, st.camera.position.distanceTo(st.controls.target) * 0.9);
  mv.normalize().multiplyScalar(speed * dt);
  st.controls.target.add(mv);
  st.camera.position.add(mv);
  st.userDrove = true;
}

function animatePackets(st: CityState, dt: number) {  if (!st.packets || !st.edgeList.length) return;
  const attr = st.packets.geometry.getAttribute('position') as THREE.BufferAttribute;
  const arr = attr.array as Float32Array;
  for (let i = 0; i < st.packetT.length; i++) {
    const e = st.edgeList[st.packetEdge[i]];
    const vol = (e.requestsPerSec ?? 0) + (e.connections ?? 0) * 10;
    const speed = dt * (0.22 + Math.min(2.2, vol / 350));
    st.packetT[i] = (st.packetT[i] + speed) % 1;
    const a = st.nodePos.get(e.source)!;
    const b = st.nodePos.get(e.destination)!;
    const t = st.packetT[i];
    const lift = 5 + Math.min(18, vol / 90);
    arr[i * 3] = a.x + (b.x - a.x) * t;
    arr[i * 3 + 1] = a.y + (b.y - a.y) * t + Math.sin(t * Math.PI) * lift;
    arr[i * 3 + 2] = a.z + (b.z - a.z) * t;
  }
  attr.needsUpdate = true;
}

// ---- labels / highlight / picking ----
const projV = new THREE.Vector3();

function updateLabels(st: CityState) {
  const w = st.renderer.domElement.clientWidth;
  const h = st.renderer.domElement.clientHeight;
  const dist = st.camera.position.distanceTo(st.controls.target);
  for (const d of st.districts) {
    projV.copy(d.pos).project(st.camera);
    const behind = projV.z > 1;
    const tooFar = dist > 520;
    if (behind || tooFar) { d.el.style.display = 'none'; continue; }
    d.el.style.display = 'block';
    d.el.style.left = ((projV.x * 0.5 + 0.5) * w) + 'px';
    d.el.style.top = ((-projV.y * 0.5 + 0.5) * h) + 'px';
  }
  // selected chip follows the building top
  const sel = propsRefSel();
  if (sel && st.nodePos.has(sel)) {
    const n = st.byId.get(sel);
    projV.copy(st.nodePos.get(sel)!).project(st.camera);
    if (projV.z > 1) { st.chip.style.display = 'none'; }
    else {
      st.chip.style.display = 'block';
      st.chip.style.left = ((projV.x * 0.5 + 0.5) * w) + 'px';
      st.chip.style.top = ((-projV.y * 0.5 + 0.5) * h) + 'px';
      st.chip.innerHTML = `<b>${esc(n?.name ?? sel)}</b><span>${esc(metricLine(n))}</span>`;
    }
  } else st.chip.style.display = 'none';
}

// read via a module cell updated by the component effect (avoids re-init)
let currentSel: string | null = null;
function propsRefSel() { return currentSel; }
export function __setSel(s: string | null) { currentSel = s; }

function metricLine(n: Node | undefined): string {
  const m = n?.metrics;
  if (!m) return '';
  const parts: string[] = [];
  if (m.reqPerSec) parts.push(fmtK(m.reqPerSec) + ' req/s');  if (m.errRate) parts.push((m.errRate * 100).toFixed(1) + '% err');
  if (m.latencyMsP95) parts.push(fmtK(m.latencyMsP95) + 'ms p95');
  if (m.replicas) parts.push(`${m.readyReplicas}/${m.replicas} ready`);
  return parts.join(' · ');
}

function applyHighlight(st: CityState, selected: string | null, blast: { downstream: string[]; upstream: string[] } | null) {
  __setSel(selected);
  const hot = new Set<string>();
  if (selected) hot.add(selected);
  blast?.downstream.forEach((d) => hot.add(d));
  blast?.upstream.forEach((u) => hot.add(u));
  // dim/select via emissive pulse on tower materials would need material registry;
  // cheap + effective: scale-pop selected, others untouched (city stays readable).
  for (const o of st.selectables) {
    const id = (o.userData as { id?: string }).id;
    if (!id) continue;
    const target = selected === id ? 1.12 : 1;
    o.scale.setScalar(target);
    const mesh = o as THREE.Mesh;
    const mat = mesh.material as THREE.MeshStandardMaterial;
    if (mat && 'emissiveIntensity' in mat && hot.size > 0) {
      const base = hot.has(id) ? 1.6 : 0.55;
      mat.emissiveIntensity = selected === id ? 2.0 : base;
    }
  }
}

function pick(e: MouseEvent, ray: THREE.Raycaster, ptr: THREE.Vector2, st: CityState): string | null {
  const r = (e.target as HTMLElement).getBoundingClientRect();
  ptr.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(ptr, st.camera);
  const hits = ray.intersectObjects(st.selectables, false);
  if (!hits.length) return null;
  return (hits[0].object.userData as { id: string }).id ?? null;
}

// Road hover: raycast the road LineSegments (generous threshold) and resolve
// which logical edge the segment belongs to.
function pickRoad(e: MouseEvent, ray: THREE.Raycaster, ptr: THREE.Vector2, st: CityState): { kind: string; edge: Edge } | null {
  if (!st.roads.length) return null;
  const r = (e.target as HTMLElement).getBoundingClientRect();
  ptr.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(ptr, st.camera);
  ray.params.Line = { threshold: 3 };
  const hits = ray.intersectObjects(st.roads, false);
  if (!hits.length) return null;
  const obj = hits[0].object as THREE.LineSegments;
  const ud = obj.userData as { kind: string; edges: Edge[] };
  const seg = Math.floor((hits[0].index ?? 0) / 2);
  const edge = ud.edges[Math.min(seg, ud.edges.length - 1)];
  return edge ? { kind: ud.kind, edge } : null;
}

function roadTooltip(st: CityState, road: { kind: string; edge: Edge }): string {
  const e = road.edge;
  const a = shortId(e.source);
  const b = shortId(e.destination);
  if (road.kind === 'wiring') {
    const label = e.type === 'targets' ? 'service wiring' : e.type === 'routes' ? 'ingress route' : 'inferred dependency';
    return `<b>${esc(a)} ┄ ${esc(b)}</b><br><span>${label} · declared, no live traffic</span>`;
  }
  const parts: string[] = [];
  if (e.requestsPerSec) parts.push(fmtK(e.requestsPerSec) + ' req/s');
  if (e.connections) parts.push(e.connections + ' conns');
  if (e.latencyMs) parts.push(fmtK(e.latencyMs) + 'ms');
  if (e.protocol) parts.push(e.protocol + (e.dstPort ? ':' + e.dstPort : ''));
  void st;
  return `<b>${esc(a)} → ${esc(b)}</b><br><span>observed · ${esc(parts.join(' · ') || 'active')}</span>`;
}

// ---- scenery + utils ----
function stars(): THREE.Points {
  const N = 600;
  const p = new Float32Array(N * 3);
  for (let i = 0; i < N; i++) {
    const r = 900 + Math.random() * 600;
    const th = Math.random() * Math.PI * 2;
    const ph = Math.random() * Math.PI * 0.45;
    p[i * 3] = r * Math.cos(th) * Math.cos(ph);
    p[i * 3 + 1] = r * Math.sin(ph) + 40;
    p[i * 3 + 2] = r * Math.sin(th) * Math.cos(ph);
  }
  const g = new THREE.BufferGeometry();
  g.setAttribute('position', new THREE.BufferAttribute(p, 3));
  return new THREE.Points(g, new THREE.PointsMaterial({ color: 0x8fa3ff, size: 1.6, sizeAttenuation: false, transparent: true, opacity: 0.7 }));
}

function disposeMat(m: THREE.Material) {
  const mm = m as THREE.MeshStandardMaterial;
  if (mm.map) mm.map.dispose();
  if (mm.emissiveMap && mm.emissiveMap !== mm.map) mm.emissiveMap.dispose();
  m.dispose();
}

function hashStr(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}

function mulberry(seed: number) {
  let a = seed;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function styleTooltip(el: HTMLDivElement) {
  Object.assign(el.style, {
    position: 'absolute', display: 'none', pointerEvents: 'none', zIndex: '5',
    background: 'rgba(8,13,30,.94)', border: '1px solid #2b4bd8', borderRadius: '8px',
    padding: '6px 10px', fontSize: '12px', color: '#dbe4ff', maxWidth: '260px',
  } as CSSStyleDeclaration);
}

function styleChip(el: HTMLDivElement) {
  Object.assign(el.style, {
    position: 'absolute', display: 'none', pointerEvents: 'none', zIndex: '4',
    transform: 'translate(-50%,-130%)', background: 'rgba(8,13,30,.92)',
    border: '1px solid #ffc233', borderRadius: '8px', padding: '5px 10px',
    fontSize: '12px', color: '#fff', whiteSpace: 'nowrap',
  } as CSSStyleDeclaration);
}
