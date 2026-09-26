import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { DefinitionsScreen } from './Definitions';
import type { ProjectSummary } from './Projects';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    listDefinitions: vi.fn(), listProjectDefinitions: vi.fn(),
    listDefinitionVersions: vi.fn(), listProjectDefinitionVersions: vi.fn(),
    getDefinition: vi.fn(), getProjectDefinition: vi.fn(),
    createDefinition: vi.fn(), createProjectDefinition: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function renderScreen(project: ProjectSummary | null = null, initialScope: 'global' | 'project' = 'global') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <DefinitionsScreen project={project} initialScope={initialScope} />
    </QueryClientProvider>,
  );
}

/** Every one of the 9 real Kinds returns empty except the ones a test explicitly overrides — mirrors how the screen really fires all 9 in parallel. */
function mockEmptyCatalog() {
  vi.mocked(api.listDefinitions).mockResolvedValue({ definitions: [] } as never);
  vi.mocked(api.listProjectDefinitions).mockResolvedValue({ definitions: [] } as never);
}

const BLOCK_ONE = { id: 'blk-1', kind: 'BLOCK', scope: { global: true }, name: 'My Block', status: 'ACTIVE', version: 2 };
const SKILL_ONE = { id: 'skl-1', kind: 'SKILL', scope: { global: true }, name: 'My Skill', status: 'DRAFT', version: 1 };

const VERSION_ONE = {
  id: 'ver-1', definitionId: 'blk-1', kind: 'BLOCK', versionNumber: 1, schemaVersion: 1,
  canonicalSource: 'kind: BLOCK', sourceHash: 'sha256:aaa', compiledSnapshot: '{"compiled":true}', compiledHash: 'sha256:bbb',
  dependencies: { pins: [{ kind: 'POLICY', definitionId: 'pol-1', versionId: 'pol-1-v1' }] },
  publishedBy: 'local-operator', publishedAt: '2026-01-01T00:00:00Z',
};
const VERSION_TWO = { ...VERSION_ONE, id: 'ver-2', versionNumber: 2, sourceHash: 'sha256:ccc', compiledHash: 'sha256:ddd', dependencies: { pins: [] } };

describe('DefinitionsScreen — catalog', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows a loading skeleton before the catalog queries resolve', () => {
    vi.mocked(api.listDefinitions).mockReturnValue(new Promise(() => {}));
    const { container } = renderScreen();
    expect(container.querySelectorAll('[aria-hidden="true"]').length).toBeGreaterThan(0);
  });

  it('merges results from multiple real Kinds into one catalog, sorted by name', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) => {
      if (kind === 'BLOCK') return { definitions: [BLOCK_ONE] } as never;
      if (kind === 'SKILL') return { definitions: [SKILL_ONE] } as never;
      return { definitions: [] } as never;
    });
    renderScreen();
    expect(await screen.findByText('My Block')).toBeInTheDocument();
    expect(screen.getByText('My Skill')).toBeInTheDocument();
  });

  it('shows an empty state when the scope has no definitions at all', async () => {
    mockEmptyCatalog();
    renderScreen();
    expect(await screen.findByText('No definitions')).toBeInTheDocument();
  });

  it('the kind filter narrows the catalog to just that Kind', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) => {
      if (kind === 'BLOCK') return { definitions: [BLOCK_ONE] } as never;
      if (kind === 'SKILL') return { definitions: [SKILL_ONE] } as never;
      return { definitions: [] } as never;
    });
    renderScreen();
    await screen.findByText('My Block');
    await userEvent.click(screen.getByRole('button', { name: 'SKILL' }));
    expect(screen.getByText('My Skill')).toBeInTheDocument();
    expect(screen.queryByText('My Block')).not.toBeInTheDocument();
  });

  it('the project scope toggle is disabled with no project selected', async () => {
    mockEmptyCatalog();
    renderScreen(null, 'global');
    await screen.findByText('No definitions');
    expect(screen.getByRole('button', { name: 'Project' })).toBeDisabled();
  });
});

