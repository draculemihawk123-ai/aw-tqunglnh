import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { RunHistoryPanel } from './RunHistoryPanel';
import { formatDuration, mergeRunHistory } from './runHistory';
import type { RunInput } from './runHistory';

// V12-12: shaped like chochuyennha's P2-01 — run 1 failed at `build` (SCOPE_VIOLATION), run 2 succeeded. Both runs' activations
// of `build` show in its history, run 1 first; ×n counts both.
const nodes = [
  { key: 'start', type: 'START', outcomes: ['next'] },
  { key: 'build', type: 'AGENT', outcomes: ['done', 'escalated'], cyclePolicy: { maxIterations: 2, escalationOutcome: 'escalated' } },
  { key: 'end', type: 'END' },
];
const edges = [
  { key: 'start-build', from: 'start', outcome: 'next', to: 'build' },
  { key: 'build-end', from: 'build', outcome: 'done', to: 'end' },
];

function runs(): RunInput[] {
  return [
    {
      runId: 'run-1', runNumber: 1, workflowVersionId: 'wfv-1',
      graph: {
        nodes, takenEdges: [{ fromNodeRunId: 'a', fromNodeKey: 'start', outcome: 'next', toNodeKey: 'build', activationSequence: 1 }],
        activations: [
          { nodeRunId: 'r1-start', nodeKey: 'start', activationSequence: 1, iteration: 0, state: 'SUCCEEDED', selectedOutcome: 'next' },
          { nodeRunId: 'r1-build', nodeKey: 'build', activationSequence: 2, iteration: 0, state: 'FAILED', resultSummary: 'The agent changed files outside its scope: backend/application.yml' },
        ],
      },
      timeline: [{
        kind: 'EXECUTION_ATTEMPT', nodeRunId: 'r1-build', nodeKey: 'build', activationSequence: 2, iteration: 0, attemptNumber: 1,
        startedAt: '2026-11-02T10:00:00Z', finishedAt: '2026-11-02T10:04:05Z', usage: { inputTokens: 1, outputTokens: 1, cachedInputTokens: 0, costUsd: 0.65 },
      } as never],
    },
    {
      runId: 'run-2', runNumber: 2, workflowVersionId: 'wfv-1',
      graph: {
        nodes,
        takenEdges: [
          { fromNodeRunId: 'b', fromNodeKey: 'start', outcome: 'next', toNodeKey: 'build', activationSequence: 1 },
          { fromNodeRunId: 'c', fromNodeKey: 'build', outcome: 'done', toNodeKey: 'end', activationSequence: 2 },
        ],
        activations: [
          { nodeRunId: 'r2-start', nodeKey: 'start', activationSequence: 1, iteration: 0, state: 'SUCCEEDED', selectedOutcome: 'next' },
          { nodeRunId: 'r2-build', nodeKey: 'build', activationSequence: 2, iteration: 0, state: 'SUCCEEDED', selectedOutcome: 'done', resultSummary: 'Fixed the reopen bug' },
          { nodeRunId: 'r2-end', nodeKey: 'end', activationSequence: 3, iteration: 0, state: 'SUCCEEDED' },
        ],
      },
      timeline: [],
    },
  ];
}

describe('mergeRunHistory (V12-12)', () => {
  it('merges every run per node, oldest first, and counts every activation and taken edge', () => {
    const merged = mergeRunHistory(runs());
    const build = merged.rowsByNode.get('build')!;
    expect(build.map(r => [r.runNumber, r.state])).toEqual([[1, 'FAILED'], [2, 'SUCCEEDED']]);
    expect(build[0]).toMatchObject({ durationMillis: 245_000, costUsd: 0.65, attempts: 1 });
    expect(merged.activations.filter(a => a.nodeKey === 'build')).toHaveLength(2);
    expect(merged.takenEdges.filter(t => t.fromNodeKey === 'start')).toHaveLength(2);
    expect(merged.missingFromLatest).toEqual([]);
  });

  it('labels the history of a run on another version and lists nodes the newest version lacks', () => {
    const input = runs();
    input[0].workflowVersionId = 'wfv-old';
    input[0].graph!.activations.push({ nodeRunId: 'r1-old', nodeKey: 'legacy-check', activationSequence: 3, iteration: 0, state: 'SUCCEEDED' });
    const merged = mergeRunHistory(input);
    expect(merged.rowsByNode.get('build')![0].olderVersion).toBe('wfv-old');
    expect(merged.rowsByNode.get('build')![1].olderVersion).toBeUndefined();
    expect(merged.missingFromLatest).toEqual(['legacy-check']);
  });

  it('formats durations', () => {
    expect([formatDuration(850), formatDuration(41_000), formatDuration(245_000), formatDuration(undefined)]).toEqual(['850 ms', '41s', '4m 05s', '—']);
  });
});

describe('RunHistoryPanel (V12-12)', () => {
  function setup() {
    const merged = mergeRunHistory(runs());
    return render(
      <RunHistoryPanel node={nodes[1]} edges={edges} takenEdges={merged.takenEdges} rows={merged.rowsByNode.get('build')!}
        renderDetail={id => <div data-testid="detail">detail of {id}</div>} onClose={() => {}} />,
    );
  }

  it('shows the node, its outcomes and both runs of build, the newest expanded', async () => {
    setup();
    const panel = screen.getByRole('complementary', { name: 'History of build' });
    expect(within(panel).getByText(/at most 3 rounds/)).toBeInTheDocument();
    expect(within(panel).getByTestId('outcome-count-done')).toHaveTextContent('1×');
    const run1 = within(panel).getByTestId('history-row-r1-build');
    const run2 = within(panel).getByTestId('history-row-r2-build');
    expect(run1).toHaveTextContent('Run 1');
    expect(run1).toHaveTextContent('backend/application.yml');
    expect(run1).toHaveTextContent('$0.65');
    expect(run2).toHaveTextContent('Fixed the reopen bug');
    expect(screen.getByTestId('detail')).toHaveTextContent('detail of r2-build');
    await userEvent.click(within(run1).getByRole('button'));
    expect(screen.getByTestId('detail')).toHaveTextContent('detail of r1-build');
  });

  it('has no automated accessibility violations', async () => {
    const { container } = setup();
    await expectNoAxeViolations(container);
  });
});
