import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { Tabs } from './ui';

const TAB_DEFS = [
  { id: 'overview', label: 'Overview' },
  { id: 'graph', label: 'Graph' },
  { id: 'evidence', label: 'Evidence', error: true },
];

describe('Tabs', () => {
  it('marks the active tab with aria-selected and puts only it in the Tab order', () => {
    render(<Tabs tabs={TAB_DEFS} active="graph" onChange={() => {}} />);
    const overview = screen.getByRole('tab', { name: 'Overview' });
    const graph = screen.getByRole('tab', { name: 'Graph' });
    expect(graph).toHaveAttribute('aria-selected', 'true');
    expect(overview).toHaveAttribute('aria-selected', 'false');
    // Roving tabindex: only the selected tab is reachable by Tab key.
    expect(graph).toHaveAttribute('tabindex', '0');
    expect(overview).toHaveAttribute('tabindex', '-1');
  });

  it('surfaces an error tab visually and to assistive tech via an accessible name', () => {
    render(<Tabs tabs={TAB_DEFS} active="overview" onChange={() => {}} />);
    expect(screen.getByLabelText('error')).toBeInTheDocument();
  });

  it('ArrowRight/ArrowLeft move selection with wraparound', async () => {
    const onChange = vi.fn();
    render(<Tabs tabs={TAB_DEFS} active="overview" onChange={onChange} />);
    screen.getByRole('tab', { name: 'Overview' }).focus();

    await userEvent.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenLastCalledWith('graph');

    await userEvent.keyboard('{ArrowLeft}');
    expect(onChange).toHaveBeenLastCalledWith('overview');

    // Wraparound: ArrowLeft from the first tab goes to the last.
    await userEvent.keyboard('{ArrowLeft}');
    expect(onChange).toHaveBeenLastCalledWith('evidence');
  });

  it('Home/End jump to the first/last tab', async () => {
    const onChange = vi.fn();
    render(<Tabs tabs={TAB_DEFS} active="graph" onChange={onChange} />);
    screen.getByRole('tab', { name: 'Graph' }).focus();

    await userEvent.keyboard('{End}');
    expect(onChange).toHaveBeenLastCalledWith('evidence');

    await userEvent.keyboard('{Home}');
    expect(onChange).toHaveBeenLastCalledWith('overview');
  });

  it('moves focus to the newly-selected tab (not just visual state)', async () => {
    const onChange = vi.fn();
    const { rerender } = render(<Tabs tabs={TAB_DEFS} active="overview" onChange={onChange} />);
    screen.getByRole('tab', { name: 'Overview' }).focus();
    await userEvent.keyboard('{ArrowRight}');
    // Re-render as the parent would after onChange updates `active`.
    rerender(<Tabs tabs={TAB_DEFS} active="graph" onChange={onChange} />);
    expect(screen.getByRole('tab', { name: 'Graph' })).toHaveFocus();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<Tabs tabs={TAB_DEFS} active="overview" onChange={() => {}} />);
    await expectNoAxeViolations(container);
  });
});
