import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { CreateDefinitionDialog } from './CreateDefinitionDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, createDefinition: vi.fn(), createProjectDefinition: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

describe('CreateDefinitionDialog', () => {
  beforeEach(() => vi.clearAllMocks());

  it('requires both fields before calling the real API', async () => {
    render(<CreateDefinitionDialog scope="global" onClose={vi.fn()} onCreated={vi.fn()} />);
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findAllByText('required')).not.toHaveLength(0);
    expect(api.createDefinition).not.toHaveBeenCalled();
  });

  it('creates a global definition with the selected Kind and reports the new id', async () => {
    vi.mocked(api.createDefinition).mockResolvedValue({ definitionId: 'blk-1', kind: 'BLOCK' } as never);
    const onCreated = vi.fn();
    render(<CreateDefinitionDialog scope="global" onClose={vi.fn()} onCreated={onCreated} />);

    await userEvent.selectOptions(screen.getByLabelText(/^Kind/), 'SKILL');
    await userEvent.type(screen.getByLabelText(/Definition ID/), 'blk-1');
    await userEvent.type(screen.getByLabelText(/^Name/), 'My Block');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('SKILL', 'blk-1'));
    expect(api.createDefinition).toHaveBeenCalledWith('SKILL', { definitionId: 'blk-1', name: 'My Block' }, expect.anything());
  });

  it('creates a project-scoped definition through the project-scoped endpoint', async () => {
    vi.mocked(api.createProjectDefinition).mockResolvedValue({ definitionId: 'skl-1', kind: 'SKILL' } as never);
    render(<CreateDefinitionDialog scope="project" projectId="proj-1" onClose={vi.fn()} onCreated={vi.fn()} />);
    await userEvent.type(screen.getByLabelText(/Definition ID/), 'skl-1');
    await userEvent.type(screen.getByLabelText(/^Name/), 'My Skill');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() => expect(api.createProjectDefinition).toHaveBeenCalledWith('proj-1', 'BLOCK', { definitionId: 'skl-1', name: 'My Skill' }, expect.anything()));
    expect(api.createDefinition).not.toHaveBeenCalled();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<CreateDefinitionDialog scope="global" onClose={vi.fn()} onCreated={vi.fn()} />);
    await expectNoAxeViolations(container);
  });
});
