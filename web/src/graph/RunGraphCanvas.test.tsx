import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { RunGraphCanvas } from './RunGraphCanvas';
import type { CanvasActivation, CanvasTakenEdge } from './RunGraphCanvas';

// V12-10: the fixture is shaped like chochuyennha's P2-02 run (wf-task-delivery-plus): the gate1 -> size -> simplify -> gate1
// loop ran three times and gate1's fourth activation failed. The recorded API responses of the four real runs named in the
// design (P2-02, P1-01, P2-01, P0-03) are not in this repository; these fixtures reproduce their shape from the kit workflow.
const plus = JSON.parse(readFileSync(join(__dirname, '..', '..', '..', 'kit', 'workflows', 'wf-task-delivery-plus.json'), 'utf8'));

function p2_02(): { activations: CanvasActivation[]; taken: CanvasTakenEdge[] } {
  const seq: [string, string, string?][] = [
    ['start', 'SUCCEEDED', 'next'], ['frame', 'SUCCEEDED', 'full'], ['plan', 'SUCCEEDED', 'done'], ['check-plan', 'SUCCEEDED', 'passed'],
    ['build', 'SUCCEEDED', 'done'], ['hygiene', 'SUCCEEDED', 'passed'],
    ['gate1', 'SUCCEEDED', 'passed'], ['size', 'SUCCEEDED', 'large'], ['simplify', 'SUCCEEDED', 'done'],
    ['gate1', 'SUCCEEDED', 'passed'], ['size', 'SUCCEEDED', 'large'], ['simplify', 'SUCCEEDED', 'done'],
    ['gate1', 'SUCCEEDED', 'passed'], ['size', 'SUCCEEDED', 'large'], ['simplify', 'SUCCEEDED', 'done'],
    ['gate1', 'FAILED'],
  ];
  const activations = seq.map(([nodeKey, state, selectedOutcome], i) => ({ nodeKey, state, selectedOutcome, activationSequence: i + 1 }));
  const taken = seq.filter(s => s[2]).map(([fromNodeKey, , outcome]) => ({ fromNodeKey, outcome: outcome! }));
  return { activations, taken };
}

function setup(over: Partial<Parameters<typeof RunGraphCanvas>[0]> = {}) {
  const { activations, taken } = p2_02();
  const onSelect = vi.fn();
  render(<RunGraphCanvas nodes={plus.nodes} edges={plus.edges} activations={activations} takenEdges={taken}
    selectedNodeKey={null} onSelect={onSelect} label="Workflow graph for run run-p2-02" {...over} />);
  return { onSelect };
}

describe('RunGraphCanvas (V12-10)', () => {
  beforeEach(() => {
    try { window.localStorage.clear(); } catch { /* ignore */ }
  });

  it('is a group of keyboard buttons, one per node, labelled with name, type, state and run count', async () => {
    const { onSelect } = setup();
    const graph = screen.getByRole('group', { name: 'Workflow graph for run run-p2-02' });
    const gate1 = within(graph).getByRole('button', { name: 'gate1, COMMAND, failed, ran 4 times' });
    expect(within(graph).getByRole('button', { name: 'simplify, AGENT, succeeded, ran 3 times' })).toBeInTheDocument();
    expect(within(graph).getByRole('button', { name: 'quality, COMMAND, not reached' })).toBeInTheDocument();
    gate1.focus();
    await userEvent.keyboard('{Enter}');
    expect(onSelect).toHaveBeenLastCalledWith('gate1');
    within(graph).getByRole('button', { name: /^simplify,/ }).focus();
    await userEvent.keyboard(' ');
    expect(onSelect).toHaveBeenLastCalledWith('simplify');
  });

  it('counts activations (×n) and taken edges (outcome ×n) and hides exit nodes behind +n', () => {
    setup();
    expect(screen.getByTestId('runs-gate1')).toHaveTextContent('×4');
    expect(screen.getByTestId('runs-size')).toHaveTextContent('×3');
    expect(screen.queryByTestId('runs-build')).not.toBeInTheDocument(); // ran once: no badge
    expect(screen.getByTestId('edge-size-simplify')).toHaveAttribute('data-taken', '3');
    expect(screen.getByTestId('edge-size-simplify')).toHaveTextContent('large ×3');
    expect(screen.queryByTestId('node-needs-info')).not.toBeInTheDocument();
    expect(screen.getByTestId('hidden-build')).toHaveTextContent('+');
  });

  it('All paths shows the exit nodes and remembers the choice', async () => {
    setup();
    await userEvent.click(screen.getByRole('button', { name: 'All paths' }));
    expect(screen.getByTestId('node-needs-info')).toBeInTheDocument();
    expect(screen.getByTestId('node-reject')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'All paths' })).toHaveAttribute('aria-pressed', 'true');
    expect(window.localStorage.getItem('aw.runGraph.showExits')).toBe('1');
  });

  it('renders even when storage throws', () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    setup();
    expect(screen.getByRole('button', { name: 'Main path' })).toHaveAttribute('aria-pressed', 'true');
    spy.mockRestore();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<RunGraphCanvas nodes={plus.nodes} edges={plus.edges} activations={p2_02().activations} takenEdges={[]}
      selectedNodeKey="gate1" onSelect={() => {}} label="Workflow graph" />);
    await expectNoAxeViolations(container);
  });
});
