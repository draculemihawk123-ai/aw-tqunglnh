import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Badge, CopyableId, InlineError, Skeleton, StatusBadge, TextField, Button } from '../components/ui';
import { AlertTriangle } from '../components/icons';
import { ApiError, getRunDiagnostics } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { RunDiagnosticsResponse } from '../api/diagnostics';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * RunDiagnosticsScreen — V7-16's own real "Run diagnostics" (Screen 13 row
 * 3, docs/design/09-v7-alpha-ui.md V7-16). The design doc is explicit this
 * screen "reuses Screen 8 row 4's own authority (GetRunDiagnostics) —
 * shared, never a new aggregate query" and that there is deliberately no
 * "list every Run needing attention" query anywhere in this system (§6.2).
 * The old prototype's fake per-run "Execution Layers"/queue/job/lease/
 * fence/provider/workspace breakdown with its own fake Rebuild/Refresh/
 * Reconcile actions was invented wholesale — dropped entirely, along with
 * the unrelated generic projection-freshness banner it borrowed (that is a
 * cross-cutting V7-04 shell concern, never owned by this screen).
 *
 * This screen is reached from a global, project-less nav item (no route
 * param carries a projectId/runId here — confirmed by reading every real
 * caller of `onDiagnostics`/`system-diagnostics` in App.tsx/LeftNav.tsx),
 * so a real operator supplies both IDs directly (the same "you already
 * have the ID from an alert/support conversation" shape the design doc's
 * own missing-aggregate note implies) and looks up the exact same real
 * GetRunDiagnostics data TaskDetail's own OverviewTab/GraphTimelineTab
 * already fetch — this time also rendering the real OrphanedAttempts/
 * Providers/Isolation/RepositoryWorkspaces fields (V7-16's own real "job/
 * lease/recovery diagnostics" line), never rendered by any earlier V7 task,
 * which were `unknown[]` gaps in web/src/api/diagnostics.ts until this task
 * hand-declared their real shape.
 */
export function RunDiagnosticsScreen({ isOffline = false }: { isOffline?: boolean }) {
  const [projectId, setProjectId] = useState('');
  const [runId, setRunId] = useState('');
  const [lookup, setLookup] = useState<{ projectId: string; runId: string } | null>(null);

  const diagQuery = useQuery({
    queryKey: ['runDiagnosticsLookup', lookup?.projectId, lookup?.runId],
    queryFn: async () => (await getRunDiagnostics(lookup!.projectId, lookup!.runId, withSessionToken())) as unknown as RunDiagnosticsResponse,
    enabled: !!lookup && !isOffline,
  });

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-6">
        <div>
          <h1 className="text-xl font-semibold text-[#172033]">Run Diagnostics</h1>
          <p className="text-sm text-[#5D697A] mt-0.5">Look up a specific Run's own real diagnostics by ID — there is no "runs needing attention" list; this reuses the identical authority the Task Detail screen's own Graph &amp; Timeline tab already uses.</p>
        </div>

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5 flex items-end gap-3">
          <div className="flex-1"><TextField label="Project ID" mono value={projectId} onChange={setProjectId} /></div>
          <div className="flex-1"><TextField label="Run ID" mono value={runId} onChange={setRunId} /></div>
          <Button intent="primary" disabled={isOffline || !projectId.trim() || !runId.trim()}
            onClick={() => setLookup({ projectId: projectId.trim(), runId: runId.trim() })}>Look Up</Button>
        </div>

        {lookup && diagQuery.isPending && (
          <div aria-hidden><Skeleton className="h-24 w-full" /></div>
        )}
        {lookup && diagQuery.isError && (
          <InlineError {...apiErrorMessage(diagQuery.error)} onRetry={() => diagQuery.refetch()} />
        )}
        {diagQuery.data && (
          <>
            <div className="flex items-center justify-between">
              <div>
                <CopyableId value={diagQuery.data.runId} />
                <p className="text-[12px] text-[#5D697A] mt-1">{diagQuery.data.projectId} / {diagQuery.data.workItemId}</p>
              </div>
              <div className="flex items-center gap-2">
                <StatusBadge state={diagQuery.data.runState} entity="run" />
                <StatusBadge state={diagQuery.data.workItemStatus} entity="workitem" />
              </div>
            </div>

            {diagQuery.data.blockers.length > 0 && (
              <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
                <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]"><h2 className="text-sm font-semibold text-[#172033]">Blockers</h2></div>
                <div className="divide-y divide-[#ECEFF4]">
                  {diagQuery.data.blockers.map(b => (
                    <div key={b.blockerId} className="px-5 py-3 flex items-start gap-3">
                      <AlertTriangle size={14} className="text-[#92400E] flex-shrink-0 mt-0.5" aria-hidden />
                      <div className="min-w-0">
                        <span className="font-mono text-[12px] text-[#92400E]">{b.type}</span>
                        <span className="text-[12px] text-[#475569] ml-2">{b.state}</span>
                        <p className="text-[13px] text-[#172033] mt-0.5">{b.reason}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
              <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
                <h2 className="text-sm font-semibold text-[#172033]">Orphaned Attempts</h2>
                <p className="text-[12px] text-[#5D697A] mt-0.5">Attempts whose owning job/lease was interrupted — recovery-reaper candidates.</p>
              </div>
              {diagQuery.data.orphanedAttempts.length === 0 ? (
                <p className="px-5 py-4 text-[13px] text-[#475569]">No orphaned attempts.</p>
              ) : (
                <div className="divide-y divide-[#ECEFF4]">
                  {diagQuery.data.orphanedAttempts.map(a => (
                    <div key={a.attemptId} className="px-5 py-3 flex items-center gap-4 flex-wrap text-[12px]">
                      <CopyableId value={a.attemptId} />
                      <span className="text-[#475569]">node: <span className="font-mono">{a.nodeRunId}</span></span>
                      <span className="text-[#475569]">attempt #{a.attemptNumber}</span>
                      {a.providerKey && <span className="text-[#475569]">provider: <span className="font-mono">{a.providerKey}</span></span>}
                      {a.hasWriteLease && <Badge label="HOLDS WRITE LEASE" intent="warning" />}
                    </div>
                  ))}
                  {diagQuery.data.orphanedAttemptsTruncated && <p className="px-5 py-2 text-[12px] text-[#475569] italic">Truncated at this response's own limit.</p>}
                </div>
              )}
            </div>

            <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
              <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]"><h2 className="text-sm font-semibold text-[#172033]">Providers</h2></div>
              {diagQuery.data.providers.length === 0 ? (
                <p className="px-5 py-4 text-[13px] text-[#475569]">No provider diagnostics recorded.</p>
              ) : (
                <div className="divide-y divide-[#ECEFF4]">
                  {diagQuery.data.providers.map(p => (
                    <div key={p.adapterBuildId} className="px-5 py-3 flex items-center gap-4 flex-wrap text-[12px]">
                      <span className="font-mono">{p.providerKey}</span>
                      <CopyableId value={p.adapterBuildId} />
                      <Badge label={p.providerConfigured ? 'CONFIGURED' : 'NOT CONFIGURED'} intent={p.providerConfigured ? 'success' : 'danger'} />
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
              <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]"><h2 className="text-sm font-semibold text-[#172033]">Isolation</h2></div>
              {diagQuery.data.isolation.length === 0 ? (
                <p className="px-5 py-4 text-[13px] text-[#475569]">No isolation diagnostics recorded.</p>
              ) : (
                <div className="divide-y divide-[#ECEFF4]">
                  {diagQuery.data.isolation.map((iso, i) => (
                    <div key={i} className="px-5 py-3 flex items-center gap-4 text-[12px]">
                      <span className="font-mono">{iso.tier}</span>
                      <Badge label={iso.enforceable ? 'ENFORCEABLE' : 'NOT ENFORCEABLE'} intent={iso.enforceable ? 'success' : 'danger'} />
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
              <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]"><h2 className="text-sm font-semibold text-[#172033]">Repository Workspaces</h2></div>
              {diagQuery.data.repositoryWorkspaces.length === 0 ? (
                <p className="px-5 py-4 text-[13px] text-[#475569]">No repository workspaces scoped to this run.</p>
              ) : (
                <div className="divide-y divide-[#ECEFF4]">
                  {diagQuery.data.repositoryWorkspaces.map(rw => (
                    <div key={rw.repositoryWorkspaceId} className="px-5 py-3 flex items-center gap-4 flex-wrap text-[12px]">
                      <span className="font-mono">{rw.repositoryId}</span>
                      <StatusBadge state={rw.state} entity="repository" />
                      <span className="text-[#475569]">generation <span className="font-mono">{rw.generation}</span></span>
                      <span className={rw.hasActiveWriteLease ? 'text-[#92400E]' : 'text-[#475569]'}>write lease: {rw.hasActiveWriteLease ? 'active' : 'none'}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
