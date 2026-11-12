/**
 * V12-09 (docs/design/15-v12-ui-operations.md, "Quy tắc hiển thị"): the pure layout of a workflow graph for the run view.
 * No React, no DOM: nodes and possible edges in, positions, edge paths, edge kinds, node shapes and the set of exit nodes out.
 *
 * - The MAIN PATH (the longest chain from START to END over non-exit outcomes, back edges removed) is laid out vertically,
 *   one row per node; its edges are straight.
 * - BACK edges (to a node of a smaller row; found by a DFS from START in declared outcome order; COMPLETION_REWORK is always
 *   one) run in their own lane on the LEFT, one lane per edge, the longer span outside — an outer lane enters its target higher,
 *   so a nested pair never crosses. A short back edge (one row) and a SHORTCUT (a forward edge that skips main nodes) run on
 *   the RIGHT.
 * - SIDE nodes (on no main path, e.g. `debug`, `simplify`) sit to the right, on the row of the main node their loop returns to.
 * - EXIT nodes are reachable only through exit outcomes (EXIT_OUTCOMES, a documented constant — never inferred from node names)
 *   or from other exit nodes. The main view hides them and counts the hidden edges on their source (`+n`); "All paths" draws them
 *   in a column on the right with dashed edges, every edge into one exit node sharing one lane.
 * - Anything else (no START, END unreachable, a cycle-only graph) falls back to a rank layout. The function never throws.
 */

export type NodeShape = 'rounded' | 'diamond' | 'double-diamond' | 'rect' | 'pill' | 'bar';
export type EdgeRole = 'main' | 'back' | 'shortcut' | 'exit' | 'side';

export interface LayoutInputNode {
  key: string;
  type: string;
  outcomes?: string[];
}

export interface LayoutInputEdge {
  key: string;
  from: string;
  to: string;
  outcome: string;
  kind?: string;
}

export interface Point { x: number; y: number }

export interface LayoutNode {
  key: string;
  type: string;
  shape: NodeShape;
  x: number; // centre
  y: number; // centre
  width: number;
  height: number;
  row: number;
  column: number; // 0 = main path, 1.. = side columns, exit column last
  placement: 'main' | 'side' | 'exit' | 'rank';
  /** Edges into exit nodes hidden in the main view (the `+n` badge). */
  hiddenExitEdges: number;
}

export interface LayoutEdge {
  key: string;
  from: string;
  to: string;
  outcome: string;
  role: EdgeRole;
  points: Point[];
  /** Index of the lane for back / shortcut / exit edges (0 = closest to the main path). */
  lane?: number;
  side?: 'left' | 'right';
  dashed: boolean;
}

export interface RunGraphLayout {
  nodes: Record<string, LayoutNode>;
  edges: LayoutEdge[];
  exitNodes: Set<string>;
  mainPath: string[];
  width: number;
  height: number;
  /** True when no main path could be found and the rank layout was used. */
  fallback: boolean;
}

export interface LayoutOptions {
  /** "All paths": draw exit nodes and their edges. Default false (main path only). */
  showExits?: boolean;
}

/** Outcomes that leave the normal flow (V12-09 step 2). A node reached only through these is an exit node. */
export const EXIT_OUTCOMES: readonly string[] = ['needs_info', 'escalated', 'rejected', 'abandon'];

const ROW_HEIGHT = 96;
const COLUMN_WIDTH = 210;
const LANE_GAP = 14;
const MARGIN = 24;

const SIZES: Record<NodeShape, { width: number; height: number }> = {
  rounded: { width: 156, height: 44 },
  rect: { width: 156, height: 40 },
  diamond: { width: 132, height: 60 },
  'double-diamond': { width: 140, height: 64 },
  pill: { width: 96, height: 32 },
  bar: { width: 156, height: 10 },
};

