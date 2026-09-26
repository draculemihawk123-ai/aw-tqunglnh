import { describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Router } from 'wouter';
import { memoryLocation } from 'wouter/memory-location';
import App from './App';

// These tests are about ROUTING, not Doctor's own real data fetching
// (that is web/src/screens/Doctor.test.tsx's own job) — Doctor is the
// default landing screen, so every test here renders it at least once and
// would otherwise make a real, unmocked fetch('/doctor') that jsdom has
// nothing to answer.
vi.mock('./api/generated', async () => {
  const actual = await vi.importActual<typeof import('./api/generated')>('./api/generated');
  return {
    ...actual,
    doctor: vi.fn().mockResolvedValue({ status: 'HEALTHY', checks: [], restartRequired: false, links: {} }),
    listAdapterBuilds: vi.fn().mockResolvedValue({ builds: [] }),
    projectsList: vi.fn().mockResolvedValue({
      projects: [
        { id: 'proj-alpha-001', name: 'platform-core', status: 'ACTIVE', version: 1 },
        { id: 'proj-beta-002', name: 'data-pipeline', status: 'ACTIVE', version: 1 },
        { id: 'proj-gamma-003', name: 'auth-service', status: 'ACTIVE', version: 1 },
      ],
    }),
    projectRepositoriesList: vi.fn().mockResolvedValue({ repositories: [] }),
    projectComponentsList: vi.fn().mockResolvedValue({ components: [] }),
  };
});
vi.mock('./api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

/** Renders <App/> with a controlled in-memory location — real wouter navigation, no jsdom history/browser needed, and `history` lets tests assert on the exact URL sequence App.tsx produced (e.g. a guard redirect). A fresh QueryClient per render (retry: false) keeps one test's query state from leaking into the next. */
function renderAppAt(path: string) {
  const memory = memoryLocation({ path, record: true });
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <Router hook={memory.hook}>
        <App />
      </Router>
    </QueryClientProvider>,
  );
  return memory;
}

describe('App routing (V7-04/V7-05A)', () => {
  it('an unmatched path (including "/") settles on /ui/doctor', async () => {
    const memory = renderAppAt('/');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/doctor'));
    expect(await screen.findByRole('heading', { name: 'Doctor' })).toBeInTheDocument();
  });

  it('a bare API-shaped path with no /ui prefix (e.g. a stray "/doctor") also settles on /ui/doctor, never confused with the SPA route', async () => {
    const memory = renderAppAt('/doctor');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/doctor'));
  });

  it('deep-links directly to a real project overview (the URL is the source of truth, not in-memory state)', async () => {
    renderAppAt('/ui/projects/proj-alpha-001');
    expect(await screen.findByRole('heading', { name: 'platform-core', level: 1 })).toBeInTheDocument();
  });

  it('route guard redirects a project-scoped URL with no matching project to /ui/projects', async () => {
    const memory = renderAppAt('/ui/projects/does-not-exist/board');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/projects'));
    expect(await screen.findByText('3 projects in this installation.')).toBeInTheDocument();
  });

  it('route guard redirects a task-scoped URL with no matching project to /ui/projects', async () => {
    const memory = renderAppAt('/ui/projects/does-not-exist/tasks/wi-0018/graph');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/projects'));
  });

  it('clicking a left-nav item while a project is selected pushes the real project-scoped URL', async () => {
    const memory = renderAppAt('/ui/projects/proj-alpha-001');
    await screen.findByRole('heading', { name: 'platform-core', level: 1 });
    await userEvent.click(screen.getByRole('button', { name: 'Board' }));
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/projects/proj-alpha-001/board'));
  });

  it('opening the one real task fixture from the board pushes the task-scoped URL', async () => {
    const memory = renderAppAt('/ui/projects/proj-alpha-001/board');
    const openTaskButton = await screen.findByRole('button', { name: 'Add distributed tracing to API gateway' });
    await userEvent.click(openTaskButton);
    await waitFor(() => expect(memory.history.at(-1)).toBe('/ui/projects/proj-alpha-001/tasks/wi-0018'));
  });
});
