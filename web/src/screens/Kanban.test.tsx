import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { KanbanScreen } from './Kanban';
import type { ProjectSummary } from './Projects';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    listWorkItemKanban: vi.fn(), getWorkItemProjectedDetail: vi.fn(), markWorkItemReady: vi.fn(),
    createRootWorkItem: vi.fn(), projectRepositoriesList: vi.fn().mockResolvedValue({ repositories: [] }),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const PROJECT: ProjectSummary = { id: 'proj-1', name: 'platform-core', repositories: [] };

const BACKLOG_CARD = {
  workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Migrate auth tokens to JWT RS256',
  isRoot: true, status: 'BACKLOG', blockerCount: 0, pendingScopeExpansionCount: 0,
};
const ACTIVE_CARD = {
  workItemId: 'wi-2', projectId: 'proj-1', familyId: 'fam-2', title: 'Add distributed tracing',
  isRoot: true, status: 'ACTIVE', activeRunStatus: 'VERIFYING', blockerCount: 0, pendingScopeExpansionCount: 0,
  repositoryBadges: [{ repositoryId: 'core-api', state: 'ACTIVE' }],
};
const BLOCKED_CARD = {
  workItemId: 'wi-3', projectId: 'proj-1', familyId: 'fam-3', title: 'Fix race condition',
  isRoot: true, status: 'BLOCKED', blockerCount: 2, topBlockerType: 'SCOPE_EXPANSION_PENDING', pendingScopeExpansionCount: 1,
  repositoryBadges: [{ repositoryId: 'worker-service', state: 'ACTIVE' }],
};

function freshness(status: 'LIVE' | 'DEGRADED' | 'STALE' = 'LIVE') {
  return { generation: 1, asOfJournalPosition: 42, status };
}

describe('KanbanScreen', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows a loading skeleton before the board resolves', () => {
    vi.mocked(api.listWorkItemKanban).mockReturnValue(new Promise(() => {}));
    const { container } = render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);
    expect(container.querySelectorAll('[aria-hidden="true"]').length).toBeGreaterThan(0);
  });

  it('groups real cards into their real status columns, with real repository badges/blockers/pending scope expansions', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD, ACTIVE_CARD, BLOCKED_CARD], freshness: freshness() } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);

    expect(await screen.findByText('Migrate auth tokens to JWT RS256')).toBeInTheDocument();
    expect(screen.getByText('Add distributed tracing')).toBeInTheDocument();
    expect(screen.getByText('core-api', { selector: 'span' })).toBeInTheDocument();
    expect(screen.getByText('worker-service', { selector: 'span' })).toBeInTheDocument();
    expect(screen.getByText(/2 blockers \(SCOPE_EXPANSION_PENDING\)/)).toBeInTheDocument();
    expect(screen.getByText('1 pending scope expansion')).toBeInTheDocument();
    expect(screen.getByText('VERIFYING')).toBeInTheDocument();
    expect(screen.getByText('completion not yet gate-verified')).toBeInTheDocument();
  });

  it('the repository filter narrows the board to only cards touching that repository', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD, ACTIVE_CARD, BLOCKED_CARD], freshness: freshness() } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);
    await screen.findByText('Add distributed tracing');

    await userEvent.selectOptions(screen.getByLabelText('Filter by repository'), 'core-api');

    expect(screen.getByText('Add distributed tracing')).toBeInTheDocument();
    expect(screen.queryByText('Fix race condition')).not.toBeInTheDocument();
    // Cards with no repository badge at all are never a repo's own membership, so they drop out of any specific-repo filter too.
    expect(screen.queryByText('Migrate auth tokens to JWT RS256')).not.toBeInTheDocument();
  });

  it('clicking a card title opens the real WorkItem, never a hardcoded fixture id', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [ACTIVE_CARD], freshness: freshness() } as never);
    const onOpenTask = vi.fn();
    render(<KanbanScreen project={PROJECT} onOpenTask={onOpenTask} />);
    await userEvent.click(await screen.findByText('Add distributed tracing'));
    expect(onOpenTask).toHaveBeenCalledWith('wi-2');
  });

  it('a stale/degraded freshness shows the real, server-reported journal position, never a fabricated one', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [], freshness: freshness('STALE') } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);
    expect(await screen.findByText('Projection Stale')).toBeInTheDocument();
    expect(screen.getByText(/JournalPosition 42/)).toBeInTheDocument();
  });

  it('Mark Ready re-checks fresh readiness first and dispatches with the fresh target version as If-Match', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD], freshness: freshness() } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
      card: BACKLOG_CARD, freshness: freshness(),
      readiness: { workItemId: 'wi-1', status: 'BACKLOG', version: 7, ready: true },
      validActions: [{ operationId: 'markWorkItemReady', scopeKind: 'PROJECT', targetVersion: 7 }],
    } as never);
    vi.mocked(api.markWorkItemReady).mockResolvedValue({} as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Mark Ready' }));

    await waitFor(() => expect(api.markWorkItemReady).toHaveBeenCalledWith('wi-1', {}, expect.objectContaining({ ifMatch: '"7"' })));
    expect(await screen.findByText(/marked ready/)).toBeInTheDocument();
  });

  it('Mark Ready never dispatches the mutation when a fresh recheck says the WorkItem is not actually ready', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD], freshness: freshness() } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
      card: BACKLOG_CARD, freshness: freshness(),
      readiness: { workItemId: 'wi-1', status: 'BACKLOG', version: 7, ready: false, problems: ['missing acceptance criteria'] },
      validActions: [],
    } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Mark Ready' }));

    expect(await screen.findByText(/missing acceptance criteria/)).toBeInTheDocument();
    expect(api.markWorkItemReady).not.toHaveBeenCalled();
  });

  it('the New WorkItem button opens the real create dialog', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD], freshness: freshness() } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);
    await screen.findByText('Migrate auth tokens to JWT RS256');

    await userEvent.click(screen.getByRole('button', { name: 'New WorkItem' }));

    expect(screen.getByRole('dialog', { name: 'Create WorkItem' })).toBeInTheDocument();
    expect(await screen.findByLabelText(/Title/)).toBeInTheDocument();
  });

  it('the New WorkItem button is disabled while offline', () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [], freshness: freshness() } as never);
    render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} isOffline />);
    expect(screen.getByRole('button', { name: 'New WorkItem' })).toBeDisabled();
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.listWorkItemKanban).mockResolvedValue({ items: [BACKLOG_CARD, ACTIVE_CARD], freshness: freshness() } as never);
    const { container } = render(<KanbanScreen project={PROJECT} onOpenTask={vi.fn()} />);
    await screen.findByText('Migrate auth tokens to JWT RS256');
    await expectNoAxeViolations(container);
  });
});
