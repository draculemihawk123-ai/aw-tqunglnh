import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { RepositorySecretsDialog, dotEnvNames } from './RepositorySecretsDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, listRepositorySecrets: vi.fn(), setRepositorySecrets: vi.fn(), deleteRepositorySecret: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

describe('RepositorySecretsDialog (V12-29)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listRepositorySecrets).mockResolvedValue({ repositoryId: 'api', protection: 'dpapi', secrets: [
      { name: 'CHOCHUYENNHA_DB_PASSWORD', version: 1, updatedBy: 'operator', updatedAt: '2026-11-03T10:00:00Z' },
    ] } as never);
    vi.mocked(api.setRepositorySecrets).mockResolvedValue({} as never);
    vi.mocked(api.deleteRepositorySecret).mockResolvedValue({} as never);
  });

  it('reads the names of a .env text and reports bad lines', () => {
    expect(dotEnvNames('# c\nexport B=1\nA="x"\nEMPTY=\nnot a line\n')).toEqual({ names: ['A', 'B'], badLines: [5] });
  });

  it('lists names only, adds a secret and imports a .env showing only its names', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(<QueryClientProvider client={client}><RepositorySecretsDialog projectId="p" repositoryId="api" repositoryName="api" onClose={() => {}} /></QueryClientProvider>);
    expect(await screen.findByText('CHOCHUYENNHA_DB_PASSWORD')).toBeInTheDocument();
    expect(screen.getByText(/Windows DPAPI/)).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText('Secret name'), 'R2_SECRET');
    await userEvent.type(screen.getByLabelText('Secret value'), 's3cr3t');
    expect(screen.getByLabelText('Secret value')).toHaveAttribute('type', 'password');
    await userEvent.click(screen.getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(api.setRepositorySecrets).toHaveBeenCalledWith('p', 'api', { values: { R2_SECRET: 's3cr3t' } }, expect.objectContaining({ idempotencyKey: expect.any(String) })));

    await userEvent.click(screen.getByText('Import a .env file'));
    const area = screen.getByLabelText('Pasted .env');
    await userEvent.click(area);
    await userEvent.paste('CHOCHUYENNHA_DB_PASSWORD=pw\nR2_KEY=k\n');
    expect(screen.getByTestId('dotenv-names')).toHaveTextContent('Will store 2: CHOCHUYENNHA_DB_PASSWORD, R2_KEY (existing ones are replaced)');
    expect(screen.getByTestId('dotenv-names')).not.toHaveTextContent('pw');
    await userEvent.click(screen.getByRole('button', { name: 'Import' }));
    await waitFor(() => expect(api.setRepositorySecrets).toHaveBeenLastCalledWith('p', 'api', { dotEnv: 'CHOCHUYENNHA_DB_PASSWORD=pw\nR2_KEY=k\n' }, expect.anything()));
  });
});
