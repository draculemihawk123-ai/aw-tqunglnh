import { describe, expect, it } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Router } from 'wouter';
import { memoryLocation } from 'wouter/memory-location';
import App from './App';

/** Renders <App/> with a controlled in-memory location — real wouter navigation, no jsdom history/browser needed, and `history` lets tests assert on the exact URL sequence App.tsx produced (e.g. a guard redirect). */
function renderAppAt(path: string) {
  const memory = memoryLocation({ path, record: true });
  render(
    <Router hook={memory.hook}>
      <App />
    </Router>,
  );
  return memory;
}

describe('App routing (V7-04)', () => {
  it('an unmatched path (including "/") settles on /doctor', async () => {
    const memory = renderAppAt('/');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/doctor'));
    expect(await screen.findByRole('heading', { name: 'Doctor' })).toBeInTheDocument();
  });

  it('deep-links directly to a real project overview (the URL is the source of truth, not in-memory state)', async () => {
    renderAppAt('/projects/proj-alpha-001');
    expect(await screen.findByRole('heading', { name: 'platform-core', level: 1 })).toBeInTheDocument();
  });

  it('route guard redirects a project-scoped URL with no matching project to /projects', async () => {
    const memory = renderAppAt('/projects/does-not-exist/board');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/projects'));
    expect(await screen.findByText('3 projects in this installation.')).toBeInTheDocument();
  });

  it('route guard redirects a task-scoped URL with no matching project to /projects', async () => {
    const memory = renderAppAt('/projects/does-not-exist/tasks/wi-0018/graph');
    await waitFor(() => expect(memory.history.at(-1)).toBe('/projects'));
  });

  it('clicking a left-nav item while a project is selected pushes the real project-scoped URL', async () => {
    const memory = renderAppAt('/projects/proj-alpha-001');
    await screen.findByRole('heading', { name: 'platform-core', level: 1 });
    await userEvent.click(screen.getByRole('button', { name: 'Board' }));
    await waitFor(() => expect(memory.history.at(-1)).toBe('/projects/proj-alpha-001/board'));
  });

  it('opening the one real task fixture from the board pushes the task-scoped URL', async () => {
    const memory = renderAppAt('/projects/proj-alpha-001/board');
    const openTaskButton = await screen.findByRole('button', { name: 'Add distributed tracing to API gateway' });
    await userEvent.click(openTaskButton);
    await waitFor(() => expect(memory.history.at(-1)).toBe('/projects/proj-alpha-001/tasks/wi-0018'));
  });
});
