import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { NewTaskDialog } from './NewTaskDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    projectRepositoriesList: vi.fn(), projectComponentsList: vi.fn(), listProjectDefinitions: vi.fn(), listProjectDefinitionVersions: vi.fn(),
    createChildWorkItem: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

function render(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

describe('NewTaskDialog (V12-14)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.projectRepositoriesList).mockResolvedValue({ repositories: [{ id: 'api', name: 'api', status: 'ACTIVE' }] } as never);
    vi.mocked(api.projectComponentsList).mockResolvedValue({ components: [{ id: 'c1', repositoryId: 'api', name: 'backend', path: 'backend' }] } as never);
    vi.mocked(api.listProjectDefinitions).mockResolvedValue({ definitions: [
      { id: 'cc-wf-task-delivery', name: 'Task delivery', kind: 'WORKFLOW' }, { id: 'cc-wf-bugfix', name: 'Bug fix', kind: 'WORKFLOW' },
    ] } as never);
    vi.mocked(api.listProjectDefinitionVersions).mockImplementation(async (_p, _k, id) => ({ items: [
      { id: `${id}-v1`, versionNumber: 1 }, { id: `${id}-v2`, versionNumber: 2 },
    ] }) as never);
    vi.mocked(api.createChildWorkItem).mockResolvedValue({ workItemId: 'wi-child' } as never);
  });

  it('creates a bug-fix child with a default join policy, the newest version of the suggested workflow and a scope warning', async () => {
    const onCreated = vi.fn();
    render(<NewTaskDialog projectId="p" parentWorkItemId="wi-root" onClose={() => {}} onCreated={onCreated} />);
    const dialog = await screen.findByRole('dialog', { name: 'New task' });
    await userEvent.selectOptions(within(dialog).getByLabelText('Kind of work'), 'bugfix');
    await userEvent.type(within(dialog).getByLabelText(/Title/), 'P1-01: reopen keeps the status');
    const behavior = within(dialog).getByLabelText(/Behavior/);
    await userEvent.clear(behavior);
    await userEvent.type(behavior, 'Reopen sets CLOSED; see backend/src/main/resources/application.yml');
    await userEvent.selectOptions(within(dialog).getByLabelText('Add a repository'), 'api');
    await userEvent.type(within(dialog).getByLabelText(/Folders/), 'backend/src/main/java');

    const bugfix = await within(dialog).findByRole('radio', { name: /Bug fix/ });
    expect(bugfix.closest('label')).toHaveTextContent('suggested');
    expect(bugfix.closest('label')).toHaveTextContent('red test first');
    await userEvent.click(bugfix);
    expect(within(dialog).getByTestId('new-task-problems')).toHaveTextContent('backend/src/main/resources/application.yml');

    await userEvent.click(within(dialog).getByRole('button', { name: 'Create task' }));
    await waitFor(() => expect(api.createChildWorkItem).toHaveBeenCalledTimes(1));
    const [, parent, body] = vi.mocked(api.createChildWorkItem).mock.calls[0];
    expect(parent).toBe('wi-root');
    expect(body).toMatchObject({
      title: 'P1-01: reopen keeps the status', parentJoinPolicy: 'ALL_CHILDREN_DONE',
      effectiveScope: [{ repositoryId: 'api', access: 'WRITE', pathScopes: ['backend/src/main/java'] }],
      contract: { workflowVersionId: 'cc-wf-bugfix-v2', riskLevel: 'LOW' },
    });
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('wi-child'));
  });

  it('refuses to create without a workflow or a title, and reads a pasted JSON', async () => {
    render(<NewTaskDialog projectId="p" parentWorkItemId="wi-root" onClose={() => {}} onCreated={() => {}} />);
    const dialog = await screen.findByRole('dialog', { name: 'New task' });
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create task' }));
    expect(within(dialog).getAllByRole('alert').map(a => a.textContent).join(' ')).toMatch(/title is required/);
    expect(api.createChildWorkItem).not.toHaveBeenCalled();

    await userEvent.click(within(dialog).getByRole('button', { name: 'Paste JSON' }));
    const area = within(dialog).getByLabelText(/work-item JSON/);
    await userEvent.click(area);
    await userEvent.paste(JSON.stringify({ title: 'From JSON', contract: { behavior: 'Do X', acceptanceCriteria: [{ description: 'X done' }] } }));
    await userEvent.click(within(dialog).getByRole('button', { name: 'Use this JSON' }));
    expect(within(dialog).getByLabelText(/Title/)).toHaveValue('From JSON');
  });
});
