import { useMemo, useState } from 'react';
import { useMutation, useQueries, useQuery } from '@tanstack/react-query';
import { ApiError, createChildWorkItem, listProjectDefinitionVersions, listProjectDefinitions, projectComponentsList, projectRepositoriesList } from '../api/generated';
import type { ComponentView, RepositoryView } from '../api/catalog';
import type { DefinitionView, VersionFieldsView } from '../api/definitions';
import { DEFAULT_PARENT_JOIN_POLICY, TEMPLATES, checkDraft, childRequest, draftFromJSON, suggestWorkflow, workflowUseWhen } from '../api/newTask';
import type { RiskLevel, TaskDraft, TaskKind, WorkflowChoice } from '../api/newTask';
import { withSessionToken } from '../api/session';
import { Button, Dialog, InlineError, Select, TextField } from '../components/ui';

/**
 * V12-14 (docs/design/15-v12-ui-operations.md, UX-02/03/25/38/42): "New task" inside a family — a child work item with a contract,
 * from a template by kind of work, with a workflow chosen from what the project published (each with "use when" and a suggestion),
 * a default parent join policy, the checks before creating, or a pasted work-item JSON. It replaces writing a JSON file and
 * running run-task.sh. The server still decides (createChildWorkItem refuses a scope wider than the parent's).
 */
export interface NewTaskDialogProps {
  projectId: string;
  parentWorkItemId: string;
  isOffline?: boolean;
  onClose: () => void;
  onCreated: (workItemId: string) => void;
}

function templateDraft(kind: TaskKind, prev?: TaskDraft): TaskDraft {
  const t = TEMPLATES[kind];
  return {
    title: prev?.title ?? '', kind, behavior: t.behavior, criteria: t.criteria.map(description => ({ description })),
    verificationSpec: t.verificationSpec, riskLevel: t.riskLevel, exclusions: [],
    scopes: prev?.scopes ?? [], workflowVersionId: prev?.workflowVersionId ?? '', parentJoinPolicy: DEFAULT_PARENT_JOIN_POLICY,
  };
}

function errorText(err: unknown): string {
  if (err instanceof ApiError) return [err.message, ...(err.details ?? []).map(d => `${d.field}: ${d.message}`)].join(' — ');
  return err instanceof Error ? err.message : 'unexpected error';
}

