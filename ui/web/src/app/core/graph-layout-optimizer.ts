import { Side } from './graph-schema';

/**
 * Pure geometry helpers and a crossing-aware post-optimisation pass for the
 * agent graph.
 *
 * The force (Fruchterman-Reingold) and stress (SMACOF) layouts only minimise
 * edge length and node overlap. Edge-edge crossings, edges cutting through
 * unrelated nodes and crowded labels are not part of their objective, so they
 * show up as visual noise. This module takes a laid-out graph and improves it
 * with a deterministic coordinate-descent search:
 *
 *   cost = crossings·1000 + nodeHits·700 + labelOverlaps·300
 *        + nodeOverlaps·1e9 + totalEdgeLength·0.02
 *
 * The search only moves nodes on a shrinking ring of candidate offsets and
 * keeps a move when the total cost drops, so the result stays close to the
 * force/stress arrangement while removing redundant crossings.
 *
 * Positions are top-left corners of the node rectangles, matching what
 * `fGroupPosition` renders in the flow canvas.
 */

export interface LayoutNode {
  id: string;
  /** top-left x */
  x: number;
  /** top-left y */
  y: number;
  w: number;
  h: number;
  kind: string;
}

export interface LayoutEdge {
  id: string;
  source: string;
  target: string;
  /** Sides that actually expose an output port on the source node. */
  sourceSides: Side[];
  /** Sides that actually expose an input port on the target node. */
  targetSides: Side[];
  label?: string;
}

export interface LayoutMetrics {
  crossings: number;
  nodeHits: number;
  labelOverlaps: number;
  nodeOverlaps: number;
  length: number;
}

export interface OptimizeResult {
  positions: Map<string, { x: number; y: number }>;
  before: LayoutMetrics;
  after: LayoutMetrics;
}

const SIDE_ORDER: Side[] = ['right', 'left', 'bottom', 'top'];

const W_CROSSING = 1000;
const W_NODE_HIT = 700;
const W_LABEL = 300;
const W_NODE_OVERLAP = 1e9;
const W_LENGTH = 0.02;

/** Label box approximation used for the label-overlap term. */
const LABEL_H = 18;
const LABEL_CHAR_W = 7;
const LABEL_MIN_W = 42;

/** Margin around nodes that an edge should not cut through. */
const NODE_HIT_MARGIN = 8;

interface Point {
  x: number;
  y: number;
}

function center(n: LayoutNode): Point {
  return { x: n.x + n.w / 2, y: n.y + n.h / 2 };
}

/**
 * Pick the node side that faces the other node. Prefers the dominant axis so
 * the connecting edge leaves the box on the side pointing at its partner.
 */
export function facingSide(from: LayoutNode, to: LayoutNode): Side {
  const a = center(from);
  const b = center(to);
  const dx = b.x - a.x;
  const dy = b.y - a.y;
  if (Math.abs(dx) >= Math.abs(dy)) return dx >= 0 ? 'right' : 'left';
  return dy >= 0 ? 'bottom' : 'top';
}

function pickAvailable(desired: Side, available: Side[]): Side {
  if (available.includes(desired)) return desired;
  if (!available.length) return desired;
  for (const s of SIDE_ORDER) if (available.includes(s)) return s;
  return available[0];
}

/**
 * Choose source and target port sides for an edge. The facing side is used
 * when the node exposes a port there; otherwise the first available side wins.
 * Exported so the renderer assigns the same sides the optimizer assumes.
 */
export function chooseSides(
  from: LayoutNode,
  to: LayoutNode,
  sourceSides: Side[],
  targetSides: Side[],
): { sourceSide: Side; targetSide: Side } {
  return {
    sourceSide: pickAvailable(facingSide(from, to), sourceSides),
    targetSide: pickAvailable(facingSide(to, from), targetSides),
  };
}

