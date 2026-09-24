import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { expectNoAxeViolations } from '../test/axe';
import { Badge, StatusBadge, VerdictBadge } from './ui';

describe('Badge', () => {
  it('renders the label as visible text, not color alone', () => {
    render(<Badge label="RUNNING" intent="runtime" />);
    // V7-03's own completion bar: "status không chỉ truyền bằng màu" — the
    // label text itself must be present in the accessible tree, not only
    // conveyed through a background color a colorblind user cannot see.
    expect(screen.getByText('RUNNING')).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<Badge label="SUCCEEDED" intent="success" />);
    await expectNoAxeViolations(container);
  });
});

describe('StatusBadge', () => {
  it.each(['BACKLOG', 'RUNNING', 'BLOCKED', 'SUCCEEDED', 'FAILED'] as const)(
    'renders %s with an icon AND the text label (not color alone)',
    (state) => {
      render(<StatusBadge state={state} />);
      // aria-hidden text still renders in the DOM (screen.getByText finds
      // it); the icon is a SEPARATE, distinguishing signal on top of color,
      // which is what this assertion actually protects against regressing.
      const badge = screen.getByRole('status');
      expect(badge).toHaveTextContent(state);
      expect(badge.querySelector('svg')).not.toBeNull();
    },
  );

  it('gives repository ACTIVE a different intent than work item ACTIVE (entity-aware)', () => {
    const { container: repo } = render(<StatusBadge state="ACTIVE" entity="repository" />);
    const { container: workitem } = render(<StatusBadge state="ACTIVE" entity="workitem" />);
    expect(repo.querySelector('[role=status]')?.className).not.toEqual(
      workitem.querySelector('[role=status]')?.className,
    );
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<StatusBadge state="RUNNING" />);
    await expectNoAxeViolations(container);
  });
});

describe('VerdictBadge', () => {
  it.each(['PASS', 'FAIL', 'ERROR', 'N/A', 'NOT_RUN'] as const)('renders %s as visible text', (verdict) => {
    render(<VerdictBadge verdict={verdict} />);
    expect(screen.getByText(verdict)).toBeInTheDocument();
  });
});
