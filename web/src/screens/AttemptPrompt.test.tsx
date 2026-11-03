import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { AttemptPromptSection } from './AttemptPrompt';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getAttemptPrompt: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

describe('AttemptPromptSection (V12-26)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getAttemptPrompt).mockImplementation(async (_p, attemptId) => ({
      attemptId, nodeRunId: 'nr', runId: 'r', nodeKey: 'build', contextSnapshotId: 'snap-1',
      prompt: JSON.stringify({ schemaVersion: 2, taskContract: { title: `for ${attemptId}` } }), bytes: 2048,
    }) as never);
  });

  it('loads the prompt only when asked, for the chosen attempt, indented', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><AttemptPromptSection projectId="p" attempts={[{ attemptId: 'a1', attemptNumber: 1 }, { attemptId: 'a2', attemptNumber: 2 }]} /></QueryClientProvider>);
    expect(api.getAttemptPrompt).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: 'Show prompt' }));
    expect(await screen.findByTestId('prompt-text')).toHaveTextContent('"title": "for a2"');
    expect(api.getAttemptPrompt).toHaveBeenCalledWith('p', 'a2', expect.anything());
    await userEvent.selectOptions(screen.getByLabelText('Attempt'), 'a1');
    await waitFor(() => expect(screen.getByTestId('prompt-text')).toHaveTextContent('"title": "for a1"'));
  });
});
