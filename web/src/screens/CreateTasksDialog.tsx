import { useMemo, useState } from 'react';
import { useQueries, useQuery } from '@tanstack/react-query';
import { createChildWorkItem, getProjectTaskDocuments, getWorkspaceSetState, listProjectDefinitionVersions, listProjectDefinitions, markWorkItemReady, setWorkItemQueue } from '../api/generated';
import { withSessionToken } from '../api/session';
import { fetchWorkspaceSource } from '../api/workspaceinspection';
import type { WorkspaceSetState } from '../api/workspaceinspection';
import type { WorkflowChoice } from '../api/newTask';
import { childRequestForTask, parseTasksJson, workflowForTask } from '../api/tasksJson';
import type { TaskSpec, TasksProblem } from '../api/tasksJson';
import { Button, Dialog, InlineError } from '../components/ui';

interface DefinitionView { id: string; name: string }
interface VersionFieldsView { id: string; versionNumber: number }

interface Row { task: TaskSpec; include: boolean; workflowVersionId: string }
interface Outcome { id: string; workItemId?: string; error?: string }

/**
 * V12-22 (docs/design/15-v12-ui-operations.md, UX-09): after the DESIGN gate of wf-feature-definition, create the child tasks the
 * design wrote into docs/features/<slug>/tasks.json — read from the family's worktree (or pasted), previewed, each with the workflow
 * its risk suggests, created in order, made ready, and (optionally) queued so they run one after another (V12-19). Replaces
 * kit/scripts/split-tasks.sh + run-task.sh.
 */
