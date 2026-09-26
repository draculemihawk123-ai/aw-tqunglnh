import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { expectNoAxeViolations } from '../test/axe';
import { RunDiagnosticsScreen } from './RunDiagnostics';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getRunDiagnostics: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function renderRunDiagnostics(isOffline = false) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <RunDiagnosticsScreen isOffline={isOffline} />
    </QueryClientProvider>,
  );
}

function diagFixture(overrides: Record<string, unknown> = {}) {
  return {
    runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
    blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [],
    repositoryWorkspaces: [], validActions: [],
    ...overrides,
  };
}

async function lookUp(projectId = 'proj-1', runId = 'run-1') {
  await userEvent.type(screen.getByLabelText('Project ID'), projectId);
  await userEvent.type(screen.getByLabelText('Run ID'), runId);
  await userEvent.click(screen.getByRole('button', { name: 'Look Up' }));
}

describe('RunDiagnosticsScreen (V7-16)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('never fetches anything until a real Project ID and Run ID are both supplied — there is no "runs needing attention" list', async () => {
    renderRunDiagnostics();
    expect(api.getRunDiagnostics).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Look Up' })).toBeDisabled();
  });

  it('looks up the real, identical GetRunDiagnostics authority Task Detail already uses, by ID', async () => {
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagFixture() as never);
    renderRunDiagnostics();

    await lookUp();

    expect(api.getRunDiagnostics).toHaveBeenCalledWith('proj-1', 'run-1', expect.objectContaining({ token: 'test-session-token' }));
    expect(await screen.findByText('run-1')).toBeInTheDocument();
  });

  it('renders real orphaned attempts, providers, and isolation diagnostics — the previously-unrendered "job/lease/recovery" fields', async () => {
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagFixture({
      orphanedAttempts: [{ attemptId: 'att-1', nodeRunId: 'nr-1', attemptNumber: 2, providerKey: 'anthropic', hasWriteLease: true }],
      providers: [{ adapterBuildId: 'build-1', providerKey: 'anthropic', providerConfigured: true }],
      isolation: [{ tier: 'OPERATOR_TRUSTED_LOCAL', enforceable: true }],
    }) as never);
    renderRunDiagnostics();

    await lookUp();

    expect(await screen.findByText('att-1')).toBeInTheDocument();
    expect(screen.getByText('HOLDS WRITE LEASE')).toBeInTheDocument();
    expect(screen.getAllByText('anthropic').length).toBeGreaterThan(0);
    expect(screen.getByText('CONFIGURED')).toBeInTheDocument();
    expect(screen.getByText('OPERATOR_TRUSTED_LOCAL')).toBeInTheDocument();
    expect(screen.getByText('ENFORCEABLE')).toBeInTheDocument();
  });

  it('never renders a fake rebuild/refresh/reconcile action — this screen only ever displays real diagnostics', async () => {
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagFixture() as never);
    const { container } = renderRunDiagnostics();

    await lookUp();
    await screen.findByText('run-1');

    const text = container.textContent ?? '';
    expect(text).not.toMatch(/rebuild projection/i);
    expect(text).not.toMatch(/refresh projection/i);
    expect(text).not.toMatch(/request reconcile/i);
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagFixture() as never);
    const { container } = renderRunDiagnostics();
    await lookUp();
    await screen.findByText('run-1');
    await expectNoAxeViolations(container);
  });
});
