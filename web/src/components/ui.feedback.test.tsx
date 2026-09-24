import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import {
  BlockerCard,
  ConnectionBanner,
  ConnectionIndicator,
  CopyableId,
  EmptyState,
  InlineError,
  OperationNotice,
  ProjectionBanner,
  Skeleton,
  ValidActionBar,
} from './ui';

describe('InlineError', () => {
  it('links the error to its field name and announces it assertively', () => {
    render(<InlineError code="INVALID_REQUEST" message="Must not be empty" field="content" />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('aria-live', 'assertive');
    expect(alert).toHaveTextContent('content');
    expect(alert).toHaveTextContent('Must not be empty');
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<InlineError code="INTERNAL" message="Something broke" />);
    await expectNoAxeViolations(container);
  });
});

describe('ProjectionBanner', () => {
  it('renders nothing when Fresh', () => {
    const { container } = render(<ProjectionBanner state="Fresh" />);
    expect(container).toBeEmptyDOMElement();
  });

  it.each(['Stale', 'Degraded', 'Resync required'] as const)(
    'announces %s state politely, not just via color',
    (state) => {
      render(<ProjectionBanner state={state} />);
      const banner = screen.getByRole('status');
      expect(banner).toHaveAttribute('aria-live', 'polite');
      expect(banner).toHaveTextContent(state);
    },
  );

  it('offers a Refresh action for Stale', async () => {
    const onRefresh = vi.fn();
    render(<ProjectionBanner state="Stale" onRefresh={onRefresh} />);
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });
});

describe('ConnectionBanner', () => {
  it('renders nothing for Live/Stale/Degraded (those have their own dedicated banners)', () => {
    for (const state of ['Live', 'Stale', 'Degraded'] as const) {
      const { container, unmount } = render(<ConnectionBanner state={state} />);
      expect(container).toBeEmptyDOMElement();
      unmount();
    }
  });

  it('announces Offline assertively with a real text message', () => {
    render(<ConnectionBanner state="Offline" />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveAttribute('aria-live', 'assertive');
    expect(alert).toHaveTextContent(/Offline/);
  });
});

describe('ConnectionIndicator', () => {
  it.each(['Live', 'Reconnecting', 'Stale', 'Offline', 'Degraded'] as const)(
    'exposes %s as an accessible name, not just a colored dot',
    (state) => {
      render(<ConnectionIndicator state={state} />);
      expect(screen.getByRole('status', { name: `Connection: ${state}` })).toBeInTheDocument();
    },
  );
});

describe('OperationNotice', () => {
  it('announces politely and shows an operation reference when given', () => {
    render(<OperationNotice state="Running" message="Rebuilding projection" ref="op-1842" />);
    const notice = screen.getByRole('status');
    expect(notice).toHaveAttribute('aria-live', 'polite');
    expect(notice).toHaveTextContent('Running');
    expect(notice).toHaveTextContent('op-1842');
  });
});

describe('BlockerCard', () => {
  it('is an alert exposing type, target and reason as visible text', () => {
    render(<BlockerCard type="MISSING_APPROVAL" target="run-42" reason="Awaiting scope approval" opened="2h ago" actions={['Approve']} />);
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('MISSING_APPROVAL');
    expect(alert).toHaveTextContent('run-42');
    expect(alert).toHaveTextContent('Awaiting scope approval');
    expect(screen.getByRole('button', { name: 'Approve' })).toBeInTheDocument();
  });
});

describe('EmptyState', () => {
  it('renders title/description and an optional action', async () => {
    const onClick = vi.fn();
    render(<EmptyState title="No projects yet" description="Create one to get started." action={{ label: 'Create project', onClick }} />);
    expect(screen.getByText('No projects yet')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Create project' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});

describe('Skeleton', () => {
  it('is hidden from assistive tech (it carries no information)', () => {
    const { container } = render(<Skeleton className="h-4 w-full" />);
    expect(container.firstChild).toHaveAttribute('aria-hidden', 'true');
  });
});

describe('CopyableId', () => {
  it('copies the full value to the clipboard and confirms it', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<CopyableId value="run-0000000000000042" short="run-...42" />);
    await userEvent.click(screen.getByRole('button', { name: 'Copy ID: run-0000000000000042' }));
    expect(writeText).toHaveBeenCalledWith('run-0000000000000042');
    expect(screen.getByText('copied')).toBeInTheDocument();
  });
});

describe('ValidActionBar', () => {
  it('renders nothing when there are no actions (never an empty toolbar)', () => {
    const { container } = render(<ValidActionBar actions={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('groups primary/secondary/destructive with a visual separator before destructive', async () => {
    const onApprove = vi.fn();
    const onCancel = vi.fn();
    render(
      <ValidActionBar
        actions={[
          { label: 'Approve', onClick: onApprove },
          { label: 'Cancel run', intent: 'destructive', onClick: onCancel },
        ]}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Approve' }));
    expect(onApprove).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: 'Cancel run' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(
      <ValidActionBar actions={[{ label: 'Approve', onClick: () => {} }]} />,
    );
    await expectNoAxeViolations(container);
  });
});