export function NewTaskDialog({ projectId, parentWorkItemId, isOffline, onClose, onCreated }: NewTaskDialogProps) {
  const [draft, setDraft] = useState<TaskDraft>(() => templateDraft('feature'));
  const [mode, setMode] = useState<'form' | 'json'>('form');
  const [json, setJson] = useState('');
  const [jsonError, setJsonError] = useState('');
  const [attempted, setAttempted] = useState(false);

  const reposQuery = useQuery({
    queryKey: ['projectRepositories', projectId],
    queryFn: async () => (await projectRepositoriesList(projectId, withSessionToken())) as unknown as { repositories: RepositoryView[] },
    enabled: !isOffline,
  });
  const componentsQuery = useQuery({
    queryKey: ['projectComponents', projectId],
    queryFn: async () => (await projectComponentsList(projectId, withSessionToken())) as unknown as { components: ComponentView[] },
    enabled: !isOffline,
  });
  const workflowsQuery = useQuery({
    queryKey: ['definitionsOfKind', 'project', projectId, 'WORKFLOW'],
    queryFn: async () => ((await listProjectDefinitions(projectId, 'WORKFLOW', withSessionToken())) as unknown as { definitions: DefinitionView[] }).definitions,
    enabled: !isOffline,
  });
  const versionQueries = useQueries({
    queries: (workflowsQuery.data ?? []).map(def => ({
      queryKey: ['definitionVersions', 'project', projectId, 'WORKFLOW', def.id],
      queryFn: async () => ((await listProjectDefinitionVersions(projectId, 'WORKFLOW', def.id, withSessionToken())) as unknown as { items: VersionFieldsView[] }).items,
      enabled: !isOffline,
    })),
  });
  const choices: WorkflowChoice[] = useMemo(() => (workflowsQuery.data ?? []).flatMap((def, i) => {
    const versions = versionQueries[i]?.data ?? [];
    const latest = [...versions].sort((a, b) => b.versionNumber - a.versionNumber)[0];
    return latest ? [{ definitionId: def.id, name: def.name, versionId: latest.id, versionNumber: latest.versionNumber }] : [];
  }), [workflowsQuery.data, versionQueries]);

  const repos = (reposQuery.data?.repositories ?? []).filter(r => r.status === 'ACTIVE');
  const components = componentsQuery.data?.components ?? [];
  const suggested = suggestWorkflow(draft, choices);
  const chosen = choices.find(c => c.versionId === draft.workflowVersionId);
  const problems = checkDraft(draft, chosen, suggested);
  const errors = problems.filter(p => p.severity === 'error');
  const warnings = problems.filter(p => p.severity === 'warning');

  const create = useMutation({
    mutationFn: () => createChildWorkItem(projectId, parentWorkItemId, childRequest(draft) as never, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: result => onCreated(result.workItemId),
  });

  const set = (patch: Partial<TaskDraft>) => setDraft(d => ({ ...d, ...patch }));
  const setScope = (i: number, patch: Partial<TaskDraft['scopes'][number]>) =>
    set({ scopes: draft.scopes.map((s, j) => (j === i ? { ...s, ...patch } : s)) });

  const submit = () => {
    setAttempted(true);
    if (errors.length === 0) create.mutate();
  };

  return (
    <Dialog
      title="New task"
      description="A child task of this work item, in the same family and worktree. Start from a template, then fill in the contract."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={create.isPending}>Cancel</Button>
          <Button intent="primary" loading={create.isPending} disabled={isOffline || mode === 'json'} onClick={submit}>Create task</Button>
        </>
      }
    >
      <div className="flex gap-2 mb-3" role="group" aria-label="How to enter the task">
        <Button size="compact" intent={mode === 'form' ? 'primary' : 'quiet'} onClick={() => setMode('form')}>Form</Button>
        <Button size="compact" intent={mode === 'json' ? 'primary' : 'quiet'} onClick={() => setMode('json')}>Paste JSON</Button>
      </div>

      {mode === 'json' ? (
        <div className="space-y-2">
          <label className="block text-[12px] text-[#475569]" htmlFor="new-task-json">The work-item JSON (the file run-task.sh takes)</label>
          <textarea id="new-task-json" value={json} onChange={e => setJson(e.target.value)} rows={10}
            className="w-full font-mono text-[12px] border border-[#CDD5DF] rounded-[6px] p-2" />
          {jsonError && <p className="text-[12px] text-[#991B1B]" role="alert">{jsonError}</p>}
          <Button size="compact" intent="secondary" onClick={() => {
            try {
              setDraft(d => draftFromJSON(json, d));
              setJsonError('');
              setMode('form');
            } catch (e) {
              setJsonError(e instanceof Error ? e.message : String(e));
            }
          }}>Use this JSON</Button>
        </div>
      ) : (
        <div className="space-y-3">
          <Select label="Kind of work" value={draft.kind} onChange={v => setDraft(templateDraft(v as TaskKind, draft))}
            options={(Object.keys(TEMPLATES) as TaskKind[]).map(k => ({ value: k, label: TEMPLATES[k].label }))} />
          <TextField label="Title" required value={draft.title} onChange={v => set({ title: v })} placeholder="P1-02: what this task does" />
          <TextField label="Behavior (WHAT)" required value={draft.behavior} onChange={v => set({ behavior: v })} />
          <fieldset className="space-y-1">
            <legend className="text-[12px] font-medium text-[#172033]">Acceptance criteria</legend>
            {draft.criteria.map((c, i) => (
              <div key={i} className="flex gap-2">
                <input aria-label={`Criterion ${i + 1}`} value={c.description}
                  onChange={e => set({ criteria: draft.criteria.map((x, j) => (j === i ? { ...x, description: e.target.value } : x)) })}
                  className="flex-1 text-[13px] border border-[#CDD5DF] rounded-[6px] px-2 py-1" />
                <Button size="compact" intent="quiet" onClick={() => set({ criteria: draft.criteria.filter((_, j) => j !== i) })} aria-label={`Remove criterion ${i + 1}`}>✕</Button>
              </div>
            ))}
            <Button size="compact" intent="quiet" onClick={() => set({ criteria: [...draft.criteria, { description: '' }] })}>Add criterion</Button>
          </fieldset>
          <TextField label="How it is verified" value={draft.verificationSpec} onChange={v => set({ verificationSpec: v })} />
          <Select label="Risk" value={draft.riskLevel} onChange={v => set({ riskLevel: v as RiskLevel })}
            options={['LOW', 'MEDIUM', 'HIGH'].map(r => ({ value: r, label: r }))} />

          <fieldset className="space-y-2">
            <legend className="text-[12px] font-medium text-[#172033]">Scope — what the task may change</legend>
            {draft.scopes.map((s, i) => (
              <div key={i} className="border border-[#ECEFF4] rounded-[6px] p-2 space-y-1">
                <div className="flex items-center gap-2">
                  <span className="text-[13px] font-medium">{repos.find(r => r.id === s.repositoryId)?.name ?? s.repositoryId}</span>
                  <span className="flex-1" />
                  <Button size="compact" intent="quiet" onClick={() => set({ scopes: draft.scopes.filter((_, j) => j !== i) })}>Remove</Button>
                </div>
                <label className="block text-[12px] text-[#475569]" htmlFor={`paths-${i}`}>Folders (comma-separated; empty = the whole repository)</label>
                <input id={`paths-${i}`} list={`dirs-${s.repositoryId}`} value={s.pathScopes.join(', ')}
                  onChange={e => setScope(i, { pathScopes: e.target.value.split(',').map(p => p.trim()).filter(Boolean) })}
                  className="w-full text-[13px] font-mono border border-[#CDD5DF] rounded-[6px] px-2 py-1" />
                <datalist id={`dirs-${s.repositoryId}`}>
                  {components.filter(c => c.repositoryId === s.repositoryId).map(c => <option key={c.id} value={c.path}>{c.name}</option>)}
                  <option value="docs">docs</option>
                </datalist>
              </div>
            ))}
            <Select label="Add a repository" value="" onChange={v => v && set({ scopes: [...draft.scopes, { repositoryId: v, pathScopes: [], reason: '' }] })}
              options={[{ value: '', label: 'Choose…' }, ...repos.filter(r => !draft.scopes.some(s => s.repositoryId === r.id)).map(r => ({ value: r.id, label: r.name }))]} />
          </fieldset>

          <fieldset className="space-y-1">
            <legend className="text-[12px] font-medium text-[#172033]">Workflow</legend>
            {choices.length === 0 && <p className="text-[12px] text-[#475569]">No workflow is published for this project yet.</p>}
            <div role="radiogroup" aria-label="Workflow" className="space-y-1">
              {choices.map(c => (
                <label key={c.versionId} className="flex items-start gap-2 text-[13px]">
                  <input type="radio" name="workflow" checked={draft.workflowVersionId === c.versionId} onChange={() => set({ workflowVersionId: c.versionId })} className="mt-1" />
                  <span>
                    <strong>{c.name}</strong> <span className="text-[12px] text-[#475569]">v{c.versionNumber}</span>
                    {suggested?.versionId === c.versionId && <span className="ml-1 text-[11px] px-1 rounded bg-[#DCFCE7] text-[#166534]">suggested</span>}
                    {workflowUseWhen(c.definitionId) && <span className="block text-[12px] text-[#475569]">{workflowUseWhen(c.definitionId)}</span>}
                  </span>
                </label>
              ))}
            </div>
          </fieldset>

          {(warnings.length > 0 || (attempted && errors.length > 0)) && (
            <ul className="space-y-1" data-testid="new-task-problems">
              {(attempted ? errors : []).map((p, i) => <li key={`e${i}`} className="text-[12px] text-[#991B1B]" role="alert">{p.message}</li>)}
              {warnings.map((p, i) => <li key={`w${i}`} className="text-[12px] text-[#92400E]">{p.message}</li>)}
            </ul>
          )}
          {create.isError && <InlineError code="CREATE_FAILED" message={errorText(create.error)} />}
        </div>
      )}
    </Dialog>
  );
}
