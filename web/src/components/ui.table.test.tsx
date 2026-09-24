import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expectNoAxeViolations } from '../test/axe';
import { Table, type TableColumn } from './ui';

interface Row {
  id: string;
  name: string;
  kind: string;
}

const ROWS: Row[] = [
  { id: 'def-1', name: 'Onboard repository', kind: 'WORKFLOW' },
  { id: 'def-2', name: 'Nightly cleanup', kind: 'GATE' },
];

const COLUMNS: TableColumn<Row>[] = [
  { key: 'name', header: 'Name', render: (row) => row.name },
  { key: 'kind', header: 'Kind', render: (row) => row.kind },
];

describe('Table', () => {
  it('links every header to its column via scope="col" (screen reader column association)', () => {
    render(<Table columns={COLUMNS} rows={ROWS} getRowKey={(r) => r.id} />);
    const nameHeader = screen.getByRole('columnheader', { name: 'Name' });
    expect(nameHeader).toHaveAttribute('scope', 'col');
  });

  it('renders every row and cell via the column render function', () => {
    render(<Table columns={COLUMNS} rows={ROWS} getRowKey={(r) => r.id} />);
    expect(screen.getByText('Onboard repository')).toBeInTheDocument();
    expect(screen.getByText('Nightly cleanup')).toBeInTheDocument();
    expect(screen.getByText('WORKFLOW')).toBeInTheDocument();
    expect(screen.getByText('GATE')).toBeInTheDocument();
  });

  it('a column with no visible header text still gets an accessible name via headerLabel', () => {
    const columnsWithActions: TableColumn<Row>[] = [
      ...COLUMNS,
      { key: 'actions', header: '', headerLabel: 'Actions', render: () => <button>Edit</button> },
    ];
    render(<Table columns={columnsWithActions} rows={ROWS} getRowKey={(r) => r.id} />);
    expect(screen.getByRole('columnheader', { name: 'Actions' })).toBeInTheDocument();
  });

  it('renders the emptyState instead of an empty table when there are no rows', () => {
    render(<Table columns={COLUMNS} rows={[]} getRowKey={(r) => r.id} emptyState={<p>No definitions yet</p>} />);
    expect(screen.getByText('No definitions yet')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('onRowClick is a mouse convenience, not a role change on <tr>', async () => {
    const onRowClick = vi.fn();
    render(<Table columns={COLUMNS} rows={ROWS} getRowKey={(r) => r.id} onRowClick={onRowClick} />);
    const rows = screen.getAllByRole('row');
    // rows[0] is the header row; data rows keep the native "row" role, never "button".
    expect(rows[1]).not.toHaveAttribute('role', 'button');
    await userEvent.click(screen.getByText('Onboard repository'));
    expect(onRowClick).toHaveBeenCalledWith(ROWS[0]);
  });

  it('marks the selected row visually distinct via isRowSelected', () => {
    render(<Table columns={COLUMNS} rows={ROWS} getRowKey={(r) => r.id} isRowSelected={(r) => r.id === 'def-2'} />);
    const rows = screen.getAllByRole('row');
    expect(rows[2].className).toContain('bg-[#EEF2FF]');
    expect(rows[1].className).not.toContain('bg-[#EEF2FF]');
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<Table columns={COLUMNS} rows={ROWS} getRowKey={(r) => r.id} />);
    await expectNoAxeViolations(container);
  });
});
