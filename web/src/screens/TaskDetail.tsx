import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Badge, StatusBadge, Button, IconButton, ValidActionBar, BlockerCard,
  CopyableId, VerdictBadge, OperationNotice, Dialog, Skeleton, Select, TextField,
  InlineError, ToastViewport, useToasts,
} from '../components/ui';
import {
  AlertTriangle, ListTree,
  CheckCircle2, ChevronDown, ChevronRight, FileDiff, FileText, ScrollText, Cpu, User, Paperclip,
} from '../components/icons';
import type { NavRoute } from '../components/shell/LeftNav';
import {
  abandonReleaseSet, appendMessage, ApiError, cancelRun, cancelWorkItem, createReleaseSet, getReleaseSet,
  getReleaseSetLocalCommitStatus, getRepositoryWorkspaceState, getRunDiagnostics, getRunGraph, getRunTimeline,
  getTaskFamily, getWorkItem, getWorkItemProjectedDetail, getWorkspaceDiff, getWorkspaceRepositoryLog,
  getWorkspaceSetState, listArtifacts, listEvidence, listMessages, listReleaseSetsForFamily,
  requestReleaseSetLocalCommit, requestWorkspaceReconciliation, requestWorkspaceSetRelease, resolveWorkItemBlocker,
  retryBlockedActivation, sealReleaseSet,
} from '../api/generated';
import type { GetTaskFamilyResponse, GetWorkItemResponse } from '../api/generated';
import type { WorkItemContract } from '../api/work';
import type { KanbanCard, WorkItemProjectedDetailResponse } from '../api/kanban';
import type { BlockerDiagnostic, RunDiagnosticsResponse } from '../api/diagnostics';
import type { GraphEdgeView, GraphNodeView, NodeActivationView, RunGraphResponse, RunTimelineResponse, TimelineEntryView } from '../api/rundetail';
import { getSessionToken, withSessionToken } from '../api/session';
import { decodeDiffPatch, fetchWorkspaceSource } from '../api/workspaceinspection';
import type { DiffContent, RepositoryLogPage, RepositoryWorkspaceState, SourceContentResult, WorkspaceSetState } from '../api/workspaceinspection';
import { VERDICT_OPTIONS } from '../api/releaseset';
import type { ReleaseSetDetail, RepositoryReleaseDetail, Verdict } from '../api/releaseset';
import { artifactContentUrl, fetchArtifactContent, isInlineSafeMediaType, PREVIEW_SIZE_LIMIT_BYTES } from '../api/evidence';
import type { ArtifactSummary, EvidenceDetail } from '../api/evidence';
import { fetchMessageContent, messageContentUrl, uploadAttachment } from '../api/message';
import type { MessageRef } from '../api/message';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * cancelWorkItemEligibleFromStatus mirrors
 * internal/delivery/httpapi/diagnostics/dto.go's own runDiagnosticsValidActions
 * WorkItemStatus rule exactly (DONE/CANCELLED never advise cancelWorkItem) —
 * used only when there is no active Run at all to fetch a real
 * GetRunDiagnostics response from (a BACKLOG/READY WorkItem can still be
 * cancelled pre-run). When a Run does exist, the real server-computed
 * validActions from GetRunDiagnostics is used instead — this fallback never
 * runs in that case.
 */
function cancelWorkItemEligibleFromStatus(status: string): boolean {
  return status !== 'DONE' && status !== 'CANCELLED';
}

// ─── Persistent Header ────────────────────────────────────────────────────────

interface TaskHeaderProps {
  projectId: string;
  projectName: string;
  workItemId: string;
  activeTab: NavRoute;
  onTabChange: (r: NavRoute, ids?: { projectId?: string; taskId?: string }) => void;
  isOffline?: boolean;
  workItem?: GetWorkItemResponse;
  family?: GetTaskFamilyResponse;
  card?: KanbanCard;
  runDiagnostics?: RunDiagnosticsResponse;
  onActionSettled: () => void;
}

