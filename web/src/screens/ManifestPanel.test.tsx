import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { ManifestPanel } from './ManifestPanel';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getDefinitionManifest: vi.fn(), runDefinitionManifest: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><ManifestPanel projectId="p1" /></QueryClientProvider>);
}

describe('ManifestPanel (V12-21)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('explains how to configure a manifest when none is', async () => {
    vi.mocked(api.getDefinitionManifest).mockResolvedValue({ projectId: 'p1', configured: false } as never);
    renderPanel();
    expect(await screen.findByText(/No manifest configured/)).toHaveTextContent('--project-manifest p1=');
  });

  it('checks, then publishes after a confirmation, and shows the output', async () => {
    vi.mocked(api.getDefinitionManifest).mockResolvedValue({ projectId: 'p1', configured: true, manifestPath: '/w/aw-project.json' } as never);
    vi.mocked(api.runDefinitionManifest)
      .mockResolvedValueOnce({ mode: 'CHECK', exitCode: 1, succeeded: false, output: 'workflow wf-x: unknown $ref', startedAt: '2026-11-03T10:00:00Z', durationMs: 900 } as never)
      .mockResolvedValueOnce({ mode: 'PUBLISH', exitCode: 0, succeeded: true, output: 'WORKFLOW wf-task-delivery v7', startedAt: '2026-11-03T10:01:00Z', durationMs: 4200 } as never);
    renderPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Check' }));
    await waitFor(() => expect(api.runDefinitionManifest).toHaveBeenCalledWith('p1', { mode: 'CHECK' }, expect.objectContaining({ idempotencyKey: expect.any(String) })));
    expect(await screen.findByTestId('manifest-run')).toHaveTextContent('Check failed (exit code 1)');
    expect(screen.getByTestId('manifest-run')).toHaveTextContent('unknown $ref');

    await userEvent.click(screen.getByRole('button', { name: 'Publish…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Publish the manifest' });
    await userEvent.click(within(dialog).getByRole('button', { name: 'Publish' }));
    await waitFor(() => expect(screen.getByTestId('manifest-run')).toHaveTextContent('Publish passed'));
    expect(api.runDefinitionManifest).toHaveBeenLastCalledWith('p1', { mode: 'PUBLISH' }, expect.anything());
  });
});
