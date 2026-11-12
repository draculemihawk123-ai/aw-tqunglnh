import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import * as inspection from '../api/workspaceinspection';
import { CreateTasksDialog } from './CreateTasksDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkspaceSetState: vi.fn(), listProjectDefinitions: vi.fn(), listProjectDefinitionVersions: vi.fn(),
    createChildWorkItem: vi.fn(), markWorkItemReady: vi.fn(), setWorkItemQueue: vi.fn() };
});
vi.mock('../api/workspaceinspection', async () => {
  const actual = await vi.importActual<typeof import('../api/workspaceinspection')>('../api/workspaceinspection');
  return { ...actual, fetchWorkspaceSource: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const TASKS = JSON.stringify([
  { id: 'T-01', title: 'Status API', pathScopes: ['backend'], riskLevel: 'LOW', lane: 'fast', behavior: 'GET status', acceptanceCriteria: ['AC-1: 200'] },
  { id: 'T-02', title: 'Badge', pathScopes: ['frontend'], riskLevel: 'HIGH', lane: 'full', behavior: 'Show it', acceptanceCriteria: ['AC-2: badge'] },
]);

describe('CreateTasksDialog (V12-22)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({ repositoryWorkspaces: [
      { repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws', repositoryId: 'api', generation: 1, state: 'READY', version: 2, currentRevision: 'abc', hasActiveWriteLease: false, validActions: [] },
    ] } as never);
    vi.mocked(api.listProjectDefinitions).mockResolvedValue({ definitions: [
      { id: 'p-wf-task-delivery', name: 'Task delivery' }, { id: 'p-wf-task-delivery-plus', name: 'Task delivery plus' },
    ] } as never);
    vi.mocked(api.listProjectDefinitionVersions).mockImplementation(async (_p, _k, id) => ({ items: [{ id: `${id}-v4`, versionNumber: 4 }] }) as never);
    vi.mocked(inspection.fetchWorkspaceSource).mockResolvedValue({ content: TASKS, binary: false } as never);
    let n = 0;
    vi.mocked(api.createChildWorkItem).mockImplementation(async () => ({ workItemId: `wi-${++n}` }) as never);
    vi.mocked(api.markWorkItemReady).mockResolvedValue({} as never);
    vi.mocked(api.setWorkItemQueue).mockResolvedValue({} as never);
  });

  it('reads tasks.json from the worktree, suggests a workflow by risk, then creates, readies and queues the tasks in order', async () => {
    const onCreated = vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><CreateTasksDialog projectId="p" parentWorkItemId="wi-root" familyId="f" onClose={() => {}} onCreated={onCreated} /></QueryClientProvider>);
    const dialog = await screen.findByRole('dialog', { name: 'Create tasks from tasks.json' });
    await userEvent.type(within(dialog).getByLabelText('Feature slug'), 'order-status');
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Read tasks.json' })).toBeEnabled());
    await userEvent.click(within(dialog).getByRole('button', { name: 'Read tasks.json' }));
    expect(inspection.fetchWorkspaceSource).toHaveBeenCalledWith('p', 'rw-1', expect.objectContaining({ path: 'docs/features/order-status/tasks.json', revision: 'abc' }));

    await within(dialog).findByTestId('task-row-T-01');
    expect(within(dialog).getByLabelText('Workflow of T-01')).toHaveValue('p-wf-task-delivery-v4');
    expect(within(dialog).getByLabelText('Workflow of T-02')).toHaveValue('p-wf-task-delivery-plus-v4');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create 2 tasks' }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(['wi-1', 'wi-2']));
    const calls = vi.mocked(api.createChildWorkItem).mock.calls;
    expect(calls.map(c => (c[2] as { title: string }).title)).toEqual(['T-01: Status API', 'T-02: Badge']);
    expect(calls[0][1]).toBe('wi-root');
    expect((calls[1][2] as { contract: { workflowVersionId: string } }).contract.workflowVersionId).toBe('p-wf-task-delivery-plus-v4');
    expect(api.markWorkItemReady).toHaveBeenCalledWith('wi-1', {}, expect.objectContaining({ ifMatch: '"1"' }));
    expect(vi.mocked(api.setWorkItemQueue).mock.calls.map(c => c[0])).toEqual(['wi-1', 'wi-2']);
  });
});
