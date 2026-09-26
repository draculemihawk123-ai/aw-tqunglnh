import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { CreateWorkItemDialog } from './CreateWorkItemDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    createRootWorkItem: vi.fn(), projectRepositoriesList: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const REPOS = {
  repositories: [
    { id: 'core-api', projectId: 'proj-1', name: 'core-api', remoteLocator: 'git@x:core-api.git', defaultRef: 'main', status: 'ACTIVE', version: 1 },
    { id: 'stale-repo', projectId: 'proj-1', name: 'stale-repo', remoteLocator: 'git@x:stale.git', defaultRef: 'main', status: 'BLOCKED', version: 1 },
  ],
};

const RESULT = {
  workItemId: 'wi-99', projectId: 'proj-1', familyId: 'fam-99', workspaceSetId: 'ws-99', status: 'BACKLOG',
  provisionedRepositories: [{ repositoryId: 'core-api', provisionJobId: 'job-1' }],
};

/** The "Add repository" button starts disabled until the real ACTIVE-repositories query resolves — wait for that before clicking, rather than clicking a disabled button (a silent no-op). */
async function addRepositoryGrant() {
  const button = await screen.findByRole('button', { name: /Add repository/ });
  await waitFor(() => expect(button).not.toBeDisabled());
  await userEvent.click(button);
}

describe('CreateWorkItemDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
  });

  it('only offers ACTIVE repositories in the scope-grant picker, never a BLOCKED one', async () => {
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);
    await addRepositoryGrant();

    const repoSelect = screen.getByRole('combobox', { name: /^Repository/ }) as HTMLSelectElement;
    const optionValues = Array.from(repoSelect.options).map(o => o.value);
    expect(optionValues).toEqual(['core-api']);
  });

  it('rejects submission with no title and no scope grant, without ever calling the API', async () => {
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Create' }));

    expect(screen.getByText('At least one repository scope grant is required.')).toBeInTheDocument();
    expect(api.createRootWorkItem).not.toHaveBeenCalled();
  });

  it('submits a real CreateRootWorkItemRequest shape with a filled title and scope grant, no contract', async () => {
    vi.mocked(api.createRootWorkItem).mockResolvedValue(RESULT as never);
    const onCreated = vi.fn();
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={onCreated} />);

    await userEvent.type(screen.getByLabelText(/Title/), 'Add distributed tracing');
    await addRepositoryGrant();
    await userEvent.type(screen.getByLabelText(/Reason/), 'implement tracing');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(api.createRootWorkItem).toHaveBeenCalledWith(
      'proj-1',
      {
        title: 'Add distributed tracing',
        initialScope: [{ repositoryId: 'core-api', access: 'WRITE', reason: 'implement tracing', pathScopes: [] }],
        contract: undefined,
      },
      expect.objectContaining({ token: 'test-session-token' }),
    ));
    expect(onCreated).toHaveBeenCalledWith(RESULT);
  });

  it('including a contract section sends real WHAT/DONE/out-of-scope/workflow-version fields', async () => {
    vi.mocked(api.createRootWorkItem).mockResolvedValue(RESULT as never);
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);

    await userEvent.type(screen.getByLabelText(/Title/), 'Add tracing');
    await addRepositoryGrant();
    await userEvent.type(screen.getByLabelText(/Reason/), 'implement tracing');

    await userEvent.click(screen.getByLabelText(/Add a readiness contract now/));
    await userEvent.type(screen.getByLabelText('Behavior (WHAT)'), 'Spans reach Jaeger');
    await userEvent.click(screen.getByRole('button', { name: /Add criterion/ }));
    await userEvent.type(screen.getByLabelText('Description'), 'Trace spans visible in Jaeger');
    await userEvent.type(screen.getByLabelText('Exclusions (out-of-scope)'), 'legacy-worker');
    await userEvent.type(screen.getByLabelText('Workflow version ID'), 'wfv-42');

    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(api.createRootWorkItem).toHaveBeenCalledWith(
      'proj-1',
      expect.objectContaining({
        contract: expect.objectContaining({
          schemaVersion: 1,
          behavior: 'Spans reach Jaeger',
          acceptanceCriteria: [{ description: 'Trace spans visible in Jaeger', verificationRef: undefined }],
          exclusions: ['legacy-worker'],
          workflowVersionId: 'wfv-42',
        }),
      }),
      expect.anything(),
    ));
  });

  it('an invalid schema version (blank or non-positive) is omitted from the contract rather than sent as garbage', async () => {
    vi.mocked(api.createRootWorkItem).mockResolvedValue(RESULT as never);
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);

    await userEvent.type(screen.getByLabelText(/Title/), 'Add tracing');
    await addRepositoryGrant();
    await userEvent.type(screen.getByLabelText(/Reason/), 'implement tracing');
    await userEvent.click(screen.getByLabelText(/Add a readiness contract now/));
    await userEvent.clear(screen.getByLabelText('Schema version'));
    await userEvent.type(screen.getByLabelText('Schema version'), '0');

    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(api.createRootWorkItem).toHaveBeenCalledWith(
      'proj-1',
      expect.objectContaining({ contract: expect.objectContaining({ schemaVersion: undefined }) }),
      expect.anything(),
    ));
  });

  it('renders real per-field diagnostics from ApiError.details on a failed create', async () => {
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    vi.mocked(api.createRootWorkItem).mockRejectedValue(
      new ApiError(400, 'INVALID_CONTRACT', 'contract invalid', [{ field: 'contract.acceptanceCriteria[0].description', message: 'must not be blank' }]),
    );
    render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);

    await userEvent.type(screen.getByLabelText(/Title/), 'Add tracing');
    await addRepositoryGrant();
    await userEvent.type(screen.getByLabelText(/Reason/), 'implement tracing');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('must not be blank')).toBeInTheDocument();
    expect(screen.getByText('contract.acceptanceCriteria[0].description')).toBeInTheDocument();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<CreateWorkItemDialog projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);
    await screen.findByLabelText(/Title/);
    await expectNoAxeViolations(container);
  });
});