/**
 * Point where the ray from `from`'s centre towards `to`'s centre leaves the
 * rectangle of `from`. Used both by the optimizer and the renderer so the
 * drawn line and the measured line agree. Returns the touched side and the
 * absolute point; callers convert it into a left/top percentage.
 */
export function borderPoint(from: LayoutNode, to: LayoutNode): { side: Side; point: Point } {
  const c = center(from);
  const o = center(to);
  let dx = o.x - c.x;
  let dy = o.y - c.y;
  if (dx === 0 && dy === 0) dx = 1;
  const tx = dx !== 0 ? (from.w / 2) / Math.abs(dx) : Infinity;
  const ty = dy !== 0 ? (from.h / 2) / Math.abs(dy) : Infinity;
  const t = Math.min(tx, ty);
  const side: Side = tx < ty
    ? (dx >= 0 ? 'right' : 'left')
    : (dy >= 0 ? 'bottom' : 'top');
  return { side, point: { x: c.x + dx * t, y: c.y + dy * t } };
}

function anchor(n: LayoutNode, side: Side): Point {
  switch (side) {
    case 'left': return { x: n.x, y: n.y + n.h / 2 };
    case 'right': return { x: n.x + n.w, y: n.y + n.h / 2 };
    case 'top': return { x: n.x + n.w / 2, y: n.y };
    default: return { x: n.x + n.w / 2, y: n.y + n.h };
  }
}

function ccw(ax: number, ay: number, bx: number, by: number, cx: number, cy: number): boolean {
  return (cy - ay) * (bx - ax) > (by - ay) * (cx - ax);
}

function segmentsCross(a: Point, b: Point, c: Point, d: Point): boolean {
  return ccw(a.x, a.y, c.x, c.y, d.x, d.y) !== ccw(b.x, b.y, c.x, c.y, d.x, d.y)
    && ccw(a.x, a.y, b.x, b.y, c.x, c.y) !== ccw(a.x, a.y, b.x, b.y, d.x, d.y);
}

/** Liang-Barsky style test: does segment p-q intersect the (expanded) rect. */
function segmentIntersectsRect(p: Point, q: Point, rx: number, ry: number, rw: number, rh: number, margin: number): boolean {
  const minX = rx - margin, minY = ry - margin, maxX = rx + rw + margin, maxY = ry + rh + margin;
  let t0 = 0, t1 = 1;
  const dx = q.x - p.x, dy = q.y - p.y;
  const clip = (p0: number, p1: number): boolean => {
    if (p0 === 0) return p1 >= 0;
    const r = p1 / p0;
    if (p0 < 0) { if (r > t1) return false; if (r > t0) t0 = r; }
    else { if (r < t0) return false; if (r < t1) t1 = r; }
    return true;
  };
  if (!clip(-dx, p.x - minX)) return false;
  if (!clip(dx, maxX - p.x)) return false;
  if (!clip(-dy, p.y - minY)) return false;
  if (!clip(dy, maxY - p.y)) return false;
  return t0 <= t1 && t1 >= 0 && t0 <= 1;
}

interface PreparedEdge {
  a: number;
  b: number;
  labelW: number;
  sourceSides: Side[];
  targetSides: Side[];
}

interface Prepared {
  nodes: LayoutNode[];
  nodesById: Map<string, number>;
  edges: PreparedEdge[];
  x: Float64Array;
  y: Float64Array;
  /** Attach each edge wherever the neighbour direction hits the border. */
  perimeter: boolean;
}

function prepare(nodes: LayoutNode[], edges: LayoutEdge[], perimeter = false): Prepared {
  const nodesById = new Map<string, number>();
  nodes.forEach((n, i) => nodesById.set(n.id, i));
  const prepared: PreparedEdge[] = [];
  for (const e of edges) {
    const a = nodesById.get(e.source);
    const b = nodesById.get(e.target);
    if (a == null || b == null || a === b) continue;
    prepared.push({
      a, b,
      labelW: Math.max(LABEL_MIN_W, (e.label?.length ?? 0) * LABEL_CHAR_W),
      sourceSides: e.sourceSides.length ? e.sourceSides : ['right'],
      targetSides: e.targetSides.length ? e.targetSides : ['left'],
    });
  }
  return {
    nodes,
    nodesById,
    edges: prepared,
    x: Float64Array.from(nodes.map(n => n.x)),
    y: Float64Array.from(nodes.map(n => n.y)),
    perimeter,
  };
}

