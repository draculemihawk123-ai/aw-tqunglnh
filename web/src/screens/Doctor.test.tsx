import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { expectNoAxeViolations } from '../test/axe';
import { DoctorScreen } from './Doctor';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, doctor: vi.fn(), listAdapterBuilds: vi.fn(), probeAdapterBuild: vi.fn(), registerAdapterBuild: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: () => ({ token: 'test-session-token' }) }));

function renderDoctor() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <DoctorScreen />
    </QueryClientProvider>,
  );
}

const HEALTHY_REPORT = {
  status: 'HEALTHY',
  checks: [
    { name: 'process_liveness', category: 'LIVENESS', status: 'HEALTHY', detail: 'process is running and able to respond' },
    { name: 'database', category: 'READINESS', status: 'HEALTHY', detail: 'database is reachable' },
    { name: 'isolation_enforcement', category: 'CAPABILITY', status: 'HEALTHY', detail: 'both tiers enforceable' },
  ],
  restartRequired: false,
  links: {},
};

describe('DoctorScreen', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a loading state before the query resolves', async () => {
    vi.mocked(api.doctor).mockReturnValue(new Promise(() => {})); // never resolves
    vi.mocked(api.listAdapterBuilds).mockReturnValue(new Promise(() => {}));
    const { container } = renderDoctor();
    expect(container.querySelectorAll('[aria-hidden="true"]').length).toBeGreaterThan(0);
  });

  it('renders real checks grouped under their own category, and the overall HEALTHY summary', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();

    expect(await screen.findByRole('heading', { name: 'Liveness' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Readiness' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Capability' })).toBeInTheDocument();
    expect(screen.getByText('process liveness')).toBeInTheDocument();
    expect(screen.getByText('All checks passing — installation is ready.')).toBeInTheDocument();
  });

  it('a DEGRADED check shows its status as text, not color alone, plus its remediation', async () => {
    vi.mocked(api.doctor).mockResolvedValue({
      status: 'DEGRADED',
      checks: [{ name: 'provider:anthropic', category: 'CAPABILITY', status: 'DEGRADED', detail: 'no executable path configured', remediation: 'set provider_executables[anthropic]' }],
      restartRequired: false,
      links: {},
    } as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();

    expect(await screen.findByText('provider anthropic')).toBeInTheDocument();
    expect(screen.getByText('DEGRADED')).toBeInTheDocument();
    expect(screen.getByText('set provider_executables[anthropic]')).toBeInTheDocument();
    expect(screen.getByText('Some checks are degraded — review before relying on affected capabilities.')).toBeInTheDocument();
  });

  it('a BLOCKED overall status is announced distinctly from DEGRADED', async () => {
    vi.mocked(api.doctor).mockResolvedValue({
      status: 'BLOCKED',
      checks: [{ name: 'database', category: 'READINESS', status: 'BLOCKED', detail: 'unreachable', remediation: 'check the configured database path' }],
      restartRequired: false,
      links: {},
    } as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();

    expect(await screen.findByText('A check is blocked — this installation cannot fully operate until resolved.')).toBeInTheDocument();
  });

  it('shows a restart-required banner when the backend reports one', async () => {
    vi.mocked(api.doctor).mockResolvedValue({ ...HEALTHY_REPORT, restartRequired: true } as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();
    expect(await screen.findByText(/restart this installation/)).toBeInTheDocument();
  });

  it('shows an error state when the Doctor endpoint cannot be reached', async () => {
    vi.mocked(api.doctor).mockRejectedValue(new Error('fetch failed'));
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();
    expect(await screen.findByRole('alert')).toHaveTextContent(/Could not reach/);
  });

  it('"Re-run all checks" refetches both queries', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();
    await screen.findByText('All checks passing — installation is ready.');
    expect(api.doctor).toHaveBeenCalledTimes(1);

    await userEvent.click(screen.getByRole('button', { name: 'Re-run all checks' }));
    await waitFor(() => expect(api.doctor).toHaveBeenCalledTimes(2));
    expect(api.listAdapterBuilds).toHaveBeenCalledTimes(2);
  });

  it('renders real registered adapter builds', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({
      builds: [{ id: 'b1', providerKey: 'anthropic', executableContentHash: 'sha256:abc123', os: 'linux/amd64', protocolVersion: 'AK-Adapter/1.2' }],
    } as never);
    renderDoctor();

    expect(await screen.findByText('anthropic')).toBeInTheDocument();
    expect(screen.getByText('REGISTERED')).toBeInTheDocument();
    expect(screen.getByText('sha256:abc123')).toBeInTheDocument();
  });

  it('shows an empty state when no adapter builds are registered', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    renderDoctor();
    expect(await screen.findByText('No adapter builds registered yet')).toBeInTheDocument();
  });

  it('"Probe new build" opens the ADR-022 probe/confirm/register dialog, and a successful registration refreshes the list and shows a toast', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds)
      .mockResolvedValueOnce({ builds: [] } as never)
      .mockResolvedValueOnce({ builds: [{ id: 'b1', providerKey: 'claude', executableContentHash: 'sha256:abc', os: 'linux/amd64', protocolVersion: 'AK-Adapter/1.2' }] } as never);
    vi.mocked(api.probeAdapterBuild).mockResolvedValue({
      tuple: {
        providerKey: 'claude', executablePath: '/usr/local/bin/claude', executableContentHash: 'sha256:abc',
        protocolVersion: 'AK-Adapter/1.2', capabilityManifestHash: 'sha256:def', os: 'linux/amd64', toolchain: 'node-20.11', configIdentity: 'isolated-default',
      },
      nonce: 'n1', expiresAt: new Date(Date.now() + 300_000).toISOString(), signature: 'sig',
    } as never);
    vi.mocked(api.registerAdapterBuild).mockResolvedValue({ build: { id: 'b1' }, alreadyExisted: false } as never);

    renderDoctor();
    await screen.findByText('No adapter builds registered yet');

    await userEvent.click(screen.getByRole('button', { name: /Probe new build/ }));
    expect(await screen.findByRole('heading', { name: 'Probe adapter build' })).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/^Provider key/), 'claude');
    await userEvent.type(screen.getByLabelText(/^Executable path/), '/usr/local/bin/claude');
    await userEvent.type(screen.getByLabelText(/^Protocol version/), 'AK-Adapter/1.2');
    await userEvent.type(screen.getByLabelText(/^OS/), 'linux/amd64');
    await userEvent.type(screen.getByLabelText(/^Toolchain/), 'node-20.11');
    await userEvent.type(screen.getByLabelText(/^Config identity/), 'isolated-default');
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));

    await screen.findByRole('heading', { name: 'Confirm adapter build candidate' });
    await userEvent.click(screen.getByRole('button', { name: 'Confirm & register' }));

    expect(await screen.findByText('Adapter build for "claude" registered.')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Confirm adapter build candidate' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('claude')).toBeInTheDocument());
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.doctor).mockResolvedValue(HEALTHY_REPORT as never);
    vi.mocked(api.listAdapterBuilds).mockResolvedValue({ builds: [] } as never);
    const { container } = renderDoctor();
    await screen.findByText('All checks passing — installation is ready.');
    await expectNoAxeViolations(container);
  });
});
