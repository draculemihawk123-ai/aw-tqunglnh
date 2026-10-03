import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { RepositoryReadinessDialog } from './RepositoryReadinessDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    repositoriesReadiness: vi.fn(),
    repositoriesReadinessProfileSet: vi.fn(),
    repositoriesReadinessVerify: vi.fn(),
    repositoriesReadinessAcceptException: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const REPOSITORY = {
  id: 'repo-1', projectId: 'proj-1', name: 'todo-app', remoteLocator: '/tmp/todo', defaultRef: 'main', status: 'ACTIVE' as const, version: 3,
};

const PROFILE = {
  version: 2,
  setup: { executable: 'npm', argv: ['ci'], timeoutSeconds: 600 },
  verification: { executable: 'npm', argv: ['test'], timeoutSeconds: 300 },
};

const FAILED_WORKSPACE = {
  repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws-1', generation: 1, workspaceState: 'READY',
  baselineState: 'FAIL' as const, admitsWriters: false,
  reason: 'the VERIFICATION baseline of readiness profile version 2 failed (PRE_EXISTING_FAILURE)',
  attempt: {
    attemptId: 'attempt-9', stage: 'VERIFICATION', outcome: 'RED' as const, failureKind: 'PRE_EXISTING_FAILURE', exitCode: 1,
    durationMs: 1200, stderrExcerpt: '2 tests failed', profileVersion: 2, createdAt: '2026-10-03T08:00:00Z',
  },
};

describe('RepositoryReadinessDialog', () => {
  beforeEach(() => vi.clearAllMocks());

  it('says that a repository without a profile is not gated', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', workspaces: [] } as never);
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    expect(await screen.findByText(/No readiness profile/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Verify again' })).not.toBeInTheDocument();
  });

  it('shows the profile, each workspace baseline state and why writers are blocked', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', profile: PROFILE, workspaces: [FAILED_WORKSPACE] } as never);
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    expect(await screen.findByText('npm test')).toBeInTheDocument();
    expect(screen.getByText('npm ci')).toBeInTheDocument();
    expect(screen.getByText('writers blocked')).toBeInTheDocument();
    expect(screen.getByText(/PRE_EXISTING_FAILURE, exit 1/)).toBeInTheDocument();
    expect(screen.getByText('2 tests failed')).toBeInTheDocument();
  });

  it('saves the profile with the commands the operator typed and reports the started baselines', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', workspaces: [] } as never);
    vi.mocked(api.repositoriesReadinessProfileSet).mockResolvedValue({ repositoryId: 'repo-1', profileVersion: 1, baselineJobsEnqueued: 2 } as never);
    const onDone = vi.fn();
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={onDone} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Set profile' }));

    await userEvent.type(screen.getByLabelText(/Verification command executable/), 'mvn');
    await userEvent.type(screen.getByLabelText(/Verification command arguments/), '-q test');
    await userEvent.click(screen.getByRole('button', { name: 'Save profile' }));

    await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1));
    expect(api.repositoriesReadinessProfileSet).toHaveBeenCalledWith(
      'repo-1',
      { setup: undefined, verification: { executable: 'mvn', argv: ['-q', 'test'], timeoutSeconds: 600 } },
      expect.anything(),
    );
    expect(onDone).toHaveBeenCalledWith('Readiness profile v1 saved for todo-app; 2 baseline run(s) started.');
  });

  it('requires a verification executable before calling the API', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', workspaces: [] } as never);
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Set profile' }));
    await userEvent.click(screen.getByRole('button', { name: 'Save profile' }));
    expect(await screen.findByText('required')).toBeInTheDocument();
    expect(api.repositoriesReadinessProfileSet).not.toHaveBeenCalled();
  });

  it('runs the baseline again', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', profile: PROFILE, workspaces: [FAILED_WORKSPACE] } as never);
    vi.mocked(api.repositoriesReadinessVerify).mockResolvedValue({ repositoryId: 'repo-1', profileVersion: 2, baselineJobsEnqueued: 1 } as never);
    const onDone = vi.fn();
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={onDone} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Verify again' }));
    await waitFor(() => expect(onDone).toHaveBeenCalledWith('Baseline started on 1 workspace(s) of todo-app.'));
    expect(api.repositoriesReadinessVerify).toHaveBeenCalledWith('repo-1', {}, expect.anything());
  });

  it('accepts a failed baseline only with a reason, naming the failed attempt', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', profile: PROFILE, workspaces: [FAILED_WORKSPACE] } as never);
    vi.mocked(api.repositoriesReadinessAcceptException).mockResolvedValue({ exceptionId: 'exc-1' } as never);
    const onDone = vi.fn();
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={onDone} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Accept exception…' }));

    const accept = screen.getByRole('button', { name: 'Accept failure' });
    expect(accept).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/Reason for accepting this failure/), 'known flaky suite, tracked in TODO-12');
    await userEvent.click(accept);

    await waitFor(() => expect(onDone).toHaveBeenCalledWith('Baseline failure accepted for todo-app.'));
    expect(api.repositoriesReadinessAcceptException).toHaveBeenCalledWith(
      'repo-1', { baselineAttemptId: 'attempt-9', reason: 'known flaky suite, tracked in TODO-12' }, expect.anything(),
    );
  });

  it('offers no exception for a workspace whose baseline passed', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({
      repositoryId: 'repo-1', profile: PROFILE,
      workspaces: [{ ...FAILED_WORKSPACE, baselineState: 'PASS', admitsWriters: true, reason: undefined, attempt: { ...FAILED_WORKSPACE.attempt, outcome: 'GREEN', failureKind: undefined, exitCode: 0, stderrExcerpt: undefined } }],
    } as never);
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    expect(await screen.findByText('writers admitted')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Accept exception…' })).not.toBeInTheDocument();
  });

  it('shows an API error inline when the profile is rejected', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', workspaces: [] } as never);
    vi.mocked(api.repositoriesReadinessProfileSet).mockRejectedValue(new api.ApiError(400, 'INVALID_REQUEST', 'verification: command timeout must be greater than zero'));
    render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Set profile' }));
    await userEvent.type(screen.getByLabelText(/Verification command executable/), 'mvn');
    await userEvent.click(screen.getByRole('button', { name: 'Save profile' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('command timeout must be greater than zero');
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.repositoriesReadiness).mockResolvedValue({ repositoryId: 'repo-1', profile: PROFILE, workspaces: [FAILED_WORKSPACE] } as never);
    const { container } = render(<RepositoryReadinessDialog repository={REPOSITORY} onClose={vi.fn()} onDone={vi.fn()} />);
    await screen.findByText('npm test');
    await expectNoAxeViolations(container);
  });
});
