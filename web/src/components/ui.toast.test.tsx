import { act, renderHook } from '@testing-library/react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { expectNoAxeViolations } from '../test/axe';
import { ToastViewport, useToasts } from './ui';

describe('useToasts', () => {
  it('show() adds a toast with a generated id when none is given', () => {
    const { result } = renderHook(() => useToasts());
    act(() => {
      result.current.show({ intent: 'success', message: 'Saved' });
    });
    expect(result.current.toasts).toHaveLength(1);
    expect(result.current.toasts[0].message).toBe('Saved');
    expect(result.current.toasts[0].id).toBeTruthy();
  });

  it('dismiss(id) removes exactly that toast', () => {
    const { result } = renderHook(() => useToasts());
    let firstId = '';
    act(() => {
      firstId = result.current.show({ intent: 'info', message: 'First' });
      result.current.show({ intent: 'info', message: 'Second' });
    });
    expect(result.current.toasts).toHaveLength(2);
    act(() => {
      result.current.dismiss(firstId);
    });
    expect(result.current.toasts).toHaveLength(1);
    expect(result.current.toasts[0].message).toBe('Second');
  });
});

describe('ToastViewport', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders nothing when there are no toasts', () => {
    const { container } = render(<ToastViewport toasts={[]} onDismiss={() => {}} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('announces the message and intent is not the only signal (icon + text both present)', () => {
    render(
      <ToastViewport
        toasts={[{ id: 't1', intent: 'success', message: 'Workflow published' }]}
        onDismiss={() => {}}
      />,
    );
    const toast = screen.getByRole('status');
    expect(toast).toHaveAttribute('aria-live', 'polite');
    expect(toast).toHaveTextContent('Workflow published');
    expect(toast.querySelector('svg')).not.toBeNull();
  });

  it('a danger toast announces assertively', () => {
    render(<ToastViewport toasts={[{ id: 't1', intent: 'danger', message: 'Publish failed' }]} onDismiss={() => {}} />);
    expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'assertive');
  });

  it('dismiss button calls onDismiss with the toast id', async () => {
    vi.useRealTimers(); // userEvent needs real timers
    const onDismiss = vi.fn();
    render(<ToastViewport toasts={[{ id: 't1', intent: 'info', message: 'Hello' }]} onDismiss={onDismiss} />);
    await userEvent.click(screen.getByRole('button', { name: 'Dismiss notification' }));
    expect(onDismiss).toHaveBeenCalledWith('t1');
  });

  it('auto-dismisses a non-danger toast after its duration', () => {
    const onDismiss = vi.fn();
    render(<ToastViewport toasts={[{ id: 't1', intent: 'success', message: 'Saved', duration: 1000 }]} onDismiss={onDismiss} />);
    act(() => {
      vi.advanceTimersByTime(999);
    });
    expect(onDismiss).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(2);
    });
    expect(onDismiss).toHaveBeenCalledWith('t1');
  });

  it('never auto-dismisses a danger toast, regardless of duration (must be acknowledged)', () => {
    const onDismiss = vi.fn();
    render(<ToastViewport toasts={[{ id: 't1', intent: 'danger', message: 'Publish failed', duration: 1000 }]} onDismiss={onDismiss} />);
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it('has no automated accessibility violations', async () => {
    vi.useRealTimers();
    const { container } = render(
      <ToastViewport
        toasts={[
          { id: 't1', intent: 'success', message: 'Saved', duration: 0 },
          { id: 't2', intent: 'warning', message: 'Slow response', duration: 0 },
        ]}
        onDismiss={() => {}}
      />,
    );
    await expectNoAxeViolations(container);
  });
});
