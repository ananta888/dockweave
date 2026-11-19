/**
 * Obstacle-avoiding edge routing for the agent graph.
 *
 * The layout optimizer keeps nodes apart and penalises edges that cross node
 * bodies, but a straight line between two border points can still cut through
 * an unrelated node. For those edges we build a visibility graph around the
 * inflated node rectangles and return the shortest polyline that clears every
 * node. The returned points are intermediate waypoints for foblex; an empty
 * array means the direct line is already clear.
 */

export interface RouterPoint {
  x: number;
  y: number;
}

export interface RouterRect {
  /** top-left */
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface RouteOptions {
  /** Extra clearance around each obstacle in flow units. */
  margin?: number;
}

const EPS = 0.5;

/** Does the open segment p→q enter the strict interior of the rectangle? */
function segmentHitsRect(p: RouterPoint, q: RouterPoint, r: RouterRect): boolean {
  const minX = r.x + EPS, minY = r.y + EPS;
  const maxX = r.x + r.w - EPS, maxY = r.y + r.h - EPS;
  if (maxX <= minX || maxY <= minY) return false;
  const dx = q.x - p.x;
  const dy = q.y - p.y;
  let t0 = 0, t1 = 1;
  const clip = (p0: number, p1: number): boolean => {
    if (Math.abs(p0) < 1e-12) return p1 >= 0;
    const t = p1 / p0;
    if (p0 < 0) { if (t > t1) return false; if (t > t0) t0 = t; }
    else { if (t < t0) return false; if (t < t1) t1 = t; }
    return true;
  };
  if (!clip(-dx, p.x - minX)) return false;
  if (!clip(dx, maxX - p.x)) return false;
  if (!clip(-dy, p.y - minY)) return false;
  if (!clip(dy, maxY - p.y)) return false;
  return t0 < t1;
}

function insideRect(p: RouterPoint, r: RouterRect): boolean {
  return p.x > r.x + EPS && p.x < r.x + r.w - EPS
    && p.y > r.y + EPS && p.y < r.y + r.h - EPS;
}

function simplify(path: RouterPoint[]): RouterPoint[] {
  if (path.length <= 2) return path;
  const out: RouterPoint[] = [path[0]];
  for (let i = 1; i < path.length - 1; i++) {
    const a = out[out.length - 1];
    const b = path[i];
    const c = path[i + 1];
    const cross = (b.x - a.x) * (c.y - b.y) - (b.y - a.y) * (c.x - b.x);
    if (Math.abs(cross) > 1e-6) out.push(b);
  }
  out.push(path[path.length - 1]);
  return out;
}

/**
 * Return the intermediate waypoints needed to connect `start` and `end`
 * without entering any obstacle. The obstacles should be the node rectangles;
 * the caller must exclude the two endpoint nodes.
 */
export function routeAroundNodes(
  start: RouterPoint,
  end: RouterPoint,
  obstacles: RouterRect[],
  options: RouteOptions = {},
): RouterPoint[] {
  const margin = options.margin ?? 18;
  const expanded: RouterRect[] = obstacles.map(o => ({
    x: o.x - margin, y: o.y - margin, w: o.w + 2 * margin, h: o.h + 2 * margin,
  }));
  if (!expanded.length) return [];
  if (!expanded.some(r => segmentHitsRect(start, end, r))) return [];
  // If an endpoint is already inside another node's clearance zone there is no
  // clean escape path; keep the direct line rather than drawing a detour that
  // also cuts through something.
  if (expanded.some(r => insideRect(start, r) || insideRect(end, r))) return [];

  const pts: RouterPoint[] = [start, end];
  for (const r of expanded) {
    pts.push(
      { x: r.x, y: r.y },
      { x: r.x + r.w, y: r.y },
      { x: r.x, y: r.y + r.h },
      { x: r.x + r.w, y: r.y + r.h },
    );
  }
  const n = pts.length;
  const visible = (a: number, b: number): boolean => {
    for (const r of expanded) if (segmentHitsRect(pts[a], pts[b], r)) return false;
    return true;
  };

  // Dijkstra over the visibility graph (linear scan; the vertex count is
  // small: 4 corners per node plus the two endpoints).
  const dist = new Array<number>(n).fill(Infinity);
  const prev = new Array<number>(n).fill(-1);
  const done = new Array<boolean>(n).fill(false);
  dist[0] = 0;
  for (let it = 0; it < n; it++) {
    let u = -1;
    let best = Infinity;
    for (let i = 0; i < n; i++) {
      if (!done[i] && dist[i] < best) { best = dist[i]; u = i; }
    }
    if (u < 0) break;
    if (u === 1) break;
    done[u] = true;
    for (let v = 0; v < n; v++) {
      if (done[v] || v === u) continue;
      const dx = pts[u].x - pts[v].x;
      const dy = pts[u].y - pts[v].y;
      const nd = dist[u] + Math.hypot(dx, dy);
      if (nd >= dist[v]) continue;
      if (!visible(u, v)) continue;
      dist[v] = nd;
      prev[v] = u;
    }
  }
  if (!Number.isFinite(dist[1])) return [];

  const path: RouterPoint[] = [];
  for (let at = 1; at !== -1; at = prev[at]) path.push(pts[at]);
  path.reverse();
  return simplify(path).slice(1, -1);
}
