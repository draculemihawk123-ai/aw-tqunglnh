import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { ApiError, exportAwDocuments, listAwDocuments } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';
import { Button, Dialog, InlineError } from '../components/ui';

interface AwDocument { path: string; size: number; content: string; truncated?: boolean }
interface ExportResult { exported: string[]; unchanged: string[]; conflicts: string[] }

/**
 * V12-33 (docs/design/15-v12-ui-operations.md, UX-45, ADR-065): the task documents this worktree keeps only in aw (.aw/features/…,
 * ignored by git) — read them here, and "Export to repo" copies them into docs/features/… of the worktree, uncommitted, for the normal
 * commit. A document that already exists there with other content stops the export until "Overwrite" is confirmed.
 */
export function AwDocumentsPanel({ projectId, repo, isOffline }: { projectId: string; repo: RepositoryWorkspaceState; isOffline?: boolean }) {
  const query = useQuery({
    queryKey: ['awDocuments', projectId, repo.repositoryWorkspaceId, repo.version],
    queryFn: async () => (await listAwDocuments(projectId, repo.repositoryWorkspaceId, withSessionToken())) as unknown as { documents: AwDocument[] },
    enabled: !isOffline && repo.state === 'READY',
    retry: false,
  });
  const [open, setOpen] = useState<AwDocument | null>(null);
  const [conflicts, setConflicts] = useState<string[]>([]);
  const [done, setDone] = useState<ExportResult | null>(null);
  const run = useMutation({
    mutationFn: async (overwrite: boolean) =>
      (await exportAwDocuments(projectId, repo.repositoryWorkspaceId, overwrite ? { overwrite } : {}, withSessionToken({ idempotencyKey: crypto.randomUUID() }))) as unknown as ExportResult,
    onSuccess: result => { setConflicts([]); setDone(result); },
    onError: err => { if (err instanceof ApiError && err.status === 409) setConflicts((err.details ?? []).map(d => d.field)); },
  });
  const docs = query.data?.documents ?? [];
  if (repo.state !== 'READY' || docs.length === 0) return null;
  return (
    <div className="px-4 py-2 bg-white border-b border-[#CDD5DF] text-[12px] text-[#475569] flex-shrink-0 space-y-1" data-testid="aw-documents">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-medium text-[#172033]">Task documents in aw</span>
        <span>{docs.length} file{docs.length === 1 ? '' : 's'}, not committed</span>
        <span className="flex-1" />
        <Button size="compact" intent="secondary" disabled={isOffline || run.isPending} onClick={() => { setDone(null); run.mutate(false); }}>Export to repo</Button>
      </div>
      <ul className="flex flex-wrap gap-x-3" aria-label="Task documents">
        {docs.map(d => (
          <li key={d.path}><button type="button" className="font-mono text-[#3659E3] hover:underline" onClick={() => setOpen(d)}>{d.path.replace(/^\.aw\/features\//, '')}</button></li>
        ))}
      </ul>
      {done && <p className="text-[#166534]" data-testid="aw-export-done">Exported {done.exported.length} file{done.exported.length === 1 ? '' : 's'} to docs/features/ (uncommitted){done.unchanged.length ? `; ${done.unchanged.length} already there` : ''}.</p>}
      {conflicts.length > 0 && (
        <div className="text-[#92400E]" data-testid="aw-export-conflicts">
          Already in the repository with other content: <span className="font-mono">{conflicts.join(', ')}</span>.{' '}
          <Button size="compact" intent="destructive" disabled={isOffline || run.isPending} onClick={() => run.mutate(true)}>Overwrite</Button>
        </div>
      )}
      {run.isError && conflicts.length === 0 && <InlineError code="EXPORT" message={run.error instanceof Error ? run.error.message : 'The export failed.'} />}
      {open && (
        <Dialog title={open.path} onClose={() => setOpen(null)} actions={<Button intent="secondary" onClick={() => setOpen(null)}>Close</Button>}>
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap font-mono text-[12px] text-[#172033]">{open.content}</pre>
          {open.truncated && <p className="text-[12px] text-[#475569]">Only the first 256 KiB are shown.</p>}
        </Dialog>
      )}
    </div>
  );
}
