import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getDefinitionManifest, runDefinitionManifest } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, Dialog, InlineError } from '../components/ui';

/** ports.ManifestStatus / ManifestRun (V12-21). */
export interface ManifestRun {
  mode: 'CHECK' | 'PUBLISH' | string;
  exitCode: number;
  succeeded: boolean;
  output: string;
  truncated?: boolean;
  startedAt: string;
  durationMs: number;
}

export interface ManifestStatus {
  projectId: string;
  configured: boolean;
  manifestPath?: string;
  scriptPath?: string;
  lastRun?: ManifestRun;
}

/**
 * V12-21 (docs/design/15-v12-ui-operations.md, UX-04, ADR-056): the project's definition manifest (aw-project.json) on the
 * Definitions page — Check runs the kit's checks, Publish publishes every definition it declares (the kit's aw-publish.py, run by the
 * server on the manifest the operator configured with `aw serve --project-manifest`). The new versions then show in the list, and
 * the version diff shows what changed.
 */
export function ManifestPanel({ projectId, isOffline }: { projectId: string; isOffline?: boolean }) {
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const status = useQuery({
    queryKey: ['definitionManifest', projectId],
    queryFn: async () => (await getDefinitionManifest(projectId, withSessionToken())) as unknown as ManifestStatus,
    enabled: !isOffline,
  });
  const run = useMutation({
    mutationFn: async (mode: 'CHECK' | 'PUBLISH') =>
      (await runDefinitionManifest(projectId, { mode }, withSessionToken({ idempotencyKey: crypto.randomUUID() }))) as unknown as ManifestRun,
    onSuccess: result => {
      setConfirm(false);
      void status.refetch();
      if (result.mode === 'PUBLISH') {
        void queryClient.invalidateQueries({ queryKey: ['definitionsOfKind'] });
        void queryClient.invalidateQueries({ queryKey: ['definitionVersions'] });
      }
    },
  });
  if (!status.data) return null;
  const shown = run.data ?? status.data.lastRun;
  return (
    <section className="px-6 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]" aria-label="Project manifest">
      {!status.data.configured ? (
        <p className="text-[12px] text-[#475569]">
          No manifest configured for this project. Start the server with <code className="font-mono">--project-manifest {projectId}=&lt;path&gt;/aw-project.json</code> to
          check and publish it here, or run <code className="font-mono">aw-publish.py aw-project.json</code>.
        </p>
      ) : (
        <>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-[12px] text-[#475569]">Manifest</span>
            <span className="font-mono text-[12px] text-[#172033] break-all">{status.data.manifestPath}</span>
            <span className="flex-1" />
            <Button size="compact" intent="secondary" disabled={isOffline || run.isPending} loading={run.isPending && run.variables === 'CHECK'}
              onClick={() => run.mutate('CHECK')}>Check</Button>
            <Button size="compact" intent="primary" disabled={isOffline || run.isPending} onClick={() => setConfirm(true)}>Publish…</Button>
          </div>
          {run.isError && <InlineError code="MANIFEST_RUN" message={run.error instanceof Error ? run.error.message : 'The run failed.'} />}
          {shown && (
            <div className="mt-2" data-testid="manifest-run">
              <p className={`text-[12px] ${shown.succeeded ? 'text-[#166534]' : 'text-[#991B1B]'}`} role="status">
                {shown.mode === 'PUBLISH' ? 'Publish' : 'Check'} {shown.succeeded ? 'passed' : `failed (exit code ${shown.exitCode})`} · {new Date(shown.startedAt).toLocaleString()} · {(shown.durationMs / 1000).toFixed(1)} s
              </p>
              <pre className="mt-1 max-h-60 overflow-auto bg-white border border-[#CDD5DF] rounded-[6px] p-2 text-[11px] whitespace-pre-wrap">{shown.truncated ? '…\n' : ''}{shown.output}</pre>
            </div>
          )}
        </>
      )}
      {confirm && (
        <Dialog title="Publish the manifest" onClose={() => setConfirm(false)}
          description="Every definition the manifest declares is published in dependency order. Unchanged definitions keep their version; changed ones get a new version. Runs already started keep the versions they pinned."
          actions={<>
            <Button intent="secondary" onClick={() => setConfirm(false)} disabled={run.isPending}>Cancel</Button>
            <Button intent="primary" loading={run.isPending} onClick={() => run.mutate('PUBLISH')}>Publish</Button>
          </>} />
      )}
    </section>
  );
}
