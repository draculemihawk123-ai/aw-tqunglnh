import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { Button, IconButton } from './ui';

describe('Button', () => {
  it('calls onClick when activated by mouse', async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Save</Button>);
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('calls onClick when activated by keyboard (Enter/Space)', async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Save</Button>);
    const button = screen.getByRole('button', { name: 'Save' });
    button.focus();
    await userEvent.keyboard('{Enter}');
    expect(onClick).toHaveBeenCalledTimes(1);
    await userEvent.keyboard(' ');
    expect(onClick).toHaveBeenCalledTimes(2);
  });

  it('is disabled and unclickable while loading', async () => {
    const onClick = vi.fn();
    render(<Button loading onClick={onClick}>Save</Button>);
    const button = screen.getByRole('button', { name: 'Save' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-disabled', 'true');
    await userEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
  });

  it('disabled prop disables and marks aria-disabled', () => {
    render(<Button disabled>Save</Button>);
    const button = screen.getByRole('button', { name: 'Save' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-disabled', 'true');
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<Button intent="primary">Save</Button>);
    await expectNoAxeViolations(container);
  });
});

describe('IconButton', () => {
  it('exposes its label to assistive tech via aria-label, not just a tooltip', () => {
    render(<IconButton label="Close panel">×</IconButton>);
    expect(screen.getByRole('button', { name: 'Close panel' })).toBeInTheDocument();
  });

  it('calls onClick', async () => {
    const onClick = vi.fn();
    render(<IconButton label="Close panel" onClick={onClick}>×</IconButton>);
    await userEvent.click(screen.getByRole('button', { name: 'Close panel' }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
