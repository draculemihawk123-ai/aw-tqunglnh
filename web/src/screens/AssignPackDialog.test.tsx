import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { AssignPackDialog } from './AssignPackDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, componentPackAssignmentsList: vi.fn(), componentPackAssignmentsAssign: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const COMPONENT = { id: 'comp-1', projectId: 'proj-1', repositoryId: 'repo-1', name: 'api', path: 'src/api', kind: 'DIRECTORY', version: 1 };

describe('AssignPackDialog', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows "no pack assigned yet" when nothing is effective', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    render(<AssignPackDialog component={COMPONENT} onClose={vi.fn()} onAssigned={vi.fn()} />);
    expect(await screen.findByText('No pack assigned yet.')).toBeInTheDocument();
  });

  it('shows the real effective assignment and its history', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({
      componentId: 'comp-1', projectId: 'proj-1',
      assignments: [
        { id: 'a1', componentId: 'comp-1', packVersionId: 'engpack@1.0.0', effectiveAt: '2026-01-01T00:00:00Z', actor: 'operator' },
        { id: 'a2', componentId: 'comp-1', packVersionId: 'engpack@2.0.0', effectiveAt: '2026-02-01T00:00:00Z', actor: 'operator' },
      ],
      effective: { id: 'a2', componentId: 'comp-1', packVersionId: 'engpack@2.0.0', effectiveAt: '2026-02-01T00:00:00Z', actor: 'operator' },
    } as never);
    render(<AssignPackDialog component={COMPONENT} onClose={vi.fn()} onAssigned={vi.fn()} />);
    expect((await screen.findAllByText('engpack@2.0.0')).length).toBeGreaterThan(0);
    expect(screen.getByText('engpack@1.0.0')).toBeInTheDocument();
  });

  it('requires a non-empty pack version id before calling the real API', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    render(<AssignPackDialog component={COMPONENT} onClose={vi.fn()} onAssigned={vi.fn()} />);
    await screen.findByText('No pack assigned yet.');
    await userEvent.click(screen.getByRole('button', { name: 'Assign' }));
    expect(await screen.findByText('required')).toBeInTheDocument();
    expect(api.componentPackAssignmentsAssign).not.toHaveBeenCalled();
  });

  it('assigns the exact version string the operator typed, and reports success', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    vi.mocked(api.componentPackAssignmentsAssign).mockResolvedValue({ assignmentId: 'a3', componentId: 'comp-1', packVersionId: 'engpack@2.4.2', effectiveAt: '2026-03-01T00:00:00Z', actor: 'operator' } as never);
    const onClose = vi.fn();
    const onAssigned = vi.fn();
    render(<AssignPackDialog component={COMPONENT} onClose={onClose} onAssigned={onAssigned} />);
    await screen.findByText('No pack assigned yet.');

    await userEvent.type(screen.getByLabelText(/Pack version ID/), 'engpack@2.4.2');
    await userEvent.click(screen.getByRole('button', { name: 'Assign' }));

    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    expect(api.componentPackAssignmentsAssign).toHaveBeenCalledWith('comp-1', { packVersionId: 'engpack@2.4.2' }, expect.anything());
    expect(onAssigned).toHaveBeenCalledWith('Pack "engpack@2.4.2" assigned to api.');
  });

  it('shows an API error inline on assignment failure', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    vi.mocked(api.componentPackAssignmentsAssign).mockRejectedValue(new api.ApiError(400, 'INVALID_REQUEST', 'request validation failed'));
    render(<AssignPackDialog component={COMPONENT} onClose={vi.fn()} onAssigned={vi.fn()} />);
    await screen.findByText('No pack assigned yet.');
    await userEvent.type(screen.getByLabelText(/Pack version ID/), 'bad');
    await userEvent.click(screen.getByRole('button', { name: 'Assign' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('request validation failed');
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    const { container } = render(<AssignPackDialog component={COMPONENT} onClose={vi.fn()} onAssigned={vi.fn()} />);
    await screen.findByText('No pack assigned yet.');
    await expectNoAxeViolations(container);
  });
});