function nodeAt(p: Prepared, i: number): LayoutNode {
  return { ...p.nodes[i], x: p.x[i], y: p.y[i] };
}

interface Geometry {
  sx: Float64Array;
  sy: Float64Array;
  tx: Float64Array;
  ty: Float64Array;
}

function buildGeometry(p: Prepared): Geometry {
  const m = p.edges.length;
  const geo: Geometry = {
    sx: new Float64Array(m), sy: new Float64Array(m),
    tx: new Float64Array(m), ty: new Float64Array(m),
  };
  for (let i = 0; i < m; i++) {
    const e = p.edges[i];
    const a = nodeAt(p, e.a);
    const b = nodeAt(p, e.b);
    const s = p.perimeter ? borderPoint(a, b).point : anchor(a, chooseSides(a, b, e.sourceSides, e.targetSides).sourceSide);
    const t = p.perimeter ? borderPoint(b, a).point : anchor(b, chooseSides(a, b, e.sourceSides, e.targetSides).targetSide);
    geo.sx[i] = s.x; geo.sy[i] = s.y;
    geo.tx[i] = t.x; geo.ty[i] = t.y;
  }
  return geo;
}

export function measure(p: Prepared, geo: Geometry): LayoutMetrics {
  const m = p.edges.length;
  const n = p.nodes.length;
  let crossings = 0;
  let nodeHits = 0;
  let labelOverlaps = 0;
  let nodeOverlaps = 0;
  let length = 0;

  const pA: Point = { x: 0, y: 0 };
  const pB: Point = { x: 0, y: 0 };
  const pC: Point = { x: 0, y: 0 };
  const pD: Point = { x: 0, y: 0 };

  // edge length + edge-edge crossings
  for (let i = 0; i < m; i++) {
    length += Math.hypot(geo.tx[i] - geo.sx[i], geo.ty[i] - geo.sy[i]);
    pA.x = geo.sx[i]; pA.y = geo.sy[i];
    pB.x = geo.tx[i]; pB.y = geo.ty[i];
    for (let j = i + 1; j < m; j++) {
      const ei = p.edges[i], ej = p.edges[j];
      if (ei.a === ej.a || ei.a === ej.b || ei.b === ej.a || ei.b === ej.b) continue;
      pC.x = geo.sx[j]; pC.y = geo.sy[j];
      pD.x = geo.tx[j]; pD.y = geo.ty[j];
      if (segmentsCross(pA, pB, pC, pD)) crossings++;
    }
  }

  // edges cutting through unrelated nodes
  for (let i = 0; i < m; i++) {
    const e = p.edges[i];
    pA.x = geo.sx[i]; pA.y = geo.sy[i];
    pB.x = geo.tx[i]; pB.y = geo.ty[i];
    for (let k = 0; k < n; k++) {
      if (k === e.a || k === e.b) continue;
      const nd = p.nodes[k];
      if (segmentIntersectsRect(pA, pB, p.x[k], p.y[k], nd.w, nd.h, NODE_HIT_MARGIN)) nodeHits++;
    }
  }

  // label boxes: midpoint of the edge, assumed axis-aligned
  for (let i = 0; i < m; i++) {
    const e = p.edges[i];
    const mx = (geo.sx[i] + geo.tx[i]) / 2;
    const my = (geo.sy[i] + geo.ty[i]) / 2;
    // label vs node boxes
    for (let k = 0; k < n; k++) {
      const nd = p.nodes[k];
      if (k === e.a || k === e.b) continue;
      if (mx + e.labelW / 2 > p.x[k] && mx - e.labelW / 2 < p.x[k] + nd.w
        && my + LABEL_H / 2 > p.y[k] && my - LABEL_H / 2 < p.y[k] + nd.h) {
        labelOverlaps++;
      }
    }
    // label vs label
    for (let j = i + 1; j < m; j++) {
      const ej = p.edges[j];
      const nx = (geo.sx[j] + geo.tx[j]) / 2;
      const ny = (geo.sy[j] + geo.ty[j]) / 2;
      const overlapX = Math.abs(mx - nx) < (e.labelW + ej.labelW) / 2 + 6;
      const overlapY = Math.abs(my - ny) < LABEL_H + 6;
      if (overlapX && overlapY) labelOverlaps++;
    }
  }

  // node rectangles must never overlap
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const a = p.nodes[i], b = p.nodes[j];
      if (p.x[i] < p.x[j] + b.w && p.x[i] + a.w > p.x[j]
        && p.y[i] < p.y[j] + b.h && p.y[i] + a.h > p.y[j]) {
        nodeOverlaps++;
      }
    }
  }

  return { crossings, nodeHits, labelOverlaps, nodeOverlaps, length };
}

