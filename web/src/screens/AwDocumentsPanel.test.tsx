import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { AwDocumentsPanel } from './AwDocumentsPanel';
import { TaskDocumentsPanel } from './TaskDocumentsPanel';
import { childRequestForTask } from '../api/tasksJson';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, listAwDocuments: vi.fn(), exportAwDocuments: vi.fn(), getProjectTaskDocuments: vi.fn(), setProjectTaskDocuments: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const repo = { repositoryWorkspaceId: 'rw1', repositoryId: 'api', state: 'READY', version: 3 } as unknown as RepositoryWorkspaceState;
const wrap = (ui: React.ReactNode) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
};

describe('task documents kept in aw (V12-33)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listAwDocuments).mockResolvedValue({ repositoryWorkspaceId: 'rw1', documents: [
      { path: '.aw/features/quote/tasks/T-01/plan.md', size: 9, content: '## Files\n' },
    ] } as never);
  });

  it('lists the documents, shows one, and exports with an overwrite after a conflict', async () => {
    vi.mocked(api.exportAwDocuments)
      .mockRejectedValueOnce(new api.ApiError(409, 'CONFLICT', 'conflicts', [{ field: 'docs/features/quote/tasks/T-01/plan.md', message: 'x' }]))
      .mockResolvedValueOnce({ exported: ['docs/features/quote/tasks/T-01/plan.md'], unchanged: [], conflicts: ['docs/features/quote/tasks/T-01/plan.md'] } as never);
    wrap(<AwDocumentsPanel projectId="p" repo={repo} />);
    await userEvent.click(await screen.findByRole('button', { name: 'quote/tasks/T-01/plan.md' }));
    expect(screen.getByText('## Files')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Close' }));
    await userEvent.click(screen.getByRole('button', { name: 'Export to repo' }));
    expect(await screen.findByTestId('aw-export-conflicts')).toHaveTextContent('docs/features/quote/tasks/T-01/plan.md');
    await userEvent.click(screen.getByRole('button', { name: 'Overwrite' }));
    await waitFor(() => expect(api.exportAwDocuments).toHaveBeenLastCalledWith('p', 'rw1', { overwrite: true }, expect.anything()));
    expect(await screen.findByTestId('aw-export-done')).toHaveTextContent('Exported 1 file to docs/features/');
  });

  it('shows aw\'s copies of a released worktree, without export', async () => {
    vi.mocked(api.listAwDocuments).mockResolvedValue({ repositoryWorkspaceId: 'rw1', workspaceState: 'RELEASED', documents: [
      { path: '.aw/features/quote/tasks/T-01/frame.md', size: 8, content: '# Frame\n', source: 'SNAPSHOT', snapshotAt: '2026-11-04T10:00:00Z' },
    ] } as never);
    wrap(<AwDocumentsPanel projectId="p" repo={{ ...repo, state: 'RELEASED' } as RepositoryWorkspaceState} />);
    expect(await screen.findByText("(aw's copy)")).toBeInTheDocument();
    expect(screen.getByText(/the worktree is released: aw's copies/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Export to repo' })).toBeNull();
  });

  it('hides itself when the worktree has no such document', async () => {
    vi.mocked(api.listAwDocuments).mockResolvedValue({ repositoryWorkspaceId: 'rw1', documents: [] } as never);
    const { container } = wrap(<AwDocumentsPanel projectId="p" repo={repo} />);
    await waitFor(() => expect(api.listAwDocuments).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it('switches the project location', async () => {
    vi.mocked(api.getProjectTaskDocuments).mockResolvedValue({ projectId: 'p', location: 'REPO', directoryPattern: 'docs/features/<feature>/tasks/<task>' } as never);
    vi.mocked(api.setProjectTaskDocuments).mockResolvedValue({} as never);
    wrap(<TaskDocumentsPanel projectId="p" />);
    await userEvent.click(await screen.findByLabelText(/Only in aw/));
    await waitFor(() => expect(api.setProjectTaskDocuments).toHaveBeenCalledWith('p', { location: 'AW' }, expect.anything()));
  });

  it('names the aw directory in the task contract when the project keeps documents in aw', () => {
    const task = { id: 'T-01', title: 'Quote', behavior: 'B.', pathScopes: ['src'], riskLevel: 'LOW', acceptanceCriteria: ['a'], lane: 'fast' };
    const ctx = { slug: 'quote', docsRepository: 'api', repositories: ['api'], workflowVersionId: 'v1' };
    expect(childRequestForTask(task as never, ctx).contract.behavior).not.toContain('.aw/');
    expect(childRequestForTask(task as never, { ...ctx, taskDocuments: 'AW' }).contract.behavior)
      .toContain('Thư mục tài liệu của task (frame.md, plan.md): .aw/features/quote/tasks/T-01/');
  });
});
