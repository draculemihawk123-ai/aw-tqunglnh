import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { RunAppPanel, parseProfile, type RunAppStatus } from './RunAppPanel';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getRunAppStatus: vi.fn(), setRunAppProfile: vi.fn(), startRunApp: vi.fn(), stopRunApp: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const repo = { repositoryWorkspaceId: 'rw1', repositoryId: 'api', state: 'READY', version: 3 } as unknown as RepositoryWorkspaceState;
const profile = { processes: [{ name: 'backend', argv: ['./mvnw', 'spring-boot:run'], port: 8080, secretNames: ['DB_PASSWORD'] }], previewUrl: 'http://localhost:3000', stopAfterMinutes: 120 };
const base: RunAppStatus = { repositoryWorkspaceId: 'rw1', repositoryId: 'api', workspaceState: 'READY', profile: { repositoryId: 'api', version: 0 }, logs: [], startBlockers: ['runapp: this repository has no Run app profile'] };

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><RunAppPanel projectId="p" repo={repo} /></QueryClientProvider>);
}

describe('RunAppPanel (V12-30)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.setRunAppProfile).mockResolvedValue({} as never);
    vi.mocked(api.startRunApp).mockResolvedValue({} as never);
    vi.mocked(api.stopRunApp).mockResolvedValue({} as never);
  });

  it('refuses a shell string or an empty profile', () => {
    expect(parseProfile('{"processes":[{"name":"a","argv":"pnpm dev"}]}').error).toMatch(/argv list/);
    expect(parseProfile('{"processes":[]}').error).toMatch(/at least one/);
    expect(parseProfile('nope').error).toBeTruthy();
    expect(parseProfile(JSON.stringify(profile)).profile?.processes[0].name).toBe('backend');
  });

  it('sets up a profile from the example and saves it with the version read', async () => {
    vi.mocked(api.getRunAppStatus).mockResolvedValue(base as never);
    mount();
    expect(await screen.findByText('no profile for this repository yet')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: 'Set up…' }));
    const area = screen.getByLabelText('Profile JSON');
    expect((area as HTMLTextAreaElement).value).toContain('spring-boot:run');
    fireEvent.change(area, { target: { value: JSON.stringify(profile) } });
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.setRunAppProfile).toHaveBeenCalledWith('p', 'api', { profile, expectedVersion: 0 }, expect.objectContaining({ idempotencyKey: expect.any(String) })));
  });

  it('starts, shows the running state, the preview link and the logs, and stops', async () => {
    vi.mocked(api.getRunAppStatus).mockResolvedValue({ ...base, profile: { repositoryId: 'api', version: 1, profile }, startBlockers: [] } as never);
    mount();
    await userEvent.click(await screen.findByRole('button', { name: 'Start' }));
    await waitFor(() => expect(api.startRunApp).toHaveBeenCalledWith('p', 'rw1', {}, expect.anything()));

    vi.mocked(api.getRunAppStatus).mockResolvedValue({
      ...base, profile: { repositoryId: 'api', version: 1, profile }, startBlockers: ['already running'],
      session: { id: 's1', state: 'RUNNING', active: true, requestedBy: 'op', deadlineAt: '2026-11-03T12:00:00Z' },
      logs: [{ name: 'backend', port: 8080, tail: 'Started in 3s\ndb=[REDACTED]' }],
    } as never);
    cleanupAndRemount();
    expect(await screen.findByText(/running · stops at/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open preview' })).toHaveAttribute('href', 'http://localhost:3000');
    await userEvent.click(screen.getByRole('button', { name: 'Logs' }));
    expect(screen.getByTestId('run-app-log-backend')).toHaveTextContent('db=[REDACTED]');
    await userEvent.click(screen.getByRole('button', { name: 'Stop' }));
    await waitFor(() => expect(api.stopRunApp).toHaveBeenCalledWith('p', 'rw1', {}, expect.anything()));
  });

  it('shows why the last session failed', async () => {
    vi.mocked(api.getRunAppStatus).mockResolvedValue({
      ...base, profile: { repositoryId: 'api', version: 1, profile }, startBlockers: [],
      session: { id: 's1', state: 'FAILED', active: false, requestedBy: 'op', deadlineAt: '2026-11-03T12:00:00Z', problem: 'port 8080 (backend) is in use' },
    } as never);
    mount();
    expect(await screen.findByTestId('run-app-state')).toHaveTextContent('failed: port 8080 (backend) is in use');
  });
});

function cleanupAndRemount() {
  document.body.innerHTML = '';
  mount();
}
