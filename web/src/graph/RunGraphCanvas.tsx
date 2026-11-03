import { useMemo, useState } from 'react';
import type { KeyboardEvent } from 'react';
import { layoutRunGraph } from './layoutRunGraph';
import type { LayoutEdge, LayoutInputEdge, LayoutInputNode, LayoutNode } from './layoutRunGraph';

/**
 * V12-10 (docs/design/15-v12-ui-operations.md, "Quy tắc hiển thị"): the run graph drawn from layoutRunGraph. Shapes by node kind,
 * colour by the state of the node's LAST activation (dashed outline: not reached yet), the last outcome as a second line, `×n`
 * for the number of activations (every activation, a blocked-then-retried one included, across every run given), `+n` for exit
 * edges hidden in the main view, taken edges coloured with `outcome ×n`. Every node is a keyboard button (Enter/Space selects it).
 */

export interface CanvasActivation {
  nodeKey: string;
  state: string;
  selectedOutcome?: string;
  activationSequence: number;
  /** Which run the activation belongs to (several runs of one work item are merged); higher is newer. */
  runOrder?: number;
}

export interface CanvasTakenEdge {
  edgeKey?: string;
  fromNodeKey: string;
  outcome: string;
  toNodeKey?: string;
}

export interface RunGraphCanvasProps {
  nodes: LayoutInputNode[];
  edges: LayoutInputEdge[];
  activations: CanvasActivation[];
  takenEdges: CanvasTakenEdge[];
  selectedNodeKey: string | null;
  onSelect: (nodeKey: string) => void;
  label: string;
}

