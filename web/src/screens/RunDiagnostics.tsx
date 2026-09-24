import React, { useState } from 'react';
import { Badge, StatusBadge, Button, CopyableId, OperationNotice, Dialog } from '../components/ui';
import { AlertTriangle, CheckCircle2, ChevronDown, ChevronRight } from '../components/icons';

const LAYERS = [
  {
    id: 'queue', name: 'Queue', status: 'healthy',
    detail: 'Job enqueued at 09:48:03. No queue contention.',
    items: [{ label: 'Job ID', value: 'job-f7a2b3c4', mono: true }, { label: 'Queue depth', value: '0' }],
  },
  {
    id: 'job', name: 'Job', status: 'healthy',
    detail: 'Job acquired by worker at 09:48:05.',
    items: [{ label: 'Worker', value: 'worker-01', mono: true }, { label: 'Attempts', value: '1/3' }],
  },
  {
    id: 'lease', name: 'Workspace Lease', status: 'degraded',
    detail: 'Lease for repo-c3d4 (worker-service) was interrupted. Quarantine triggered at 09:53:12.',
    items: [{ label: 'Lease ID', value: 'lease-9e8d7c6b', mono: true }, { label: 'State', value: 'INTERRUPTED' }],
  },
  {
    id: 'fence', name: 'Concurrency Fence', status: 'healthy',
    detail: 'No concurrent runs on overlapping scope.',
    items: [{ label: 'Fence key', value: 'proj-alpha-001/wi-0018', mono: true }],
  },
  {
    id: 'provider', name: 'Provider', status: 'healthy',
    detail: 'anthropic/claude-sonnet-4-6 responding. Rate limit: 72% used.',
    items: [{ label: 'Provider', value: 'anthropic' }, { label: 'Model', value: 'claude-sonnet-4-6' }],
  },
  {
    id: 'workspace', name: 'Workspace', status: 'degraded',
    detail: 'worker-service scope is quarantined. Reconciliation required before writes can resume.',
    items: [{ label: 'WorkspaceSet', value: 'ws-a1b2c3d4', mono: true }, { label: 'Generation', value: '3' }],
  },
];

const CORR_ROWS = [
  { label: 'Run ID', value: 'run-8f7a2c91' },
  { label: 'Job ID', value: 'job-f7a2b3c4' },
  { label: 'Lease ID', value: 'lease-9e8d7c6b' },
  { label: 'Trace ID', value: 'trace-d4e5f6a7b8c9d0e1' },
  { label: 'Correlation', value: 'corr-x9y2z3w4-a1b2c3d4' },
];

type ProjectionState = 'Fresh' | 'Stale' | 'Degraded';

