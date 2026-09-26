import { useEffect, useMemo, useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  getDefinition, listDefinitions, listProjectDefinitions,
  listDefinitionVersions, listProjectDefinitionVersions, getProjectDefinition,
} from '../api/generated';
import { DEFINITION_KINDS } from '../api/definitions';
import type { DefinitionKind, DefinitionView, VersionFieldsView } from '../api/definitions';
import { withSessionToken } from '../api/session';
import {
  Badge, Button, CopyableId, EmptyState, InlineError, Select, Skeleton, StatusBadge,
  ToastViewport, useToasts,
} from '../components/ui';
import type { BadgeIntent } from '../components/ui';
import { Plus } from '../components/icons';
import type { ProjectSummary } from './Projects';
import { CreateDefinitionDialog } from './CreateDefinitionDialog';
import { DefinitionEditorDialog } from './DefinitionEditorDialog';

type DefScope = 'global' | 'project';

const STATUS_INTENT: Record<DefinitionView['status'], BadgeIntent> = {
  DRAFT: 'neutral',
  ACTIVE: 'success',
  ARCHIVED: 'warning',
};

interface DefinitionsScreenProps {
  project: ProjectSummary | null;
  initialScope: DefScope;
  isOffline?: boolean;
}

/**
 * V7-07B's own real catalog + version-detail screen — the read-only half
 * of docs/design/09-v7-alpha-ui.md's own V7-07 line ("catalog filters,
 * immutable version selector, SourceHash/CompiledSnapshotHash, dependency/
 * resource/adapter pins và compatibility diagnostics"). Replaces the
 * Figma-Make prototype's entirely fake DEFINITIONS array/YAML editor
 * wholesale — the declarative editor + validate/publish flow this same
 * file used to bolt on top of that fake data is V7-08's own separate scope
 * ("Declarative editor, validate và publish"), deliberately not rebuilt
 * here against real data: a real editor needs its own real design, not a
 * fake one wearing real catalog data underneath it.
 *
 * There is no single "list every Definition of every Kind" endpoint
 * (internal/delivery/httpapi/definitions only ever lists one Kind at a
 * time, GET /definitions/{kind} — V7-07A's own new route) — this screen
 * fires all nine Kind queries in parallel per scope and merges them
 * client-side, which is exactly the "browse all definition kinds" V7-07's
 * own goal describes, still a handful of local requests for a
 * single-operator installation's own catalog.
 */
