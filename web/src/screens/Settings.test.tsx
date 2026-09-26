import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import * as settingsApi from '../api/settings';
import { expectNoAxeViolations } from '../test/axe';
import { SettingsScreen } from './Settings';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getSafeSettings: vi.fn() };
});
vi.mock('../api/session', () => ({
  withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }),
  getSessionToken: () => 'test-session-token',
}));
vi.mock('../api/settings', async () => {
  const actual = await vi.importActual<typeof import('../api/settings')>('../api/settings');
  return { ...actual, updateSafeSettings: vi.fn() };
});

function renderSettings(isOffline = false) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <SettingsScreen isOffline={isOffline} />
    </QueryClientProvider>,
  );
}

function stringField(value: string, source = 'sqlite', masked = '') {
  return { effective: value, source, maskedByStartupSource: masked };
}

function settingsDetailFixture(overrides: Record<string, unknown> = {}) {
  return {
    desired: {
      managedWorkspaceRoot: '/var/ak/workspaces', managedArtifactRoot: '/var/ak/artifacts',
      evidenceRetention: '168h0m0s', processOutputLimit: 1048576,
      providerExecutablePath: '/usr/local/bin/claude', providerDefaultModel: 'claude-sonnet-5',
      providerCredentialRef: '[REDACTED]',
    },
    version: 3, updatedAt: '2026-09-27T00:00:00Z', updatedBy: 'local-operator', restartRequired: false,
    effective: {
      managedWorkspaceRoot: stringField('/var/ak/workspaces'), managedArtifactRoot: stringField('/var/ak/artifacts'),
      evidenceRetention: stringField('168h0m0s'), processOutputLimit: { effective: 1048576, source: 'sqlite', maskedByStartupSource: '' },
      providerExecutablePath: stringField('/usr/local/bin/claude'), providerDefaultModel: stringField('claude-sonnet-5'),
      providerCredentialRef: stringField('[REDACTED]'),
    },
    ...overrides,
  };
}

describe('SettingsScreen (V7-16)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a real offline message rather than fetching settings', async () => {
    renderSettings(true);
    expect(await screen.findByText('Reconnect to view and edit settings.')).toBeInTheDocument();
    expect(api.getSafeSettings).not.toHaveBeenCalled();
  });

  it('renders the real desired values and their real effective/source picture, never a fabricated one', async () => {
    vi.mocked(api.getSafeSettings).mockResolvedValue(settingsDetailFixture() as never);
    renderSettings();

    expect(await screen.findByDisplayValue('/var/ak/workspaces')).toBeInTheDocument();
    expect(screen.getByDisplayValue('claude-sonnet-5')).toBeInTheDocument();
    expect(screen.getByDisplayValue('168h0m0s')).toBeInTheDocument();
    expect(screen.getAllByText(/effective next restart:/).length).toBeGreaterThan(0);
  });

  it('never displays a real secret value for the credential reference, and never pre-fills the editable field with the server\'s own always-masked marker either (real bug found live: resubmitting that marker verbatim always fails real server validation)', async () => {
    vi.mocked(api.getSafeSettings).mockResolvedValue(settingsDetailFixture() as never);
    renderSettings();

    await screen.findByDisplayValue('/var/ak/workspaces');
    // The read-only "effective next restart" audit line still shows the real masked marker...
    expect(screen.getAllByText(/\[REDACTED\]/).length).toBeGreaterThan(0);
    // ...but the EDITABLE draft field is blank, never pre-filled with it.
    expect(screen.getByLabelText('Provider credential reference')).toHaveValue('');
    expect(screen.queryByText(/sk-ant|sk-proj|api[_-]?key.{0,3}[:=]/i)).not.toBeInTheDocument();
  });

  it('saves with a freshly re-checked If-Match version, not the one loaded at page-open time', async () => {
    vi.mocked(api.getSafeSettings)
      .mockResolvedValueOnce(settingsDetailFixture({ version: 3 }) as never)
      .mockResolvedValueOnce(settingsDetailFixture({ version: 9 }) as never);
    vi.mocked(settingsApi.updateSafeSettings).mockResolvedValue(settingsDetailFixture({ version: 10 }) as never);
    renderSettings();

    const modelField = await screen.findByDisplayValue('claude-sonnet-5');
    await userEvent.clear(modelField);
    await userEvent.type(modelField, 'claude-opus-5');
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }));

    await waitFor(() => expect(settingsApi.updateSafeSettings).toHaveBeenCalledWith(
      expect.objectContaining({ providerDefaultModel: 'claude-opus-5' }),
      expect.objectContaining({ token: 'test-session-token', ifMatch: '"9"' }),
    ));
  });

  it('announces that a restart is required after a save, and shows it as an honest banner', async () => {
    vi.mocked(api.getSafeSettings).mockResolvedValue(settingsDetailFixture() as never);
    vi.mocked(settingsApi.updateSafeSettings).mockResolvedValue(settingsDetailFixture({ restartRequired: true }) as never);
    renderSettings();

    const modelField = await screen.findByDisplayValue('claude-sonnet-5');
    await userEvent.clear(modelField);
    await userEvent.type(modelField, 'claude-opus-5');
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }));

    await waitFor(() => expect(screen.getAllByText(/restart is required/).length).toBeGreaterThan(0));
  });

  it('a real conflicting save (409) surfaces as a real InlineError, never silently retried', async () => {
    vi.mocked(api.getSafeSettings).mockResolvedValue(settingsDetailFixture() as never);
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    vi.mocked(settingsApi.updateSafeSettings).mockRejectedValue(new ApiError(409, 'CONFLICT', 'If-Match does not match the current version; reload and retry'));
    renderSettings();

    const modelField = await screen.findByDisplayValue('claude-sonnet-5');
    await userEvent.clear(modelField);
    await userEvent.type(modelField, 'claude-opus-5');
    await userEvent.click(screen.getByRole('button', { name: 'Save Settings' }));

    expect(await screen.findByText('If-Match does not match the current version; reload and retry')).toBeInTheDocument();
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.getSafeSettings).mockResolvedValue(settingsDetailFixture() as never);
    const { container } = renderSettings();
    await screen.findByDisplayValue('/var/ak/workspaces');
    await expectNoAxeViolations(container);
  });
});
