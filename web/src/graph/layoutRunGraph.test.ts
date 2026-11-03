import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { EXIT_OUTCOMES, findBackEdges, findExitNodes, layoutRunGraph, nodeShape } from './layoutRunGraph';
import type { LayoutInputEdge, LayoutInputNode, RunGraphLayout } from './layoutRunGraph';

// V12-09: the fixtures are the real workflows the kit ships (read from kit/workflows, so a change there is tested at once) plus
// one with FORK/JOIN, which no kit workflow uses yet. chochuyennha's wf-docs-change is not in this repository; its graph is the
// same shape as these (START, AGENT/COMMAND/APPROVAL, END) and should be added here as a fixture when it is.
const KIT = join(__dirname, '..', '..', '..', 'kit', 'workflows');

interface Fixture { name: string; nodes: LayoutInputNode[]; edges: LayoutInputEdge[] }

const kitFixtures: Fixture[] = readdirSync(KIT).filter(f => f.endsWith('.json')).map(f => {
  const doc = JSON.parse(readFileSync(join(KIT, f), 'utf8')) as { nodes: LayoutInputNode[]; edges: LayoutInputEdge[] };
  return { name: f.replace(/\.json$/, ''), nodes: doc.nodes, edges: doc.edges };
});

const forkJoin: Fixture = {
  name: 'fork-join',
  nodes: [
    { key: 'start', type: 'START', outcomes: ['next'] },
    { key: 'fork', type: 'FORK', outcomes: ['next'] },
    { key: 'api', type: 'AGENT', outcomes: ['done'] },
    { key: 'web', type: 'AGENT', outcomes: ['done'] },
    { key: 'web-check', type: 'COMMAND', outcomes: ['passed', 'failed'] },
    { key: 'join', type: 'JOIN', outcomes: ['next'] },
    { key: 'end', type: 'END' },
  ],
  edges: [
    { key: 'start-fork', from: 'start', outcome: 'next', to: 'fork' },
    { key: 'fork-api', from: 'fork', outcome: 'next', to: 'api' },
    { key: 'fork-web', from: 'fork', outcome: 'next', to: 'web' },
    { key: 'web-check', from: 'web', outcome: 'done', to: 'web-check' },
    { key: 'check-web', from: 'web-check', outcome: 'failed', to: 'web' },
    { key: 'check-join', from: 'web-check', outcome: 'passed', to: 'join' },
    { key: 'api-join', from: 'api', outcome: 'done', to: 'join' },
    { key: 'join-end', from: 'join', outcome: 'next', to: 'end' },
  ],
};

const fixtures = [...kitFixtures, forkJoin];

function overlaps(layout: RunGraphLayout): string[] {
  const list = Object.values(layout.nodes);
  const hits: string[] = [];
  for (let i = 0; i < list.length; i++) for (let j = i + 1; j < list.length; j++) {
    const a = list[i], b = list[j];
    if (Math.abs(a.x - b.x) < (a.width + b.width) / 2 && Math.abs(a.y - b.y) < (a.height + b.height) / 2) hits.push(`${a.key}/${b.key}`);
  }
  return hits;
}