function score(m: LayoutMetrics): number {
  return m.crossings * W_CROSSING
    + m.nodeHits * W_NODE_HIT
    + m.labelOverlaps * W_LABEL
    + m.nodeOverlaps * W_NODE_OVERLAP
    + m.length * W_LENGTH;
}

/** Separate overlapping node rectangles with a comfortable gap. */
function separate(p: Prepared, gapX: number, gapY: number): void {
  const n = p.nodes.length;
  for (let pass = 0; pass < 16; pass++) {
    let moved = false;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const a = p.nodes[i], b = p.nodes[j];
        const minDX = (a.w + b.w) / 2 + gapX;
        const minDY = (a.h + b.h) / 2 + gapY;
        const dx = (p.x[i] + a.w / 2) - (p.x[j] + b.w / 2);
        const dy = (p.y[i] + a.h / 2) - (p.y[j] + b.h / 2);
        if (Math.abs(dx) < minDX && Math.abs(dy) < minDY) {
          moved = true;
          if (Math.abs(dx) * minDY > Math.abs(dy) * minDX) {
            const push = Math.sign(dx || 1) * (minDX - Math.abs(dx));
            p.x[i] += push / 2; p.x[j] -= push / 2;
          } else {
            const push = Math.sign(dy || 1) * (minDY - Math.abs(dy));
            p.y[i] += push / 2; p.y[j] -= push / 2;
          }
        }
      }
    }
    if (!moved) break;
  }
}

const DIRECTIONS: Point[] = [];
for (let k = 0; k < 16; k++) {
  const a = (k / 16) * 2 * Math.PI;
  DIRECTIONS.push({ x: Math.cos(a), y: Math.sin(a) });
}

/**
 * Improve a laid-out graph. Mutates nothing; returns new positions plus the
 * metrics before and after so callers can log or test the improvement.
 */