/** The shape of a node, from data the definition already has (no extra field). */
export function nodeShape(node: LayoutInputNode): NodeShape {
  const outcomes = node.outcomes?.length ?? 0;
  switch (node.type) {
    case 'START': case 'END': return 'pill';
    case 'FORK': case 'JOIN': return 'bar';
    case 'APPROVAL': return 'double-diamond';
    case 'COMMAND': return outcomes >= 2 ? 'diamond' : 'rect';
    case 'MACHINE_GATE': case 'ROUTER': return outcomes >= 2 ? 'diamond' : 'rect';
    default: return 'rounded';
  }
}

function sized(node: LayoutInputNode) {
  const shape = nodeShape(node);
  return { shape, ...SIZES[shape] };
}

export function layoutRunGraph(nodes: LayoutInputNode[], edges: LayoutInputEdge[], options: LayoutOptions = {}): RunGraphLayout {
  try {
    return layoutMain(nodes, edges, options) ?? layoutRanked(nodes, edges, options);
  } catch {
    return layoutRanked(nodes, edges, options);
  }
}

// ─── classification ───────────────────────────────────────────────────────────

/** Back edges by a DFS from START, visiting outgoing edges in the node's declared outcome order. */
export function findBackEdges(nodes: LayoutInputNode[], edges: LayoutInputEdge[]): Set<string> {
  const back = new Set<string>();
  for (const e of edges) if (e.kind === 'COMPLETION_REWORK') back.add(e.key);
  const byKey = new Map(nodes.map(n => [n.key, n]));
  const out = new Map<string, LayoutInputEdge[]>();
  for (const e of edges) {
    if (back.has(e.key)) continue;
    const list = out.get(e.from) ?? [];
    list.push(e);
    out.set(e.from, list);
  }
  for (const [from, list] of out) {
    const order = byKey.get(from)?.outcomes ?? [];
    list.sort((a, b) => rank(order, a.outcome) - rank(order, b.outcome));
  }
  const state = new Map<string, 'open' | 'done'>();
  const visit = (key: string) => {
    state.set(key, 'open');
    for (const e of out.get(key) ?? []) {
      const s = state.get(e.to);
      if (s === 'open') back.add(e.key);
      else if (s === undefined) visit(e.to);
    }
    state.set(key, 'done');
  };
  const starts = nodes.filter(n => n.type === 'START').map(n => n.key);
  for (const s of starts.length > 0 ? starts : nodes.slice(0, 1).map(n => n.key)) if (!state.has(s)) visit(s);
  for (const n of nodes) if (!state.has(n.key)) visit(n.key);
  return back;
}

function rank(order: string[], outcome: string): number {
  const i = order.indexOf(outcome);
  return i < 0 ? order.length : i;
}

/** Exit nodes: every incoming edge has an exit outcome or comes from another exit node (a fixpoint). START/END never are. */
export function findExitNodes(nodes: LayoutInputNode[], edges: LayoutInputEdge[]): Set<string> {
  const incoming = new Map<string, LayoutInputEdge[]>();
  for (const e of edges) {
    const list = incoming.get(e.to) ?? [];
    list.push(e);
    incoming.set(e.to, list);
  }
  const exits = new Set<string>();
  let changed = true;
  while (changed) {
    changed = false;
    for (const n of nodes) {
      if (exits.has(n.key) || n.type === 'START' || n.type === 'END') continue;
      const ins = (incoming.get(n.key) ?? []).filter(e => e.from !== n.key);
      if (ins.length === 0) continue;
      if (ins.every(e => EXIT_OUTCOMES.includes(e.outcome) || exits.has(e.from))) {
        exits.add(n.key);
        changed = true;
      }
    }
  }
  return exits;
}

// ─── main layout ──────────────────────────────────────────────────────────────