export function DefinitionsScreen({ project, initialScope, isOffline = false }: DefinitionsScreenProps) {
  const [scope, setScope] = useState<DefScope>(initialScope);
  const [kindFilter, setKindFilter] = useState<DefinitionKind | 'ALL'>('ALL');
  const [selected, setSelected] = useState<{ kind: DefinitionKind; id: string } | null>(null);
  const [selectedVersionId, setSelectedVersionId] = useState<string | null>(null);
  const [createDialog, setCreateDialog] = useState(false);
  const [editorDialog, setEditorDialog] = useState(false);
  const { toasts, show, dismiss } = useToasts();
  const queryClient = useQueryClient();

  useEffect(() => {
    setScope(initialScope);
    setSelected(null);
  }, [initialScope, project?.id]);

  const projectId = project?.id;
  const catalogQueries = useQueries({
    queries: DEFINITION_KINDS.map(kind => ({
      queryKey: ['definitionsOfKind', scope, projectId, kind],
      queryFn: async () => {
        const result = scope === 'project' && projectId
          ? await listProjectDefinitions(projectId, kind, withSessionToken())
          : await listDefinitions(kind, withSessionToken());
        return (result as unknown as { definitions: DefinitionView[] }).definitions;
      },
      enabled: !isOffline && (scope === 'global' || !!projectId),
    })),
  });

  const isLoadingCatalog = catalogQueries.some(q => q.isPending);
  const catalogError = catalogQueries.find(q => q.isError)?.error;
  const definitions = useMemo(
    () => catalogQueries.flatMap(q => q.data ?? []).sort((a, b) => a.name.localeCompare(b.name)),
    [catalogQueries],
  );
  const filtered = kindFilter === 'ALL' ? definitions : definitions.filter(d => d.kind === kindFilter);

  const versionsQuery = useQuery({
    queryKey: ['definitionVersions', scope, projectId, selected?.kind, selected?.id],
    queryFn: async () => {
      const result = scope === 'project' && projectId
        ? await listProjectDefinitionVersions(projectId, selected!.kind, selected!.id, withSessionToken())
        : await listDefinitionVersions(selected!.kind, selected!.id, withSessionToken());
      return (result as unknown as { items: VersionFieldsView[] }).items;
    },
    enabled: !isOffline && !!selected,
  });

  const detailQuery = useQuery({
    queryKey: ['definitionDetail', scope, projectId, selected?.kind, selected?.id],
    queryFn: async () => {
      const result = scope === 'project' && projectId
        ? await getProjectDefinition(projectId, selected!.kind, selected!.id, withSessionToken())
        : await getDefinition(selected!.kind, selected!.id, withSessionToken());
      return result as unknown as DefinitionView;
    },
    enabled: !isOffline && !!selected,
  });

  const versions = versionsQuery.data ?? [];
  useEffect(() => {
    if (versions.length === 0) { setSelectedVersionId(null); return; }
    if (!versions.some(v => v.id === selectedVersionId)) {
      setSelectedVersionId(versions[versions.length - 1].id); // oldest-first order; last = latest
    }
  }, [versions, selectedVersionId]);
  const selectedVersion = versions.find(v => v.id === selectedVersionId) ?? null;

  const changeScope = (nextScope: DefScope) => {
    setScope(nextScope);
    setSelected(null);
  };

  const selectDefinition = (d: DefinitionView) => {
    setSelected({ kind: d.kind, id: d.id });
    setSelectedVersionId(null);
  };

  return (
    <div className="flex-1 flex overflow-hidden">
      <div className="flex-1 flex flex-col overflow-hidden border-r border-[#CDD5DF]">
        <div className="px-6 py-4 border-b border-[#CDD5DF] bg-white">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-xl font-semibold text-[#172033]">Definitions</h1>
              <p className="text-[12px] text-[#475569] mt-0.5">{scope === 'project' ? `Project scope · ${project?.name ?? 'Select a project'} · ${project?.id ?? '—'}` : 'Global / Installation scope'}</p>
            </div>
            <Button intent="primary" size="compact" onClick={() => setCreateDialog(true)} icon={<Plus size={13} aria-hidden />} disabled={isOffline || (scope === 'project' && !project)}>New Definition</Button>
          </div>
          <div className="flex items-center gap-3 mt-3">
            <div className="flex rounded-[6px] border border-[#CDD5DF] overflow-hidden">
              {(['global', 'project'] as const).map(s => (
                <button key={s} onClick={() => changeScope(s)} aria-pressed={scope === s} disabled={s === 'project' && !project}
                  className={`px-3 py-1.5 text-xs font-medium capitalize transition-colors disabled:opacity-50 ${scope === s ? 'bg-[#3659E3] text-white' : 'bg-white text-[#5D697A] hover:bg-[#F3F5F8]'}`}>
                  {s === 'global' ? 'Global / Installation' : 'Project'}
                </button>
              ))}
            </div>
            <div className="flex gap-1 flex-wrap">
              <button onClick={() => setKindFilter('ALL')}
                className={`px-2.5 py-1 text-xs rounded-[6px] border transition-colors ${kindFilter === 'ALL' ? 'bg-[#EEF2FF] text-[#3659E3] border-[#C7D2FE] font-medium' : 'bg-white text-[#5D697A] border-[#CDD5DF] hover:border-[#AAB4C3]'}`}>
                all
              </button>
              {DEFINITION_KINDS.map(k => (
                <button key={k} onClick={() => setKindFilter(k)}
                  className={`px-2.5 py-1 text-xs rounded-[6px] border transition-colors ${kindFilter === k ? 'bg-[#EEF2FF] text-[#3659E3] border-[#C7D2FE] font-medium' : 'bg-white text-[#5D697A] border-[#CDD5DF] hover:border-[#AAB4C3]'}`}>
                  {k}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto bg-[#E9EDF3] p-4">
          {isLoadingCatalog ? (
            <div className="space-y-2" aria-hidden><Skeleton className="h-10 w-full" /><Skeleton className="h-10 w-full" /><Skeleton className="h-10 w-full" /></div>
          ) : catalogError ? (
            <InlineError code="UNKNOWN" message={catalogError instanceof Error ? catalogError.message : 'failed to load catalog'} />
          ) : filtered.length === 0 ? (
            <EmptyState title="No definitions" description={scope === 'project' ? 'No definitions have been created in this project scope yet.' : 'No definitions have been created at the global scope yet.'} />
          ) : (
            <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-[#ECEFF4] bg-[#F8FAFC]">
                    <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Name / ID</th>
                    <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Kind</th>
                    <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Status</th>
                    <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Generation</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#ECEFF4]">
                  {filtered.map(def => (
                    <tr key={`${def.kind}:${def.id}`}
                      onClick={() => selectDefinition(def)}
                      className={`hover:bg-[#F8FAFC] cursor-pointer transition-colors ${selected?.id === def.id && selected?.kind === def.kind ? 'bg-[#EEF2FF]' : ''}`}>
                      <td className="px-5 py-3">
                        <div className="font-medium text-[#172033]">{def.name}</div>
                        <CopyableId value={def.id} />
                      </td>
                      <td className="px-5 py-3"><Badge label={def.kind} intent="neutral" /></td>
                      <td className="px-5 py-3"><StatusBadge state={def.status} /></td>
                      <td className="px-5 py-3 font-mono text-xs text-[#5D697A]">v{def.version}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {!selected ? (
        <p className="p-5 text-[13px] text-[#475569] w-80 xl:w-96 flex-shrink-0">Select a definition to inspect its published versions.</p>
      ) : (
        <div className="w-80 xl:w-96 flex flex-col bg-[#F3F5F8] flex-shrink-0 overflow-y-auto">
          <div className="px-5 py-4 border-b border-[#CDD5DF] bg-white">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold text-[#172033]">{detailQuery.data?.name ?? selected.id}</h2>
              <Badge label={selected.kind} intent="neutral" />
            </div>
            <div className="flex items-center gap-2 mt-1">
              {detailQuery.data && <Badge label={detailQuery.data.status} intent={STATUS_INTENT[detailQuery.data.status]} />}
              <span className="text-[12px] text-[#475569]">generation {detailQuery.data?.version ?? '—'}</span>
            </div>
            <Button intent="secondary" size="compact" className="mt-2 w-full" disabled={isOffline} onClick={() => setEditorDialog(true)}>Author new version…</Button>
          </div>
          <div className="p-5 space-y-5">
            {(detailQuery.isError || versionsQuery.isError) && (
              <InlineError code="UNKNOWN" message="failed to load definition detail" onRetry={() => { detailQuery.refetch(); versionsQuery.refetch(); }} />
            )}

            {versionsQuery.isPending ? (
              <Skeleton className="h-8 w-full" />
            ) : versions.length === 0 ? (
              <EmptyState title="No published versions" description="This definition has never been published — it exists only as a draft." />
            ) : (
              <>
                <div>
                  <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Version</h3>
                  <Select
                    label="Version"
                    value={selectedVersionId ?? ''}
                    onChange={setSelectedVersionId}
                    options={[...versions].reverse().map(v => ({ value: v.id, label: `v${v.versionNumber} — immutable` }))}
                  />
                </div>
                {selectedVersion && (
                  <>
                    <div>
                      <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Hashes</h3>
                      <div className="space-y-2 bg-white rounded-[8px] border border-[#CDD5DF] p-3">
                        <div>
                          <div className="text-[12px] text-[#475569] mb-0.5">SourceHash</div>
                          <div className="font-mono text-xs text-[#172033] break-all">{selectedVersion.sourceHash}</div>
                        </div>
                        <div className="h-px bg-[#ECEFF4]" />
                        <div>
                          <div className="text-[12px] text-[#475569] mb-0.5">CompiledSnapshotHash</div>
                          <div className="font-mono text-xs text-[#172033] break-all">{selectedVersion.compiledHash}</div>
                        </div>
                      </div>
                    </div>
                    <div>
                      <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Dependency Pins ({selectedVersion.dependencies.pins?.length ?? 0})</h3>
                      <div className="space-y-1">
                        {(selectedVersion.dependencies.pins?.length ?? 0) > 0 ? (
                          selectedVersion.dependencies.pins!.map(pin => (
                            <div key={`${pin.kind}:${pin.definitionId}:${pin.versionId}`} className="font-mono text-xs text-[#172033] bg-white rounded-[6px] border border-[#CDD5DF] px-3 py-2">
                              {pin.kind}:{pin.definitionId}@{pin.versionId}
                            </div>
                          ))
                        ) : <div className="text-[12px] text-[#475569]">No dependency pins.</div>}
                      </div>
                    </div>
                    <div>
                      <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Compiled Snapshot</h3>
                      <p className="text-[12px] text-[#475569] mb-1.5">Resource/adapter pins live inside this kind's own compiled document — shown raw rather than parsed for a specific kind's schema.</p>
                      <pre className="font-mono text-[11px] text-[#172033] bg-white rounded-[6px] border border-[#CDD5DF] p-3 max-h-48 overflow-auto whitespace-pre-wrap break-all">{selectedVersion.compiledSnapshot}</pre>
                    </div>
                    <div className="text-[12px] text-[#475569]">
                      Published by {selectedVersion.publishedBy} at {new Date(selectedVersion.publishedAt).toLocaleString()}
                    </div>
                  </>
                )}
              </>
            )}
          </div>
        </div>
      )}

      {createDialog && (
        <CreateDefinitionDialog
          scope={scope}
          projectId={projectId}
          onClose={() => setCreateDialog(false)}
          onCreated={(kind, id) => {
            queryClient.invalidateQueries({ queryKey: ['definitionsOfKind', scope, projectId, kind] });
            setCreateDialog(false);
            setSelected({ kind, id });
            show({ intent: 'success', message: `Definition "${id}" created as a draft.` });
          }}
        />
      )}
      {editorDialog && selected && detailQuery.data && (
        <DefinitionEditorDialog
          definition={detailQuery.data}
          scope={scope}
          projectId={projectId}
          onClose={() => setEditorDialog(false)}
          onPublished={version => {
            queryClient.invalidateQueries({ queryKey: ['definitionVersions', scope, projectId, selected.kind, selected.id] });
            queryClient.invalidateQueries({ queryKey: ['definitionDetail', scope, projectId, selected.kind, selected.id] });
            queryClient.invalidateQueries({ queryKey: ['definitionsOfKind', scope, projectId, selected.kind] });
            setEditorDialog(false);
            setSelectedVersionId(version.id);
            show({ intent: 'success', message: `Published v${version.versionNumber} of "${detailQuery.data?.name}".` });
          }}
        />
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}
