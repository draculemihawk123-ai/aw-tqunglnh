import { useState } from 'react';
import { RunGraphCanvas } from './RunGraphCanvas';
import type { LayoutInputEdge, LayoutInputNode } from './layoutRunGraph';

/**
 * V12-15 (docs/design/15-v12-ui-operations.md, UX-44): a workflow drawn with the run graph's layout and shapes but no run data —
 * on the Definitions page (a selected WORKFLOW version) and in the New task form (the chosen workflow). Picking a node lists where
 * its outcomes lead.
 */
export function workflowGraphFrom(...sources: (string | undefined)[]): { nodes: LayoutInputNode[]; edges: LayoutInputEdge[] } | null {
  for (const source of sources) {
    if (!source) continue;
    try {
      const doc = JSON.parse(source) as { nodes?: LayoutInputNode[]; edges?: LayoutInputEdge[]; document?: { nodes?: LayoutInputNode[]; edges?: LayoutInputEdge[] } };
      const body = doc.nodes ? doc : doc.document;
      if (body?.nodes && Array.isArray(body.nodes) && Array.isArray(body.edges)) return { nodes: body.nodes, edges: body.edges };
    } catch {
      /* not JSON (a YAML source): try the next one */
    }
  }
  return null;
}

export function WorkflowDiagram({ nodes, edges, label, height = 420 }: { nodes: LayoutInputNode[]; edges: LayoutInputEdge[]; label: string; height?: number }) {
  const [selected, setSelected] = useState<string | null>(null);
  const node = nodes.find(n => n.key === selected);
  return (
    <div className="border border-[#CDD5DF] rounded-[8px] overflow-hidden bg-white" data-testid="workflow-diagram">
      <div style={{ height }}>
        <RunGraphCanvas nodes={nodes} edges={edges} activations={[]} takenEdges={[]} selectedNodeKey={selected}
          onSelect={key => setSelected(selected === key ? null : key)} label={label} />
      </div>
      {node && (
        <div className="px-3 py-2 border-t border-[#ECEFF4] text-[12px]" data-testid="workflow-diagram-node">
          <span className="font-medium text-[#172033]">{node.key}</span> <span className="font-mono text-[#475569]">{node.type}</span>
          <ul className="mt-1 space-y-0.5">
            {edges.filter(e => e.from === node.key).map(e => (
              <li key={e.key}><span className="font-mono text-[#3659E3]">{e.outcome}</span> → {e.to}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
