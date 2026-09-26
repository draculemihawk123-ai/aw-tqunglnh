import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { DefinitionEditorDialog } from './DefinitionEditorDialog';
import type { DefinitionView } from '../api/definitions';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    validateDefinitionDraft: vi.fn(), validateProjectDefinitionDraft: vi.fn(),
    publishDefinitionVersion: vi.fn(), publishProjectDefinitionVersion: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const BLOCK: DefinitionView = { id: 'blk-1', kind: 'BLOCK', scope: { global: true }, name: 'My Block', status: 'DRAFT', version: 1 };
const WORKFLOW: DefinitionView = { id: 'wf-1', kind: 'WORKFLOW', scope: { global: true }, name: 'My Workflow', status: 'DRAFT', version: 1 };

const VALIDATED = {
  id: 'ver-1', definitionId: 'blk-1', kind: 'BLOCK', versionNumber: 1, schemaVersion: 1,
  canonicalSource: '{}', sourceHash: 'sha256:aaa', compiledSnapshot: '{"compiled":true}', compiledHash: 'sha256:bbb',
  dependencies: { pins: [] }, publishedBy: 'local-operator', publishedAt: '2026-01-01T00:00:00Z',
};

describe('DefinitionEditorDialog', () => {
  beforeEach(() => vi.clearAllMocks());

  it('Publish is disabled until a successful Validate', async () => {
    render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Publish…' })).toBeDisabled();
  });

  it('validating calls the real API with the typed content, then enables Publish', async () => {
    vi.mocked(api.validateDefinitionDraft).mockResolvedValue(VALIDATED as never);
    render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);

    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{"a":1}' } });
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }));

    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish…' })).not.toBeDisabled());
    expect(api.validateDefinitionDraft).toHaveBeenCalledWith('BLOCK', 'blk-1', expect.objectContaining({ content: '{"a":1}', format: 'json' }), expect.anything());
    expect(await screen.findByText(/Valid — SourceHash/)).toBeInTheDocument();
  });

  it('editing the content after a successful validate disables Publish again until re-validated', async () => {
    vi.mocked(api.validateDefinitionDraft).mockResolvedValue(VALIDATED as never);
    render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{"a":1}' } });
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish…' })).not.toBeDisabled());

    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{"a":12}' } });

    expect(screen.getByRole('button', { name: 'Publish…' })).toBeDisabled();
  });

  it('a validation failure shows real per-field diagnostics, never a fabricated hash', async () => {
    vi.mocked(api.validateDefinitionDraft).mockRejectedValue(
      new api.ApiError(400, 'INVALID_REQUEST', 'document validation failed', [{ field: 'outcomes', message: 'line 3, column 1: outcomes is required' }]),
    );
    render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);
    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{}' } });
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }));

    expect(await screen.findByText('outcomes is required', { exact: false })).toBeInTheDocument();
    expect(screen.getByText('outcomes', { selector: 'span.font-mono' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Publish…' })).toBeDisabled();
  });

  it('the confirm step shows the exact validated hashes and publishes the exact same body', async () => {
    vi.mocked(api.validateDefinitionDraft).mockResolvedValue(VALIDATED as never);
    vi.mocked(api.publishDefinitionVersion).mockResolvedValue({ ...VALIDATED, versionNumber: 1 } as never);
    const onPublished = vi.fn();
    render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={onPublished} />);
    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{"a":1}' } });
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Publish…' })).not.toBeDisabled());

    await userEvent.click(screen.getByRole('button', { name: 'Publish…' }));
    expect(await screen.findByRole('heading', { name: 'Publish My Block' })).toBeInTheDocument();
    expect(screen.getByText('sha256:aaa')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Confirm Publish' }));
    await waitFor(() => expect(onPublished).toHaveBeenCalled());
    expect(api.publishDefinitionVersion).toHaveBeenCalledWith('BLOCK', 'blk-1', expect.objectContaining({ content: '{"a":1}', format: 'json' }), expect.anything());
  });

  it('WORKFLOW kinds never send a format/dependencies field and hide the pin editor', async () => {
    vi.mocked(api.validateDefinitionDraft).mockResolvedValue({ ...VALIDATED, kind: 'WORKFLOW' } as never);
    render(<DefinitionEditorDialog definition={WORKFLOW} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);
    expect(screen.queryByText('Dependency Pins')).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Document content'), { target: { value: '{"schemaVersion":"1"}' } });
    await userEvent.click(screen.getByRole('button', { name: 'Validate' }));
    await waitFor(() => expect(api.validateDefinitionDraft).toHaveBeenCalled());
    const body = vi.mocked(api.validateDefinitionDraft).mock.calls[0][2];
    expect(body.format).toBeUndefined();
    expect(body.dependencies).toBeUndefined();
  });

  it('has no automated accessibility violations', async () => {
    const { container } = render(<DefinitionEditorDialog definition={BLOCK} scope="global" onClose={vi.fn()} onPublished={vi.fn()} />);
    await expectNoAxeViolations(container);
  });
});