function layoutMain(nodes: LayoutInputNode[], edges: LayoutInputEdge[], options: LayoutOptions): RunGraphLayout | null {
  const byKey = new Map(nodes.map(n => [n.key, n]));
  const start = nodes.find(n => n.type === 'START');
  const ends = new Set(nodes.filter(n => n.type === 'END').map(n => n.key));
  if (!start || ends.size === 0) return null;
  const valid = edges.filter(e => byKey.has(e.from) && byKey.has(e.to));
  const back = findBackEdges(nodes, valid);
  const exits = findExitNodes(nodes, valid);
  const isExitEdge = (e: LayoutInputEdge) => exits.has(e.to) || exits.has(e.from);

  // Longest path over the DAG of forward, non-exit edges (topological order by Kahn).
  const dagEdges = valid.filter(e => !back.has(e.key) && !isExitEdge(e) && e.from !== e.to);
  const dagNodes = nodes.filter(n => !exits.has(n.key)).map(n => n.key);
  const outs = new Map<string, LayoutInputEdge[]>();
  const indeg = new Map<string, number>(dagNodes.map(k => [k, 0]));
  for (const e of dagEdges) {
    outs.set(e.from, [...(outs.get(e.from) ?? []), e]);
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
  }
  const order: string[] = [];
  const queue = dagNodes.filter(k => (indeg.get(k) ?? 0) === 0);
  const remaining = new Map(indeg);
  while (queue.length > 0) {
    const k = queue.shift()!;
    order.push(k);
    for (const e of outs.get(k) ?? []) {
      const d = (remaining.get(e.to) ?? 0) - 1;
      remaining.set(e.to, d);
      if (d === 0) queue.push(e.to);
    }
  }
  if (order.length !== dagNodes.length) return null; // not a DAG after removing back edges: fall back
  // The main path is the longest one among those that take the FEWEST detours, a detour being an edge whose outcome is not
  // the source node's first declared outcome (the normal flow: `passed`, `done`, `approved`); otherwise a loop that happens to
  // continue forward (size -> simplify -> size-review -> quality) would be "longer" than the flow it detours from.
  // Only an edge inside a loop counts (same strongly connected component, exit nodes left out): frame -> build (`fast`, its
  // first outcome) skips plan but is no loop, so the longest path still goes through plan.
  const component = stronglyConnected(dagNodes, valid.filter(e => !isExitEdge(e)));
  const detour = (e: LayoutInputEdge) => {
    const declared = byKey.get(e.from)?.outcomes ?? [];
    return declared.length > 1 && declared[0] !== e.outcome && component.get(e.from) === component.get(e.to) ? 1 : 0;
  };
  const better = (a: [number, number], b: [number, number] | undefined) => b === undefined || a[0] < b[0] || (a[0] === b[0] && a[1] > b[1]);
  const score = new Map<string, [number, number]>(); // [detours, length]
  const depth = new Map<string, number>();
  const via = new Map<string, string>();
  score.set(start.key, [0, 0]);
  depth.set(start.key, 0);
  for (const k of order) {
    const sc = score.get(k);
    const d = depth.get(k);
    for (const e of outs.get(k) ?? []) {
      if (d !== undefined && (depth.get(e.to) ?? -1) < d + 1) depth.set(e.to, d + 1);
      if (sc === undefined) continue;
      const candidate: [number, number] = [sc[0] + detour(e), sc[1] + 1];
      if (better(candidate, score.get(e.to))) {
        score.set(e.to, candidate);
        via.set(e.to, k);
      }
    }
  }
  let end: string | undefined;
  for (const k of ends) if (score.has(k) && (end === undefined || better(score.get(k)!, score.get(end)!))) end = k;
  if (end === undefined) return null;
  const mainPath: string[] = [];
  for (let k: string | undefined = end; k !== undefined; k = via.get(k)) mainPath.unshift(k);
  if (mainPath[0] !== start.key) return null;
  const mainRow = new Map(mainPath.map((k, i) => [k, i]));

  // Rows of side nodes: the row of the main node their loop returns to, else their depth.
  const rowOf = new Map<string, number>(mainRow);
  const side = dagNodes.filter(k => !mainRow.has(k));
  for (const k of side) {
    const returns = valid.filter(e => e.from === k && mainRow.has(e.to) && back.has(e.key)).map(e => mainRow.get(e.to)!);
    rowOf.set(k, returns.length > 0 ? Math.min(...returns) : Math.min(depth.get(k) ?? 0, mainPath.length - 1));
  }

  // Columns: main 0; side nodes take the first free column on their row.
  const taken = new Set<string>(mainPath.map(k => `${mainRow.get(k)}:0`));
  const columnOf = new Map<string, number>(mainPath.map(k => [k, 0]));
  for (const k of side.sort((a, b) => (rowOf.get(a)! - rowOf.get(b)!) || a.localeCompare(b))) {
    let c = 1;
    while (taken.has(`${rowOf.get(k)}:${c}`)) c++;
    taken.add(`${rowOf.get(k)}:${c}`);
    columnOf.set(k, c);
  }
  const sideColumns = Math.max(0, ...[...columnOf.values()]);

  // Exit nodes (All paths only): a column right of the side columns, row = the smallest row of a source.
  const showExits = options.showExits ?? false;
  const exitColumn = sideColumns + 1;
  if (showExits) {
    const exitList = [...exits].sort();
    let guard = 0;
    while (exitList.some(k => !rowOf.has(k)) && guard++ < exitList.length + 2) {
      for (const k of exitList) {
        if (rowOf.has(k)) continue;
        const sources = valid.filter(e => e.to === k && rowOf.has(e.from)).map(e => rowOf.get(e.from)!);
        if (sources.length > 0) rowOf.set(k, Math.min(...sources));
      }
    }
    for (const k of exitList) {
      if (!rowOf.has(k)) rowOf.set(k, 0);
      let r = rowOf.get(k)!;
      while (taken.has(`${r}:${exitColumn}`)) r++;
      rowOf.set(k, r);
      taken.add(`${r}:${exitColumn}`);
      columnOf.set(k, exitColumn);
    }
  }

  // Edge roles and lanes.
  const visible = valid.filter(e => showExits || !isExitEdge(e));
  const hidden = new Map<string, number>();
  if (!showExits) for (const e of valid) if (exits.has(e.to) && !exits.has(e.from)) hidden.set(e.from, (hidden.get(e.from) ?? 0) + 1);
  const isMainStep = (e: LayoutInputEdge) => mainRow.has(e.from) && mainRow.has(e.to) && mainRow.get(e.to) === mainRow.get(e.from)! + 1;
  const leftBack: LayoutInputEdge[] = [];
  const rightLoop: LayoutInputEdge[] = [];
  for (const e of visible) {
    if (!(mainRow.has(e.from) && mainRow.has(e.to)) || isExitEdge(e) || isMainStep(e) || e.from === e.to) continue;
    const span = mainRow.get(e.from)! - mainRow.get(e.to)!;
    if (span >= 2) leftBack.push(e);
    else rightLoop.push(e); // a one-row back edge, or a shortcut forward
  }
  const span = (e: LayoutInputEdge) => Math.abs(mainRow.get(e.from)! - mainRow.get(e.to)!);
  leftBack.sort((a, b) => span(a) - span(b) || a.key.localeCompare(b.key));
  rightLoop.sort((a, b) => span(a) - span(b) || a.key.localeCompare(b.key));
  const leftLane = new Map(leftBack.map((e, i) => [e.key, i]));
  const rightLane = new Map(rightLoop.map((e, i) => [e.key, i]));

  // Geometry.
  const leftMargin = MARGIN + (leftBack.length + 1) * LANE_GAP;
  const mainWidth = Math.max(...mainPath.map(k => sized(byKey.get(k)!).width));
  const mainX = leftMargin + mainWidth / 2;
  const rightLaneBase = mainX + mainWidth / 2 + LANE_GAP;
  const sideBase = rightLaneBase + (rightLoop.length + 1) * LANE_GAP;
  const columnX = (c: number) => (c === 0 ? mainX : sideBase + (c - 1) * COLUMN_WIDTH + COLUMN_WIDTH / 2);
  const exitLaneBase = sideBase + (exitColumn - 1) * COLUMN_WIDTH - LANE_GAP;

  const laid: Record<string, LayoutNode> = {};
  for (const [k, row] of rowOf) {
    const n = byKey.get(k);
    if (!n || (!showExits && exits.has(k))) continue;
    const s = sized(n);
    const column = columnOf.get(k) ?? 0;
    laid[k] = {
      key: k, type: n.type, shape: s.shape, width: s.width, height: s.height, row, column,
      x: columnX(column), y: MARGIN + row * ROW_HEIGHT + ROW_HEIGHT / 2,
      placement: mainRow.has(k) ? 'main' : exits.has(k) ? 'exit' : 'side', hiddenExitEdges: hidden.get(k) ?? 0,
    };
  }

  // Entry heights: within one target, an outer (larger lane) left edge enters higher.
  const entryOffset = new Map<string, number>();
  const intoTarget = new Map<string, LayoutInputEdge[]>();
  for (const e of leftBack) intoTarget.set(e.to, [...(intoTarget.get(e.to) ?? []), e]);
  for (const list of intoTarget.values()) {
    const sorted = [...list].sort((a, b) => leftLane.get(a.key)! - leftLane.get(b.key)!); // inner first: it enters lowest
    sorted.forEach((e, i) => entryOffset.set(e.key, i));
  }
  const exitLaneOf = new Map<string, number>([...exits].sort().map((k, i) => [k, i]));

  const out: LayoutEdge[] = [];
  for (const e of visible) {
    const a = laid[e.from];
    const b = laid[e.to];
    if (!a || !b) continue;
    const base = { key: e.key, from: e.from, to: e.to, outcome: e.outcome, dashed: false };
    if (e.from === e.to) {
      out.push({ ...base, role: 'side', points: [right(a, -6), { x: a.x + a.width / 2 + 18, y: a.y - 6 }, { x: a.x + a.width / 2 + 18, y: a.y + 6 }, right(a, 6)] });
    } else if (isExitEdge(e)) {
      const lane = exitLaneOf.get(exits.has(e.to) ? e.to : e.from) ?? 0;
      const x = exitLaneBase - lane * (LANE_GAP / 2);
      out.push({ ...base, role: 'exit', dashed: true, lane, side: 'right', points: [right(a), { x, y: a.y }, { x, y: b.y }, left(b)] });
    } else if (isMainStep(e)) {
      out.push({ ...base, role: 'main', points: [bottom(a), top(b)] });
    } else if (leftLane.has(e.key)) {
      const lane = leftLane.get(e.key)!;
      const x = leftMargin - (lane + 1) * LANE_GAP;
      const step = Math.min(6, (b.height / 2 - 2) / Math.max(1, intoTarget.get(e.to)!.length));
      const entryY = b.y - entryOffset.get(e.key)! * step; // outer lanes enter higher, so nested pairs never cross
      out.push({ ...base, role: 'back', lane, side: 'left', points: [left(a), { x, y: a.y }, { x, y: entryY }, { x: b.x - b.width / 2, y: entryY }] });
    } else if (rightLane.has(e.key)) {
      const lane = rightLane.get(e.key)!;
      const x = rightLaneBase + lane * LANE_GAP;
      const role: EdgeRole = mainRow.get(e.to)! < mainRow.get(e.from)! ? 'back' : 'shortcut';
      out.push({ ...base, role, lane, side: 'right', points: [right(a, 4), { x, y: a.y + 4 }, { x, y: b.y - 4 }, right(b, -4)] });
    } else {
      // An edge to or from a side node: one elbow.
      const sameRow = Math.abs(a.y - b.y) < 1;
      const points = sameRow
        ? [a.x < b.x ? right(a) : left(a), b.x > a.x ? left(b) : right(b)]
        : [a.x < b.x ? right(a) : a.x > b.x ? left(a) : bottom(a), { x: b.x, y: a.y }, b.y > a.y ? top(b) : bottom(b)];
      const role: EdgeRole = back.has(e.key) ? 'back' : 'side';
      out.push({ ...base, role, points });
    }
  }

  return finish(laid, out, exits, mainPath, false);
}