export function RunDiagnosticsScreen({
  isOffline = false,
  projectionState = 'Fresh',
  freshness = 'just now',
}: {
  isOffline?: boolean;
  projectionState?: ProjectionState;
  freshness?: string;
}) {
  const [expanded, setExpanded] = useState<string | null>('lease');
  const [action, setAction] = useState<'rebuild' | 'refresh' | 'reconcile' | null>(null);
  const [operation, setOperation] = useState<string | null>(null);

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-xl font-semibold text-[#172033]">Run Diagnostics</h1>
            <p className="text-sm text-[#5D697A] mt-0.5">
              <span className="font-mono">run-8f7a2c91</span> · platform-core / wi-0018
            </p>
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge state="RUNNING" entity="workitem" />
            <span className="text-xs text-[#5D697A]">started 09:48:03</span>
          </div>
        </div>

        {operation && <OperationNotice state="Requested" message="Operation accepted; waiting for the authoritative projection update." ref={operation} />}

        {/* Projection health is driven by the same shell state as the global banner. */}
        <div className={`flex items-center gap-3 p-3 rounded-[8px] border ${projectionState === 'Fresh' ? 'bg-[#DCFCE7] border-[#86EFAC]' : 'bg-[#FEF3C7] border-[#FCD34D]'}`}>
          {projectionState === 'Fresh'
            ? <CheckCircle2 size={16} aria-hidden className="text-[#166534]" />
            : <AlertTriangle size={16} aria-hidden className="text-[#92400E]" />}
          <div className="flex-1 min-w-0">
            <span className={`text-sm font-medium ${projectionState === 'Fresh' ? 'text-[#166534]' : 'text-[#92400E]'}`}>Projection {projectionState}</span>
            <p className={`text-xs mt-0.5 ${projectionState === 'Fresh' ? 'text-[#166534]' : 'text-[#92400E]'}`}>
              {projectionState === 'Fresh'
                ? `Read model is current; last updated ${freshness}.`
                : projectionState === 'Stale'
                  ? `Cached read model last updated ${freshness}. Refresh before relying on recent events.`
                  : 'Projection cursor was interrupted. Some timeline events may be missing.'}
            </p>
          </div>
          {projectionState !== 'Fresh' && (
            <Button size="compact" intent="quiet" className="text-[#92400E] border-[#FCD34D] flex-shrink-0" disabled={isOffline}
              onClick={() => setAction(projectionState === 'Degraded' ? 'rebuild' : 'refresh')}>
              {projectionState === 'Degraded' ? 'Rebuild Projection' : 'Refresh Projection'}
            </Button>
          )}
        </div>

        {/* Layer cards */}
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Execution Layers</h2>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {LAYERS.map(layer => (
              <div key={layer.id}>
                <button
                  onClick={() => setExpanded(expanded === layer.id ? null : layer.id)}
                  className="w-full flex items-center gap-4 px-5 py-4 text-left hover:bg-[#FAFBFC] transition-colors"
                >
                  {layer.status === 'healthy' ? <CheckCircle2 size={14} aria-hidden className="text-[#166534]" /> : <AlertTriangle size={14} aria-hidden className="text-[#92400E]" />}
                  <span className="text-sm font-medium text-[#172033] w-36">{layer.name}</span>
                  <Badge label={layer.status === 'healthy' ? 'HEALTHY' : 'WARNING'} intent={layer.status === 'healthy' ? 'success' : 'warning'} />
                  <span className="text-sm text-[#5D697A] flex-1 truncate">{layer.detail}</span>
                  {expanded === layer.id ? <ChevronDown size={14} aria-hidden className="text-[#475569]" /> : <ChevronRight size={14} aria-hidden className="text-[#475569]" />}
                </button>
                {expanded === layer.id && (
                  <div className="px-5 pb-4 bg-[#FAFBFC] border-t border-[#ECEFF4]">
                    <div className="grid grid-cols-2 gap-x-8 gap-y-2 mt-3 text-sm">
                      {layer.items.map(item => (
                        <div key={item.label}>
                          <div className="text-xs text-[#5D697A]">{item.label}</div>
                          {item.mono
                            ? <CopyableId value={item.value} />
                            : <div className="text-sm font-medium text-[#172033]">{item.value}</div>}
                        </div>
                      ))}
                    </div>
                    {layer.status === 'degraded' && (
                      <div className="mt-3 p-3 rounded-[6px] bg-[#FEF3C7] border border-[#FCD34D] text-xs text-[#92400E]">
                        <div className="font-medium">WORKSPACE_QUARANTINE</div>
                        <div className="mt-0.5">{layer.detail}</div>
                        <div className="mt-2 flex gap-2">
                          <Button size="compact" intent="quiet" className="text-[#92400E] border-[#FCD34D]" disabled={isOffline} onClick={() => setAction('reconcile')}>Request Reconcile</Button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* Correlation IDs */}
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Correlation References</h2>
            <p className="text-xs text-[#5D697A] mt-0.5">Safe identifiers only. No PID, argv, cwd, or secret values.</p>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {CORR_ROWS.map(row => (
              <div key={row.label} className="flex items-center gap-4 px-5 py-3">
                <span className="text-sm text-[#5D697A] w-32">{row.label}</span>
                <CopyableId value={row.value} />
              </div>
            ))}
          </div>
        </div>
      </div>

      {action && (
        <Dialog
          title={action === 'rebuild' ? 'Rebuild Projection' : action === 'refresh' ? 'Refresh Projection' : 'Request Workspace Reconcile'}
          description={action === 'rebuild'
            ? 'Rebuild the run projection from its durable journal position.'
            : action === 'refresh'
              ? 'Request a fresh projection read from the current durable watermark.'
              : 'Request reconciliation for the quarantined worker-service workspace.'}
          onClose={() => setAction(null)}
          actions={<><Button intent="secondary" onClick={() => setAction(null)}>Cancel</Button><Button intent="primary" onClick={() => {
            setOperation(action === 'rebuild' ? 'op-projection-4d2a' : action === 'refresh' ? 'op-refresh-6a9e' : 'op-reconcile-7b1c');
            setAction(null);
          }}>{action === 'rebuild' ? 'Request rebuild' : action === 'refresh' ? 'Request refresh' : 'Request reconcile'}</Button></>}
        >
          <dl className="space-y-2 text-[13px]">
            <div className="flex gap-3"><dt className="w-32 text-[#475569]">Run</dt><dd><CopyableId value="run-8f7a2c91" /></dd></div>
            <div className="flex gap-3"><dt className="w-32 text-[#475569]">Target</dt><dd>{action === 'reconcile' ? 'repo-c3d4 / worker-service' : 'projection / journal watermark'}</dd></div>
            <div className="flex gap-3"><dt className="w-32 text-[#475569]">Current version</dt><dd className="font-mono">3</dd></div>
          </dl>
        </Dialog>
      )}
    </div>
  );
}