describe('DefinitionsScreen — version detail', () => {
  beforeEach(() => vi.clearAllMocks());

  it('selecting a definition shows its latest published version — real hashes and dependency pins', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) =>
      (kind === 'BLOCK' ? { definitions: [BLOCK_ONE] } : { definitions: [] }) as never);
    vi.mocked(api.getDefinition).mockResolvedValue(BLOCK_ONE as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [VERSION_ONE, VERSION_TWO] } as never);
    renderScreen();

    await userEvent.click(await screen.findByText('My Block'));

    expect(await screen.findByText('sha256:ccc')).toBeInTheDocument(); // latest (v2) shown by default
    expect(screen.getByText('sha256:ddd')).toBeInTheDocument();
    expect(screen.getByText('No dependency pins.')).toBeInTheDocument();
  });

  it('the version selector switches the shown hashes to an earlier immutable version', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) =>
      (kind === 'BLOCK' ? { definitions: [BLOCK_ONE] } : { definitions: [] }) as never);
    vi.mocked(api.getDefinition).mockResolvedValue(BLOCK_ONE as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [VERSION_ONE, VERSION_TWO] } as never);
    renderScreen();
    await userEvent.click(await screen.findByText('My Block'));
    await screen.findByText('sha256:ccc');

    await userEvent.selectOptions(screen.getByLabelText('Version'), 'ver-1');

    expect(await screen.findByText('sha256:aaa')).toBeInTheDocument();
    expect(screen.getByText('POLICY:pol-1@pol-1-v1')).toBeInTheDocument();
  });

  it('a definition with no published versions shows an honest empty state, not a fabricated hash', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) =>
      (kind === 'SKILL' ? { definitions: [SKILL_ONE] } : { definitions: [] }) as never);
    vi.mocked(api.getDefinition).mockResolvedValue(SKILL_ONE as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [] } as never);
    renderScreen();
    await userEvent.click(await screen.findByText('My Skill'));
    expect(await screen.findByText('No published versions')).toBeInTheDocument();
  });

  it('has no automated accessibility violations with a definition selected', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) =>
      (kind === 'BLOCK' ? { definitions: [BLOCK_ONE] } : { definitions: [] }) as never);
    vi.mocked(api.getDefinition).mockResolvedValue(BLOCK_ONE as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [VERSION_ONE] } as never);
    const { container } = renderScreen();
    await userEvent.click(await screen.findByText('My Block'));
    await screen.findByText('sha256:aaa');
    await expectNoAxeViolations(container);
  });
});

describe('DefinitionsScreen — create + author flow wiring', () => {
  beforeEach(() => vi.clearAllMocks());

  it('"New Definition" opens the real create dialog and selects the new definition on success', async () => {
    mockEmptyCatalog();
    vi.mocked(api.createDefinition).mockResolvedValue({ definitionId: 'blk-new', kind: 'BLOCK' } as never);
    vi.mocked(api.getDefinition).mockResolvedValue({ id: 'blk-new', kind: 'BLOCK', scope: { global: true }, name: 'New Block', status: 'DRAFT', version: 1 } as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [] } as never);
    renderScreen();
    await screen.findByText('No definitions');

    await userEvent.click(screen.getByRole('button', { name: /New Definition/ }));
    expect(await screen.findByRole('heading', { name: 'New Definition' })).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/Definition ID/), 'blk-new');
    await userEvent.type(screen.getByLabelText(/^Name/), 'New Block');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText(/created as a draft/)).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Author new version…' })).toBeInTheDocument();
  });

  it('"Author new version" opens the real editor dialog for the selected definition', async () => {
    mockEmptyCatalog();
    vi.mocked(api.listDefinitions).mockImplementation(async (kind: string) =>
      (kind === 'BLOCK' ? { definitions: [BLOCK_ONE] } : { definitions: [] }) as never);
    vi.mocked(api.getDefinition).mockResolvedValue(BLOCK_ONE as never);
    vi.mocked(api.listDefinitionVersions).mockResolvedValue({ items: [] } as never);
    renderScreen();

    await userEvent.click(await screen.findByText('My Block'));
    await screen.findByText('No published versions');
    await userEvent.click(screen.getByRole('button', { name: 'Author new version…' }));

    expect(await screen.findByRole('heading', { name: 'Edit My Block' })).toBeInTheDocument();
  });
});