/** Tarjan's strongly connected components: node key -> component index. */
function stronglyConnected(keys: string[], edges: LayoutInputEdge[]): Map<string, number> {
  const out = new Map<string, string[]>();
  for (const e of edges) out.set(e.from, [...(out.get(e.from) ?? []), e.to]);
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const comp = new Map<string, number>();
  let counter = 0;
  let components = 0;
  const visit = (v: string) => {
    index.set(v, counter);
    low.set(v, counter);
    counter++;
    stack.push(v);
    onStack.add(v);
    for (const w of out.get(v) ?? []) {
      if (!index.has(w)) {
        visit(w);
        low.set(v, Math.min(low.get(v)!, low.get(w)!));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v)!, index.get(w)!));
      }
    }
    if (low.get(v) === index.get(v)) {
      let w: string;
      do {
        w = stack.pop()!;
        onStack.delete(w);
        comp.set(w, components);
      } while (w !== v);
      components++;
    }
  };
  for (const k of keys) if (!index.has(k)) visit(k);
  return comp;
}

function left(n: LayoutNode, dy = 0): Point { return { x: n.x - n.width / 2, y: n.y + dy }; }
function right(n: LayoutNode, dy = 0): Point { return { x: n.x + n.width / 2, y: n.y + dy }; }
function top(n: LayoutNode): Point { return { x: n.x, y: n.y - n.height / 2 }; }
function bottom(n: LayoutNode): Point { return { x: n.x, y: n.y + n.height / 2 }; }

