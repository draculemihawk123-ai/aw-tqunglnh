import React, { useState } from 'react';
import {
  Badge, StatusBadge, Button, IconButton, ValidActionBar, BlockerCard,
  CopyableId, VerdictBadge, OperationNotice, Dialog, Skeleton,
} from '../components/ui';
import {
  AlertTriangle, Maximize2, ZoomIn, ZoomOut, ListTree,
  CheckCircle2, ChevronDown, ChevronRight, FileDiff, FileText, ScrollText, Cpu, User,
} from '../components/icons';
import type { NavRoute } from '../components/shell/LeftNav';

// ─── Shared fixture constants ─────────────────────────────────────────────────

const WI_ID = 'wi-0018';
const RUN_ID = 'run-8f7a2c91';
const WORKFLOW = 'feature-workflow@v3.1.0';

// ─── Persistent Header ────────────────────────────────────────────────────────

type ApproveDialogState = null | 'open' | 'requested';

interface TaskHeaderProps {
  activeTab: NavRoute;
  onTabChange: (r: NavRoute) => void;
  isOffline?: boolean;
}

function TaskHeader({ activeTab, onTabChange, isOffline }: TaskHeaderProps) {
  const [approveDialog, setApproveDialog] = useState<ApproveDialogState>(null);
  const [cancelRunDialog, setCancelRunDialog] = useState(false);
  const [cancelWiDialog, setCancelWiDialog] = useState(false);
  const [operation, setOperation] = useState<null | { ref: string; message: string }>(null);

  const submitCancellation = (target: 'run' | 'workitem') => {
    setCancelRunDialog(false);
    setCancelWiDialog(false);
    setOperation(target === 'run'
      ? { ref: 'op-cancel-run-91d4', message: 'Cancel Run requested. Awaiting server acknowledgment before the run enters CANCELLING.' }
      : { ref: 'op-cancel-wi-2ac8', message: 'Cancel WorkItem requested. Active runs must quiesce before a terminal state is confirmed.' });
  };

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
          <span>platform-core</span>
          <span aria-hidden>/</span>
          <span className="text-[#172033] font-medium">Add distributed tracing to API gateway</span>
          <CopyableId value={WI_ID} />
        </nav>
        {/* State + actions row */}
        <div className="flex items-center gap-3 flex-wrap">
          <div className="flex items-center gap-1.5">
            <span className="text-[12px] text-[#475569]">WorkItem</span>
            <StatusBadge state="ACTIVE" entity="workitem" />
          </div>
          <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
          <div className="flex items-center gap-1.5">
            <span className="text-[12px] text-[#475569]">Run</span>
            <StatusBadge state="RUNNING" entity="workitem" />
            <CopyableId value={RUN_ID} short="run-8f7a2c" />
          </div>
          <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
          <span className="text-[12px] text-[#475569]">
            workflow: <span className="font-mono font-medium text-[#172033]">{WORKFLOW}</span>
          </span>
          <div className="h-4 w-px bg-[#CDD5DF]" aria-hidden />
          <div className="flex gap-1.5" aria-label="Repository scope">
            <span className="text-[12px] px-1.5 py-0.5 rounded-[4px] bg-[#F1F5F9] text-[#475569] border border-[#CBD5E1]">core-api</span>
            <span className="text-[12px] px-1.5 py-0.5 rounded-[4px] bg-[#F1F5F9] text-[#475569] border border-[#CBD5E1]">worker-service</span>
          </div>
          <span className="text-[12px] text-[#475569]">updated 09:55:12</span>
          <div className="flex-1" />
          {/* Single ValidActionBar — server-provided named actions only */}
          <ValidActionBar actions={[
            { label: 'Approve Node', onClick: () => setApproveDialog('open'), disabled: isOffline },
            { label: 'Cancel Run', intent: 'secondary', onClick: () => setCancelRunDialog(true), disabled: isOffline },
            { label: 'Cancel WorkItem', intent: 'destructive', onClick: () => setCancelWiDialog(true), disabled: isOffline },
          ]} />
        </div>
        {operation && <OperationNotice state="Requested" message={operation.message} ref={operation.ref} />}
      </div>
      {/* Blocker banner */}
      <div className="px-6 pb-2">
        <BlockerCard
          type="APPROVAL_REQUIRED"
          target={`${RUN_ID} / node: APPROVAL_01`}
          reason="Awaiting operator approval to continue with a partial ReleaseSet after worker-service verification failed."
          opened="09:53:48"
          actions={[]}
        />
      </div>
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
              onClick={() => onTabChange(tab.id)}
              onKeyDown={event => {
                const index = tabs.findIndex(item => item.id === tab.id);
                const next = event.key === 'ArrowRight' ? (index + 1) % tabs.length : event.key === 'ArrowLeft' ? (index + tabs.length - 1) % tabs.length : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : -1;
                if (next < 0) return;
                event.preventDefault();
                onTabChange(tabs[next].id);
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

      {/* ── Approve Node dialog ────────────────────────────────────────── */}
      {approveDialog === 'open' && (
        <Dialog
          title="Approve Node: APPROVAL_01"
          description="Confirming approval allows the run to evaluate a partial ReleaseSet. It does not change the failed verdict or perform a remote Git operation."
          onClose={() => setApproveDialog(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setApproveDialog(null)}>Cancel</Button>
              <Button intent="primary" disabled={isOffline} title={isOffline ? 'Reconnect to perform this action' : undefined} onClick={() => setApproveDialog('requested')}>Confirm Approval</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5">
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Project</dt><dd>platform-core</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">WorkItem</dt><dd><CopyableId value={WI_ID} /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Run</dt><dd><CopyableId value={RUN_ID} /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Node</dt><dd className="font-mono">APPROVAL_01</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32 flex-shrink-0">Requested evidence</dt><dd>rset-f7a2b3c4 · core-api PASS, worker-service FAIL; test-results.json. Failed evidence remains failed.</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Scope impact</dt><dd>Continue with PASS repository only; worker-service remains quarantined</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Actor</dt><dd>local-operator</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-32">Workflow version</dt><dd className="font-mono">{WORKFLOW}</dd></div>
          </dl>
        </Dialog>
      )}
      {approveDialog === 'requested' && (
        <Dialog
          title="Approval Submitted"
          onClose={() => setApproveDialog(null)}
          actions={<Button intent="primary" onClick={() => setApproveDialog(null)}>Close</Button>}
        >
          <OperationNotice state="Requested" message="Approval submitted. Waiting for server acknowledgment." ref="op-apr-7f2c" />
          <p className="text-[12px] text-[#475569] mt-2">Node and run state will update when the server confirms via event stream. Do not assume terminal success.</p>
        </Dialog>
      )}

      {/* ── Cancel Run dialog ─────────────────────────────────────────── */}
      {cancelRunDialog && (
        <Dialog
          title="Cancel Run"
          description="The run will enter CANCELLING and stop accepting new node activations. In-progress attempts may still complete gracefully."
          onClose={() => setCancelRunDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setCancelRunDialog(false)}>Keep Run</Button>
              <Button intent="destructive" disabled={isOffline} onClick={() => submitCancellation('run')}>Cancel Run</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Run</dt><dd><CopyableId value={RUN_ID} /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Enters state</dt><dd><Badge label="CANCELLING" intent="runtime" /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Workflow</dt><dd className="font-mono text-[12px]">{WORKFLOW}</dd></div>
          </dl>
        </Dialog>
      )}

      {/* ── Cancel WorkItem dialog ────────────────────────────────────── */}
      {cancelWiDialog && (
        <Dialog
          title="Cancel WorkItem"
          description="Cancelling a WorkItem requests termination. Active runs must quiesce before the WorkItem becomes CANCELLED."
          onClose={() => setCancelWiDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setCancelWiDialog(false)}>Keep WorkItem</Button>
              <Button intent="destructive" disabled={isOffline} onClick={() => submitCancellation('workitem')}>Cancel WorkItem</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">WorkItem</dt><dd><CopyableId value={WI_ID} /></dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Active run</dt><dd><CopyableId value={RUN_ID} /></dd></div>
            <div className="flex gap-3 items-start"><dt className="text-[#475569] w-28 flex-shrink-0">Impact</dt>
              <dd className="text-[#92400E] bg-[#FEF3C7] rounded-[4px] px-2 py-1 border border-[#FCD34D]">Active run will enter CANCELLING. WorkItem moves to CANCELLED only after the run reaches a terminal state.</dd>
            </div>
          </dl>
        </Dialog>
      )}
    </div>
  );
}

// ─── Overview Tab ─────────────────────────────────────────────────────────────

function OverviewTab() {
  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-4">
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5">
          <h2 className="text-sm font-semibold text-[#172033] mb-4">Contract</h2>
          <dl className="grid grid-cols-[auto_1fr] gap-x-8 gap-y-3 text-sm">
            <dt className="text-[#475569] font-medium">WHAT</dt>
            <dd>Add distributed tracing instrumentation to the API gateway and worker service using OpenTelemetry. Export traces to the configured collector.</dd>
            <dt className="text-[#475569] font-medium">Acceptance Criteria</dt>
            <dd>
              <ol className="space-y-1 list-decimal list-inside text-[#172033]">
                <li className="text-[13px]">All HTTP routes emit spans with correct service.name and trace propagation headers.</li>
                <li className="text-[13px]">Worker job processing emits child spans linked to originating HTTP span.</li>
                <li className="text-[13px]">Unit tests cover trace context propagation.</li>
                <li className="text-[13px]">No secret values or PII appear in span attributes.</li>
              </ol>
            </dd>
            <dt className="text-[#475569] font-medium">Exclusions</dt>
            <dd className="text-[#475569] text-[13px]">Database query traces, UI instrumentation, sampling rate configuration.</dd>
            <dt className="text-[#475569] font-medium">Risk</dt>
            <dd className="text-[13px] text-[#92400E] bg-[#FEF3C7] px-2 py-1 rounded-[4px] border border-[#FCD34D]">Performance overhead if trace export is synchronous. Exporter must use async/buffered mode.</dd>
            <dt className="text-[#475569] font-medium">Scope</dt>
            <dd>
              <div className="space-y-1 font-mono text-[12px]">
                <div className="flex gap-2"><span className="text-[#166534] bg-[#DCFCE7] px-1 rounded">WRITE</span><span>core-api / src/api</span></div>
                <div className="flex gap-2"><span className="text-[#166534] bg-[#DCFCE7] px-1 rounded">WRITE</span><span>worker-service / src/worker</span></div>
                <div className="flex gap-2"><span className="text-[#1E40AF] bg-[#DBEAFE] px-1 rounded">READ</span><span>core-api / config</span></div>
              </div>
            </dd>
          </dl>
        </div>

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5">
          <h2 className="text-sm font-semibold text-[#172033] mb-3">Active Run</h2>
          <div className="flex items-center gap-4 flex-wrap text-[13px]">
            <div className="flex items-center gap-2">
              <StatusBadge state="RUNNING" entity="workitem" />
              <CopyableId value={RUN_ID} />
            </div>
            <span className="text-[#475569]">workflow: <span className="font-mono text-[12px]">{WORKFLOW}</span></span>
            <span className="text-[#475569]">started 09:48:03 · 8 m 14 s elapsed</span>
            <span className="text-[#475569]">provider: anthropic / claude-sonnet-4-6</span>
          </div>
          <div className="mt-3">
            <OperationNotice state="Running" message="Awaiting operator approval at node APPROVAL_01" ref="op-ref-7f2c" />
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── Graph & Timeline Tab ─────────────────────────────────────────────────────

type GNodeState = 'DONE' | 'RUNNING' | 'WAITING' | 'PENDING' | 'FAILED' | 'BLOCKED';
interface GNode { id: string; kind: string; label: string; state: GNodeState; x: number; y: number }
interface GEdge { from: string; to: string; kind?: 'normal' | 'rework'; label?: string }

const GRAPH_NODES: GNode[] = [
  { id: 'START',       kind: 'START',        label: 'Start',         state: 'DONE',    x: 95,  y: 30  },
  { id: 'AGENT_01',    kind: 'AGENT',        label: 'Plan Changes',  state: 'DONE',    x: 65,  y: 110 },
  { id: 'FORK_01',     kind: 'FORK',         label: 'Fork',          state: 'DONE',    x: 95,  y: 195 },
  { id: 'AGENT_02a',   kind: 'AGENT',        label: 'Impl: core-api',state: 'DONE',    x: 15,  y: 275 },
  { id: 'AGENT_02b',   kind: 'AGENT',        label: 'Impl: worker',  state: 'DONE',    x: 120, y: 275 },
  { id: 'JOIN_01',     kind: 'JOIN',         label: 'Join',          state: 'DONE',    x: 95,  y: 360 },
  { id: 'MGATE',       kind: 'MACHINE_GATE', label: 'Verify Tests',  state: 'FAILED',  x: 65,  y: 440 },
  { id: 'APPROVAL_01', kind: 'APPROVAL',     label: 'Approval',      state: 'WAITING', x: 65,  y: 525 },
  { id: 'END',         kind: 'END',          label: 'End',           state: 'PENDING', x: 95,  y: 610 },
];

const GRAPH_EDGES: GEdge[] = [
  { from: 'START',    to: 'AGENT_01' },
  { from: 'AGENT_01', to: 'FORK_01' },
  { from: 'FORK_01',  to: 'AGENT_02a' },
  { from: 'FORK_01',  to: 'AGENT_02b' },
  { from: 'AGENT_02a',to: 'JOIN_01' },
  { from: 'AGENT_02b',to: 'JOIN_01' },
  { from: 'JOIN_01',  to: 'MGATE' },
  { from: 'MGATE',    to: 'APPROVAL_01', label: 'REVIEW' },
  { from: 'MGATE',    to: 'AGENT_01', kind: 'rework', label: 'REWORK' },
  { from: 'APPROVAL_01', to: 'END' },
];

const NODE_BG:   Record<GNodeState, string> = { DONE: '#DCFCE7', RUNNING: '#EDE9FE', WAITING: '#FEF3C7', PENDING: '#F1F5F9', FAILED: '#FEE2E2', BLOCKED: '#FEE2E2' };
const NODE_BD:   Record<GNodeState, string> = { DONE: '#86EFAC', RUNNING: '#C4B5FD', WAITING: '#FCD34D', PENDING: '#CBD5E1', FAILED: '#FCA5A5', BLOCKED: '#FCA5A5' };
const NODE_TEXT: Record<GNodeState, string> = { DONE: '#166534', RUNNING: '#5B21B6', WAITING: '#92400E', PENDING: '#475569', FAILED: '#991B1B', BLOCKED: '#991B1B' };

const TIMELINE_EVENTS = [
  { id: 'e1', time: '09:48:03', type: 'RUN_STARTED',         node: null,          actor: 'local-operator', detail: 'Run created; workspace provisioning started.' },
  { id: 'e2', time: '09:48:07', type: 'NODE_STARTED',        node: 'START',       actor: 'runtime',        detail: 'Start node activated.' },
  { id: 'e3', time: '09:49:19', type: 'NODE_COMPLETED',      node: 'AGENT_01',    actor: 'anthropic',      detail: 'Plan Changes completed in 1 m 12 s.' },
  { id: 'e4', time: '09:49:21', type: 'FORK_ACTIVATED',      node: 'FORK_01',     actor: 'runtime',        detail: 'Parallel branches AGENT_02a and AGENT_02b started.' },
  { id: 'e5', time: '09:52:08', type: 'NODE_COMPLETED',      node: 'JOIN_01',     actor: 'runtime',        detail: 'Join: both branches completed.' },
  { id: 'e6', time: '09:53:44', type: 'MACHINE_GATE_FAILED', node: 'MGATE',       actor: 'runtime',        detail: 'Verification completed: 47 PASS, 1 FAIL in worker-service.' },
  { id: 'e7', time: '09:53:48', type: 'APPROVAL_WAITING',    node: 'APPROVAL_01', actor: 'runtime',        detail: 'Waiting for operator decision on partial ReleaseSet recovery.' },
];

function GraphTimelineTab() {
  const [selectedNode, setSelectedNode] = useState('APPROVAL_01');
  const [showList, setShowList] = useState(false);
  const [expandedEvent, setExpandedEvent] = useState<string | null>(null);
  const W = 245, H = 670;

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Graph panel — 58% */}
      <div className="flex flex-col overflow-hidden" style={{ flex: '0 0 58%', minWidth: 0 }}>
        <div className="flex items-center justify-between px-4 py-2 border-b border-[#CDD5DF] bg-[#F3F5F8]">
          <span className="text-[12px] font-semibold text-[#475569]">Workflow Graph</span>
          <div className="flex gap-1">
            <IconButton label="Zoom in"><ZoomIn size={14} aria-hidden /></IconButton>
            <IconButton label="Zoom out"><ZoomOut size={14} aria-hidden /></IconButton>
            <IconButton label="Fit to view"><Maximize2 size={14} aria-hidden /></IconButton>
            <Button size="compact" intent={showList ? 'primary' : 'quiet'} onClick={() => setShowList(!showList)}>
              <ListTree size={13} aria-hidden /> {showList ? 'Canvas' : 'Accessible List'}
            </Button>
          </div>
        </div>
        {showList ? (
          <div className="flex-1 overflow-y-auto p-4" aria-label="Accessible workflow graph list">
            <ol className="space-y-2">
              {GRAPH_NODES.map(node => (
                <li key={node.id}>
                <button type="button" aria-pressed={selectedNode === node.id}
                  onClick={() => setSelectedNode(node.id)}
                  className={`w-full text-left p-3 rounded-[8px] border cursor-pointer transition-colors ${selectedNode === node.id ? 'bg-[#EEF2FF] border-[#C7D2FE]' : 'bg-white border-[#CDD5DF] hover:border-[#AAB4C3]'}`}>
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-[12px] text-[#475569]">{node.kind}</span>
                    <span className="text-[13px] font-medium text-[#172033]">{node.label}</span>
                    <StatusBadge state={node.state} entity="workitem" />
                  </div>
                  <div className="text-[12px] text-[#475569] mt-1">
                    {GRAPH_EDGES.filter(e => e.to === node.id).map(e => `← ${e.from}${e.label ? ` (${e.label})` : ''}`).join(' · ')}
                    {GRAPH_EDGES.filter(e => e.from === node.id).map(e => ` → ${e.to}${e.label ? ` (${e.label})` : ''}`).join(' · ')}
                  </div>
                </button></li>
              ))}
            </ol>
          </div>
        ) : (
          <div className="flex-1 overflow-auto bg-[#FAFBFC]">
            <svg width={W} height={H} className="mx-auto mt-4" role="img" aria-label="Workflow graph for run run-8f7a2c91">
              <defs>
                <marker id="arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#AAB4C3" />
                </marker>
                <marker id="arr-rw" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="#FCD34D" />
                </marker>
              </defs>
              {GRAPH_EDGES.map((e, i) => {
                const f = GRAPH_NODES.find(n => n.id === e.from)!;
                const t = GRAPH_NODES.find(n => n.id === e.to)!;
                const rw = e.kind === 'rework';
                const x1 = f.x + 55, y1 = f.y + 24, x2 = t.x + 55, y2 = t.y;
                const path = rw
                  ? `M ${x1} ${y1} C ${x1 - 70} ${y1 + 50}, ${x2 - 70} ${y2 - 50}, ${x2} ${y2}`
                  : `M ${x1} ${y1} L ${x2} ${y2}`;
                return (
                  <g key={i}>
                    <path d={path} stroke={rw ? '#FCD34D' : '#CDD5DF'} strokeWidth={1.5}
                      strokeDasharray={rw ? '5,3' : undefined} fill="none"
                      markerEnd={`url(#${rw ? 'arr-rw' : 'arr'})`} />
                    {e.label && <text x={(x1 + x2) / 2 + (rw ? -60 : 4)} y={(y1 + y2) / 2}
                      fontSize="12" fill={rw ? '#92400E' : '#5D697A'} fontFamily="JetBrains Mono, monospace">{e.label}</text>}
                  </g>
                );
              })}
              {GRAPH_NODES.map(node => {
                const sel = selectedNode === node.id;
                const round = ['START', 'END', 'FORK', 'JOIN'].includes(node.kind);
                const bg = NODE_BG[node.state], bd = NODE_BD[node.state], fc = NODE_TEXT[node.state];
                return (
                  <g key={node.id} onClick={() => setSelectedNode(node.id)} style={{ cursor: 'pointer' }} role="button" aria-label={`${node.kind} ${node.label} — ${node.state}`} tabIndex={0} onKeyDown={e => e.key === 'Enter' && setSelectedNode(node.id)}>
                    {round
                      ? <circle cx={node.x + 55} cy={node.y + 12} r={22} fill={bg} stroke={sel ? '#3659E3' : bd} strokeWidth={sel ? 2.5 : 1.5} />
                      : <rect x={node.x} y={node.y} width={110} height={46} rx="8" fill={bg} stroke={sel ? '#3659E3' : bd} strokeWidth={sel ? 2.5 : 1.5} />
                    }
                    <text x={node.x + 55} y={node.y + (round ? 8 : 17)} textAnchor="middle" fontSize="12" fontWeight="500" fill={fc} fontFamily="Inter, sans-serif">{node.kind}</text>
                    <text x={node.x + 55} y={node.y + (round ? 22 : 33)} textAnchor="middle" fontSize="12" fill={fc} fontFamily="Inter, sans-serif">{node.label}</text>
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
        <div className="px-4 py-2 border-b border-[#CDD5DF] bg-[#F3F5F8]">
          <span className="text-[12px] font-semibold text-[#475569]">Timeline</span>
        </div>
        <div className="flex-1 overflow-y-auto p-3 space-y-1" role="list" aria-label="Run timeline">
          {TIMELINE_EVENTS.map(ev => (
            <div key={ev.id} role="listitem">
              <button
                onClick={() => setExpandedEvent(expandedEvent === ev.id ? null : ev.id)}
                className="w-full flex items-center gap-3 px-3 py-2.5 text-left rounded-[6px] border border-[#ECEFF4] bg-white hover:border-[#CDD5DF] transition-colors"
              >
                <span className="font-mono text-[12px] text-[#475569] flex-shrink-0 w-16">{ev.time}</span>
                <span className="font-mono text-[12px] text-[#475569] flex-shrink-0 truncate">{ev.type}</span>
                {ev.node && <span className="text-[12px] px-1.5 py-0.5 bg-[#EEF2FF] text-[#3659E3] rounded-[4px] flex-shrink-0">{ev.node}</span>}
                <span className="text-[12px] text-[#172033] truncate flex-1 text-right">{ev.detail}</span>
              </button>
              {expandedEvent === ev.id && (
                <div className="mx-3 border-x border-b border-[#ECEFF4] rounded-b-[6px] px-3 py-2 bg-[#FAFBFC] text-[12px] text-[#475569]">
                  <div>Actor: <span className="font-mono text-[#172033]">{ev.actor}</span></div>
                  <div className="mt-1">Causation: <span className="font-mono text-[#475569]">corr-{ev.id}-f7a2b3c4</span></div>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

// ─── Workspace Tab ────────────────────────────────────────────────────────────

// Per-repository fixture data — paths and content are repo-specific
const REPO_FIXTURES = {
  'core-api': {
    revision: 'a3f9e8b',
    quarantined: false,
    lease: 'active',
    baseRevision: '3c1f2d0',
    diffPath: 'src/api/middleware/tracing.ts',
    diffContent: [
      '--- a/src/api/middleware/tracing.ts',
      '+++ b/src/api/middleware/tracing.ts',
      '@@ -0,0 +1,18 @@',
      "+import { NodeSDK } from '@opentelemetry/sdk-node';",
      "+import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';",
      "+import { Resource } from '@opentelemetry/resources';",
      '+',
      '+const exporter = new OTLPTraceExporter({',
      "+  url: process.env.OTEL_EXPORTER_OTLP_ENDPOINT,",
      '+});',
      '+',
      '+export const sdk = new NodeSDK({',
      "+  resource: new Resource({ 'service.name': 'core-api' }),",
      '+  traceExporter: exporter,',
      '+});',
    ],
    logLines: [
      '09:48:07  [INFO]  Lease acquired: repo-a1b2 / core-api @ 3c1f2d0',
      '09:48:09  [INFO]  AGENT_01 start — Plan Changes',
      '09:49:19  [INFO]  AGENT_01 complete in 70 s',
      '09:49:21  [INFO]  AGENT_02a start — Impl: core-api',
      '09:52:05  [INFO]  AGENT_02a complete; 2 files modified',
      '09:53:44  [INFO]  Tests passed: 47/47',
    ],
  },
  'worker-service': {
    revision: 'b7c2f1d',
    quarantined: true,
    lease: 'interrupted',
    baseRevision: '9e2a1b4',
    diffPath: 'src/worker/telemetry.ts',
    diffContent: [
      '--- a/src/worker/telemetry.ts',
      '+++ b/src/worker/telemetry.ts',
      '@@ -1,8 +1,22 @@',
      " import { Job } from 'bullmq';",
      '+import { context, propagation, trace } from \'@opentelemetry/api\';',
      '+',
      ' export async function processJob(job: Job) {',
      "-  return runJobLogic(job);",
      '+  // Extract trace context from job headers',
      '+  const carrier = job.data._traceContext ?? {};',
      '+  const ctx = propagation.extract(context.active(), carrier);',
      '+  const tracer = trace.getTracer(\'worker-service\');',
      '+  return context.with(ctx, () =>',
      "+    tracer.startActiveSpan('processJob', span => {",
      '+      span.setAttribute(\'job.id\', job.id);',
      '+      return runJobLogic(job).finally(() => span.end());',
      '+    })',
      '+  );',
      ' }',
    ],
    logLines: [
      '09:48:08  [INFO]  Lease acquired: repo-c3d4 / worker-service @ 9e2a1b4',
      '09:49:21  [INFO]  AGENT_02b start — Impl: worker-service',
      '09:51:58  [INFO]  AGENT_02b complete; 1 file modified',
      '09:53:12  [WARN]  Quarantine triggered: unexpected file modification outside declared scope path',
      '09:53:14  [WARN]  Lease interrupted; workspace writes suspended',
    ],
  },
} as const;

type RepoKey = keyof typeof REPO_FIXTURES;

function WorkspaceTab({ isOffline }: { isOffline?: boolean }) {
  const [activeRepo, setActiveRepo] = useState<RepoKey>('core-api');
  const [viewMode, setViewMode] = useState<'source' | 'diff' | 'log'>('diff');
  const [showReleaseSet, setShowReleaseSet] = useState(false);
  const [rsState, setRsState] = useState<'DRAFT' | 'SEALED' | 'ABANDONED'>('DRAFT');
  const [sealDialog, setSealDialog] = useState(false);
  const [abandonDialog, setAbandonDialog] = useState(false);
  const [commitDialog, setCommitDialog] = useState(false);
  const [releaseDialog, setReleaseDialog] = useState(false);
  const [operation, setOperation] = useState<null | { state: 'Requested' | 'Completed'; ref: string; message: string }>(null);

  const requestWorkspaceAction = (
    ref: string,
    requestedMessage: string,
    completedMessage: string,
    onCompleted?: () => void,
  ) => {
    setOperation({ state: 'Requested', ref, message: requestedMessage });
    window.setTimeout(() => {
      onCompleted?.();
      setOperation({ state: 'Completed', ref, message: completedMessage });
    }, 900);
  };

  const repos = (['core-api', 'worker-service'] as const);
  const fix = REPO_FIXTURES[activeRepo];

  const diffLines = fix.diffContent.map((line, i) => ({
    line,
    color: line.startsWith('+') && !line.startsWith('+++')
      ? 'bg-[#DCFCE7] text-[#166534]'
      : line.startsWith('-') && !line.startsWith('---')
      ? 'bg-[#FEE2E2] text-[#991B1B]'
      : line.startsWith('@@')
      ? 'bg-[#EEF2FF] text-[#3659E3]'
      : 'text-[#172033]',
  }));

  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      {/* Repo tabs */}
      <div className="flex border-b border-[#CDD5DF] bg-white px-4 gap-1 flex-shrink-0 items-center">
        {repos.map(r => {
          const f = REPO_FIXTURES[r];
          const isActive = activeRepo === r;
          return (
            <button key={r} onClick={() => { setActiveRepo(r); setViewMode('diff'); }}
              aria-selected={isActive}
              aria-label={`${r} revision ${f.revision}${f.quarantined ? ', quarantined' : ''}`}
              className={`flex items-center gap-2 px-3 py-2.5 text-[13px] border-b-2 transition-colors ${isActive ? 'border-[#3659E3] text-[#3659E3] font-semibold' : 'border-transparent text-[#475569] hover:text-[#172033]'}`}>
              {r}
              <span className={`font-mono text-[12px] ${isActive ? 'text-[#3659E3]' : 'text-[#475569]'}`}>{f.revision}</span>
              {f.quarantined && (
                <span className="w-2 h-2 rounded-full bg-[#FCD34D] flex-shrink-0" title="Quarantined — lease interrupted" aria-label="Quarantined" />
              )}
            </button>
          );
        })}
        <div className="flex-1" />
        <Button size="compact" intent={showReleaseSet ? 'primary' : 'quiet'} onClick={() => setShowReleaseSet(!showReleaseSet)}>
          {showReleaseSet ? 'Hide ReleaseSet' : 'ReleaseSet'}
        </Button>
      </div>

      {/* Quarantine warning */}
      {fix.quarantined && (
        <div role="alert" className="px-4 py-2 bg-[#FEF3C7] border-b border-[#FCD34D] flex items-center gap-2 flex-shrink-0">
          <AlertTriangle size={13} className="text-[#92400E] flex-shrink-0" aria-hidden />
          <span className="text-[12px] text-[#92400E] font-medium">{activeRepo} quarantined</span>
          <span className="text-[12px] text-[#92400E] opacity-75">— lease interrupted at 09:53:12; workspace writes suspended.</span>
          <div className="flex-1" />
          <Button size="compact" intent="quiet" className="text-[#92400E] border-[#FCD34D]" disabled={isOffline}
            onClick={() => requestWorkspaceAction('op-reconcile-73a1', 'Reconcile requested for worker-service.', 'Reconcile request accepted; awaiting a fresh workspace projection.')}>Request Reconcile</Button>
        </div>
      )}

      {/* WorkspaceSet status */}
      <div className="px-4 py-2 bg-[#F8FAFC] border-b border-[#ECEFF4] flex items-center gap-4 text-[12px] flex-shrink-0">
        <span className="text-[#475569]">WorkspaceSet <span className="font-mono text-[#172033]">ws-a1b2c3d4</span></span>
        <Badge label="RUNNING" intent="runtime" />
        <span className="text-[#475569]">generation <span className="font-mono text-[#172033]">3</span></span>
        <span className="text-[#475569]">base <span className="font-mono text-[#172033]">{fix.baseRevision}</span></span>
        <span className="text-[#475569]">current <span className="font-mono text-[#172033]">{fix.revision}</span></span>
        <span className={`${fix.quarantined ? 'text-[#92400E]' : 'text-[#475569]'}`}>
          lease: <span className="font-mono text-[#172033]">{fix.lease}</span>
        </span>
      </div>

      <div className="flex flex-1 overflow-hidden">
        {/* Viewer area */}
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
            <span className="text-[12px] text-[#475569] self-center pr-2">Read-only · {fix.diffPath}</span>
          </div>

          {/* Light-theme viewer */}
          <div className="flex-1 overflow-auto bg-white border-r border-[#ECEFF4]">
            {viewMode === 'diff' && (
              <div className="font-mono text-[12px] leading-6 p-4 select-text">
                <div className="text-[12px] text-[#475569] mb-3 pb-2 border-b border-[#ECEFF4]">
                  {fix.diffPath} · {activeRepo}@{fix.revision} ← {fix.baseRevision}
                </div>
                {diffLines.map(({ line, color }, i) => (
                  <div key={i} className={`px-2 -mx-2 ${color}`}>{line || ' '}</div>
                ))}
              </div>
            )}
            {viewMode === 'source' && (
              <div role="region" aria-label={`${activeRepo} source ${fix.diffPath} at ${fix.revision}`} className="p-4 text-[12px] text-[#172033]">
                <p className="mb-3 text-[#475569]">{fix.diffPath} · {activeRepo}@{fix.revision} · read-only fixture excerpt</p>
                <pre className="font-mono whitespace-pre-wrap break-words">{fix.diffContent.filter(line => !line.startsWith('---') && !line.startsWith('+++') && !line.startsWith('@@') && !line.startsWith('-')).map(line => line.slice(1)).join('\n')}</pre>
              </div>
            )}
            {viewMode === 'log' && (
              <div className="font-mono text-[12px] leading-6 p-4 space-y-0.5">
                {fix.logLines.map((line, i) => {
                  const isWarn = line.includes('[WARN]');
                  return (
                    <div key={i} className={isWarn ? 'text-[#92400E] bg-[#FEF3C7] -mx-4 px-4' : 'text-[#172033]'}>
                      {line}
                    </div>
                  );
                })}
                <div className="text-[#475569] italic mt-2">— log truncated —</div>
              </div>
            )}
          </div>
        </div>

        {/* ReleaseSet panel */}
        {showReleaseSet && (
          <div className="w-80 flex-shrink-0 border-l border-[#CDD5DF] bg-[#F3F5F8] overflow-y-auto">
            <div className="px-4 py-3 border-b border-[#CDD5DF] flex items-center justify-between">
              <span className="text-[13px] font-semibold text-[#172033]">ReleaseSet</span>
              <StatusBadge state={rsState} entity="workitem" />
            </div>
            <div className="p-4 space-y-4">
              <div className="space-y-1.5 text-[12px]">
                <div className="flex gap-2"><span className="text-[#475569] w-28">Manifest rev</span><span className="font-mono">rset-f7a2b3c4</span></div>
                <div className="flex gap-2"><span className="text-[#475569] w-28">Content hash</span><span className="font-mono truncate">sha256:9e8d…c012</span></div>
                <div className="flex gap-2"><span className="text-[#475569] w-28">Created</span><span>09:52:10</span></div>
              </div>
              {/* Per-repo verdicts */}
              <div>
                <h3 className="text-[12px] font-semibold text-[#475569] uppercase tracking-wider mb-2">Per-repository</h3>
                <div className="space-y-2">
                  {[
                    { repo: 'core-api',       base: '3c1f2d0', result: 'a3f9e8b', verdict: 'PASS' as const  },
                    { repo: 'worker-service', base: '9e2a1b4', result: 'b7c2f1d', verdict: 'FAIL' as const  },
                  ].map(row => (
                    <div key={row.repo} className="bg-white rounded-[6px] border border-[#CDD5DF] p-3 text-[12px] space-y-1">
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-[#172033]">{row.repo}</span>
                        <VerdictBadge verdict={row.verdict} />
                      </div>
                      <div className="font-mono text-[12px] text-[#475569]">
                        {row.base} → {row.result}
                      </div>
                      {row.verdict === 'FAIL' && (
                        <div className="text-[12px] text-[#991B1B]">Lease interrupted; workspace quarantined.</div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
              {/* ReleaseSet actions */}
              <div className="space-y-2 pt-2">
                {rsState === 'DRAFT' && (
                  <>
                    <Button intent="primary" size="compact" className="w-full justify-center" disabled={true}>
                      Seal ReleaseSet <span className="opacity-70 ml-1 text-[12px]">(requires all PASS)</span>
                    </Button>
                    <Button intent="secondary" size="compact" className="w-full justify-center" disabled={isOffline}
                      onClick={() => setSealDialog(true)}>
                      Seal Anyway (partial)
                    </Button>
                    <Button intent="destructive" size="compact" className="w-full justify-center" disabled={isOffline}
                      onClick={() => setAbandonDialog(true)}>
                      Abandon
                    </Button>
                  </>
                )}
                {rsState === 'SEALED' && (
                  <>
                    <Button intent="primary" size="compact" className="w-full justify-center" disabled={isOffline} onClick={() => setCommitDialog(true)}>Local Commit</Button>
                    <Button intent="secondary" size="compact" className="w-full justify-center" disabled={isOffline} onClick={() => setReleaseDialog(true)}>Release Workspace</Button>
                  </>
                )}
                {rsState === 'ABANDONED' && (
                  <div className="text-[12px] text-[#475569] italic">ReleaseSet abandoned. No further actions.</div>
                )}
              </div>
              {operation && <OperationNotice state={operation.state} message={operation.message} ref={operation.ref} />}
            </div>
          </div>
        )}
      </div>

      {sealDialog && (
        <Dialog
          title="Seal ReleaseSet (partial)"
          description="worker-service verdict is FAIL. Sealing with a partial result; Local Commit will include only PASS repositories."
          onClose={() => setSealDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setSealDialog(false)}>Cancel</Button>
              <Button intent="primary" disabled={isOffline} onClick={() => {
                setSealDialog(false);
                requestWorkspaceAction('op-seal-f71b', 'Partial ReleaseSet seal requested.', 'ReleaseSet sealed with the PASS repository only.', () => setRsState('SEALED'));
              }}>Seal Partial ReleaseSet</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">ReleaseSet</dt><dd className="font-mono">rset-f7a2b3c4</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">PASS repos</dt><dd>core-api</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">FAIL repos</dt><dd className="text-[#991B1B]">worker-service (quarantined)</dd></div>
          </dl>
        </Dialog>
      )}
      {abandonDialog && (
        <Dialog
          title="Abandon ReleaseSet"
          description="Abandoning this ReleaseSet is irreversible. A new ReleaseSet must be created for any subsequent release attempt."
          onClose={() => setAbandonDialog(false)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setAbandonDialog(false)}>Cancel</Button>
              <Button intent="destructive" disabled={isOffline} onClick={() => {
                setAbandonDialog(false);
                requestWorkspaceAction('op-abandon-8c20', 'ReleaseSet abandonment requested.', 'ReleaseSet abandonment confirmed.', () => setRsState('ABANDONED'));
              }}>Abandon ReleaseSet</Button>
            </>
          }
        >
          <dl className="text-[13px] space-y-1.5">
            <div className="flex gap-3"><dt className="text-[#475569] w-28">ReleaseSet</dt><dd className="font-mono">rset-f7a2b3c4</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-28">Current state</dt><dd><Badge label="DRAFT" intent="neutral" /></dd></div>
          </dl>
        </Dialog>
      )}
      {commitDialog && (
        <Dialog
          title="Create Local Commit"
          description="Create a local commit from the sealed PASS repository only. No remote Git operation will run."
          onClose={() => setCommitDialog(false)}
          actions={<>
            <Button intent="secondary" onClick={() => setCommitDialog(false)}>Cancel</Button>
            <Button intent="primary" disabled={isOffline} onClick={() => {
              setCommitDialog(false);
              requestWorkspaceAction('op-commit-64b2', 'Local commit requested for core-api.', 'Local commit created. No remote push was performed.');
            }}>Create Local Commit</Button>
          </>}
        >
          <p className="text-[13px] text-[#475569]">Target: <span className="font-mono text-[#172033]">core-api@a3f9e8b</span></p>
        </Dialog>
      )}
      {releaseDialog && (
        <Dialog
          title="Release Workspace Lease"
          description="Release the active workspace lease after local artifacts have been retained. This does not push or merge code."
          onClose={() => setReleaseDialog(false)}
          actions={<>
            <Button intent="secondary" onClick={() => setReleaseDialog(false)}>Keep Lease</Button>
            <Button intent="destructive" disabled={isOffline} onClick={() => {
              setReleaseDialog(false);
              requestWorkspaceAction('op-release-a921', 'Workspace lease release requested.', 'Workspace lease released; retained artifacts remain readable.');
            }}>Release Workspace</Button>
          </>}
        >
          <p className="text-[13px] text-[#475569]">WorkspaceSet: <span className="font-mono text-[#172033]">ws-a1b2c3d4</span></p>
        </Dialog>
      )}
    </div>
  );
}

// ─── Evidence Tab ─────────────────────────────────────────────────────────────

const CRITERIA = [
  { id: 'ac-1', text: 'All HTTP routes emit spans with correct service.name and trace propagation headers.', phase: 'VERIFY', verdict: 'PASS' as const,   evaluator: 'runtime/test-runner',  revision: 'a3f9e8b' },
  { id: 'ac-2', text: 'Worker job processing emits child spans linked to originating HTTP span.',             phase: 'VERIFY', verdict: 'FAIL' as const,   evaluator: 'runtime/test-runner',  revision: 'b7c2f1d' },
  { id: 'ac-3', text: 'Unit tests cover trace context propagation.',                                          phase: 'VERIFY', verdict: 'PASS' as const,   evaluator: 'runtime/test-runner',  revision: 'a3f9e8b' },
  { id: 'ac-4', text: 'No secret values or PII appear in span attributes.',                                   phase: 'VERIFY', verdict: 'ERROR' as const,  evaluator: 'runtime/security-gate',revision: 'a3f9e8b' },
];

function EvidenceTab() {
  const [expanded, setExpanded] = useState<string | null>(null);
  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-4">
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Acceptance Criteria Verdicts</h2>
            <p className="text-[12px] text-[#475569] mt-0.5">RevisionSet <span className="font-mono">rset-f7a2b3c4</span> · core-api@a3f9e8b, worker-service@b7c2f1d</p>
          </div>
          <div className="divide-y divide-[#ECEFF4]" role="list">
            {CRITERIA.map(c => (
              <div key={c.id} role="listitem">
                <button
                  onClick={() => setExpanded(expanded === c.id ? null : c.id)}
                  className="w-full flex items-start gap-4 px-5 py-4 text-left hover:bg-[#FAFBFC] transition-colors"
                  aria-expanded={expanded === c.id}
                >
                  <VerdictBadge verdict={c.verdict} />
                  <div className="flex-1 min-w-0">
                    <p className="text-[13px] text-[#172033]">{c.text}</p>
                    <div className="flex items-center gap-3 mt-1 flex-wrap text-[12px] text-[#475569]">
                      <span>phase: <span className="font-mono">{c.phase}</span></span>
                      <span>evaluator: <span className="font-mono">{c.evaluator}</span></span>
                      <span>revision: <span className="font-mono">{c.revision}</span></span>
                    </div>
                  </div>
                  {expanded === c.id
                    ? <ChevronDown size={14} className="text-[#475569] flex-shrink-0" aria-hidden />
                    : <ChevronRight size={14} className="text-[#475569] flex-shrink-0" aria-hidden />}
                </button>
                {expanded === c.id && (
                  <div className="px-5 pb-4 bg-[#FAFBFC] border-t border-[#ECEFF4]">
                    {c.verdict === 'ERROR' && (
                      <div className="mt-3 p-3 rounded-[6px] bg-[#FEE2E2] border border-[#FCA5A5] text-[12px] text-[#991B1B]">
                        <div className="font-medium">SECURITY_GATE_TIMEOUT</div>
                        <div className="mt-0.5">Security evaluator timed out after 30 s. Evidence is incomplete — this criterion is ERROR, not PASS. Missing evidence is never PASS.</div>
                        <div className="font-mono mt-1 opacity-60">correlation: corr-ac4-x9y2z3w4</div>
                      </div>
                    )}
                    {c.verdict === 'FAIL' && (
                      <div className="mt-3 p-3 rounded-[6px] bg-[#FEE2E2] border border-[#FCA5A5] text-[12px] text-[#991B1B]">
                        <div className="font-mono">worker.tracing.spec.ts:42</div>
                        <div>expected span with parentId but got undefined.</div>
                        <Button size="compact" intent="quiet" className="mt-2 text-[#991B1B] border-[#FCA5A5]">Download Artifact</Button>
                      </div>
                    )}
                    {c.verdict === 'PASS' && (
                      <div className="mt-3 text-[12px] text-[#475569]">
                        Evidence: <span className="font-mono text-[#172033]">artifact-{c.id}-pass.json</span> · 4.2 KB · <span className="inline-flex items-center gap-1 text-[#166534]"><CheckCircle2 size={12} aria-hidden /> verified, not tampered</span>
                      </div>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Artifacts</h2>
          </div>
          <div className="divide-y divide-[#ECEFF4]" role="list">
            {[
              { name: 'test-results.json',   size: '12.4 KB', hash: 'sha256:7b2c…', state: 'ok',       hold: false },
              { name: 'security-scan.json',  size: '2.1 KB',  hash: 'sha256:a3f9…', state: 'tampered', hold: true  },
              { name: 'trace-sample.json',   size: '84.6 KB', hash: 'sha256:c8d1…', state: 'truncated',hold: false },
            ].map(artifact => (
              <div key={artifact.name} className="flex items-center gap-4 px-5 py-3" role="listitem">
                <span className="font-mono text-[12px] text-[#172033]">{artifact.name}</span>
                <span className="text-[12px] text-[#475569]">{artifact.size}</span>
                <span className="font-mono text-[12px] text-[#475569]">{artifact.hash}</span>
                {artifact.state === 'tampered'  && <Badge label="TAMPERED"  intent="danger"  />}
                {artifact.state === 'truncated' && <Badge label="TRUNCATED" intent="warning" />}
                {artifact.hold && <Badge label="HOLD" intent="info" />}
                <div className="flex-1" />
                {artifact.state !== 'tampered'
                  ? <Button size="compact" intent="quiet">Preview</Button>
                  : <span className="text-[12px] text-[#991B1B]">Preview disabled (tampered)</span>}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── Chat Tab ─────────────────────────────────────────────────────────────────

const MESSAGES = [
  { id: 'm1', author: 'local-operator', time: '09:48:01', body: 'Start the tracing implementation. Scope is core-api and worker-service only. No remote Git operations.', attachment: null, attempt: null },
  { id: 'm2', author: 'agent / anthropic', time: '09:48:09', body: 'Understood. I will implement OpenTelemetry instrumentation in both services. Starting with core-api middleware setup.', attachment: null, attempt: 'attempt-01' },
  { id: 'm3', author: 'agent / anthropic', time: '09:53:46', body: 'Implementation complete in core-api (47/47 tests pass). Worker-service span linkage test has a failure — parent span context is not propagated across the queue boundary.', attachment: 'test-results.json', attempt: 'attempt-02' },
  { id: 'm4', author: 'local-operator', time: '09:55:12', body: 'Check the worker enqueue path — context may not be extracted from message headers.', attachment: null, attempt: null },
];

function ChatTab({ isOffline }: { isOffline?: boolean }) {
  const [draft, setDraft] = useState('');
  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Message list + composer */}
      <div className="flex-1 flex flex-col overflow-hidden">
        <div className="flex-1 overflow-y-auto p-5 space-y-4" role="log" aria-label="Conversation messages" aria-live="polite">
          {MESSAGES.map(msg => {
            const isAgent = msg.author.startsWith('agent');
            return (
              <article key={msg.id} className={`flex gap-3 ${isAgent ? '' : 'flex-row-reverse'}`} aria-label={`Message from ${msg.author} at ${msg.time}`}>
                <div className={`w-7 h-7 rounded-full flex items-center justify-center flex-shrink-0 border ${isAgent ? 'bg-[#EDE9FE] text-[#5B21B6] border-[#C4B5FD]' : 'bg-[#EEF2FF] text-[#3659E3] border-[#C7D2FE]'}`} aria-hidden>
                  {isAgent ? <Cpu size={14} /> : <User size={14} />}
                </div>
                <div className={`max-w-lg ${isAgent ? '' : 'items-end flex flex-col'}`}>
                  <div className={`flex items-center gap-2 mb-1 ${isAgent ? '' : 'flex-row-reverse'}`}>
                    <span className="text-[12px] font-medium text-[#172033]">{msg.author}</span>
                    <span className="text-[12px] text-[#475569]">{msg.time}</span>
                    {msg.attempt && <span className="font-mono text-[12px] px-1.5 py-0.5 bg-[#EDE9FE] text-[#5B21B6] rounded-[4px]">{msg.attempt}</span>}
                  </div>
                  <div className={`rounded-[8px] px-3.5 py-2.5 text-[13px] text-[#172033] ${isAgent ? 'bg-[#F3F5F8] border border-[#CDD5DF]' : 'bg-[#EEF2FF] border border-[#C7D2FE]'}`}>
                    {msg.body}
                  </div>
                  {msg.attachment && (
                    <div className="flex items-center gap-2 mt-1.5 px-2 py-1 bg-white border border-[#CDD5DF] rounded-[6px] text-[12px] text-[#475569]">
                      <FileText size={12} aria-hidden />
                      <span className="font-mono">{msg.attachment}</span>
                    </div>
                  )}
                </div>
              </article>
            );
          })}
        </div>

        <div className="border-t border-[#CDD5DF] p-4 bg-white flex-shrink-0">
          <div className="flex gap-2">
            <textarea
              value={draft}
              onChange={e => setDraft(e.target.value)}
              placeholder="Add a platform message…"
              aria-label="Message composer"
              className="flex-1 h-20 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none resize-none"
            />
            <div className="flex flex-col gap-2">
              <Button intent="primary" size="compact" disabled={isOffline || !draft.trim()} onClick={() => setDraft('')}>Send</Button>
              <Button intent="secondary" size="compact" disabled={isOffline} icon={<FileText size={13} aria-hidden />}>Attach</Button>
            </div>
          </div>
        </div>
      </div>

      {/* Typed control rail — clearly NOT the message composer */}
      <div className="w-64 border-l border-[#CDD5DF] bg-[#F3F5F8] flex flex-col flex-shrink-0" role="complementary" aria-label="Typed action controls">
        <div className="px-4 py-3 border-b border-[#CDD5DF]">
          <p className="text-[12px] font-semibold text-[#475569]">Task Action Panel</p>
          <p className="text-[12px] text-[#475569] mt-0.5">These controls are separate from the message composer. See task header for Approve / Cancel.</p>
        </div>
        <div className="p-4 space-y-4">
          <div className="p-3 bg-[#EEF2FF] border border-[#C7D2FE] rounded-[8px] text-[12px] text-[#1E40AF]">
            <p className="font-medium">Partial recovery approval waiting at APPROVAL_01</p>
            <p className="mt-1 opacity-75">Use <strong>Approve Node</strong> in the task header above to submit approval.</p>
          </div>
          <div className="space-y-1.5">
            <p className="text-[12px] font-medium text-[#172033]">Scope Decision</p>
            <Button intent="secondary" size="compact" className="w-full justify-center" disabled={isOffline}>Request Scope Expansion</Button>
          </div>
          <div className="h-px bg-[#CDD5DF]" />
          <div className="space-y-1.5">
            <p className="text-[12px] font-medium text-[#172033]">Wait Signal</p>
            <Button intent="secondary" size="compact" className="w-full justify-center" disabled={isOffline}>Send Wait Signal</Button>
          </div>
          <div className="h-px bg-[#CDD5DF]" />
          <div className="space-y-1.5">
            <p className="text-[12px] font-medium text-[#172033]">Context Snapshot</p>
            <div className="p-2 rounded-[6px] bg-white border border-[#CDD5DF] text-[12px] text-[#475569]">
              <span className="font-mono">ctx-f7a2</span> · 4 messages, 3 artifacts
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── Root export ──────────────────────────────────────────────────────────────

interface TaskDetailProps {
  activeTab: NavRoute;
  onTabChange: (r: NavRoute) => void;
  isOffline?: boolean;
}

export function TaskDetailScreen({ activeTab, onTabChange, isOffline }: TaskDetailProps) {
  return (
    <div className="flex-1 flex flex-col overflow-hidden">
      <TaskHeader activeTab={activeTab} onTabChange={onTabChange} isOffline={isOffline} />
      <div id="workitem-tab-panel" role="tabpanel" aria-labelledby={`tab-${activeTab}`} className="flex-1 flex flex-col min-h-0 overflow-hidden">
      {activeTab === 'task-overview'   && <OverviewTab />}
      {activeTab === 'task-graph'      && <GraphTimelineTab />}
      {activeTab === 'task-workspace'  && <WorkspaceTab isOffline={isOffline} />}
      {activeTab === 'task-evidence'   && <EvidenceTab />}
      {activeTab === 'task-chat'       && <ChatTab isOffline={isOffline} />}
      </div>
    </div>
  );
}