function TaskHeader({
  projectId, projectName, workItemId, activeTab, onTabChange, isOffline,
  workItem, family, card, runDiagnostics, onActionSettled,
}: TaskHeaderProps) {
  const [cancelRunDialog, setCancelRunDialog] = useState(false);
  const [cancelWiDialog, setCancelWiDialog] = useState(false);
  const [cancelReason, setCancelReason] = useState('');
  const [resolvingBlocker, setResolvingBlocker] = useState<BlockerDiagnostic | null>(null);
  const [resolveMode, setResolveMode] = useState<'RESOLVED' | 'WAIVED'>('RESOLVED');
  const [resolveReason, setResolveReason] = useState('');
  const [policyGrantRef, setPolicyGrantRef] = useState('');
  const { toasts, show, dismiss } = useToasts();

  const activeRunId = card?.activeRunId;

  const cancelRunMutation = useMutation({
    mutationFn: async () => {
      if (!activeRunId) throw new Error('no active run');
      const fresh = (await getRunDiagnostics(projectId, activeRunId, withSessionToken())) as unknown as RunDiagnosticsResponse;
      if (!fresh.validActions.some(a => a.operationId === 'cancelRun')) {
        throw new Error('This run can no longer be cancelled — its state changed. Refresh to see the current state.');
      }
      return cancelRun(activeRunId, { reason: cancelReason.trim() }, withSessionToken());
    },
    onSuccess: result => {
      setCancelRunDialog(false);
      setCancelReason('');
      show({ intent: 'info', message: `Cancel Run requested — run is entering ${result.state}.` });
      onActionSettled();
    },
    onError: err => show({ intent: 'danger', message: err instanceof Error ? err.message : apiErrorMessage(err).message, duration: 0 }),
  });

  const cancelWorkItemMutation = useMutation({
    mutationFn: async () => {
      if (activeRunId) {
        const fresh = (await getRunDiagnostics(projectId, activeRunId, withSessionToken())) as unknown as RunDiagnosticsResponse;
        if (!fresh.validActions.some(a => a.operationId === 'cancelWorkItem')) {
          throw new Error('This work item can no longer be cancelled — its state changed. Refresh to see the current state.');
        }
      } else {
        const fresh = (await getWorkItem(projectId, workItemId, withSessionToken())) as unknown as GetWorkItemResponse;
        if (!cancelWorkItemEligibleFromStatus(fresh.status)) {
          throw new Error('This work item has already reached a terminal status and cannot be cancelled.');
        }
      }
      return cancelWorkItem(workItemId, { reason: cancelReason.trim() }, withSessionToken());
    },
    onSuccess: result => {
      setCancelWiDialog(false);
      setCancelReason('');
      show({ intent: 'info', message: `Cancel WorkItem requested — work item status is ${result.status}.` });
      onActionSettled();
    },
    onError: err => show({ intent: 'danger', message: err instanceof Error ? err.message : apiErrorMessage(err).message, duration: 0 }),
  });

  const resolveBlockerMutation = useMutation({
    mutationFn: async () => {
      if (!resolvingBlocker || !activeRunId) throw new Error('no blocker selected');
      const fresh = (await getRunDiagnostics(projectId, activeRunId, withSessionToken())) as unknown as RunDiagnosticsResponse;
      const freshBlocker = fresh.blockers.find(b => b.blockerId === resolvingBlocker.blockerId);
      if (!freshBlocker || !freshBlocker.validActions.some(a => a.operationId === 'resolveWorkItemBlocker')) {
        throw new Error('This blocker can no longer be resolved this way — its state changed. Refresh to see the current state.');
      }
      return resolveWorkItemBlocker(resolvingBlocker.blockerId, {
        mode: resolveMode, reason: resolveReason.trim(),
        policyGrantRef: resolveMode === 'WAIVED' ? policyGrantRef.trim() : undefined,
      }, withSessionToken());
    },
    onSuccess: result => {
      setResolvingBlocker(null);
      setResolveReason('');
      setPolicyGrantRef('');
      setResolveMode('RESOLVED');
      show({ intent: 'success', message: `Blocker ${result.state.toLowerCase()}${result.workItemUnblocked ? ' — work item has no remaining open blockers' : ''}.` });
      onActionSettled();
    },
    onError: err => show({ intent: 'danger', message: err instanceof Error ? err.message : apiErrorMessage(err).message, duration: 0 }),
  });

  const runValidActions = runDiagnostics?.validActions ?? [];
  const canCancelRun = !!activeRunId && runValidActions.some(a => a.operationId === 'cancelRun');
  const canCancelWorkItem = activeRunId
    ? runValidActions.some(a => a.operationId === 'cancelWorkItem')
    : !!workItem && cancelWorkItemEligibleFromStatus(workItem.status);

  const openBlockers = (runDiagnostics?.blockers ?? []).filter(b => b.state === 'OPEN');

  const tabs = [
    { id: 'task-overview' as NavRoute,   label: 'Overview' },
    { id: 'task-graph' as NavRoute,      label: 'Graph & Timeline' },
    { id: 'task-workspace' as NavRoute,  label: 'Workspace' },
    { id: 'task-evidence' as NavRoute,   label: 'Evidence' },
    { id: 'task-chat' as NavRoute,       label: 'Chat' },
  ];

  return (
    <div className="bg-white border-b border-[#CDD5DF] flex-shrink-0">
      {/* Identity row */}
      <div className="px-6 py-3 space-y-2">
        <nav aria-label="WorkItem breadcrumb" className="flex items-center gap-1.5 text-[12px] text-[#475569]">
          <span>{projectName}</span>
          <span aria-hidden>/</span>
          <span className="text-[#172033] font-medium">{workItem?.title ?? '…'}</span>
          <CopyableId value={workItemId} />
        </nav>
        {/* State + actions row */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-1.5">
            <span className="text-[12px] text-[#475569]">WorkItem</span>
            {workItem ? <StatusBadge state={workItem.status} entity="workitem" /> : <Skeleton className="h-5 w-16" />}
          </div>
          {family && (
            <>
              <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
              <div className="flex items-center gap-1.5">
                <span className="text-[12px] text-[#475569]">Family</span>
                <StatusBadge state={family.status} entity="workitem" />
              </div>
            </>
          )}
          {activeRunId && (
            <>
              <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
              <div className="flex items-center gap-1.5">
                <span className="text-[12px] text-[#475569]">Run</span>
                {runDiagnostics ? <StatusBadge state={runDiagnostics.runState} entity="workitem" /> : <Skeleton className="h-5 w-16" />}
                <CopyableId value={activeRunId} />
              </div>
            </>
          )}
          {workItem?.workflowVersionId && (
            <>
              <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
              <span className="text-[12px] text-[#475569]">
                workflow: <span className="font-mono font-medium text-[#172033]">{workItem.workflowVersionId}</span>
              </span>
            </>
          )}
          {(card?.repositoryBadges?.length ?? 0) > 0 && (
            <>
              <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
              <div className="flex gap-1.5" aria-label="Repository scope">
                {card!.repositoryBadges!.map(b => (
                  <span key={b.repositoryId} className="text-[12px] px-1.5 py-0.5 rounded-[4px] bg-[#F1F5F9] text-[#475569] border border-[#CBD5E1]">{b.repositoryId}</span>
                ))}
              </div>
            </>
          )}
          <div className="flex-1" />
          {/* Single ValidActionBar — only actions the real server-computed data currently advertises */}
          <ValidActionBar actions={[
            ...(canCancelRun ? [{ label: 'Cancel Run', intent: 'secondary' as const, onClick: () => setCancelRunDialog(true), disabled: isOffline }] : []),
            ...(canCancelWorkItem ? [{ label: 'Cancel WorkItem', intent: 'destructive' as const, onClick: () => setCancelWiDialog(true), disabled: isOffline }] : []),
          ]} />
        </div>
      </div>
      {/* Blocker banner(s) — real, server-reported open blockers only */}
      {openBlockers.length > 0 && (
        <div className="px-6 pb-2 space-y-2">
          {openBlockers.map(b => (
            <BlockerCard
              key={b.blockerId}
              type={b.type}
              target={b.sourceNodeRunId ? `${activeRunId} / node run: ${b.sourceNodeRunId}` : activeRunId ?? ''}
              reason={b.reason}
              opened={new Date(b.openedAt).toLocaleString()}
              actions={
                b.validActions.some(a => a.operationId === 'resolveWorkItemBlocker')
                  ? [{ label: 'Resolve', onClick: () => { setResolvingBlocker(b); setResolveMode('RESOLVED'); setResolveReason(''); setPolicyGrantRef(''); }, disabled: isOffline }]
                  : b.validActions.some(a => a.operationId === 'retryBlockedActivation')
                  ? [{ label: 'View in Graph & Timeline', onClick: () => onTabChange('task-graph', { projectId, taskId: workItemId }) }]
                  : []
              }
            />
          ))}
        </div>
      )}
      {/* Tabs */}
      <div className="px-6" role="tablist" aria-label="WorkItem detail sections">
        <div className="flex border-b border-transparent -mb-px gap-1">
          {tabs.map(tab => (
            <button
              key={tab.id}
              role="tab"
              id={`tab-${tab.id}`}
              aria-controls="workitem-tab-panel"
              tabIndex={activeTab === tab.id ? 0 : -1}
              aria-selected={activeTab === tab.id}
              onClick={() => onTabChange(tab.id, { projectId, taskId: workItemId })}
              onKeyDown={event => {
                const index = tabs.findIndex(item => item.id === tab.id);
                const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1;
                if (next < 0) return;
                event.preventDefault();
                onTabChange(tabs[next].id, { projectId, taskId: workItemId });
                document.getElementById(`tab-${tabs[next].id}`)?.focus();
              }}
              className={`px-4 py-2.5 text-[13px] font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? 'border-[#3659E3] text-[#3659E3] bg-white font-semibold'
                  : 'border-transparent text-[#475569] hover:text-[#172033]'
              }`}
            >{tab.label}</button>
          ))}
        </div>
      </div>

      {/* ── Cancel Run dialog ─────────────────────────────────────────── */}
      {cancelRunDialog && (
        <Dialog
          title="Cancel Run"
          description="The run will enter CANCELLING and stop accepting new node activations. In-progress attempts may still complete gracefully — this does not report the run as already cancelled."
          onClose={() => setCancelRunDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setCancelRunDialog(false)} disabled={cancelRunMutation.isPending}>Keep Run</Button>
              <Button intent="destructive" loading={cancelRunMutation.isPending} disabled={isOffline || !cancelReason.trim()} onClick={() => cancelRunMutation.mutate()}>Cancel Run</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5 mb-3">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Run</dt><dd><CopyableId value={activeRunId ?? ''} /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Enters state</dt><dd><Badge label="CANCELLING" intent="runtime" /></dd></div>
          </dl>
          <TextField label="Reason" required value={cancelReason} onChange={setCancelReason} placeholder="Why this run is being cancelled" />
          {cancelRunMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(cancelRunMutation.error)} /></div>}
        </Dialog>
      )}

      {/* ── Cancel WorkItem dialog ────────────────────────────────────── */}
      {cancelWiDialog && (
        <Dialog
          title="Cancel WorkItem"
          description="Cancelling a WorkItem requests termination. Active runs must quiesce before the WorkItem becomes CANCELLED — this does not report that outcome immediately."
          onClose={() => setCancelWiDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setCancelWiDialog(false)} disabled={cancelWorkItemMutation.isPending}>Keep WorkItem</Button>
              <Button intent="destructive" loading={cancelWorkItemMutation.isPending} disabled={isOffline || !cancelReason.trim()} onClick={() => cancelWorkItemMutation.mutate()}>Cancel WorkItem</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5 mb-3">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">WorkItem</dt><dd><CopyableId value={workItemId} /></dd></div>
            {activeRunId && <div className="flex gap-3"><dt className="text-[#475569] w-28">Active run</dt><dd><CopyableId value={activeRunId} /></dd></div>}
          </dl>
          <TextField label="Reason" required value={cancelReason} onChange={setCancelReason} placeholder="Why this work item is being cancelled" />
          {cancelWorkItemMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(cancelWorkItemMutation.error)} /></div>}
        </Dialog>
      )}

      {/* ── Resolve Blocker dialog ────────────────────────────────────── */}
      {resolvingBlocker && (
        <Dialog
          title="Resolve Blocker"
          description="Resolving marks this blocker closed; waiving records that it was intentionally bypassed. Neither retries the blocked node — that stays a separate action."
          onClose={() => setResolvingBlocker(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setResolvingBlocker(null)} disabled={resolveBlockerMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={resolveBlockerMutation.isPending}
                disabled={isOffline || !resolveReason.trim() || (resolveMode === 'WAIVED' && !policyGrantRef.trim())}
                onClick={() => resolveBlockerMutation.mutate()}>Confirm</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5 mb-3">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Blocker</dt><dd className="font-mono text-[12px]">{resolvingBlocker.type}</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Reason</dt><dd>{resolvingBlocker.reason}</dd></div>
          </dl>
          <div className="space-y-3">
            <Select label="Mode" required value={resolveMode} onChange={v => setResolveMode(v as 'RESOLVED' | 'WAIVED')}
              options={[{ value: 'RESOLVED', label: 'RESOLVED' }, { value: 'WAIVED', label: 'WAIVED' }]} />
            <TextField label="Reason" required value={resolveReason} onChange={setResolveReason} placeholder="Why this blocker is being closed" />
            {resolveMode === 'WAIVED' && (
              <TextField label="Policy grant reference" required value={policyGrantRef} onChange={setPolicyGrantRef} placeholder="Required when waiving" />
            )}
          </div>
          {resolveBlockerMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(resolveBlockerMutation.error)} /></div>}
        </Dialog>
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

// ─── Overview Tab ─────────────────────────────────────────────────────────────

function OverviewTab({ workItem, runDiagnostics }: { workItem?: GetWorkItemResponse; runDiagnostics?: RunDiagnosticsResponse }) {
  const contract = (workItem?.contract ?? null) as WorkItemContract | null;

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-4">
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5">
          <h2 className="text-sm font-semibold text-[#172033] mb-4">Contract</h2>
          {!contract ? (
            <p className="text-[13px] text-[#475569]">No readiness contract has been set on this WorkItem yet.</p>
          ) : (
            <dl className="grid grid-cols-[auto_1fr] gap-x-8 gap-y-3 text-sm">
              <dt className="text-[#475569] font-medium">WHAT</dt>
              <dd>{contract.behavior || <span className="text-[#475569] text-[13px]">Not set</span>}</dd>
              <dt className="text-[#475569] font-medium">Acceptance Criteria</dt>
              <dd>
                {contract.acceptanceCriteria && contract.acceptanceCriteria.length > 0 ? (
                  <ol className="space-y-1 list-decimal list-inside text-[#172033]">
                    {contract.acceptanceCriteria.map((c, i) => (
                      <li key={i} className="text-[13px]">
                        {c.description}
                        {c.verificationRef && <span className="text-[#475569] font-mono text-[12px]"> — {c.verificationRef}</span>}
                      </li>
                    ))}
                  </ol>
                ) : <span className="text-[#475569] text-[13px]">None recorded</span>}
              </dd>
              <dt className="text-[#475569] font-medium">Verification spec</dt>
              <dd className="text-[13px]">{contract.verificationSpec || <span className="text-[#475569]">Not set</span>}</dd>
              <dt className="text-[#475569] font-medium">Exclusions</dt>
              <dd className="text-[#475569] text-[13px]">{contract.exclusions && contract.exclusions.length > 0 ? contract.exclusions.join(', ') : 'None recorded'}</dd>
              <dt className="text-[#475569] font-medium">Risk</dt>
              <dd className="text-[13px]">
                {contract.riskLevel
                  ? <span className="text-[#92400E] bg-[#FEF3C7] px-2 py-1 rounded-[4px] border border-[#FCD34D]">{contract.riskLevel}</span>
                  : <span className="text-[#475569]">Not set</span>}
              </dd>
              {contract.workflowVersionId && (
                <>
                  <dt className="text-[#475569] font-medium">Workflow version</dt>
                  <dd className="font-mono text-[13px]">{contract.workflowVersionId}</dd>
                </>
              )}
            </dl>
          )}
        </div>

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5">
          <h2 className="text-sm font-semibold text-[#172033] mb-3">Active Run</h2>
          {!runDiagnostics ? (
            <p className="text-[13px] text-[#475569]">No active run for this WorkItem.</p>
          ) : (
            <div className="flex items-center gap-4 flex-wrap text-[13px]">
              <div className="flex items-center gap-2">
                <StatusBadge state={runDiagnostics.runState} entity="workitem" />
                <CopyableId value={runDiagnostics.runId} />
              </div>
              {runDiagnostics.blockers.filter(b => b.state === 'OPEN').length > 0 && (
                <span className="text-[#92400E]">{runDiagnostics.blockers.filter(b => b.state === 'OPEN').length} open blocker(s)</span>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── Graph & Timeline Tab ─────────────────────────────────────────────────────

/**
 * computeLayout ranks each node by its longest FLOW-only path from a
 * source (in-degree 0) node — COMPLETION_REWORK edges are excluded from
 * ranking (V7-12's own "graph renderer read-only" scope never asks for a
 * general graph-layout engine; this is the smallest rule that places a
 * FORK's own branches side by side at the same rank and never lets a
 * rework back-edge turn the ranking into a cycle). A node FLOW edges never
 * reach (should not happen for a real compiled WorkflowVersion) falls back
 * to one rank past the deepest real one rather than crashing.
 */
function computeLayout(nodes: GraphNodeView[], edges: GraphEdgeView[]): Record<string, { x: number; y: number }> {
  const flowEdges = edges.filter(e => e.kind !== 'COMPLETION_REWORK');
  const outAdj = new Map<string, string[]>();
  const inDegree = new Map<string, number>();
  for (const n of nodes) { outAdj.set(n.key, []); inDegree.set(n.key, 0); }
  for (const e of flowEdges) {
    outAdj.get(e.from)?.push(e.to);
    inDegree.set(e.to, (inDegree.get(e.to) ?? 0) + 1);
  }
  const rank = new Map<string, number>();
  const remaining = new Map(inDegree);
  const queue: string[] = [];
  for (const n of nodes) if ((inDegree.get(n.key) ?? 0) === 0) { rank.set(n.key, 0); queue.push(n.key); }
  for (let i = 0; i < queue.length; i++) {
    const key = queue[i];
    for (const to of outAdj.get(key) ?? []) {
      rank.set(to, Math.max(rank.get(to) ?? 0, (rank.get(key) ?? 0) + 1));
      const d = (remaining.get(to) ?? 0) - 1;
      remaining.set(to, d);
      if (d === 0) queue.push(to);
    }
  }
  const maxRank = Math.max(0, ...[...rank.values()]);
  for (const n of nodes) if (!rank.has(n.key)) rank.set(n.key, maxRank + 1);

  const byRank = new Map<number, string[]>();
  for (const n of nodes) {
    const r = rank.get(n.key)!;
    if (!byRank.has(r)) byRank.set(r, []);
    byRank.get(r)!.push(n.key);
  }
  const positions: Record<string, { x: number; y: number }> = {};
  const rowHeight = 90, colWidth = 140;
  for (const [r, keys] of byRank) {
    keys.forEach((key, i) => { positions[key] = { x: i * colWidth + 60, y: r * rowHeight + 30 }; });
  }
  return positions;
}

const STRUCTURAL_KINDS = new Set(['START', 'END', 'FORK', 'JOIN']);

const NODE_STYLE: Record<string, { bg: string; bd: string; fc: string }> = {
  '':          { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#475569' }, // not yet reached
  PENDING:     { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#475569' },
  READY:       { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#475569' },
  QUEUED:      { bg: '#DBEAFE', bd: '#93C5FD', fc: '#1E40AF' },
  RUNNING:     { bg: '#EDE9FE', bd: '#C4B5FD', fc: '#5B21B6' },
  WAITING:     { bg: '#FEF3C7', bd: '#FCD34D', fc: '#92400E' },
  BLOCKED:     { bg: '#FEE2E2', bd: '#FCA5A5', fc: '#991B1B' },
  SUCCEEDED:   { bg: '#DCFCE7', bd: '#86EFAC', fc: '#166534' },
  FAILED:      { bg: '#FEE2E2', bd: '#FCA5A5', fc: '#991B1B' },
  SKIPPED:     { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#5D697A' },
  CANCELLED:   { bg: '#F1F5F9', bd: '#CBD5E1', fc: '#5D697A' },
};

interface GraphTimelineTabProps {
  projectId: string;
  runId?: string;
  runDiagnostics?: RunDiagnosticsResponse;
  isOffline?: boolean;
  onActionSettled: () => void;
}

function GraphTimelineTab({ projectId, runId, runDiagnostics, isOffline, onActionSettled }: GraphTimelineTabProps) {
  const [selectedNodeKey, setSelectedNodeKey] = useState<string | null>(null);
  const [showList, setShowList] = useState(false);
  const [expandedEntry, setExpandedEntry] = useState<number | null>(null);
  const [retrying, setRetrying] = useState<{ nodeRunId: string; nodeKey: string } | null>(null);
  const [retryReason, setRetryReason] = useState('');
  const { toasts, show, dismiss } = useToasts();

  const graphQuery = useInfiniteQuery({
    queryKey: ['runGraph', projectId, runId],
    queryFn: async ({ pageParam }) => {
      const q: Record<string, string> = { limit: '200' };
      if (pageParam) q.cursor = pageParam;
      return (await getRunGraph(runId!, { ...withSessionToken(), query: q })) as unknown as RunGraphResponse;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => last.nextCursor,
    enabled: !isOffline && !!runId,
  });

  const timelineQuery = useInfiniteQuery({
    queryKey: ['runTimeline', projectId, runId],
    queryFn: async ({ pageParam }) => {
      const q: Record<string, string> = { limit: '200' };
      if (pageParam) q.cursor = pageParam;
      return (await getRunTimeline(runId!, { ...withSessionToken(), query: q })) as unknown as RunTimelineResponse;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => last.nextCursor,
    enabled: !isOffline && !!runId,
  });

  const retryMutation = useMutation({
    mutationFn: () => retryBlockedActivation(retrying!.nodeRunId, { reason: retryReason.trim() }, withSessionToken()),
    onSuccess: result => {
      setRetrying(null);
      setRetryReason('');
      if (result.retried) {
        show({ intent: 'success', message: `Retry accepted — a fresh activation (${result.reactivatedNodeRunId}) was scheduled.` });
      } else if (result.alreadyRetried) {
        show({ intent: 'info', message: 'This activation was already retried by an earlier call.' });
      } else if (result.failureReason === 'ADAPTER_BUILD_DRIFT') {
        show({ intent: 'danger', message: 'Retry cannot recover this adapter build drift. Use Cancel Run in the header above instead.', duration: 0 });
      } else {
        show({ intent: 'danger', message: `Retry still fails: ${result.failureReason}${result.failureDetail ? ' — ' + result.failureDetail : ''}`, duration: 0 });
      }
      onActionSettled();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  if (!runId) {
    return <div className="flex-1 flex items-center justify-center bg-[#E9EDF3]"><p className="text-[13px] text-[#475569]">No run to graph — this WorkItem has never started one.</p></div>;
  }
  if (graphQuery.isPending || timelineQuery.isPending) {
    return <div className="flex-1 p-4 space-y-2" aria-hidden><Skeleton className="h-24 w-full" /><Skeleton className="h-24 w-full" /></div>;
  }
  if (graphQuery.isError) {
    return <div className="p-4"><InlineError {...apiErrorMessage(graphQuery.error)} onRetry={() => graphQuery.refetch()} /></div>;
  }

  const nodes = graphQuery.data!.pages[0].nodes;
  const possibleEdges = graphQuery.data!.pages[0].possibleEdges;
  const activations = graphQuery.data!.pages.flatMap(p => p.activations);
  const entries = timelineQuery.data?.pages.flatMap(p => p.entries) ?? [];
  const positions = computeLayout(nodes, possibleEdges);

  const latestActivationByNode = new Map<string, NodeActivationView>();
  for (const a of activations) {
    const current = latestActivationByNode.get(a.nodeKey);
    if (!current || a.activationSequence > current.activationSequence) latestActivationByNode.set(a.nodeKey, a);
  }

  // The one admission blocker (if any) RetryBlockedActivation may act on —
  // server-provided (blocker.validActions), never inferred from a node's
  // own displayed state text (the design doc's own "không suy action từ
  // status text" line, applied here exactly as V7-11's TaskHeader already
  // applies it to CancelRun/CancelWorkItem).
  const retryableByNodeRunId = new Map<string, BlockerDiagnostic>();
  for (const b of runDiagnostics?.blockers ?? []) {
    if (b.state === 'OPEN' && b.sourceNodeRunId && b.validActions.some(a => a.operationId === 'retryBlockedActivation')) {
      retryableByNodeRunId.set(b.sourceNodeRunId, b);
    }
  }

  const maxX = Math.max(160, ...Object.values(positions).map(p => p.x)) + 100;
  const maxY = Math.max(90, ...Object.values(positions).map(p => p.y)) + 60;

  const timelineEntries = selectedNodeKey ? entries.filter(e => e.nodeKey === selectedNodeKey) : entries;

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Graph panel — 58% */}
      <div className="flex flex-col overflow-hidden" style={{ flex: '0 0 58%', minWidth: 0 }}>
        <div className="flex items-center justify-between px-4 py-2 border-b border-[#CDD5DF] bg-[#F3F5F8]">
          <span className="text-[12px] font-semibold text-[#475569]">Workflow Graph</span>
          <div className="flex gap-1">
            {selectedNodeKey && <Button size="compact" intent="quiet" onClick={() => setSelectedNodeKey(null)}>Clear filter ({selectedNodeKey})</Button>}
            <Button size="compact" intent={showList ? 'primary' : 'quiet'} onClick={() => setShowList(!showList)}>
              <ListTree size={13} aria-hidden /> {showList ? 'Canvas' : 'Accessible List'}
            </Button>
          </div>
        </div>
        {showList ? (
          <div className="flex-1 overflow-y-auto p-4" aria-label="Accessible workflow graph list">
            <ol className="space-y-2">
              {nodes.map(node => {
                const activation = latestActivationByNode.get(node.key);
                const retryable = activation ? retryableByNodeRunId.get(activation.nodeRunId) : undefined;
                return (
                  <li key={node.key}>
                    <button type="button" aria-pressed={selectedNodeKey === node.key}
                      onClick={() => setSelectedNodeKey(selectedNodeKey === node.key ? null : node.key)}
                      className={`w-full text-left p-3 rounded-[8px] border cursor-pointer transition-colors ${selectedNodeKey === node.key ? 'bg-[#EEF2FF] border-[#C7D2FE]' : 'bg-white border-[#CDD5DF] hover:border-[#AAB4C3]'}`}>
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-[12px] text-[#475569]">{node.type}</span>
                        <span className="text-[13px] font-medium text-[#172033]">{node.key}</span>
                        {activation && <StatusBadge state={activation.state} entity="workitem" />}
                        {!activation && <span className="text-[12px] text-[#475569]">not yet reached</span>}
                      </div>
                      <div className="text-[12px] text-[#475569] mt-1">
                        {possibleEdges.filter(e => e.to === node.key).map(e => `← ${e.from}${e.outcome ? ` (${e.outcome})` : ''}`).join(' · ')}
                        {possibleEdges.filter(e => e.from === node.key).map(e => ` → ${e.to} (${e.outcome})`).join(' · ')}
                      </div>
                    </button>
                    {retryable && activation && (
                      <Button size="compact" intent="secondary" className="mt-1.5" disabled={isOffline}
                        onClick={() => { setRetrying({ nodeRunId: activation.nodeRunId, nodeKey: node.key }); setRetryReason(''); }}>Retry</Button>
                    )}
                  </li>
                );
              })}
            </ol>
          </div>
        ) : (
          <div className="flex-1 overflow-auto bg-[#FAFBFC]">
            <svg width={maxX} height={maxY} className="mx-auto mt-4" role="img" aria-label={`Workflow graph for run ${runId}`}>
              <defs>
                <marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#AAB4C3" />
                </marker>
                <marker id="arr-rw" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#FCD34D" />
                </marker>
                <marker id="arr-taken" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#3659E3" />
                </marker>
              </defs>
              {possibleEdges.map((e, i) => {
                const f = positions[e.from], t = positions[e.to];
                if (!f || !t) return null;
                const rw = e.kind === 'COMPLETION_REWORK';
                const taken = (graphQuery.data?.pages.flatMap(p => p.takenEdges ?? []) ?? []).some(te => te.edgeKey === e.key || (te.fromNodeKey === e.from && te.outcome === e.outcome));
                const x1 = f.x + 55, y1 = f.y + 24, x2 = t.x + 55, y2 = t.y;
                const path = rw
                  ? `M ${x1} ${y1} C ${x1 - 70} ${y1 + 50}, ${x2 - 70} ${y2 - 50}, ${x2} ${y2}`
                  : `M ${x1} ${y1} L ${x2} ${y2}`;
                const stroke = rw ? '#FCD34D' : taken ? '#3659E3' : '#CDD5DF';
                return (
                  <g key={e.key || i}>
                    <path d={path} stroke={stroke} strokeWidth={taken ? 2 : 1.5}
                      strokeDasharray={rw ? '5,3' : undefined} fill="none"
                      markerEnd={`url(#${rw ? 'arr-rw' : taken ? 'arr-taken' : 'arr'})`} />
                    <text x={(x1 + x2) / 2 + (rw ? -60 : 4)} y={(y1 + y2) / 2}
                      fontSize="12" fill={rw ? '#92400E' : '#5D697A'} fontFamily="JetBrains Mono, monospace">{e.outcome}</text>
                  </g>
                );
              })}
              {nodes.map(node => {
                const pos = positions[node.key];
                if (!pos) return null;
                const activation = latestActivationByNode.get(node.key);
                const sel = selectedNodeKey === node.key;
                const round = STRUCTURAL_KINDS.has(node.type);
                const style = NODE_STYLE[activation?.state ?? ''] ?? NODE_STYLE[''];
                return (
                  // Mouse-only convenience, the same documented pattern ui.tsx's own
                  // Table.onRowClick uses: never role="button"/tabIndex here, since
                  // the outer <svg role="img"> is a single read-only picture and
                  // ARIA forbids nesting an interactive control inside it. The real,
                  // keyboard-reachable equivalent (select a node to filter the
                  // timeline) lives in the Accessible List's own real <button> rows.
                  <g key={node.key} onClick={() => setSelectedNodeKey(sel ? null : node.key)} style={{ cursor: 'pointer' }}
                    aria-hidden>
                    {round
                      ? <circle cx={pos.x + 55} cy={pos.y + 12} r={22} fill={style.bg} stroke={sel ? '#3659E3' : style.bd} strokeWidth={sel ? 2.5 : 1.5} />
                      : <rect x={pos.x} y={pos.y} width={110} height={46} rx="8" fill={style.bg} stroke={sel ? '#3659E3' : style.bd} strokeWidth={sel ? 2.5 : 1.5} />
                    }
                    <text x={pos.x + 55} y={pos.y + (round ? 8 : 17)} textAnchor="middle" fontSize="12" fontWeight="500" fill={style.fc} fontFamily="Inter, sans-serif">{node.type}</text>
                    <text x={pos.x + 55} y={pos.y + (round ? 22 : 33)} textAnchor="middle" fontSize="12" fill={style.fc} fontFamily="Inter, sans-serif">{node.key}</text>
                  </g>
                );
              })}
            </svg>
          </div>
        )}
      </div>
      {/* Drag handle */}
      <div className="w-1 bg-[#E9EDF3] border-x border-[#CDD5DF] cursor-col-resize flex-shrink-0" aria-hidden title="Resize panels" />
      {/* Timeline panel — 42% */}
      <div className="flex flex-col overflow-hidden" style={{ flex: '0 0 42%', minWidth: 0 }}>
        <div className="px-4 py-2 border-b border-[#CDD5DF] bg-[#F3F5F8] flex items-center justify-between">
          <span className="text-[12px] font-semibold text-[#475569]">Timeline{selectedNodeKey ? ` — ${selectedNodeKey}` : ''}</span>
        </div>
        {timelineEntries.length === 0 && <p className="text-[12px] text-[#475569] p-3">No timeline entries yet.</p>}
        <div className="flex-1 overflow-y-auto p-3 space-y-1" role="list" aria-label="Run timeline">
          {timelineEntries.map((ev, i) => {
            const retryable = ev.kind === 'NODE_RUN' ? retryableByNodeRunId.get(ev.nodeRunId) : undefined;
            const failed = ev.attemptState && ['FAILED', 'TIMED_OUT', 'LOST', 'INDETERMINATE'].includes(ev.attemptState);
            return (
              <div key={i} role="listitem">
                <button
                  onClick={() => setExpandedEntry(expandedEntry === i ? null : i)}
                  className="w-full flex items-center gap-3 px-3 py-2.5 text-left rounded-[6px] border border-[#ECEFF4] bg-white hover:border-[#CDD5DF] transition-colors"
                >
                  <span className="font-mono text-[12px] text-[#475569] flex-shrink-0 w-10">#{ev.activationSequence}</span>
                  <span className="font-mono text-[12px] text-[#475569] flex-shrink-0 truncate">{ev.kind}{ev.attemptNumber ? ` #${ev.attemptNumber}` : ''}</span>
                  <span className="text-[12px] px-1.5 py-0.5 bg-[#EEF2FF] text-[#3659E3] rounded-[4px] flex-shrink-0">{ev.nodeKey}</span>
                  <span className={`text-[12px] truncate flex-1 text-right ${failed ? 'text-[#991B1B]' : 'text-[#172033]'}`}>{ev.kind === 'NODE_RUN' ? ev.nodeState : ev.attemptState}</span>
                </button>
                {expandedEntry === i && (
                  <div className="mx-3 border-x border-b border-[#ECEFF4] rounded-b-[6px] px-3 py-2 bg-[#FAFBFC] text-[12px] text-[#475569] space-y-1">
                    {ev.selectedOutcome && <div>Outcome: <span className="font-mono text-[#172033]">{ev.selectedOutcome}</span></div>}
                    {ev.blockReason && <div>Block reason: <span className="text-[#991B1B]">{ev.blockReason}</span></div>}
                    {ev.reactivationReason && <div>Reactivation: <span className="font-mono text-[#172033]">{ev.reactivationReason}</span></div>}
                    {ev.providerKey && <div>Provider: <span className="font-mono text-[#172033]">{ev.providerKey}</span></div>}
                    {ev.terminationReason && <div>Termination reason: <span className="font-mono text-[#991B1B]">{ev.terminationReason}</span></div>}
                    {ev.failureCode && <div>Failure code: <span className="font-mono text-[#991B1B]">{ev.failureCode}</span></div>}
                    {ev.lastCheckpointId && <div>Last checkpoint: <span className="font-mono text-[#172033]">{ev.lastCheckpointId}</span></div>}
                    {ev.startedAt && <div>Started: {new Date(ev.startedAt).toLocaleString()}</div>}
                    {ev.finishedAt && <div>Finished: {new Date(ev.finishedAt).toLocaleString()}</div>}
                    {retryable && ev.kind === 'NODE_RUN' && (
                      <Button size="compact" intent="secondary" disabled={isOffline}
                        onClick={() => { setRetrying({ nodeRunId: ev.nodeRunId, nodeKey: ev.nodeKey }); setRetryReason(''); }}>Retry</Button>
                    )}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {retrying && (
        <Dialog
          title="Retry Blocked Activation"
          description="Retrying re-checks admission fresh — it never assumes the blocker's own cause has been fixed. A still-failing retry leaves the existing blocker open, unresolved."
          onClose={() => setRetrying(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setRetrying(null)} disabled={retryMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={retryMutation.isPending} disabled={isOffline || !retryReason.trim()} onClick={() => retryMutation.mutate()}>Retry</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5 mb-3">
            <div className="flex gap-3"><dt className="text-[#475569] w-24">Node</dt><dd className="font-mono text-[12px]">{retrying.nodeKey}</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-24">NodeRun</dt><dd><CopyableId value={retrying.nodeRunId} /></dd></div>
          </dl>
          <TextField label="Reason" required value={retryReason} onChange={setRetryReason} placeholder="Why this activation is being retried" />
          {retryMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(retryMutation.error)} /></div>}
        </Dialog>
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

// ─── Workspace Tab ────────────────────────────────────────────────────────────

function shortRev(rev?: string): string {
  return rev ? rev.slice(0, 10) : '';
}

interface WorkspaceTabProps {
  projectId: string;
  familyId?: string;
  isOffline?: boolean;
}

/**
 * WorkspaceTab — V7-13's own real "repository tabs with source/diff/log
 * read-only, revision/scope/lease/quarantine status" (docs/design/
 * 09-v7-alpha-ui.md V7-13's own Thực hiện line). ReleaseSet actions (Seal/
 * Abandon/Local Commit/Release) are the prototype's own fake UI here, but
 * they are V7-13A's own separate scope (its own dependency line: "Phụ
 * thuộc: V7-13, V6-10F") — dropped wholesale rather than left as dead
 * buttons, the same "delete the fake capability outright" discipline every
 * other V7 task this session already established.
 *
 * "không browser terminal trong Alpha" — there is deliberately no command-
 * execution UI anywhere in this tab, only three bounded read queries
 * (source/diff/repository-log) plus one real async intent
 * (requestWorkspaceReconciliation). "focus repo không thay runtime scope" —
 * switching the active repository tab only ever changes local component
 * state; it never dispatches a mutation of any kind.
 */
function WorkspaceTab({ projectId, familyId, isOffline }: WorkspaceTabProps) {
  const [activeRepositoryWorkspaceId, setActiveRepositoryWorkspaceId] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<'diff' | 'source' | 'log'>('diff');
  const [sourcePathDraft, setSourcePathDraft] = useState('');
  const [sourcePath, setSourcePath] = useState('');
  const [sourceRevisionSide, setSourceRevisionSide] = useState<'current' | 'base'>('current');
  const [showReleaseSet, setShowReleaseSet] = useState(false);
  const { toasts, show, dismiss } = useToasts();

  const workspaceSetQuery = useQuery({
    queryKey: ['workspaceSetState', projectId, familyId],
    queryFn: async () => (await getWorkspaceSetState(projectId, familyId!, withSessionToken())) as unknown as WorkspaceSetState,
    enabled: !isOffline && !!familyId,
  });

  const repos = workspaceSetQuery.data?.repositoryWorkspaces ?? [];
  const activeRepo: RepositoryWorkspaceState | undefined = repos.find(r => r.repositoryWorkspaceId === activeRepositoryWorkspaceId) ?? repos[0];

  const diffQuery = useQuery({
    queryKey: ['workspaceDiff', projectId, activeRepo?.repositoryWorkspaceId, activeRepo?.generation],
    queryFn: async () => (await getWorkspaceDiff(projectId, activeRepo!.repositoryWorkspaceId, {
      ...withSessionToken(),
      query: {
        repositoryId: activeRepo!.repositoryId, workspaceSetId: activeRepo!.workspaceSetId,
        base: activeRepo!.baseRevision ?? '', baseGeneration: String(activeRepo!.generation),
        result: activeRepo!.currentRevision ?? '', resultGeneration: String(activeRepo!.generation),
      },
    })) as unknown as DiffContent,
    enabled: !isOffline && !!activeRepo?.baseRevision && !!activeRepo?.currentRevision,
  });

  const logQuery = useInfiniteQuery({
    queryKey: ['workspaceRepositoryLog', projectId, activeRepo?.repositoryWorkspaceId],
    queryFn: async ({ pageParam }) => {
      const query: Record<string, string> = {
        repositoryId: activeRepo!.repositoryId, workspaceSetId: activeRepo!.workspaceSetId,
        anchor: activeRepo!.currentRevision ?? '', anchorGeneration: String(activeRepo!.generation),
      };
      if (pageParam) query.cursor = pageParam;
      return (await getWorkspaceRepositoryLog(projectId, activeRepo!.repositoryWorkspaceId, { ...withSessionToken(), query })) as unknown as RepositoryLogPage;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => last.nextCursor,
    enabled: !isOffline && viewMode === 'log' && !!activeRepo?.currentRevision,
  });

  const sourceQuery = useQuery({
    queryKey: ['workspaceSource', projectId, activeRepo?.repositoryWorkspaceId, sourcePath, sourceRevisionSide, activeRepo?.generation],
    queryFn: () => fetchWorkspaceSource(projectId, activeRepo!.repositoryWorkspaceId, {
      repositoryId: activeRepo!.repositoryId, workspaceSetId: activeRepo!.workspaceSetId, path: sourcePath,
      revision: (sourceRevisionSide === 'base' ? activeRepo!.baseRevision : activeRepo!.currentRevision) ?? '',
      generation: activeRepo!.generation,
    }),
    enabled: !isOffline && viewMode === 'source' && !!activeRepo && !!sourcePath.trim(),
  });

  const reconcileMutation = useMutation({
    mutationFn: () => requestWorkspaceReconciliation(projectId, activeRepo!.repositoryWorkspaceId, {}, withSessionToken({ ifMatch: `"${activeRepo!.version}"` })),
    onSuccess: result => {
      show({ intent: 'info', message: `Reconcile requested — repository workspace is entering ${result.state}.` });
      workspaceSetQuery.refetch();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  if (workspaceSetQuery.isPending) {
    return <div className="flex-1 p-4 space-y-2" aria-hidden><Skeleton className="h-24 w-full" /></div>;
  }
  if (workspaceSetQuery.isError) {
    return <div className="p-4"><InlineError {...apiErrorMessage(workspaceSetQuery.error)} onRetry={() => workspaceSetQuery.refetch()} /></div>;
  }
  if (repos.length === 0) {
    return <div className="flex-1 flex items-center justify-center bg-[#E9EDF3]"><p className="text-[13px] text-[#475569]">No repository workspaces provisioned for this task family.</p></div>;
  }

  const canReconcile = activeRepo?.validActions.some(a => a.operationId === 'requestWorkspaceReconciliation') ?? false;
  const diffFiles = diffQuery.data?.files ?? [];

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Repo tabs — switching here only ever sets local state, never dispatches anything */}
      <div className="flex border-b border-[#CDD5DF] bg-white px-4 gap-1 flex-shrink-0 items-center overflow-x-auto">
        {repos.map(r => {
          const isActive = activeRepo?.repositoryWorkspaceId === r.repositoryWorkspaceId;
          return (
            <button key={r.repositoryWorkspaceId}
              onClick={() => { setActiveRepositoryWorkspaceId(r.repositoryWorkspaceId); setViewMode('diff'); setSourcePath(''); setSourcePathDraft(''); }}
              aria-pressed={isActive}
              aria-label={`${r.repositoryId} revision ${shortRev(r.currentRevision)}${r.state === 'QUARANTINED' ? ', quarantined' : ''}`}
              className={`flex items-center gap-2 px-3 py-2.5 text-[13px] border-b-2 transition-colors flex-shrink-0 ${isActive ? 'border-[#3659E3] text-[#3659E3] font-semibold' : 'border-transparent text-[#475569] hover:text-[#172033]'}`}>
              {r.repositoryId}
              <span className={`font-mono text-[12px] ${isActive ? 'text-[#3659E3]' : 'text-[#475569]'}`}>{shortRev(r.currentRevision)}</span>
              {r.state === 'QUARANTINED' && (
                <span className="w-2 h-2 rounded-full bg-[#FCD34D] flex-shrink-0" title="Quarantined" aria-label="Quarantined" />
              )}
            </button>
          );
        })}
        <div className="flex-1" />
        <Button size="compact" intent={showReleaseSet ? 'primary' : 'quiet'} onClick={() => setShowReleaseSet(!showReleaseSet)}>
          {showReleaseSet ? 'Hide ReleaseSet' : 'ReleaseSet'}
        </Button>
      </div>

      {activeRepo && activeRepo.state === 'QUARANTINED' && (
        <div role="alert" className="px-4 py-2 bg-[#FEF3C7] border-b border-[#FCD34D] flex items-center gap-2 flex-shrink-0">
          <AlertTriangle size={13} className="text-[#92400E] flex-shrink-0" aria-hidden />
          <span className="text-[12px] text-[#92400E] font-medium">{activeRepo.repositoryId} quarantined</span>
          {activeRepo.lastProvisionErrorCode && <span className="text-[12px] text-[#92400E] opacity-75 font-mono">{activeRepo.lastProvisionErrorCode}</span>}
          <div className="flex-1" />
          {canReconcile && (
            <Button size="compact" intent="quiet" className="text-[#92400E] border-[#FCD34D]" disabled={isOffline} loading={reconcileMutation.isPending}
              onClick={() => reconcileMutation.mutate()}>Request Reconcile</Button>
          )}
        </div>
      )}

      {/* Per-repository status */}
      {activeRepo && (
        <div className="px-4 py-2 bg-[#F8FAFC] border-b border-[#ECEFF4] flex items-center gap-4 text-[12px] flex-shrink-0 flex-wrap">
          <StatusBadge state={activeRepo.state} entity="repository" />
          <span className="text-[#475569]">generation <span className="font-mono text-[#172033]">{activeRepo.generation}</span></span>
          {activeRepo.branchRef && <span className="text-[#475569]">branch <span className="font-mono text-[#172033]">{activeRepo.branchRef}</span></span>}
          <span className="text-[#475569]">base <span className="font-mono text-[#172033]">{shortRev(activeRepo.baseRevision)}</span></span>
          <span className="text-[#475569]">current <span className="font-mono text-[#172033]">{shortRev(activeRepo.currentRevision)}</span></span>
          <span className={activeRepo.hasActiveWriteLease ? 'text-[#92400E]' : 'text-[#475569]'}>
            write lease: <span className="font-mono">{activeRepo.hasActiveWriteLease ? 'active' : 'none'}</span>
          </span>
          {canReconcile && activeRepo.state !== 'QUARANTINED' && (
            <Button size="compact" intent="quiet" disabled={isOffline} loading={reconcileMutation.isPending} onClick={() => reconcileMutation.mutate()}>Reconcile</Button>
          )}
        </div>
      )}

      <div className="flex flex-1 overflow-hidden">
        <div className="flex-1 flex flex-col overflow-hidden">
          {/* Viewer tabs */}
          <div className="flex border-b border-[#CDD5DF] bg-[#F8FAFC] px-4 gap-1 flex-shrink-0 items-center">
            {([
              { mode: 'diff' as const, Icon: FileDiff, label: 'Diff' },
              { mode: 'source' as const, Icon: FileText, label: 'Source' },
              { mode: 'log' as const, Icon: ScrollText, label: 'Log' },
            ]).map(({ mode, Icon, label }) => (
              <button key={mode} onClick={() => setViewMode(mode)}
                className={`flex items-center gap-1.5 px-3 py-2 text-[12px] font-medium border-b-2 transition-colors ${viewMode === mode ? 'border-[#3659E3] text-[#3659E3]' : 'border-transparent text-[#475569] hover:text-[#172033]'}`}>
                <Icon size={13} aria-hidden /> {label}
              </button>
            ))}
            <div className="flex-1" />
            <span className="text-[12px] text-[#475569] self-center pr-2">Read-only</span>
          </div>

          <div className="flex-1 overflow-auto bg-white border-r border-[#ECEFF4]">
            {viewMode === 'diff' && (
              !activeRepo?.baseRevision || !activeRepo?.currentRevision ? (
                <p className="p-4 text-[13px] text-[#475569]">This repository workspace has no revision recorded yet.</p>
              ) : diffQuery.isPending ? (
                <div className="p-4" aria-hidden><Skeleton className="h-24 w-full" /></div>
              ) : diffQuery.isError ? (
                <div className="p-4"><InlineError {...apiErrorMessage(diffQuery.error)} onRetry={() => diffQuery.refetch()} /></div>
              ) : (
                <div className="text-[12px]">
                  <div className="px-4 py-2 border-b border-[#ECEFF4] text-[#475569] flex items-center gap-2 flex-wrap">
                    <span className="font-mono">{shortRev(activeRepo.baseRevision)}</span> → <span className="font-mono">{shortRev(activeRepo.currentRevision)}</span>
                    <span>· {diffFiles.length} file{diffFiles.length === 1 ? '' : 's'} changed</span>
                    {diffQuery.data?.filesTruncated && <Badge label="FILES TRUNCATED" intent="warning" />}
                    {diffQuery.data?.patchTruncated && <Badge label="PATCH TRUNCATED" intent="warning" />}
                  </div>
                  {diffFiles.length === 0 ? (
                    <p className="p-4 text-[#475569]">No changes between the base and current revision.</p>
                  ) : (
                    <div className="divide-y divide-[#ECEFF4]">
                      {diffFiles.map(f => (
                        <div key={f.path} className="flex items-center gap-3 px-4 py-2">
                          <span className="font-mono text-[#172033] flex-1 min-w-0 truncate">{f.path}</span>
                          {f.binary
                            ? <span className="text-[#475569]">binary</span>
                            : <span className="text-[#166534]">+{f.additions}</span>}
                          {!f.binary && <span className="text-[#991B1B]">-{f.deletions}</span>}
                          <Button size="compact" intent="quiet" onClick={() => { setSourcePath(f.path); setSourcePathDraft(f.path); setSourceRevisionSide('current'); setViewMode('source'); }}>View Source</Button>
                        </div>
                      ))}
                    </div>
                  )}
                  {diffQuery.data?.patch && (
                    <pre className="font-mono text-[12px] leading-6 p-4 whitespace-pre-wrap break-words border-t border-[#ECEFF4]">
                      {decodeDiffPatch(diffQuery.data.patch).split('\n').map((line, i) => (
                        <div key={i} className={
                          line.startsWith('+') && !line.startsWith('+++') ? 'bg-[#DCFCE7] text-[#166534] px-2 -mx-2'
                          : line.startsWith('-') && !line.startsWith('---') ? 'bg-[#FEE2E2] text-[#991B1B] px-2 -mx-2'
                          : line.startsWith('@@') ? 'bg-[#EEF2FF] text-[#3659E3] px-2 -mx-2'
                          : 'text-[#172033]'
                        }>{line || ' '}</div>
                      ))}
                    </pre>
                  )}
                </div>
              )
            )}

            {viewMode === 'source' && (
              <div role="region" aria-label={activeRepo ? `${activeRepo.repositoryId} source` : 'source'} className="p-4 text-[12px] text-[#172033]">
                <div className="flex items-end gap-2 mb-3">
                  <div className="flex-1">
                    <TextField label="Path" value={sourcePathDraft} onChange={setSourcePathDraft} placeholder="src/api/index.ts" mono />
                  </div>
                  <Select label="Revision" value={sourceRevisionSide} onChange={v => setSourceRevisionSide(v as 'base' | 'current')}
                    options={[{ value: 'current', label: 'Current' }, { value: 'base', label: 'Base' }]} />
                  <Button intent="primary" size="compact" onClick={() => setSourcePath(sourcePathDraft.trim())} disabled={!sourcePathDraft.trim()}>Load</Button>
                </div>
                {!sourcePath ? (
                  <p className="text-[#475569]">Enter a path (or click "View Source" from a changed file in the Diff tab).</p>
                ) : sourceQuery.isPending ? (
                  <Skeleton className="h-24 w-full" />
                ) : sourceQuery.isError ? (
                  <InlineError {...apiErrorMessage(sourceQuery.error)} onRetry={() => sourceQuery.refetch()} />
                ) : sourceQuery.data?.binary ? (
                  <p className="text-[#475569]">Binary file ({sourceQuery.data.totalBytes} bytes) — not previewable.</p>
                ) : (
                  <>
                    <p className="mb-2 text-[#475569]">
                      {sourcePath} · {sourceRevisionSide} @ <span className="font-mono">{shortRev(sourceQuery.data?.revision)}</span>
                      {sourceQuery.data?.truncated && <span className="ml-2"><Badge label="TRUNCATED" intent="warning" /></span>}
                    </p>
                    <pre className="font-mono whitespace-pre-wrap break-words">{sourceQuery.data?.content}</pre>
                  </>
                )}
              </div>
            )}

            {viewMode === 'log' && (
              !activeRepo?.currentRevision ? (
                <p className="p-4 text-[13px] text-[#475569]">This repository workspace has no revision recorded yet.</p>
              ) : logQuery.isPending ? (
                <div className="p-4" aria-hidden><Skeleton className="h-24 w-full" /></div>
              ) : logQuery.isError ? (
                <div className="p-4"><InlineError {...apiErrorMessage(logQuery.error)} onRetry={() => logQuery.refetch()} /></div>
              ) : (
                <div className="divide-y divide-[#ECEFF4]">
                  {(logQuery.data?.pages.flatMap(p => p.entries) ?? []).map(entry => (
                    <div key={entry.commitId} className="px-4 py-2.5 text-[12px]">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-[#475569]">{entry.commitId.slice(0, 10)}</span>
                        <span className="text-[#172033] truncate">{entry.subject}{entry.subjectTruncated ? '…' : ''}</span>
                      </div>
                      <div className="text-[#475569] mt-0.5">{entry.authorName} &lt;{entry.authorEmail}&gt; · {new Date(entry.authoredAt).toLocaleString()}</div>
                    </div>
                  ))}
                  {logQuery.data?.pages.at(-1)?.truncated && (
                    <p className="px-4 py-2 text-[12px] text-[#475569] italic">Log output truncated at this page's own byte limit.</p>
                  )}
                  {logQuery.hasNextPage && (
                    <div className="px-4 py-2 flex justify-center">
                      <Button intent="secondary" size="compact" loading={logQuery.isFetchingNextPage} onClick={() => logQuery.fetchNextPage()}>Load more</Button>
                    </div>
                  )}
                </div>
              )
            )}
          </div>
        </div>
        {showReleaseSet && (
          <ReleaseSetPanel
            projectId={projectId} familyId={familyId} repos={repos}
            workspaceSetVersion={workspaceSetQuery.data?.version} workspaceSetState={workspaceSetQuery.data?.state}
            workspaceSetValidActions={workspaceSetQuery.data?.validActions ?? []}
            isOffline={isOffline}
            onWorkspaceSetChanged={() => workspaceSetQuery.refetch()}
          />
        )}
      </div>
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

// ─── ReleaseSet Panel (V7-13A) ─────────────────────────────────────────────────

interface ReleaseSetPanelProps {
  projectId: string;
  familyId?: string;
  repos: RepositoryWorkspaceState[];
  workspaceSetVersion?: number;
  workspaceSetState?: string;
  workspaceSetValidActions: { operationId: string; scopeKind: string; targetVersion: number }[];
  isOffline?: boolean;
  onWorkspaceSetChanged: () => void;
}

/**
 * ReleaseSetPanel — V7-13A's own real "local release có confirm rõ ràng và
 * không có lối ra remote" (docs/design/09-v7-alpha-ui.md). Every label here
 * is deliberately local-only vocabulary (Create/Seal/Abandon/Local
 * Commit/Release Workspace) — never push/PR/merge/force-push, matching
 * `internal/delivery/httpapi/releaseset`'s own architecture-tested "no
 * remote route of any kind" guarantee
 * (`TestRegisterRoutes_ExcludesAnyRemoteGitVerb`).
 *
 * `sealReleaseSet` has NO server-side "all repositories must PASS" gate at
 * all (confirmed by reading `internal/domain/work/release_set.go` — sealing
 * is a plain state transition, the verdict mix is never checked) — the old
 * prototype's disabled "Seal ReleaseSet (requires all PASS)" primary button
 * plus a separate "Seal Anyway (partial)" secondary button was pure
 * fiction. There is only one real Seal action; the confirm dialog shows the
 * real per-repository verdicts so the operator can see for themselves
 * whether this is an all-PASS or a partial/mixed release before confirming.
 *
 * Local Commit's own `expectedWorkspaceVersion` fence is refetched fresh
 * (`onWorkspaceSetChanged` → the parent's `workspaceSetQuery.refetch()`)
 * immediately before every dispatch — the design doc's own "stale revision"
 * Verify scenario is a real optimistic-concurrency conflict this command
 * enforces server-side (`ports.ErrOptimisticConflict`), so this fresh
 * recheck exists to make that conflict rare, never to replace the server's
 * own real fence.
 */
function ReleaseSetPanel({
  projectId, familyId, repos, workspaceSetVersion, workspaceSetState, workspaceSetValidActions, isOffline, onWorkspaceSetChanged,
}: ReleaseSetPanelProps) {
  const [createOpen, setCreateOpen] = useState(false);
  const [createEntries, setCreateEntries] = useState<Record<string, { base: string; result: string; verdict: Verdict }>>({});
  const [sealing, setSealing] = useState<ReleaseSetDetail | null>(null);
  const [abandoning, setAbandoning] = useState<ReleaseSetDetail | null>(null);
  const [releaseConfirmOpen, setReleaseConfirmOpen] = useState(false);
  const [committing, setCommitting] = useState<{ releaseSet: ReleaseSetDetail; entry: RepositoryReleaseDetail } | null>(null);
  const [commitMessage, setCommitMessage] = useState('');
  const [commitAuthorName, setCommitAuthorName] = useState('local-operator');
  const [commitAuthorEmail, setCommitAuthorEmail] = useState('local-operator@localhost');
  const [pollingLocalCommitId, setPollingLocalCommitId] = useState<string | null>(null);
  const { toasts, show, dismiss } = useToasts();

  const releaseSetsQuery = useQuery({
    queryKey: ['releaseSetsForFamily', projectId, familyId],
    queryFn: async () => (await listReleaseSetsForFamily(projectId, familyId!, withSessionToken())) as unknown as { items: ReleaseSetDetail[] },
    enabled: !isOffline && !!familyId,
  });

  const localCommitStatusQuery = useQuery({
    queryKey: ['releaseSetLocalCommit', projectId, pollingLocalCommitId],
    queryFn: async () => (await getReleaseSetLocalCommitStatus(projectId, committing!.releaseSet.releaseSetId, pollingLocalCommitId!, withSessionToken())) as unknown as { state: string; failureReason?: string },
    enabled: !!pollingLocalCommitId,
    refetchInterval: q => (q.state.data?.state === 'REQUESTED' ? 1000 : false),
  });

  const openCreateDialog = () => {
    const entries: Record<string, { base: string; result: string; verdict: Verdict }> = {};
    for (const r of repos) entries[r.repositoryWorkspaceId] = { base: r.baseRevision ?? '', result: r.currentRevision ?? '', verdict: 'NOT_RUN' };
    setCreateEntries(entries);
    setCreateOpen(true);
  };

  const createMutation = useMutation({
    mutationFn: () => createReleaseSet(projectId, familyId!, {
      repositories: repos.map(r => ({
        repositoryId: r.repositoryId,
        baseVcsObjectId: createEntries[r.repositoryWorkspaceId]?.base ?? '',
        resultVcsObjectId: createEntries[r.repositoryWorkspaceId]?.result ?? '',
        verdict: createEntries[r.repositoryWorkspaceId]?.verdict ?? 'NOT_RUN',
      })),
    }, withSessionToken()),
    onSuccess: result => {
      setCreateOpen(false);
      show({ intent: 'success', message: `ReleaseSet ${result.releaseSetId} created (${result.state}).` });
      releaseSetsQuery.refetch();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  const sealMutation = useMutation({
    mutationFn: () => sealReleaseSet(projectId, sealing!.releaseSetId, {}, withSessionToken({ ifMatch: `"${sealing!.version}"` })),
    onSuccess: result => {
      setSealing(null);
      show({ intent: 'success', message: `ReleaseSet ${result.releaseSetId} is now ${result.state}.` });
      releaseSetsQuery.refetch();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  const abandonMutation = useMutation({
    mutationFn: () => abandonReleaseSet(projectId, abandoning!.releaseSetId, {}, withSessionToken({ ifMatch: `"${abandoning!.version}"` })),
    onSuccess: result => {
      setAbandoning(null);
      show({ intent: 'info', message: `ReleaseSet ${result.releaseSetId} is now ${result.state}.` });
      releaseSetsQuery.refetch();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  const releaseMutation = useMutation({
    mutationFn: () => requestWorkspaceSetRelease(projectId, familyId!, {}, withSessionToken({ ifMatch: `"${workspaceSetVersion}"` })),
    onSuccess: result => {
      setReleaseConfirmOpen(false);
      show({ intent: 'info', message: `Release requested (job ${result.releaseJobId}) — this is an asynchronous job, not an immediate operation; the workspace set transitions once it completes.` });
      onWorkspaceSetChanged();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  const commitMutation = useMutation({
    mutationFn: async () => {
      const repo = repos.find(r => r.repositoryId === committing!.entry.repositoryId);
      if (!repo) throw new Error('repository workspace no longer found');
      // Fresh recheck: both ExpectedReleaseSetVersion and ExpectedWorkspaceVersion are
      // real optimistic-concurrency fences the server enforces (ports.ErrOptimisticConflict)
      // — refetched here immediately before dispatch, never trusted from a value that has
      // been sitting in either query's own cache since this dialog first opened.
      const [freshReleaseSet, freshWorkspace] = await Promise.all([
        getReleaseSet(projectId, committing!.releaseSet.releaseSetId, withSessionToken()) as unknown as Promise<ReleaseSetDetail>,
        getRepositoryWorkspaceState(projectId, repo.repositoryWorkspaceId, withSessionToken()) as unknown as Promise<RepositoryWorkspaceState>,
      ]);
      return requestReleaseSetLocalCommit(projectId, committing!.releaseSet.releaseSetId, {
        expectedReleaseSetVersion: freshReleaseSet.version,
        repositoryWorkspaceId: repo.repositoryWorkspaceId,
        expectedWorkspaceVersion: freshWorkspace.version,
        message: commitMessage.trim(), authorName: commitAuthorName.trim(), authorEmail: commitAuthorEmail.trim(),
      }, withSessionToken());
    },
    onSuccess: result => {
      show({ intent: 'info', message: `Local commit requested for ${committing!.entry.repositoryId} — entering ${result.state}.` });
      setPollingLocalCommitId(result.releaseSetLocalCommitId);
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  const canRelease = workspaceSetValidActions.some(a => a.operationId === 'requestWorkspaceSetRelease');
  const releaseSets = releaseSetsQuery.data?.items ?? [];

  return (
    <div className="w-96 flex-shrink-0 border-l border-[#CDD5DF] bg-[#F3F5F8] overflow-y-auto flex flex-col">
      <div className="px-4 py-3 border-b border-[#CDD5DF] flex items-center justify-between flex-shrink-0">
        <span className="text-[13px] font-semibold text-[#172033]">ReleaseSet</span>
        <Button size="compact" intent="primary" disabled={isOffline || repos.length === 0} onClick={openCreateDialog}>Create</Button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {canRelease && (
          <div className="p-4 border-b border-[#CDD5DF] bg-white">
            <p className="text-[12px] text-[#475569] mb-2">Workspace set is eligible for release{workspaceSetState ? ` (currently ${workspaceSetState})` : ''}.</p>
            <Button intent="secondary" size="compact" className="w-full justify-center" disabled={isOffline} onClick={() => setReleaseConfirmOpen(true)}>Release Workspace</Button>
          </div>
        )}
        {releaseSetsQuery.isPending && <div className="p-4" aria-hidden><Skeleton className="h-16 w-full" /></div>}
        {releaseSetsQuery.isError && <div className="p-4"><InlineError {...apiErrorMessage(releaseSetsQuery.error)} onRetry={() => releaseSetsQuery.refetch()} /></div>}
        {releaseSetsQuery.isSuccess && releaseSets.length === 0 && (
          <p className="p-4 text-[12px] text-[#475569]">No ReleaseSets yet for this task family.</p>
        )}
        {releaseSets.map(rs => (
          <div key={rs.releaseSetId} className="p-4 border-b border-[#ECEFF4] space-y-2">
            <div className="flex items-center justify-between">
              <CopyableId value={rs.releaseSetId} short={rs.releaseSetId.slice(0, 8)} />
              <StatusBadge state={rs.state} entity="workitem" />
            </div>
            <div className="text-[12px] text-[#475569] font-mono truncate">{rs.contentHash}</div>
            <div className="space-y-1.5">
              {rs.entries.map(entry => (
                <div key={entry.repositoryId} className="bg-white rounded-[6px] border border-[#CDD5DF] p-2 text-[12px] space-y-1">
                  <div className="flex items-center justify-between">
                    <span className="font-medium text-[#172033]">{entry.repositoryId}</span>
                    <VerdictBadge verdict={entry.verdict as 'PASS' | 'FAIL' | 'ERROR' | 'N/A' | 'NOT_RUN'} />
                  </div>
                  <div className="font-mono text-[12px] text-[#475569]">{shortRev(entry.baseVcsObjectId)} → {shortRev(entry.resultVcsObjectId)}</div>
                  {rs.state === 'SEALED' && (
                    <Button size="compact" intent="quiet" disabled={isOffline}
                      onClick={() => { setCommitting({ releaseSet: rs, entry }); setCommitMessage(''); }}>Local Commit</Button>
                  )}
                </div>
              ))}
            </div>
            {rs.state === 'CREATED' && (
              <div className="flex gap-2 pt-1">
                <Button intent="secondary" size="compact" className="flex-1 justify-center" disabled={isOffline} onClick={() => setSealing(rs)}>Seal</Button>
                <Button intent="destructive" size="compact" className="flex-1 justify-center" disabled={isOffline} onClick={() => setAbandoning(rs)}>Abandon</Button>
              </div>
            )}
            {rs.state === 'ABANDONED' && <p className="text-[12px] text-[#475569] italic">Abandoned. No further actions.</p>}
          </div>
        ))}
      </div>

      {createOpen && (
        <Dialog
          title="Create ReleaseSet"
          description="Records one exact base→result revision and verdict per repository. This never runs a remote Git operation."
          onClose={() => setCreateOpen(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setCreateOpen(false)} disabled={createMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={createMutation.isPending}
                disabled={isOffline || repos.some(r => !createEntries[r.repositoryWorkspaceId]?.base?.trim() || !createEntries[r.repositoryWorkspaceId]?.result?.trim())}
                onClick={() => createMutation.mutate()}>Create</Button>
            </>
          }
        >
          <div className="space-y-3">
            {repos.map(r => {
              const entry = createEntries[r.repositoryWorkspaceId] ?? { base: '', result: '', verdict: 'NOT_RUN' as Verdict };
              return (
                <div key={r.repositoryWorkspaceId} className="bg-[#F8FAFC] border border-[#CDD5DF] rounded-[6px] p-2 space-y-2">
                  <p className="text-[12px] font-medium text-[#172033]">{r.repositoryId}</p>
                  <div className="flex gap-2">
                    <TextField label="Base revision" id={`base-${r.repositoryWorkspaceId}`} mono value={entry.base}
                      onChange={v => setCreateEntries(cur => ({ ...cur, [r.repositoryWorkspaceId]: { ...entry, base: v } }))} />
                    <TextField label="Result revision" id={`result-${r.repositoryWorkspaceId}`} mono value={entry.result}
                      onChange={v => setCreateEntries(cur => ({ ...cur, [r.repositoryWorkspaceId]: { ...entry, result: v } }))} />
                  </div>
                  <Select label="Verdict" id={`verdict-${r.repositoryWorkspaceId}`} value={entry.verdict}
                    onChange={v => setCreateEntries(cur => ({ ...cur, [r.repositoryWorkspaceId]: { ...entry, verdict: v as Verdict } }))}
                    options={VERDICT_OPTIONS.map(v => ({ value: v, label: v }))} required />
                </div>
              );
            })}
          </div>
          {createMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(createMutation.error)} /></div>}
        </Dialog>
      )}

      {sealing && (
        <Dialog
          title="Seal ReleaseSet"
          description="Sealing locks this ReleaseSet's own entries and allows Local Commit. This never checks whether every repository passed — review the real per-repository verdicts below first."
          onClose={() => setSealing(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setSealing(null)} disabled={sealMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={sealMutation.isPending} disabled={isOffline} onClick={() => sealMutation.mutate()}>Seal</Button>
            </>
          }
        >
          <div className="space-y-1.5">
            {sealing.entries.map(entry => (
              <div key={entry.repositoryId} className="flex items-center justify-between text-[13px]">
                <span>{entry.repositoryId}</span>
                <VerdictBadge verdict={entry.verdict as 'PASS' | 'FAIL' | 'ERROR' | 'N/A' | 'NOT_RUN'} />
              </div>
            ))}
          </div>
          {sealMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(sealMutation.error)} /></div>}
        </Dialog>
      )}

      {abandoning && (
        <Dialog
          title="Abandon ReleaseSet"
          description="Abandoning this ReleaseSet is irreversible. A new ReleaseSet must be created for any subsequent release attempt."
          onClose={() => setAbandoning(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setAbandoning(null)} disabled={abandonMutation.isPending}>Cancel</Button>
              <Button intent="destructive" loading={abandonMutation.isPending} disabled={isOffline} onClick={() => abandonMutation.mutate()}>Abandon</Button>
            </>
          }
        >
          <p className="text-[13px] text-[#475569]">ReleaseSet <CopyableId value={abandoning.releaseSetId} /></p>
          {abandonMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(abandonMutation.error)} /></div>}
        </Dialog>
      )}

      {releaseConfirmOpen && (
        <Dialog
          title="Release Workspace"
          description="This dispatches an asynchronous release job — the workspace set enters RELEASING and reaches a terminal state only once that job completes. It is not an immediate operation, and it never performs any remote Git operation."
          onClose={() => setReleaseConfirmOpen(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setReleaseConfirmOpen(false)} disabled={releaseMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={releaseMutation.isPending} disabled={isOffline} onClick={() => releaseMutation.mutate()}>Request Release</Button>
            </>
          }
        >
          {releaseMutation.isError && <InlineError {...apiErrorMessage(releaseMutation.error)} />}
        </Dialog>
      )}

      {committing && (
        <Dialog
          title="Create Local Commit"
          description="Creates a local commit in this repository's own workspace from the sealed ReleaseSet entry. This never performs a remote Git operation (no push, no PR)."
          onClose={() => { setCommitting(null); setPollingLocalCommitId(null); }}
          actions={
            <>
              <Button intent="secondary" onClick={() => { setCommitting(null); setPollingLocalCommitId(null); }} disabled={commitMutation.isPending}>Cancel</Button>
              <Button intent="primary" loading={commitMutation.isPending}
                disabled={isOffline || !commitMessage.trim() || !commitAuthorName.trim() || !commitAuthorEmail.trim() || !!pollingLocalCommitId}
                onClick={() => commitMutation.mutate()}>Create Local Commit</Button>
            </>
          }
        >
          <p className="text-[13px] text-[#475569] mb-3">Target: <span className="font-mono text-[#172033]">{committing.entry.repositoryId}@{shortRev(committing.entry.resultVcsObjectId)}</span></p>
          <div className="space-y-3">
            <TextField label="Message" required value={commitMessage} onChange={setCommitMessage} placeholder="Release commit message" />
            <TextField label="Author name" required value={commitAuthorName} onChange={setCommitAuthorName} />
            <TextField label="Author email" required value={commitAuthorEmail} onChange={setCommitAuthorEmail} mono />
          </div>
          {commitMutation.isError && <div className="mt-2"><InlineError {...apiErrorMessage(commitMutation.error)} /></div>}
          {pollingLocalCommitId && (
            <div className="mt-3">
              <OperationNotice
                state={localCommitStatusQuery.data?.state === 'COMMITTED' ? 'Completed' : localCommitStatusQuery.data?.state === 'FAILED' ? 'Failed' : 'Running'}
                message={localCommitStatusQuery.data?.state === 'FAILED' ? localCommitStatusQuery.data?.failureReason : `Local commit ${localCommitStatusQuery.data?.state ?? 'REQUESTED'}`}
                ref={pollingLocalCommitId}
              />
            </div>
          )}
        </Dialog>
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

// ─── Evidence Tab ─────────────────────────────────────────────────────────────

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function shortHash(hash: string): string {
  return hash.length > 14 ? `${hash.slice(0, 14)}…` : hash;
}

interface ArtifactRowProps {
  projectId: string; workItemId: string; evidenceId: string; artifact: ArtifactSummary;
  onPreview: (artifact: ArtifactSummary) => void;
}

/**
 * ArtifactRow — real per-artifact metadata (never a locator, per
 * internal/app/runtime/queries.go's own "Không làm: KHÔNG expose locator"
 * line) plus real Preview/Download actions. Preview is offered only for the
 * server's own closed inline-safe media-type allow-list
 * (internal/delivery/httpapi/media.go's own inlineSafeContentTypes) and only
 * under PREVIEW_SIZE_LIMIT_BYTES — this is a UX convenience, never a second
 * security boundary: the server's own Content-Disposition header is what
 * actually forces an unsafe type (text/html, image/svg+xml, ...) to
 * download rather than render, so a real raw HTML/script artifact can never
 * execute even if this client-side gate were somehow bypassed. A PURGED
 * artifact (its own real, durable AttachState — the retention sweeper has
 * already deleted its underlying bytes) offers neither action: there is
 * nothing left to fetch.
 */
function ArtifactRow({ projectId, workItemId, evidenceId, artifact, onPreview }: ArtifactRowProps) {
  const purged = artifact.attachState === 'PURGED';
  const expired = !!artifact.expiresAt && new Date(artifact.expiresAt).getTime() < Date.now();
  const previewEligible = !purged && isInlineSafeMediaType(artifact.mediaType) && artifact.size <= PREVIEW_SIZE_LIMIT_BYTES;
  const tooLargeToPreview = !purged && isInlineSafeMediaType(artifact.mediaType) && artifact.size > PREVIEW_SIZE_LIMIT_BYTES;

  return (
    <div className="flex items-center gap-3 px-5 py-3 flex-wrap" role="listitem">
      <span className="font-mono text-[12px] text-[#172033]">{artifact.artifactId}</span>
      <span className="text-[12px] text-[#475569]">{artifact.mediaType}</span>
      <span className="text-[12px] text-[#475569]">{formatBytes(artifact.size)}</span>
      <span className="font-mono text-[12px] text-[#475569]" title={artifact.contentHash}>{shortHash(artifact.contentHash)}</span>
      {artifact.sensitivity !== 'PUBLIC' && <Badge label={String(artifact.sensitivity)} intent="warning" />}
      {artifact.redacted && <Badge label="REDACTED" intent="warning" />}
      {artifact.hold && <Badge label="HOLD" intent="info" />}
      {purged && <Badge label="PURGED" intent="danger" />}
      {!purged && expired && <Badge label="EXPIRED" intent="warning" />}
      <div className="flex-1" />
      {purged ? (
        <span className="text-[12px] text-[#475569]">Content purged by retention — metadata kept for audit only.</span>
      ) : (
        <>
          {previewEligible && <Button size="compact" intent="quiet" onClick={() => onPreview(artifact)}>Preview</Button>}
          {tooLargeToPreview && <span className="text-[12px] text-[#475569]">Too large to preview ({formatBytes(artifact.size)})</span>}
          <a href={artifactContentUrl(projectId, workItemId, evidenceId, artifact.artifactId)} download
            className="inline-flex items-center gap-1 text-[12px] text-[#3659E3] hover:underline px-2 py-1">Download</a>
        </>
      )}
    </div>
  );
}

interface EvidenceRowProps {
  projectId: string; workItemId: string; evidence: EvidenceDetail; isOffline?: boolean;
  onPreview: (evidenceId: string, artifact: ArtifactSummary) => void;
}

function EvidenceRow({ projectId, workItemId, evidence, isOffline, onPreview }: EvidenceRowProps) {
  const [expanded, setExpanded] = useState(false);
  const artifactsQuery = useQuery({
    queryKey: ['evidenceArtifacts', projectId, workItemId, evidence.evidenceId],
    queryFn: async () => (await listArtifacts(projectId, workItemId, evidence.evidenceId, withSessionToken())) as unknown as { items: ArtifactSummary[] },
    enabled: expanded && !isOffline,
  });

  return (
    <div role="listitem">
      <button
        onClick={() => setExpanded(v => !v)}
        className="w-full flex items-start gap-4 px-5 py-4 text-left hover:bg-[#FAFBFC] transition-colors"
        aria-expanded={expanded}
      >
        <VerdictBadge verdict={evidence.verdict} />
        <div className="flex-1 min-w-0">
          <p className="text-[13px] text-[#172033] font-mono">{evidence.kind}</p>
          <div className="flex items-center gap-3 mt-1 flex-wrap text-[12px] text-[#475569]">
            <span>revisionSet: <span className="font-mono">{shortHash(evidence.revisionSetHash)}</span></span>
            <span>policy: <span className="font-mono">{evidence.policyVersion}</span></span>
            <span>{new Date(evidence.createdAt).toLocaleString()}</span>
          </div>
          {!!evidence.revisions?.length && (
            <div className="flex items-center gap-2 mt-1 flex-wrap text-[12px] text-[#475569]">
              {evidence.revisions.map(r => (
                <span key={r.repositoryId} className="font-mono">{r.repositoryId}@{r.vcsObjectId.slice(0, 10)}</span>
              ))}
            </div>
          )}
        </div>
        {expanded
          ? <ChevronDown size={14} className="text-[#475569] flex-shrink-0" aria-hidden />
          : <ChevronRight size={14} className="text-[#475569] flex-shrink-0" aria-hidden />}
      </button>
      {expanded && (
        <div className="bg-[#FAFBFC] border-t border-[#ECEFF4]">
          {artifactsQuery.isPending ? (
            <div className="p-4" aria-hidden><Skeleton className="h-12 w-full" /></div>
          ) : artifactsQuery.isError ? (
            <div className="p-4"><InlineError {...apiErrorMessage(artifactsQuery.error)} onRetry={() => artifactsQuery.refetch()} /></div>
          ) : (artifactsQuery.data?.items.length ?? 0) === 0 ? (
            <p className="px-5 py-3 text-[12px] text-[#475569]">No artifacts referenced by this evidence row.</p>
          ) : (
            <div className="divide-y divide-[#ECEFF4]" role="list">
              {artifactsQuery.data!.items.map(a => (
                <ArtifactRow key={a.artifactId} projectId={projectId} workItemId={workItemId} evidenceId={evidence.evidenceId}
                  artifact={a} onPreview={artifact => onPreview(evidence.evidenceId, artifact)} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

interface ArtifactPreviewDialogProps {
  projectId: string; workItemId: string; evidenceId: string; artifact: ArtifactSummary; onClose: () => void;
}

/**
 * ArtifactPreviewDialog fetches real content on open. A tampered artifact
 * (bytes no longer matching their own recorded hash — internal/adapters/
 * artifactstore's own Verify) or a since-purged one both surface here as a
 * real InlineError, never a silently-served response — this task's own
 * "tampered/expired/redacted/large artifact E2E" Verify bullet. Text content
 * is always rendered as plain, React-escaped text (never innerHTML) even
 * though the server's own inline-safe allow-list already guarantees this
 * dialog is never reached for text/html or image/svg+xml in the first
 * place — a raw HTML/script artifact can never execute either way.
 */
function ArtifactPreviewDialog({ projectId, workItemId, evidenceId, artifact, onClose }: ArtifactPreviewDialogProps) {
  const contentQuery = useQuery({
    queryKey: ['artifactContent', projectId, workItemId, evidenceId, artifact.artifactId],
    queryFn: () => fetchArtifactContent(projectId, workItemId, evidenceId, artifact.artifactId),
    retry: false,
  });

  const objectUrl = React.useMemo(() => {
    if (!contentQuery.data) return null;
    return URL.createObjectURL(contentQuery.data.blob);
  }, [contentQuery.data]);
  React.useEffect(() => () => { if (objectUrl) URL.revokeObjectURL(objectUrl); }, [objectUrl]);

  const [text, setText] = useState<string | null>(null);
  React.useEffect(() => {
    if (!contentQuery.data) return;
    const base = contentQuery.data.contentType.split(';')[0]?.trim().toLowerCase();
    if (base === 'text/plain' || base === 'text/csv' || base === 'application/json') {
      contentQuery.data.blob.text().then(setText);
    } else {
      setText(null);
    }
  }, [contentQuery.data]);

  const base = contentQuery.data?.contentType.split(';')[0]?.trim().toLowerCase();

  return (
    <Dialog title={`Preview — ${artifact.artifactId}`} description={artifact.mediaType} onClose={onClose}
      actions={<Button intent="secondary" onClick={onClose}>Close</Button>}>
      {contentQuery.isPending ? (
        <div aria-hidden><Skeleton className="h-40 w-full" /></div>
      ) : contentQuery.isError ? (
        <InlineError {...apiErrorMessage(contentQuery.error)} onRetry={() => contentQuery.refetch()} />
      ) : base?.startsWith('image/') && objectUrl ? (
        <img src={objectUrl} alt={artifact.artifactId} className="max-w-full max-h-[60vh] mx-auto" />
      ) : base === 'application/pdf' && objectUrl ? (
        <embed src={objectUrl} type="application/pdf" className="w-full h-[60vh]" />
      ) : text !== null ? (
        <pre className="font-mono text-[12px] whitespace-pre-wrap break-words max-h-[60vh] overflow-auto p-3 bg-white border border-[#ECEFF4] rounded-[6px]">{text}</pre>
      ) : (
        <p className="text-[13px] text-[#475569]">This content type cannot be previewed.</p>
      )}
    </Dialog>
  );
}

function EvidenceTab({ projectId, workItemId, isOffline }: { projectId: string; workItemId: string; isOffline?: boolean }) {
  const [previewing, setPreviewing] = useState<{ evidenceId: string; artifact: ArtifactSummary } | null>(null);

  const evidenceQuery = useQuery({
    queryKey: ['evidenceList', projectId, workItemId],
    queryFn: async () => (await listEvidence(projectId, workItemId, withSessionToken())) as unknown as { items: EvidenceDetail[] },
    enabled: !isOffline,
  });

  if (evidenceQuery.isPending) {
    return <div className="flex-1 p-6" aria-hidden><Skeleton className="h-24 w-full" /></div>;
  }
  if (evidenceQuery.isError) {
    return <div className="flex-1 p-6"><InlineError {...apiErrorMessage(evidenceQuery.error)} onRetry={() => evidenceQuery.refetch()} /></div>;
  }
  const items = evidenceQuery.data?.items ?? [];

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-4">
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Evidence</h2>
          </div>
          {items.length === 0 ? (
            <p className="px-5 py-4 text-[13px] text-[#475569]">No evidence recorded yet for this WorkItem.</p>
          ) : (
            <div className="divide-y divide-[#ECEFF4]" role="list">
              {items.map(e => (
                <EvidenceRow key={e.evidenceId} projectId={projectId} workItemId={workItemId} evidence={e} isOffline={isOffline}
                  onPreview={(evidenceId, artifact) => setPreviewing({ evidenceId, artifact })} />
              ))}
            </div>
          )}
        </div>
      </div>
      {previewing && (
        <ArtifactPreviewDialog projectId={projectId} workItemId={workItemId} evidenceId={previewing.evidenceId}
          artifact={previewing.artifact} onClose={() => setPreviewing(null)} />
      )}
    </div>
  );
}

// ─── Chat Tab ─────────────────────────────────────────────────────────────────

/**
 * MessageText decodes blob as text and renders it as plain, React-escaped
 * children inside a <pre> — never innerHTML — so a raw HTML/script message
 * body can never execute even though the server's own Content-Disposition
 * header already guarantees it would never be served inline in a real
 * browser tab either way.
 */
function MessageText({ blob }: { blob: Blob }) {
  const [text, setText] = useState<string | null>(null);
  useEffect(() => {
    let cancelled = false;
    blob.text().then(t => { if (!cancelled) setText(t); });
    return () => { cancelled = true; };
  }, [blob]);
  if (text === null) return null;
  return <p className="whitespace-pre-wrap break-words">{text}</p>;
}

function MessageImage({ blob, alt }: { blob: Blob; alt: string }) {
  const url = useMemo(() => URL.createObjectURL(blob), [blob]);
  useEffect(() => () => URL.revokeObjectURL(url), [url]);
  return <img src={url} alt={alt} className="max-w-xs rounded-[6px] border border-[#CDD5DF]" />;
}

/**
 * MessageBody fetches a Message's own real content (fetchMessageContent,
 * web/src/api/message.ts — the hand-written fetch V7-15 adds for the
 * getMessageContent route it also adds, since no other route could ever
 * read a Message's own real text) and renders it according to its real
 * Content-Type: a chat-typed text message (text/plain/csv/json) decodes as
 * plain escaped text; a small image attachment renders inline; anything
 * else (including any non-inline-safe type — reusing evidence.ts's own
 * isInlineSafeMediaType, the same closed allow-list
 * internal/delivery/httpapi/media.go enforces server-side) is offered only
 * as a real Download link, never rendered.
 */
function MessageBody({ projectId, workItemId, message }: { projectId: string; workItemId: string; message: MessageRef }) {
  const contentQuery = useQuery({
    queryKey: ['messageContent', projectId, workItemId, message.messageId],
    queryFn: () => fetchMessageContent(projectId, workItemId, message.messageId),
    // A Message row is immutable/append-only (internal/app/message/commands.go's
    // own doc comment) — its own content never changes once created, so
    // treating a cache hit as permanently fresh is correct, not a shortcut:
    // without this, TanStack Query's own default staleTime:0 would trigger
    // an immediate redundant background refetch even for the operator's own
    // just-sent message, whose content this component already optimistically
    // seeded into the cache (see the composer's own onSuccess below).
    staleTime: Infinity,
  });
  if (contentQuery.isPending) {
    return <div aria-hidden><Skeleton className="h-4 w-32" /></div>;
  }
  if (contentQuery.isError) {
    return <InlineError {...apiErrorMessage(contentQuery.error)} onRetry={() => contentQuery.refetch()} />;
  }
  const { contentType, blob } = contentQuery.data;
  const base = contentType.split(';')[0]?.trim().toLowerCase() ?? '';
  if (base === 'text/plain' || base === 'text/csv' || base === 'application/json') {
    return <MessageText blob={blob} />;
  }
  if (isInlineSafeMediaType(contentType) && base.startsWith('image/') && blob.size <= PREVIEW_SIZE_LIMIT_BYTES) {
    return <MessageImage blob={blob} alt={message.messageId} />;
  }
  return (
    <a href={messageContentUrl(projectId, workItemId, message.messageId)} download
      className="inline-flex items-center gap-2 px-2 py-1 bg-white border border-[#CDD5DF] rounded-[6px] text-[12px] text-[#3659E3] hover:underline">
      <FileText size={12} aria-hidden />
      <span>Download attachment ({contentType})</span>
    </a>
  );
}

function ChatTab({ projectId, workItemId, isOffline }: { projectId: string; workItemId: string; isOffline?: boolean }) {
  const queryClient = useQueryClient();
  const { toasts, show, dismiss } = useToasts();
  const [draft, setDraft] = useState('');
  // Fixed for the lifetime of one compose attempt — a Retry after a failed
  // send reuses this SAME key so the server's own receipt-replay returns
  // the SAME Message rather than creating a duplicate (design doc's own
  // "dùng lại đúng idempotency key, không tạo message mới"); a fresh key is
  // minted only once THIS attempt actually succeeds.
  const [composeKey, setComposeKey] = useState(() => crypto.randomUUID());
  const fileInputRef = useRef<HTMLInputElement>(null);

  const messagesQuery = useInfiniteQuery({
    queryKey: ['messages', projectId, workItemId],
    queryFn: async ({ pageParam }) => (await listMessages(projectId, workItemId,
      withSessionToken({ query: pageParam ? { cursor: pageParam as string } : {} }))) as unknown as { items: MessageRef[]; nextCursor?: string },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: last => last.nextCursor,
    enabled: !isOffline,
    // No shared app-shell SSE/journal-cursor wiring exists yet for this
    // screen to key a real push-based resync off of (web/src/api/sse.ts's
    // own watchProjectEvents has no real caller anywhere in this app yet —
    // V7-04B built and unit-tested it, but no screen's own Freshness field
    // gives Chat a real initial journal cursor to open a stream from).
    // Polling is the honest interim: real, working, and doesn't guess at an
    // integration this task did not build.
    refetchInterval: isOffline ? false : 4000,
  });
  const messages = messagesQuery.data?.pages.flatMap(p => p.items) ?? [];

  const sendMutation = useMutation({
    mutationFn: () => appendMessage(projectId, workItemId, {
      role: 'USER', content: draft, contentType: 'text/plain',
    }, withSessionToken({ idempotencyKey: composeKey })),
    onSuccess: result => {
      // The operator already holds the exact text just sent — seed the
      // content cache directly rather than round-tripping a real fetch for
      // content this client already has (the message package's own doc
      // comment: "the operator's own USER-authored messages are the one
      // case that never needs this").
      queryClient.setQueryData(['messageContent', projectId, workItemId, result.messageId], {
        contentType: 'text/plain', blob: new Blob([draft], { type: 'text/plain' }),
      });
      setDraft('');
      setComposeKey(crypto.randomUUID());
      messagesQuery.refetch();
    },
    onError: () => { /* draft + composeKey both stay — Retry reuses the identical attempt */ },
  });

  const attachMutation = useMutation({
    mutationFn: (file: File) => uploadAttachment(projectId, workItemId, file, {
      role: 'USER', token: getSessionToken(), idempotencyKey: crypto.randomUUID(),
    }),
    onSuccess: () => {
      show({ intent: 'success', message: 'Attachment uploaded.' });
      messagesQuery.refetch();
    },
    onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
  });

  return (
    <div className="flex-1 flex overflow-hidden">
      <div className="flex-1 flex flex-col overflow-hidden">
        <div className="flex-1 overflow-y-auto p-5 space-y-4" role="log" aria-label="Conversation messages" aria-live="polite">
          {messagesQuery.isPending ? (
            <div aria-hidden><Skeleton className="h-24 w-full" /></div>
          ) : messagesQuery.isError ? (
            <InlineError {...apiErrorMessage(messagesQuery.error)} onRetry={() => messagesQuery.refetch()} />
          ) : messages.length === 0 ? (
            <p className="text-[13px] text-[#475569]">No messages yet — start the conversation below.</p>
          ) : (
            messages.map(msg => {
              const isUser = msg.role === 'USER';
              return (
                <article key={msg.messageId} className={`flex gap-3 ${isUser ? 'flex-row-reverse' : ''}`}
                  aria-label={`Message from ${msg.actor} (${msg.role}) at ${new Date(msg.createdAt).toLocaleString()}`}>
                  <div className={`w-7 h-7 rounded-full flex items-center justify-center flex-shrink-0 border ${isUser ? 'bg-[#EEF2FF] text-[#3659E3] border-[#C7D2FE]' : 'bg-[#EDE9FE] text-[#5B21B6] border-[#C4B5FD]'}`} aria-hidden>
                    {isUser ? <User size={14} /> : <Cpu size={14} />}
                  </div>
                  <div className={`max-w-lg ${isUser ? 'items-end flex flex-col' : ''}`}>
                    <div className={`flex items-center gap-2 mb-1 ${isUser ? 'flex-row-reverse' : ''}`}>
                      <span className="text-[12px] font-medium text-[#172033]">{msg.actor}</span>
                      <span className="font-mono text-[12px] px-1.5 py-0.5 bg-[#F3F5F8] text-[#475569] rounded-[4px]">{msg.role}</span>
                      <span className="text-[12px] text-[#475569]">{new Date(msg.createdAt).toLocaleTimeString()}</span>
                      {msg.attemptId && <span className="font-mono text-[12px] px-1.5 py-0.5 bg-[#EDE9FE] text-[#5B21B6] rounded-[4px]">{msg.attemptId}</span>}
                    </div>
                    <div className={`rounded-[8px] px-3.5 py-2.5 text-[13px] text-[#172033] ${isUser ? 'bg-[#EEF2FF] border border-[#C7D2FE]' : 'bg-[#F3F5F8] border border-[#CDD5DF]'}`}>
                      <MessageBody projectId={projectId} workItemId={workItemId} message={msg} />
                    </div>
                  </div>
                </article>
              );
            })
          )}
          {messagesQuery.hasNextPage && (
            <div className="flex justify-center">
              <Button intent="secondary" size="compact" loading={messagesQuery.isFetchingNextPage} onClick={() => messagesQuery.fetchNextPage()}>Load more</Button>
            </div>
          )}
        </div>

        <div className="border-t border-[#CDD5DF] p-4 bg-white flex-shrink-0">
          {sendMutation.isError && (
            <div className="mb-2">
              <InlineError {...apiErrorMessage(sendMutation.error)} onRetry={() => sendMutation.mutate()} />
            </div>
          )}
          <div className="flex gap-2">
            <textarea
              value={draft}
              onChange={e => setDraft(e.target.value)}
              placeholder="Add a platform message…"
              aria-label="Message composer"
              disabled={isOffline}
              className="flex-1 h-20 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none resize-none"
            />
            <div className="flex flex-col gap-2">
              <Button intent="primary" size="compact" disabled={isOffline || !draft.trim()} loading={sendMutation.isPending} onClick={() => sendMutation.mutate()}>Send</Button>
              <input ref={fileInputRef} type="file" className="hidden" aria-hidden
                onChange={e => { const f = e.target.files?.[0]; if (f) attachMutation.mutate(f); e.target.value = ''; }} />
              <Button intent="secondary" size="compact" disabled={isOffline} loading={attachMutation.isPending}
                icon={<Paperclip size={13} aria-hidden />} onClick={() => fileInputRef.current?.click()}>Attach</Button>
            </div>
          </div>
          <p aria-live="polite" className="sr-only">
            {attachMutation.isPending ? 'Uploading attachment…' : attachMutation.isSuccess ? 'Attachment uploaded.' : ''}
          </p>
        </div>
      </div>
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}

// ─── Root export ──────────────────────────────────────────────────────────────

interface TaskDetailProps {
  projectId: string;
  projectName: string;
  workItemId: string;
  activeTab: NavRoute;
  onTabChange: (r: NavRoute, ids?: { projectId?: string; taskId?: string }) => void;
  isOffline?: boolean;
}

/**
 * TaskDetailScreen — V7-11's own real "Task detail overview and actions"
 * (docs/design/09-v7-alpha-ui.md V7-11's own Thực hiện line). Fetches once,
 * at this root, and hands the already-fetched data down as props to
 * TaskHeader/OverviewTab — a single source of truth for both, rather than
 * each independently re-fetching. task-overview (V7-11), task-graph
 * (V7-12), task-workspace (V7-13/V7-13A), task-evidence (V7-14) and
 * task-chat (V7-15) all consume real data now.
 */
export function TaskDetailScreen({ projectId, projectName, workItemId, activeTab, onTabChange, isOffline }: TaskDetailProps) {
  const queryClient = useQueryClient();

  const workItemQuery = useQuery({
    queryKey: ['workItemAuthoritative', projectId, workItemId],
    queryFn: async () => (await getWorkItem(projectId, workItemId, withSessionToken())) as unknown as GetWorkItemResponse,
    enabled: !isOffline,
  });

  const familyId = workItemQuery.data?.familyId;
  const familyQuery = useQuery({
    queryKey: ['taskFamily', projectId, familyId],
    queryFn: async () => (await getTaskFamily(projectId, familyId!, withSessionToken())) as unknown as GetTaskFamilyResponse,
    enabled: !isOffline && !!familyId,
  });

  const cardQuery = useQuery({
    queryKey: ['workItemProjectedDetail', workItemId],
    queryFn: async () => (await getWorkItemProjectedDetail(workItemId, withSessionToken())) as unknown as WorkItemProjectedDetailResponse,
    enabled: !isOffline,
  });

  const activeRunId = cardQuery.data?.card.activeRunId;
  const runDiagnosticsQuery = useQuery({
    queryKey: ['runDiagnostics', projectId, activeRunId],
    queryFn: async () => (await getRunDiagnostics(projectId, activeRunId!, withSessionToken())) as unknown as RunDiagnosticsResponse,
    enabled: !isOffline && !!activeRunId,
  });

  const onActionSettled = () => {
    queryClient.invalidateQueries({ queryKey: ['workItemAuthoritative', projectId, workItemId] });
    queryClient.invalidateQueries({ queryKey: ['taskFamily', projectId, familyId] });
    queryClient.invalidateQueries({ queryKey: ['workItemProjectedDetail', workItemId] });
    queryClient.invalidateQueries({ queryKey: ['runDiagnostics', projectId, activeRunId] });
    queryClient.invalidateQueries({ queryKey: ['runGraph', projectId, activeRunId] });
    queryClient.invalidateQueries({ queryKey: ['runTimeline', projectId, activeRunId] });
    queryClient.invalidateQueries({ queryKey: ['kanban', projectId] });
  };

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <TaskHeader
        projectId={projectId} projectName={projectName} workItemId={workItemId}
        activeTab={activeTab} onTabChange={onTabChange} isOffline={isOffline}
        workItem={workItemQuery.data} family={familyQuery.data} card={cardQuery.data?.card}
        runDiagnostics={runDiagnosticsQuery.data} onActionSettled={onActionSettled}
      />
      <div id="workitem-tab-panel" role="tabpanel" aria-labelledby={`tab-${activeTab}`} className="flex-1 flex flex-col min-h-0 overflow-hidden">
      {activeTab === 'task-overview'   && <OverviewTab workItem={workItemQuery.data} runDiagnostics={runDiagnosticsQuery.data} />}
      {activeTab === 'task-graph'      && (
        <GraphTimelineTab projectId={projectId} runId={activeRunId} runDiagnostics={runDiagnosticsQuery.data} isOffline={isOffline} onActionSettled={onActionSettled} />
      )}
      {activeTab === 'task-workspace'  && <WorkspaceTab projectId={projectId} familyId={familyId} isOffline={isOffline} />}
      {activeTab === 'task-evidence'   && <EvidenceTab projectId={projectId} workItemId={workItemId} isOffline={isOffline} />}
      {activeTab === 'task-chat'       && <ChatTab projectId={projectId} workItemId={workItemId} isOffline={isOffline} />}
      </div>
    </div>
  );
}