export function optimizeLayout(
  nodes: LayoutNode[],
  edges: LayoutEdge[],
  options: { seed?: number; perimeter?: boolean } = {},
): OptimizeResult {
  const p = prepare(nodes, edges, options.perimeter === true);
  const n = p.nodes.length;
  const before = measure(p, buildGeometry(p));

  // Keep the pass cheap for very large graphs; still run a lighter version.
  const heavy = n <= 60 && p.edges.length <= 140;

  // Hard drift guard: never move a node beyond the original bounding box plus
  // a margin. Crossing removal must not fling a low-degree node into empty
  // space, because fit-to-screen would then zoom the whole graph tiny.
  const minX = Math.min(...nodes.map(nd => nd.x));
  const minY = Math.min(...nodes.map(nd => nd.y));
  const spanW = Math.max(...nodes.map(nd => nd.x + nd.w)) - minX;
  const spanH = Math.max(...nodes.map(nd => nd.y + nd.h)) - minY;
  const margin = Math.max(260, 0.35 * Math.max(spanW, spanH));
  const bounds = { minX: minX - margin, maxX: minX + spanW + margin, minY: minY - margin, maxY: minY + spanH + margin };
  const inBounds = (i: number, x: number, y: number): boolean =>
    x >= bounds.minX && x + p.nodes[i].w <= bounds.maxX
    && y >= bounds.minY && y + p.nodes[i].h <= bounds.maxY;

  if (n >= 3 && p.edges.length >= 2) {
    let current = score(before);
    const radii = heavy ? [200, 140, 95, 60, 35, 20] : [160, 100, 55];
    const sweeps = heavy ? 3 : 2;

    for (const radius of radii) {
      for (let sweep = 0; sweep < sweeps; sweep++) {
        let improved = false;
        for (let i = 0; i < n; i++) {
          const cx = p.x[i], cy = p.y[i];
          let bestScore = current;
          let bestX = cx, bestY = cy;
          for (const d of DIRECTIONS) {
            p.x[i] = cx + d.x * radius;
            p.y[i] = cy + d.y * radius;
            if (!inBounds(i, p.x[i], p.y[i])) continue;
            // A move must not introduce an organisation-breaking overlap; the
            // score already penalises it heavily, but checking early is cheap.
            const s = score(measure(p, buildGeometry(p)));
            if (s < bestScore) { bestScore = s; bestX = p.x[i]; bestY = p.y[i]; }
          }
          p.x[i] = bestX; p.y[i] = bestY;
          if (bestScore < current) { current = bestScore; improved = true; }
        }
        if (!improved) break;
      }
    }

    // Swap positions of same-kind nodes. On a bipartite agent↔resource graph
    // this reorders the resource side without changing any node's shape and is
    // often the cheapest way to remove a whole group of crossings.
    const byKind = new Map<string, number[]>();
    for (let i = 0; i < n; i++) {
      const kind = p.nodes[i].kind;
      const list = byKind.get(kind);
      if (list) list.push(i); else byKind.set(kind, [i]);
    }
    for (let round = 0; heavy && round < 4; round++) {
      let improved = false;
      for (const list of byKind.values()) {
        if (list.length < 2) continue;
        for (let a = 0; a < list.length; a++) {
          for (let b = a + 1; b < list.length; b++) {
            const i = list[a], j = list[b];
            const ix = p.x[i], iy = p.y[i];
            const jx0 = p.x[j], jy0 = p.y[j];
            if (!inBounds(i, jx0, jy0) || !inBounds(j, ix, iy)) continue;
            p.x[i] = jx0; p.y[i] = jy0;
            p.x[j] = ix; p.y[j] = iy;
            const s = score(measure(p, buildGeometry(p)));
            if (s < current) { current = s; improved = true; }
            else {
              const jx = p.x[j], jy = p.y[j];
              p.x[j] = p.x[i]; p.y[j] = p.y[i];
              p.x[i] = jx; p.y[i] = jy;
            }
          }
        }
      }
      if (!improved) break;
    }

    // Final hard separation so no node pair can overlap after the search.
    separate(p, 56, 48);
  }

  const after = measure(p, buildGeometry(p));
  const positions = new Map<string, { x: number; y: number }>();
  nodes.forEach((nd, i) => positions.set(nd.id, { x: Math.round(p.x[i]), y: Math.round(p.y[i]) }));
  return { positions, before, after };
}

/** Count edge-edge crossings for an arbitrary already-positioned graph. */
export function countCrossings(nodes: LayoutNode[], edges: LayoutEdge[], perimeter = false): number {
  const p = prepare(nodes, edges, perimeter);
  return measure(p, buildGeometry(p)).crossings;
}
