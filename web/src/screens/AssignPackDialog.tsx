import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { componentPackAssignmentsAssign, componentPackAssignmentsList, ApiError } from '../api/generated';
import type { ComponentView, PackAssignmentListView } from '../api/catalog';
import { withSessionToken } from '../api/session';
import { Button, CopyableId, Dialog, InlineError, Skeleton, TextField } from '../components/ui';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

function formatTimestamp(value: string): string {
  return new Date(value).toLocaleString();
}

/**
 * AssignPackDialog — V7-06B's own "exact-version Engineering Pack
 * assignment cho component" (docs/design/09-v7-alpha-ui.md V7-06's own
 * line). Deliberately a single required text field, not a browsable
 * version picker: internal/domain/project/component.go's own PackVersionID
 * doc comment says this plainly — "it never resolves or validates that the
 * pack version actually exists ... a later task ... owns that check if one
 * is ever needed" — the same real constraint `aw pack-assignment assign`
 * (internal/delivery/cli/catalog/packassignment.go) accepts today. A
 * browsable catalog of published Engineering Pack versions is V7-07's own
 * scope ("Definition catalog và version detail"), not this task's; building
 * a picker now would either duplicate that later work or invent a listing
 * API this leaf was never given.
 */
export function AssignPackDialog({ component, onClose, onAssigned }: {
  component: ComponentView; onClose: () => void; onAssigned: (message: string) => void;
}) {
  const [packVersionId, setPackVersionId] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const queryClient = useQueryClient();

  const assignmentsQuery = useQuery({
    queryKey: ['componentPackAssignments', component.id],
    queryFn: async () => (await componentPackAssignmentsList(component.id, withSessionToken())) as unknown as PackAssignmentListView,
  });

  const mutation = useMutation({
    mutationFn: () => componentPackAssignmentsAssign(component.id, { packVersionId: packVersionId.trim() }, withSessionToken()),
  });

  const trimmed = packVersionId.trim();

  function handleAssign() {
    setSubmitted(true);
    if (!trimmed) return;
    mutation.mutate(undefined, {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: ['componentPackAssignments', component.id] });
        onAssigned(`Pack "${trimmed}" assigned to ${component.name}.`);
        onClose();
      },
    });
  }

  return (
    <Dialog
      title="Assign Engineering Pack"
      description={`Pin an exact, immutable Engineering Pack version to ${component.name}.`}
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} onClick={handleAssign}>Assign</Button>
        </>
      }
    >
      <div className="space-y-4">
        <dl className="grid grid-cols-[100px_1fr] gap-x-3 gap-y-1 text-[13px]">
          <dt className="text-[#5D697A]">Component</dt><dd><CopyableId value={component.id} short={component.name} /></dd>
          <dt className="text-[#5D697A]">Path</dt><dd className="font-mono text-[12px] text-[#172033]">{component.path}</dd>
        </dl>

        <div>
          <h3 className="text-[12px] font-semibold text-[#172033] uppercase tracking-wide mb-1.5">Currently effective</h3>
          {assignmentsQuery.isPending ? (
            <Skeleton className="h-8 w-full" />
          ) : assignmentsQuery.isError ? (
            <InlineError {...apiErrorMessage(assignmentsQuery.error)} onRetry={() => assignmentsQuery.refetch()} />
          ) : assignmentsQuery.data?.effective ? (
            <div className="flex items-center gap-2 text-[13px]">
              <span className="font-mono">{assignmentsQuery.data.effective.packVersionId}</span>
              <span className="text-[12px] text-[#5D697A]">since {formatTimestamp(assignmentsQuery.data.effective.effectiveAt)}</span>
            </div>
          ) : (
            <p className="text-[13px] text-[#5D697A]">No pack assigned yet.</p>
          )}
        </div>

        {!assignmentsQuery.isPending && !assignmentsQuery.isError && (assignmentsQuery.data?.assignments.length ?? 0) > 1 && (
          <div>
            <h3 className="text-[12px] font-semibold text-[#172033] uppercase tracking-wide mb-1.5">Assignment history</h3>
            <ul className="space-y-1 max-h-32 overflow-y-auto">
              {[...(assignmentsQuery.data?.assignments ?? [])].reverse().map(a => (
                <li key={a.id} className="text-[12px] text-[#475569] flex items-center gap-2">
                  <span className="font-mono">{a.packVersionId}</span>
                  <span>— {formatTimestamp(a.effectiveAt)} by {a.actor}</span>
                </li>
              ))}
            </ul>
          </div>
        )}

        <TextField label="Pack version ID" required mono value={packVersionId} error={submitted && !trimmed ? 'required' : undefined}
          helper="the exact, published Engineering Pack version identity — not validated against the definitions catalog yet"
          placeholder="engpack-backend@2.4.2" onChange={setPackVersionId} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}

