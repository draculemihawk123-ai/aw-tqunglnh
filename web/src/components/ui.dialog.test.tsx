import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { Dialog, Drawer } from './ui';

describe('Dialog', () => {
  it('moves focus into the dialog on open', () => {
    render(<Dialog title="Confirm" onClose={() => {}}><p>Body</p></Dialog>);
    // Focus landed on the first focusable element inside the dialog (its
    // own <h2 tabIndex={-1}> title), not left on whatever the page had
    // focused before the dialog opened.
    expect(screen.getByRole('heading', { name: 'Confirm' })).toHaveFocus();
  });

  it('restores focus to whatever was focused before the dialog opened, on unmount', () => {
    render(
      <div>
        <button>Open trigger</button>
      </div>,
    );
    const trigger = screen.getByRole('button', { name: 'Open trigger' });
    trigger.focus();
    const { unmount } = render(<Dialog title="Confirm" onClose={() => {}}><p>Body</p></Dialog>);
    expect(screen.getByRole('heading', { name: 'Confirm' })).toHaveFocus();
    unmount();
    expect(trigger).toHaveFocus();
  });

  it('Escape calls onClose unless isDurable', async () => {
    const onClose = vi.fn();
    render(<Dialog title="Confirm" onClose={onClose}><p>Body</p></Dialog>);
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('durable dialogs ignore Escape (no accidental dismissal of an in-flight operation)', async () => {
    const onClose = vi.fn();
    render(<Dialog title="Deploying" onClose={onClose} isDurable><p>Body</p></Dialog>);
    await userEvent.keyboard('{Escape}');
    expect(onClose).not.toHaveBeenCalled();
  });

  it('durable dialogs render no close button', () => {
    render(<Dialog title="Deploying" onClose={() => {}} isDurable><p>Body</p></Dialog>);
    expect(screen.queryByRole('button', { name: 'Close dialog' })).not.toBeInTheDocument();
  });

  it('Tab wraps from the last focusable element back to the first (focus trap)', async () => {
    render(
      <Dialog
        title="Confirm"
        onClose={() => {}}
        actions={<button>Confirm</button>}
      >
        <p>Body</p>
      </Dialog>,
    );
    const confirmButton = screen.getByRole('button', { name: 'Confirm' });
    confirmButton.focus();
    await userEvent.tab();
    // Wrapped back to the first focusable element (the dialog's own title).
    expect(screen.getByRole('heading', { name: 'Confirm' })).toHaveFocus();
  });

  it('is exposed as a modal dialog labelled by its own title', () => {
    render(<Dialog title="Confirm deletion" onClose={() => {}}><p>Body</p></Dialog>);
    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Confirm deletion');
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(
      <Dialog title="Confirm" description="Are you sure?" onClose={() => {}} actions={<button>OK</button>}>
        <p>Body</p>
      </Dialog>,
    );
    await expectNoAxeViolations(container);
  });
});

describe('Drawer', () => {
  it('Escape calls onClose', async () => {
    const onClose = vi.fn();
    render(<Drawer title="Details" onClose={onClose}><p>Body</p></Drawer>);
    await userEvent.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('its close button has an accessible name', () => {
    render(<Drawer title="Details" onClose={() => {}}><p>Body</p></Drawer>);
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeInTheDocument();
  });
});