function finish(nodes: Record<string, LayoutNode>, edges: LayoutEdge[], exits: Set<string>, mainPath: string[], fallback: boolean): RunGraphLayout {
  let width = 0;
  let height = 0;
  for (const n of Object.values(nodes)) {
    width = Math.max(width, n.x + n.width / 2);
    height = Math.max(height, n.y + n.height / 2);
  }
  for (const e of edges) for (const p of e.points) {
    width = Math.max(width, p.x);
    height = Math.max(height, p.y);
  }
  return { nodes, edges, exitNodes: exits, mainPath, width: width + MARGIN, height: height + MARGIN, fallback };
}

// ─── fallback: rank layout ────────────────────────────────────────────────────

function layoutRanked(nodes: LayoutInputNode[], edges: LayoutInputEdge[], options: LayoutOptions): RunGraphLayout {
  const byKey = new Map(nodes.map(n => [n.key, n]));
  let exits = new Set<string>();
  let back = new Set<string>();
  try {
    exits = findExitNodes(nodes, edges);
    back = findBackEdges(nodes, edges);
  } catch { /* keep empty sets */ }
  const shown = nodes.filter(n => options.showExits || !exits.has(n.key));
  const depth = new Map<string, number>();
  const start = nodes.find(n => n.type === 'START') ?? nodes[0];
  if (start) {
    depth.set(start.key, 0);
    const queue = [start.key];
    while (queue.length > 0) {
      const k = queue.shift()!;
      for (const e of edges) {
        if (e.from !== k || back.has(e.key) || depth.has(e.to) || !byKey.has(e.to)) continue;
        depth.set(e.to, depth.get(k)! + 1);
        queue.push(e.to);
      }
    }
  }
  const maxDepth = Math.max(0, ...[...depth.values()]);
  const perRow = new Map<number, number>();
  const laid: Record<string, LayoutNode> = {};
  for (const n of shown) {
    const row = depth.get(n.key) ?? maxDepth + 1;
    const column = perRow.get(row) ?? 0;
    perRow.set(row, column + 1);
    const s = sized(n);
    laid[n.key] = {
      key: n.key, type: n.type, shape: s.shape, width: s.width, height: s.height, row, column,
      x: MARGIN + column * COLUMN_WIDTH + COLUMN_WIDTH / 2, y: MARGIN + row * ROW_HEIGHT + ROW_HEIGHT / 2,
      placement: 'rank', hiddenExitEdges: 0,
    };
  }
  const out: LayoutEdge[] = [];
  for (const e of edges) {
    const a = laid[e.from];
    const b = laid[e.to];
    if (!a || !b) continue;
    out.push({
      key: e.key, from: e.from, to: e.to, outcome: e.outcome, role: back.has(e.key) ? 'back' : exits.has(e.to) ? 'exit' : 'main',
      dashed: exits.has(e.to), points: [{ x: a.x, y: a.y + a.height / 2 }, { x: b.x, y: b.y - b.height / 2 }],
    });
  }
  return finish(laid, out, exits, [], true);
}
