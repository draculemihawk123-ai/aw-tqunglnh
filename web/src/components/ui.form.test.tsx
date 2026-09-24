import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { Checkbox, Select, TextField } from './ui';

describe('TextField', () => {
  it('links its label to the input via htmlFor/id', () => {
    render(<TextField label="Repository URL" value="" />);
    // getByLabelText only succeeds if <label htmlFor> really points at the
    // input's id — this is what makes a screen reader announce the label
    // when the field receives focus.
    expect(screen.getByLabelText('Repository URL')).toBeInTheDocument();
  });

  it("links its error message to the input via aria-describedby (V7-03's own completion bar: error liên kết field)", () => {
    render(<TextField label="Repository URL" value="bad-url" error="Must be a valid Git URL" />);
    const input = screen.getByLabelText('Repository URL');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    const describedBy = input.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    const errorEl = document.getElementById(describedBy!);
    expect(errorEl).toHaveTextContent('Must be a valid Git URL');
    expect(errorEl).toHaveAttribute('role', 'alert');
  });

  it('links helper text (not just error) via aria-describedby when there is no error', () => {
    render(<TextField label="Repository URL" value="" helper="e.g. https://github.com/org/repo.git" />);
    const input = screen.getByLabelText('Repository URL');
    expect(input).not.toHaveAttribute('aria-invalid', 'true');
    const describedBy = input.getAttribute('aria-describedby');
    expect(document.getElementById(describedBy!)).toHaveTextContent('e.g. https://github.com/org/repo.git');
  });

  it('marks a required field for assistive tech, not just visually', () => {
    render(<TextField label="Repository URL" value="" required />);
    // The label's own accessible text includes the visual "*" (rendered as
    // a sibling span inside <label>), so match loosely on the label text.
    expect(screen.getByLabelText(/Repository URL/)).toHaveAttribute('aria-required', 'true');
  });

  it('calls onChange with the new value', async () => {
    const onChange = vi.fn();
    render(<TextField label="Name" value="" onChange={onChange} />);
    await userEvent.type(screen.getByLabelText('Name'), 'x');
    expect(onChange).toHaveBeenCalledWith('x');
  });

  it('has no automated accessibility violations, with or without an error', async () => {
    const clean = render(<TextField label="Name" value="ok" />);
    await expectNoAxeViolations(clean.container);
    clean.unmount();

    const errored = render(<TextField label="Name" value="" error="Required" />);
    await expectNoAxeViolations(errored.container);
  });
});

describe('Select', () => {
  it('links its label to the control and lists every option', () => {
    render(
      <Select
        label="Kind"
        value="WORKFLOW"
        onChange={() => {}}
        options={[
          { value: 'WORKFLOW', label: 'Workflow' },
          { value: 'GATE', label: 'Gate' },
        ]}
      />,
    );
    const select = screen.getByLabelText('Kind');
    expect(select).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Workflow' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Gate' })).toBeInTheDocument();
  });

  it('calls onChange with the selected value', async () => {
    const onChange = vi.fn();
    render(
      <Select
        label="Kind"
        value="WORKFLOW"
        onChange={onChange}
        options={[
          { value: 'WORKFLOW', label: 'Workflow' },
          { value: 'GATE', label: 'Gate' },
        ]}
      />,
    );
    await userEvent.selectOptions(screen.getByLabelText('Kind'), 'GATE');
    expect(onChange).toHaveBeenCalledWith('GATE');
  });
});

describe('Checkbox', () => {
  it('links its label to the input (clicking the label toggles it)', async () => {
    const onChange = vi.fn();
    render(<Checkbox label="Include archived" checked={false} onChange={onChange} />);
    await userEvent.click(screen.getByLabelText('Include archived'));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<Checkbox label="Include archived" checked={true} onChange={() => {}} />);
    await expectNoAxeViolations(container);
  });
});