const STYLE: Record<string, { bg: string; bd: string; fc: string }> = {
  '': { bg: '#FFFFFF', bd: '#AAB4C3', fc: '#475569' },
  PENDING: { bg: '#F1F5F9', bd: '#94A3B8', fc: '#475569' },
  READY: { bg: '#F1F5F9', bd: '#94A3B8', fc: '#475569' },
  QUEUED: { bg: '#DBEAFE', bd: '#93C5FD', fc: '#1E40AF' },
  RUNNING: { bg: '#EDE9FE', bd: '#8B5CF6', fc: '#5B21B6' },
  WAITING: { bg: '#FEF3C7', bd: '#F59E0B', fc: '#92400E' },
  BLOCKED: { bg: '#FEE2E2', bd: '#F87171', fc: '#991B1B' },
  SUCCEEDED: { bg: '#DCFCE7', bd: '#4ADE80', fc: '#166534' },
  FAILED: { bg: '#FEE2E2', bd: '#EF4444', fc: '#991B1B' },
  SKIPPED: { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#5D697A' },
  CANCELLED: { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#5D697A' },
};

const STATE_WORD: Record<string, string> = {
  '': 'not reached', PENDING: 'pending', READY: 'ready', QUEUED: 'queued', RUNNING: 'running', WAITING: 'waiting',
  BLOCKED: 'blocked', SUCCEEDED: 'succeeded', FAILED: 'failed', SKIPPED: 'skipped', CANCELLED: 'cancelled',
};

const STORAGE_KEY = 'aw.runGraph.showExits';

function loadShowExits(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

function saveShowExits(value: boolean) {
  try {
    window.localStorage.setItem(STORAGE_KEY, value ? '1' : '0');
  } catch {
    /* a private window or blocked storage: the choice is just not remembered */
  }
}

function pathOf(points: { x: number; y: number }[]): string {
  return points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x} ${p.y}`).join(' ');
}

function NodeShape({ node, fill, stroke, dashed, selected }: { node: LayoutNode; fill: string; stroke: string; dashed: boolean; selected: boolean }) {
  const { x, y, width: w, height: h } = node;
  const common = { fill, stroke: selected ? '#3659E3' : stroke, strokeWidth: selected ? 2.5 : 1.5, strokeDasharray: dashed ? '4,3' : undefined };
  const diamond = (dx: number, dy: number) => `${x},${y - dy} ${x + dx},${y} ${x},${y + dy} ${x - dx},${y}`;
  switch (node.shape) {
    case 'diamond':
      return <polygon points={diamond(w / 2, h / 2)} {...common} />;
    case 'double-diamond':
      return (
        <>
          <polygon points={diamond(w / 2, h / 2)} {...common} />
          <polygon points={diamond(w / 2 - 6, h / 2 - 4)} fill="none" stroke={common.stroke} strokeWidth={1} strokeDasharray={common.strokeDasharray} />
        </>
      );
    case 'pill':
      return <rect x={x - w / 2} y={y - h / 2} width={w} height={h} rx={h / 2} {...common} />;
    case 'bar':
      return <rect x={x - w / 2} y={y - h / 2} width={w} height={h} rx={2} {...common} fill={stroke} />;
    case 'rect':
      return <rect x={x - w / 2} y={y - h / 2} width={w} height={h} rx={2} {...common} />;
    default:
      return <rect x={x - w / 2} y={y - h / 2} width={w} height={h} rx={10} {...common} />;
  }
}

export function RunGraphCanvas({ nodes, edges, activations, takenEdges, selectedNodeKey, onSelect, label }: RunGraphCanvasProps) {
  const [showExits, setShowExits] = useState(loadShowExits);
  const layout = useMemo(() => layoutRunGraph(nodes, edges, { showExits }), [nodes, edges, showExits]);

  const byNode = useMemo(() => {
    const m = new Map<string, { count: number; last?: CanvasActivation }>();
    for (const a of activations) {
      const entry = m.get(a.nodeKey) ?? { count: 0 };
      entry.count++;
      const newer = !entry.last || (a.runOrder ?? 0) > (entry.last.runOrder ?? 0) ||
        ((a.runOrder ?? 0) === (entry.last.runOrder ?? 0) && a.activationSequence > entry.last.activationSequence);
      if (newer) entry.last = a;
      m.set(a.nodeKey, entry);
    }
    return m;
  }, [activations]);

  const takenCount = (e: LayoutEdge) =>
    takenEdges.filter(t => (t.edgeKey ? t.edgeKey === e.key : t.fromNodeKey === e.from && t.outcome === e.outcome && (!t.toNodeKey || t.toNodeKey === e.to))).length;

  const toggle = (value: boolean) => {
    setShowExits(value);
    saveShowExits(value);
  };
  const onKey = (key: string) => (ev: KeyboardEvent) => {
    if (ev.key === 'Enter' || ev.key === ' ') {
      ev.preventDefault();
      onSelect(key);
    }
  };

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center gap-1 px-3 py-1.5 border-b border-[#ECEFF4] bg-white" role="group" aria-label="Graph paths">
        <button type="button" aria-pressed={!showExits} onClick={() => toggle(false)}
          className={`text-[12px] px-2 py-0.5 rounded-[4px] border ${!showExits ? 'bg-[#EEF2FF] border-[#C7D2FE] text-[#3659E3]' : 'border-transparent text-[#475569]'}`}>Main path</button>
        <button type="button" aria-pressed={showExits} onClick={() => toggle(true)}
          className={`text-[12px] px-2 py-0.5 rounded-[4px] border ${showExits ? 'bg-[#EEF2FF] border-[#C7D2FE] text-[#3659E3]' : 'border-transparent text-[#475569]'}`}>All paths</button>
        {layout.fallback && <span className="text-[12px] text-[#92400E] ml-2">No main path found: ranked layout</span>}
      </div>
      <div className="flex-1 overflow-auto bg-[#FAFBFC]">
        <svg width={layout.width} height={layout.height} role="group" aria-label={label} className="mx-auto my-2" data-testid="run-graph">
          <defs>
            <marker id="rg-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="#AAB4C3" />
            </marker>
            <marker id="rg-arrow-taken" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="#3659E3" />
            </marker>
          </defs>
          {layout.edges.map(e => {
            const n = takenCount(e);
            const last = e.points[e.points.length - 1];
            const mid = e.points[Math.floor((e.points.length - 1) / 2)];
            const labelAt = e.role === 'main' ? { x: mid.x + 6, y: (e.points[0].y + last.y) / 2 + 4 } : { x: mid.x + 4, y: mid.y - 3 };
            return (
              <g key={e.key} data-testid={`edge-${e.key}`} data-taken={n}>
                <path d={pathOf(e.points)} fill="none" stroke={n > 0 ? '#3659E3' : '#CDD5DF'} strokeWidth={n > 0 ? 2 : 1.25}
                  strokeDasharray={e.dashed ? '5,4' : undefined} markerEnd={`url(#${n > 0 ? 'rg-arrow-taken' : 'rg-arrow'})`} />
                <text x={labelAt.x} y={labelAt.y} fontSize="11" fill={n > 0 ? '#3659E3' : '#5D697A'} fontFamily="JetBrains Mono, monospace">
                  {e.outcome}{n > 1 ? ` ×${n}` : ''}
                </text>
              </g>
            );
          })}
          {Object.values(layout.nodes).map(node => {
            const info = byNode.get(node.key);
            const state = info?.last?.state ?? '';
            const style = STYLE[state] ?? STYLE[''];
            const selected = selectedNodeKey === node.key;
            const count = info?.count ?? 0;
            const aria = `${node.key}, ${node.type}, ${STATE_WORD[state] ?? state.toLowerCase()}${count > 0 ? `, ran ${count} time${count === 1 ? '' : 's'}` : ''}`;
            const textY = node.shape === 'bar' ? node.y - 10 : node.y - (info?.last?.selectedOutcome ? 3 : -4);
            return (
              <g key={node.key} role="button" tabIndex={0} aria-label={aria} aria-pressed={selected} data-testid={`node-${node.key}`}
                onClick={() => onSelect(node.key)} onKeyDown={onKey(node.key)} style={{ cursor: 'pointer', outline: 'none' }}>
                <NodeShape node={node} fill={style.bg} stroke={style.bd} dashed={!info} selected={selected} />
                <text x={node.x} y={textY} textAnchor="middle" fontSize="12" fontWeight="500" fill={style.fc} fontFamily="Inter, sans-serif">{node.key}</text>
                {info?.last?.selectedOutcome && node.shape !== 'bar' && (
                  <text x={node.x} y={node.y + 12} textAnchor="middle" fontSize="10.5" fill={style.fc} fontFamily="JetBrains Mono, monospace">{info.last.selectedOutcome}</text>
                )}
                {count > 1 && (
                  <g data-testid={`runs-${node.key}`}>
                    <rect x={node.x + node.width / 2 - 14} y={node.y - node.height / 2 - 8} width={26} height={15} rx={7} fill="#3659E3" />
                    <text x={node.x + node.width / 2 - 1} y={node.y - node.height / 2 + 3} textAnchor="middle" fontSize="10" fill="#FFFFFF" fontFamily="Inter, sans-serif">×{count}</text>
                  </g>
                )}
                {node.hiddenExitEdges > 0 && (
                  <g data-testid={`hidden-${node.key}`}>
                    <title>{`${node.hiddenExitEdges} exit path${node.hiddenExitEdges === 1 ? '' : 's'} hidden — show All paths`}</title>
                    <rect x={node.x + node.width / 2 - 14} y={node.y + node.height / 2 - 7} width={24} height={14} rx={7} fill="#E2E8F0" />
                    <text x={node.x + node.width / 2 - 2} y={node.y + node.height / 2 + 3} textAnchor="middle" fontSize="10" fill="#475569" fontFamily="Inter, sans-serif">+{node.hiddenExitEdges}</text>
                  </g>
                )}
              </g>
            );
          })}
        </svg>
      </div>
    </div>
  );
}
