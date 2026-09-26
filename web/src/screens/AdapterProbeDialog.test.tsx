import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { AdapterProbeDialog } from './AdapterProbeDialog';

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, probeAdapterBuild: vi.fn(), registerAdapterBuild: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: () => ({ token: 'test-session-token' }) }));

const CANDIDATE_TOKEN = {
  tuple: {
    providerKey: 'claude',
    executablePath: '/usr/local/bin/claude',
    executableContentHash: 'sha256:abc123',
    protocolVersion: 'AK-Adapter/1.2',
    capabilityManifestHash: 'sha256:def456',
    os: 'linux/amd64',
    toolchain: 'node-20.11',
    configIdentity: 'isolated-default',
  },
  nonce: 'nonce-1',
  expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
  signature: 'sig-1',
};

async function fillRequiredFields() {
  await userEvent.type(screen.getByLabelText(/^Provider key/), 'claude');
  await userEvent.type(screen.getByLabelText(/^Executable path/), '/usr/local/bin/claude');
  await userEvent.type(screen.getByLabelText(/^Protocol version/), 'AK-Adapter/1.2');
  await userEvent.type(screen.getByLabelText(/^OS/), 'linux/amd64');
  await userEvent.type(screen.getByLabelText(/^Toolchain/), 'node-20.11');
  await userEvent.type(screen.getByLabelText(/^Config identity/), 'isolated-default');
}

describe('AdapterProbeDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows required-field errors when Probe is clicked with an empty form', async () => {
    const onClose = vi.fn();
    render(<AdapterProbeDialog onClose={onClose} onRegistered={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    expect(await screen.findAllByText('required')).not.toHaveLength(0);
    expect(api.probeAdapterBuild).not.toHaveBeenCalled();
  });

  it('rejects a candidate with supportsStart unchecked before ever calling the API', async () => {
    render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByLabelText('Supports start'));
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    expect(await screen.findByText(/never a valid candidate/)).toBeInTheDocument();
    expect(api.probeAdapterBuild).not.toHaveBeenCalled();
  });

  it('probes, then shows the server-measured candidate for review before registering', async () => {
    vi.mocked(api.probeAdapterBuild).mockResolvedValue(CANDIDATE_TOKEN as never);
    render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));

    expect(await screen.findByRole('heading', { name: 'Confirm adapter build candidate' })).toBeInTheDocument();
    expect(screen.getByText('sha256:abc123')).toBeInTheDocument();
    expect(screen.getByText('sha256:def456')).toBeInTheDocument();
    expect(api.probeAdapterBuild).toHaveBeenCalledWith(
      expect.objectContaining({ providerKey: 'claude', executablePath: '/usr/local/bin/claude' }),
      expect.anything(),
    );
  });

  it('registers the exact candidate token and manifest the probe returned, then reports success and closes', async () => {
    vi.mocked(api.probeAdapterBuild).mockResolvedValue(CANDIDATE_TOKEN as never);
    vi.mocked(api.registerAdapterBuild).mockResolvedValue({ build: { id: 'b1' }, alreadyExisted: false } as never);
    const onClose = vi.fn();
    const onRegistered = vi.fn();
    render(<AdapterProbeDialog onClose={onClose} onRegistered={onRegistered} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    await screen.findByRole('heading', { name: 'Confirm adapter build candidate' });

    await userEvent.click(screen.getByRole('button', { name: 'Confirm & register' }));

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(onRegistered).toHaveBeenCalledWith(expect.objectContaining({ intent: 'success' }));
    expect(api.registerAdapterBuild).toHaveBeenCalledWith(
      { token: CANDIDATE_TOKEN, capabilityManifest: expect.objectContaining({ supportsStart: true }) },
      expect.anything(),
    );
  });

  it('shows a probe API error inline without ever reaching the candidate view', async () => {
    vi.mocked(api.probeAdapterBuild).mockRejectedValue(new api.ApiError(409, 'CONFLICT', 'idempotency key reused with a different request'));
    render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('idempotency key reused with a different request');
    expect(screen.queryByRole('heading', { name: 'Confirm adapter build candidate' })).not.toBeInTheDocument();
  });

  it('a register error (e.g. expired token) lets the operator go back and probe again', async () => {
    vi.mocked(api.probeAdapterBuild).mockResolvedValue(CANDIDATE_TOKEN as never);
    vi.mocked(api.registerAdapterBuild).mockRejectedValue(
      new api.ApiError(409, 'CONFLICT', 'candidate token has expired; probe again to obtain a fresh token'),
    );
    render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    await screen.findByRole('heading', { name: 'Confirm adapter build candidate' });
    await userEvent.click(screen.getByRole('button', { name: 'Confirm & register' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(/expired/);
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('heading', { name: 'Probe adapter build' })).toBeInTheDocument();
    // Form values are preserved across the round trip back to step 1.
    expect(screen.getByLabelText(/^Provider key/)).toHaveValue('claude');
  });

  it('has no automated accessibility violations on the probe form', async () => {
    const { container } = render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await expectNoAxeViolations(container);
  });

  it('has no automated accessibility violations on the candidate review step', async () => {
    vi.mocked(api.probeAdapterBuild).mockResolvedValue(CANDIDATE_TOKEN as never);
    const { container } = render(<AdapterProbeDialog onClose={vi.fn()} onRegistered={vi.fn()} />);
    await fillRequiredFields();
    await userEvent.click(screen.getByRole('button', { name: 'Probe' }));
    await screen.findByRole('heading', { name: 'Confirm adapter build candidate' });
    await expectNoAxeViolations(container);
  });
});