describe('layoutRunGraph (V12-09)', () => {
  it('has the kit workflows as fixtures', () => {
    expect(kitFixtures.map(f => f.name)).toEqual(expect.arrayContaining(['wf-task-delivery-plus', 'wf-bugfix', 'wf-feature-definition']));
  });

  for (const f of fixtures) {
    for (const showExits of [false, true]) {
      describe(`${f.name} (${showExits ? 'all paths' : 'main path'})`, () => {
        const layout = layoutRunGraph(f.nodes, f.edges, { showExits });

        it('finds a main path from START to END (no fallback)', () => {
          expect(layout.fallback).toBe(false);
          expect(f.nodes.find(n => n.key === layout.mainPath[0])?.type).toBe('START');
          expect(f.nodes.find(n => n.key === layout.mainPath[layout.mainPath.length - 1])?.type).toBe('END');
        });

        it('places every shown node, no two overlapping', () => {
          const expected = f.nodes.filter(n => showExits || !layout.exitNodes.has(n.key)).map(n => n.key).sort();
          expect(Object.keys(layout.nodes).sort()).toEqual(expected);
          expect(overlaps(layout)).toEqual([]);
        });

        it('gives every shown edge a path, dashed exactly for exit edges', () => {
          const shown = f.edges.filter(e => layout.nodes[e.from] && layout.nodes[e.to]);
          expect(layout.edges.map(e => e.key).sort()).toEqual(shown.map(e => e.key).sort());
          for (const e of layout.edges) {
            expect(e.points.length).toBeGreaterThanOrEqual(2);
            expect(e.dashed).toBe(e.role === 'exit');
          }
        });

        it('gives every node the shape of the table', () => {
          for (const n of Object.values(layout.nodes)) {
            const def = f.nodes.find(d => d.key === n.key)!;
            expect(n.shape).toBe(nodeShape(def));
          }
        });

        it('nests left lanes: a longer back edge is outside and enters its target higher', () => {
          const left = layout.edges.filter(e => e.side === 'left');
          const rowSpan = (e: (typeof left)[number]) => [layout.nodes[e.to].row, layout.nodes[e.from].row] as const;
          for (const a of left) for (const b of left) {
            if (a === b) continue;
            const [aTop, aBottom] = rowSpan(a);
            const [bTop, bBottom] = rowSpan(b);
            const nested = aTop <= bTop && bBottom <= aBottom && (aBottom - aTop) > (bBottom - bTop);
            if (!nested) continue;
            expect(a.lane!).toBeGreaterThan(b.lane!); // the outer one
            if (a.to === b.to) expect(a.points[2].y).toBeLessThanOrEqual(b.points[2].y);
          }
        });
      });
    }
  }

  it('wf-task-delivery-plus: the shape rules of the decided design', () => {
    const f = kitFixtures.find(x => x.name === 'wf-task-delivery-plus')!;
    const shapes = Object.fromEntries(f.nodes.map(n => [n.key, nodeShape(n)]));
    expect(shapes).toMatchObject({
      start: 'pill', end: 'pill', build: 'rounded', gate1: 'diamond', size: 'diamond', hygiene: 'diamond', quality: 'diamond',
      'check-plan': 'diamond', gate2: 'double-diamond', 'size-review': 'double-diamond', reject: 'rect',
    });
  });

  it('wf-task-delivery-plus: needs-info and reject are exit nodes; simplify and debug are side nodes on the row they return to', () => {
    const f = kitFixtures.find(x => x.name === 'wf-task-delivery-plus')!;
    expect([...findExitNodes(f.nodes, f.edges)].sort()).toEqual(['needs-info', 'reject']);
    const layout = layoutRunGraph(f.nodes, f.edges);
    expect(layout.nodes.simplify.placement).toBe('side');
    expect(layout.nodes.simplify.row).toBe(layout.nodes.gate1.row);
    expect(layout.nodes.debug.row).toBe(layout.nodes.build.row);
    expect(layout.nodes['needs-info']).toBeUndefined();
    expect(layout.nodes.build.hiddenExitEdges).toBeGreaterThan(0); // build -> needs-info, build -> reject
    // frame -> build (fast) skips plan/check-plan: a shortcut on the right.
    expect(layout.edges.find(e => e.key === 'frame-build')).toMatchObject({ role: 'shortcut', side: 'right' });
    // check-plan -> plan is a one-row back edge: right; gate2 -> build is a long back edge: left.
    expect(layout.edges.find(e => e.from === 'check-plan' && e.to === 'plan')).toMatchObject({ role: 'back', side: 'right' });
    expect(layout.edges.find(e => e.from === 'gate2' && e.to === 'build')).toMatchObject({ role: 'back', side: 'left' });
  });

  it('All paths gathers the edges into one exit node in one lane', () => {
    const f = kitFixtures.find(x => x.name === 'wf-task-delivery-plus')!;
    const layout = layoutRunGraph(f.nodes, f.edges, { showExits: true });
    const intoReject = layout.edges.filter(e => e.to === 'reject');
    expect(intoReject.length).toBeGreaterThan(2);
    expect(new Set(intoReject.map(e => e.points[1].x)).size).toBe(1);
  });

  it('a back edge is found by DFS; COMPLETION_REWORK is always one', () => {
    const nodes: LayoutInputNode[] = [{ key: 's', type: 'START' }, { key: 'a', type: 'AGENT', outcomes: ['done'] }, { key: 'e', type: 'END' }];
    const edges: LayoutInputEdge[] = [
      { key: 's-a', from: 's', to: 'a', outcome: 'next' }, { key: 'a-e', from: 'a', to: 'e', outcome: 'done' },
      { key: 'e-a', from: 'a', to: 'a', outcome: 'rework', kind: 'COMPLETION_REWORK' },
    ];
    expect(findBackEdges(nodes, edges).has('e-a')).toBe(true);
  });

  it('falls back to a rank layout and never throws on a graph without a main path', () => {
    const nodes: LayoutInputNode[] = [{ key: 'a', type: 'AGENT' }, { key: 'b', type: 'AGENT' }];
    const edges: LayoutInputEdge[] = [{ key: 'a-b', from: 'a', to: 'b', outcome: 'x' }, { key: 'b-a', from: 'b', to: 'a', outcome: 'y' }, { key: 'bad', from: 'a', to: 'ghost', outcome: 'z' }];
    const layout = layoutRunGraph(nodes, edges);
    expect(layout.fallback).toBe(true);
    expect(Object.keys(layout.nodes).sort()).toEqual(['a', 'b']);
    expect(overlaps(layout)).toEqual([]);
    expect(() => layoutRunGraph([], [])).not.toThrow();
  });

  it('documents the exit outcomes as a constant', () => {
    expect(EXIT_OUTCOMES).toEqual(['needs_info', 'escalated', 'rejected', 'abandon']);
  });
});
