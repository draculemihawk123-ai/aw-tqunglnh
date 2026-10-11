import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { ApiError, projectsArchive, projectsRename } from '../api/generated';
import type { ProjectView } from '../api/catalog';
import { withSessionToken } from '../api/session';
import { Button, Dialog, InlineError, TextField } from '../components/ui';

/**
 * V11-13 (ADR-044) — rename and archive a project. Archiving replaces deleting: nothing is erased, the project becomes read-only and leaves the default
 * list, and Restore brings it back exactly as it was. The server refuses while something is still running or open and names each thing.
 */

export const MAX_PROJECT_NAME = 200;

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) {
    const code = (err.details ?? []).find(d => d.field === 'repository')?.message ?? err.code;
    return { code, message: err.message };
  }
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

function refusalItems(err: unknown): string[] {
  return err instanceof ApiError ? (err.details ?? []).filter(d => d.field === 'items').map(d => d.message) : [];
}

export function RenameProjectDialog({ project, onClose, onDone }: { project: ProjectView; onClose: () => void; onDone: (message: string) => void }) {
  const [name, setName] = useState(project.name);
  const trimmed = name.trim();
  const problem = !trimmed ? 'a name is required' : trimmed.length > MAX_PROJECT_NAME ? `at most ${MAX_PROJECT_NAME} characters (now ${trimmed.length})` : undefined;
  const unchanged = trimmed === project.name;
  const mutation = useMutation({
    mutationFn: () => projectsRename(project.id, { name: trimmed }, withSessionToken({ ifMatch: `"${project.version}"` })),
  });
  return (
    <Dialog
      title={`Rename ${project.name}`}
      description="Only the name changes; the project id, repositories and history stay as they are."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} disabled={!!problem || unchanged}
            onClick={() => mutation.mutate(undefined, { onSuccess: () => onDone(`Renamed to ${trimmed}.`) })}>Rename</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Project name" required value={name} onChange={setName} error={problem && name !== project.name ? problem : undefined} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}

export function ArchiveProjectDialog({ project, onClose, onDone }: { project: ProjectView; onClose: () => void; onDone: (message: string) => void }) {
  const [reason, setReason] = useState('');
  const mutation = useMutation({
    mutationFn: () => projectsArchive(project.id, { reason: reason.trim() }, withSessionToken({ ifMatch: `"${project.version}"` })),
  });
  const items = refusalItems(mutation.error);
  return (
    <Dialog
      title={`Archive ${project.name}`}
      description="Archiving does not delete anything. The project becomes read-only, leaves the default list, and can be restored at any time. It cannot be archived while a run, an active work item or a release is still open."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="destructive" loading={mutation.isPending}
            onClick={() => mutation.mutate(undefined, { onSuccess: () => onDone(`${project.name} archived.`) })}>Archive</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Reason (optional)" value={reason} onChange={setReason} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
        {items.length > 0 && (
          <ul className="text-[12px] text-[#172033] list-disc pl-5 space-y-0.5" aria-label="Still running or open" data-testid="project-holders">
            {items.map(item => <li key={item}>{item}</li>)}
          </ul>
        )}
      </div>
    </Dialog>
  );
}
