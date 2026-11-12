import { useMutation, useQuery } from '@tanstack/react-query';
import { getProjectTaskDocuments, setProjectTaskDocuments } from '../api/generated';
import { withSessionToken } from '../api/session';
import { InlineError } from '../components/ui';

interface TaskDocumentsView { projectId: string; location: 'REPO' | 'AW' | string; directoryPattern: string; version?: number }

/**
 * V12-33 (docs/design/15-v12-ui-operations.md, UX-45, ADR-065): where the kit's per-task documents (frame.md, plan.md) are written —
 * in the repository (the default, committed with the task) or only in aw (.aw/ of the worktree, which git ignores; shown on the task's
 * Workspace tab with "Export to repo"). 02-spec.md, 03-design.md and tasks.json always stay in the repository. A task keeps the
 * directory its contract was created with.
 */
export function TaskDocumentsPanel({ projectId, isOffline, readOnly }: { projectId: string; isOffline?: boolean; readOnly?: boolean }) {
  const query = useQuery({
    queryKey: ['projectTaskDocuments', projectId],
    queryFn: async () => (await getProjectTaskDocuments(projectId, withSessionToken())) as unknown as TaskDocumentsView,
    enabled: !isOffline && !!projectId,
  });
  const save = useMutation({
    mutationFn: (location: string) => setProjectTaskDocuments(projectId, { location }, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { void query.refetch(); },
  });
  if (!query.data) return null;
  const current = query.data.location;
  return (
    <section className="bg-white border border-[#CDD5DF] rounded-[8px] p-4" aria-label="Task documents">
      <h2 className="text-[14px] font-semibold text-[#172033]">Task documents</h2>
      <p className="text-[12px] text-[#475569] mt-0.5">
        Where each task's <code>frame.md</code> and <code>plan.md</code> go. The feature's <code>02-spec.md</code>, <code>03-design.md</code> and
        <code> tasks.json</code> always stay in the repository. Applies to tasks created from now on.
      </p>
      <fieldset className="mt-2 space-y-1 text-[13px]" disabled={isOffline || readOnly || save.isPending}>
        <legend className="sr-only">Location</legend>
        <label className="flex items-center gap-2">
          <input type="radio" name="task-documents" checked={current === 'REPO'} onChange={() => save.mutate('REPO')} />
          In the repository <span className="font-mono text-[12px] text-[#475569]">docs/features/&lt;feature&gt;/tasks/&lt;task&gt;/</span> (committed)
        </label>
        <label className="flex items-center gap-2">
          <input type="radio" name="task-documents" checked={current === 'AW'} onChange={() => save.mutate('AW')} />
          Only in aw <span className="font-mono text-[12px] text-[#475569]">.aw/features/&lt;feature&gt;/tasks/&lt;task&gt;/</span> (not committed; Export to repo on the Workspace tab)
        </label>
      </fieldset>
      {save.isError && <InlineError code="TASK_DOCUMENTS" message={save.error instanceof Error ? save.error.message : 'Saving failed.'} />}
    </section>
  );
}

export type { TaskDocumentsView };