export function CreateTasksDialog({ projectId, parentWorkItemId, familyId, isOffline, onClose, onCreated }: {
  projectId: string; parentWorkItemId: string; familyId?: string; isOffline?: boolean; onClose: () => void; onCreated: (ids: string[]) => void;
}) {
  const [slug, setSlug] = useState('');
  const [pasted, setPasted] = useState('');
  const [docsRepository, setDocsRepository] = useState('');
  const [rows, setRows] = useState<Row[]>([]);
  const [problems, setProblems] = useState<TasksProblem[]>([]);
  const [readError, setReadError] = useState('');
  const [queue, setQueue] = useState(true);
  const [busy, setBusy] = useState(false);
  const [outcomes, setOutcomes] = useState<Outcome[]>([]);

  // V12-33 (ADR-065): where each task's frame.md and plan.md go.
  const taskDocumentsQuery = useQuery({
    queryKey: ['projectTaskDocuments', projectId],
    queryFn: async () => (await getProjectTaskDocuments(projectId, withSessionToken())) as unknown as { location: string },
    enabled: !isOffline,
  });
  const setQuery = useQuery({
    queryKey: ['workspaceSetState', projectId, familyId],
    queryFn: async () => (await getWorkspaceSetState(projectId, familyId!, withSessionToken())) as unknown as WorkspaceSetState,
    enabled: !isOffline && !!familyId,
  });
  const repos = (setQuery.data?.repositoryWorkspaces ?? []).filter(r => r.state === 'READY' && !!r.currentRevision);
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
    const latest = [...(versionQueries[i]?.data ?? [])].sort((a, b) => b.versionNumber - a.versionNumber)[0];
    return latest ? [{ definitionId: def.id, name: def.name, versionId: latest.id, versionNumber: latest.versionNumber }] : [];
  }), [workflowsQuery.data, versionQueries]);

  function load(text: string, fromRepository: string) {
    const parsed = parseTasksJson(text);
    setProblems(parsed.problems);
    setDocsRepository(fromRepository);
    setRows(parsed.tasks.map(task => ({ task, include: true, workflowVersionId: workflowForTask(task, choices)?.versionId ?? '' })));
    setOutcomes([]);
  }

  async function readFromWorktree() {
    setReadError('');
    const path = `docs/features/${slug.trim()}/tasks.json`;
    for (const repo of repos) {
      try {
        const source = await fetchWorkspaceSource(projectId, repo.repositoryWorkspaceId, {
          repositoryId: repo.repositoryId, workspaceSetId: repo.workspaceSetId, path, revision: repo.currentRevision!, generation: repo.generation,
        });
        if (!source.binary && source.content.trim()) {
          load(source.content, repo.repositoryId);
          return;
        }
      } catch {
        /* not in this repository: try the next one */
      }
    }
    setReadError(`${path} is not in the recorded revision of any repository of this family. If the design is not committed yet, paste tasks.json below.`);
  }

  async function create() {
    setBusy(true);
    const selected = rows.filter(r => r.include);
    const done: Outcome[] = [];
    const ctx = { slug: slug.trim(), docsRepository: docsRepository || repos[0]?.repositoryId || '', repositories: (setQuery.data?.repositoryWorkspaces ?? []).map(r => r.repositoryId),
      taskDocuments: taskDocumentsQuery.data?.location };
    for (const row of selected) {
      try {
        const created = await createChildWorkItem(projectId, parentWorkItemId,
          childRequestForTask(row.task, { ...ctx, workflowVersionId: row.workflowVersionId }) as never,
          withSessionToken({ idempotencyKey: `tasks-json:${parentWorkItemId}:${ctx.slug}:${row.task.id}` }));
        const outcome: Outcome = { id: row.task.id, workItemId: created.workItemId };
        try {
          await markWorkItemReady(created.workItemId, {}, withSessionToken({ ifMatch: '"1"' }));
          if (queue) await setWorkItemQueue(created.workItemId, { queued: true }, withSessionToken({ idempotencyKey: `tasks-json-queue:${created.workItemId}` }));
        } catch (e) {
          outcome.error = `created, but ${queue ? 'marking it ready or queueing it' : 'marking it ready'} failed: ${e instanceof Error ? e.message : String(e)}`;
        }
        done.push(outcome);
      } catch (e) {
        done.push({ id: row.task.id, error: e instanceof Error ? e.message : String(e) });
        break; // the order matters: stop at the first task that could not be created
      }
      setOutcomes([...done]);
    }
    setOutcomes(done);
    setBusy(false);
    const ids = done.filter(o => o.workItemId).map(o => o.workItemId!);
    if (ids.length === selected.length && done.every(o => !o.error)) onCreated(ids);
  }

  const missingWorkflow = rows.some(r => r.include && !r.workflowVersionId);
  return (
    <Dialog wide title="Create tasks from tasks.json" onClose={onClose}
      description="The tasks the feature design wrote, created as child tasks in order (the same scope and contract split-tasks.sh writes)."
      actions={<>
        <Button intent="secondary" onClick={onClose} disabled={busy}>Close</Button>
        <Button intent="primary" loading={busy} disabled={isOffline || busy || !rows.some(r => r.include) || missingWorkflow || !slug.trim()} onClick={() => { void create(); }}>
          Create {rows.filter(r => r.include).length} task{rows.filter(r => r.include).length === 1 ? '' : 's'}
        </Button>
      </>}>
      <div className="flex items-end gap-2 flex-wrap">
        <label className="text-[12px] text-[#475569]">
          Feature folder (docs/features/…)
          <input aria-label="Feature slug" value={slug} onChange={e => setSlug(e.target.value)} placeholder="order-status"
            className="block mt-1 border border-[#CDD5DF] rounded-[6px] px-2 py-1 text-[13px]" />
        </label>
        <Button size="compact" intent="secondary" disabled={isOffline || !slug.trim() || repos.length === 0} onClick={() => { void readFromWorktree(); }}>Read tasks.json</Button>
      </div>
      {readError && <p className="text-[12px] text-[#92400E] mt-2" role="alert">{readError}</p>}
      <details className="mt-2">
        <summary className="text-[12px] text-[#3659E3] cursor-pointer">Paste tasks.json instead</summary>
        <textarea aria-label="Pasted tasks.json" rows={4} value={pasted} onChange={e => setPasted(e.target.value)}
          className="w-full mt-1 border border-[#CDD5DF] rounded-[6px] p-2 font-mono text-[12px]" />
        <Button size="compact" intent="secondary" disabled={!pasted.trim()} onClick={() => load(pasted, repos[0]?.repositoryId ?? '')}>Use this</Button>
      </details>
      {problems.length > 0 && (
        <ul className="mt-2 text-[12px] text-[#991B1B]" aria-label="Problems in tasks.json">
          {problems.map((p, i) => <li key={i}>{p.message}</li>)}
        </ul>
      )}
      {rows.length > 0 && (
        <table className="w-full mt-3 text-[12px]" aria-label="Tasks to create">
          <thead><tr className="text-left text-[#475569]"><th className="py-1">Create</th><th>Task</th><th>Repository · scope</th><th>Risk</th><th>Workflow</th><th>Result</th></tr></thead>
          <tbody>
            {rows.map((row, i) => {
              const outcome = outcomes.find(o => o.id === row.task.id);
              return (
                <tr key={row.task.id} className="border-t border-[#ECEFF4] align-top" data-testid={`task-row-${row.task.id}`}>
                  <td className="py-1"><input type="checkbox" aria-label={`Create ${row.task.id}`} checked={row.include}
                    onChange={e => setRows(rs => rs.map((r, j) => j === i ? { ...r, include: e.target.checked } : r))} /></td>
                  <td><span className="font-mono">{row.task.id}</span> {row.task.title}<div className="text-[#475569]">{row.task.acceptanceCriteria.length} acceptance criteria · lane {row.task.lane ?? '—'}</div></td>
                  <td className="font-mono">{row.task.repository ?? docsRepository} · {[...new Set([...row.task.pathScopes, 'docs'])].sort().join(', ')}</td>
                  <td>{row.task.riskLevel}</td>
                  <td>
                    <select aria-label={`Workflow of ${row.task.id}`} value={row.workflowVersionId}
                      onChange={e => setRows(rs => rs.map((r, j) => j === i ? { ...r, workflowVersionId: e.target.value } : r))}
                      className="border border-[#CDD5DF] rounded-[4px] px-1 py-0.5 text-[12px] bg-white">
                      <option value="">choose…</option>
                      {choices.map(c => <option key={c.versionId} value={c.versionId}>{c.name} v{c.versionNumber}</option>)}
                    </select>
                  </td>
                  <td>{outcome?.workItemId && !outcome.error && <span className="text-[#166534]">created</span>}
                    {outcome?.error && <span className="text-[#991B1B]">{outcome.error}</span>}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {rows.length > 0 && (
        <label className="flex items-center gap-2 mt-3 text-[12px] text-[#475569]">
          <input type="checkbox" checked={queue} onChange={e => setQueue(e.target.checked)} />
          Queue them in this order (the next starts when the one before is done)
        </label>
      )}
      {outcomes.some(o => o.error) && <InlineError code="CREATE_TASKS" message="Some tasks were not created or not queued; see the Result column." />}
    </Dialog>
  );
}
