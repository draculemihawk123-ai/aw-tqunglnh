import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkflowDiagram, workflowGraphFrom } from './WorkflowDiagram';

const bugfix = readFileSync(join(__dirname, '..', '..', '..', 'kit', 'workflows', 'wf-bugfix.json'), 'utf8');

describe('WorkflowDiagram (V12-15)', () => {
  it('reads a workflow document (or a wrapped one) and ignores what is not one', () => {
    expect(workflowGraphFrom(bugfix)?.nodes.length).toBeGreaterThan(5);
    expect(workflowGraphFrom('not json', JSON.stringify({ document: { nodes: [{ key: 's', type: 'START' }], edges: [] } }))?.nodes).toHaveLength(1);
    expect(workflowGraphFrom('{"a":1}', undefined)).toBeNull();
  });

  it('draws every node as a button with no run data, and lists the outcomes of the node picked', async () => {
    const graph = workflowGraphFrom(bugfix)!;
    render(<WorkflowDiagram nodes={graph.nodes} edges={graph.edges} label="Workflow wf-bugfix" />);
    const group = screen.getByRole('group', { name: 'Workflow wf-bugfix' });
    const repro = within(group).getByRole('button', { name: /^repro, AGENT, not reached$/ });
    await userEvent.click(repro);
    expect(screen.getByTestId('workflow-diagram-node')).toHaveTextContent('expect-fail');
  });
});
