import React, { useState } from 'react';
import { Badge, Button, CopyableId, OperationNotice } from '../components/ui';
import { CreateWorkItemDrawer } from './CreateWorkItem';
import type { ProjectSummary } from './Projects';
import { AlertTriangle, CheckCircle2, Cpu, MoreHorizontal } from '../components/icons';

type KanbanState = 'BACKLOG' | 'READY' | 'ACTIVE' | 'BLOCKED' | 'DONE' | 'CANCELLED';

export interface WorkItemCard {
  id: string;
  title: string;
  state: KanbanState;
  repos: string[];
  component: string;
  blockerCount: number;
  agentClaim?: string;
  verifiedDone: boolean;
  freshness: string;
  validActions: { label: string; targetState?: KanbanState }[];
}

export const INITIAL_CARDS: WorkItemCard[] = [
  {
    id: 'wi-0012', title: 'Migrate auth tokens to JWT RS256', state: 'BACKLOG',
    repos: ['core-api'], component: 'src/api', blockerCount: 0,
    verifiedDone: false, freshness: '4m',
    validActions: [{ label: 'Mark Ready', targetState: 'READY' }],
  },
  {
    id: 'wi-0015', title: 'Refactor worker queue to use Redis streams', state: 'READY',
    repos: ['worker-service'], component: 'src/worker', blockerCount: 0,
    verifiedDone: false, freshness: '1m',
    validActions: [{ label: 'Start Run', targetState: 'ACTIVE' }],
  },
  {
    id: 'wi-0018', title: 'Add distributed tracing to API gateway', state: 'ACTIVE',
    repos: ['core-api', 'worker-service'], component: 'src/api', blockerCount: 0,
    agentClaim: 'Agent: task complete (unverified)', verifiedDone: false, freshness: '30s',
    validActions: [{ label: 'Cancel Run' }],
  },
  {
    id: 'wi-0009', title: 'Fix race condition in session manager', state: 'BLOCKED',
    repos: ['core-api'], component: 'src/api', blockerCount: 2,
    verifiedDone: false, freshness: '12m',
    validActions: [{ label: 'Resolve WorkItem Blocker' }, { label: 'Cancel WorkItem' }],
  },
  {
    id: 'wi-0006', title: 'Add pagination to list endpoints', state: 'DONE',
    repos: ['core-api'], component: 'src/api', blockerCount: 0,
    verifiedDone: true, freshness: '2h',
    validActions: [],
  },
  {
    id: 'wi-0003', title: 'Update dependency: express 4→5', state: 'CANCELLED',
    repos: ['core-api'], component: 'src/api', blockerCount: 0,
    verifiedDone: false, freshness: '1d',
    validActions: [],
  },
];

const COLUMN_ORDER: KanbanState[] = ['BACKLOG', 'READY', 'ACTIVE', 'BLOCKED', 'DONE', 'CANCELLED'];

const STATE_INTENT = {
  BACKLOG: 'neutral', READY: 'info', ACTIVE: 'runtime', BLOCKED: 'warning', DONE: 'success', CANCELLED: 'neutral',
} as const;

interface Props {
  project: ProjectSummary;
  cards: WorkItemCard[];
  setCards: React.Dispatch<React.SetStateAction<WorkItemCard[]>>;
  onOpenTask: () => void;
  isOffline?: boolean;
}

export function KanbanScreen({ project, cards, setCards, onOpenTask, isOffline = false }: Props) {
  const [dragItem, setDragItem] = useState<string | null>(null);
  const [dragReject, setDragReject] = useState<string | null>(null);
  const [filterRepo, setFilterRepo] = useState<string>('all');
  const [filterState, setFilterState] = useState<string>('all');
  const [actionMenu, setActionMenu] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [pendingOps, setPendingOps] = useState<Record<string, 'Requested' | 'Completed'>>({});

  const allRepos = project.repositories.map(repository => repository.name);

  const filtered = cards.filter(c => {
    if (filterRepo !== 'all' && !c.repos.includes(filterRepo)) return false;
    if (filterState !== 'all' && c.state !== filterState) return false;
    return true;
  });

  const handleAction = (cardId: string, action: { label: string; targetState?: KanbanState }) => {
    if (isOffline) return;
    setActionMenu(null);
    setPendingOps(prev => ({ ...prev, [cardId]: 'Requested' }));
    window.setTimeout(() => {
      if (action.targetState) setCards(prev => prev.map(c => {
        if (c.id !== cardId) return c;
        const nextActions = action.targetState === 'READY'
          ? [{ label: 'Start Run', targetState: 'ACTIVE' as KanbanState }]
          : action.targetState === 'ACTIVE'
            ? [{ label: 'Cancel Run' }]
            : c.validActions;
        return { ...c, state: action.targetState!, validActions: nextActions };
      }));
      setPendingOps(prev => ({ ...prev, [cardId]: 'Completed' }));
      window.setTimeout(() => setPendingOps(prev => { const next = { ...prev }; delete next[cardId]; return next; }), 900);
    }, 900);
  };

  return (
    <div className="flex flex-col flex-1 overflow-hidden">
      {/* Toolbar */}
      <div className="px-6 py-3 border-b border-[#CDD5DF] bg-white flex items-center gap-3 flex-wrap flex-shrink-0">
        <h1 className="text-sm font-semibold text-[#172033] mr-2">Board</h1>
        <span className="text-xs text-[#5D697A] mr-2">{project.name}</span>
        <div className="flex-1" />
        {/* Filters */}
        <div className="flex items-center gap-2">
          <span className="text-xs text-[#5D697A]">Repository</span>
          <select aria-label="Filter by repository" value={filterRepo} onChange={e => setFilterRepo(e.target.value)}
            className="h-7 px-2 rounded-[6px] border border-[#CDD5DF] bg-white text-xs focus:border-[#3659E3] outline-none">
            <option value="all">All</option>
            {allRepos.map(r => <option key={r} value={r}>{r}</option>)}
          </select>
        </div>
        <Button intent="primary" size="compact" disabled={isOffline} title={isOffline ? 'Reconnect to create a WorkItem' : undefined} onClick={() => setCreateOpen(true)}>Create WorkItem</Button>
      </div>

      {/* Columns */}
      <div className="flex-1 overflow-x-auto p-4" tabIndex={0} role="region" aria-label="Board columns, scroll horizontally for more states">
        <div className="flex gap-3 h-full min-w-max">
          {COLUMN_ORDER.map(col => {
            const colCards = filtered.filter(c => c.state === col);
            return (
              <div
                key={col}
                className="flex flex-col w-64 rounded-[12px] bg-[#F3F5F8] border border-[#CDD5DF] island-shadow overflow-hidden"
                onDragOver={e => e.preventDefault()}
                onDrop={() => {
                  if (!dragItem || isOffline) return;
                  const card = cards.find(c => c.id === dragItem);
                  if (!card) return;
                  const validAction = card.validActions.find(a => a.targetState === col);
                  if (validAction) {
                    handleAction(dragItem, validAction);
                  } else {
                    setDragReject(dragItem);
                    setTimeout(() => setDragReject(null), 1500);
                  }
                  setDragItem(null);
                }}
              >
                {/* Column header */}
                <div className="px-3 py-2.5 border-b border-[#CDD5DF] flex items-center gap-2">
                  <Badge label={col} intent={STATE_INTENT[col] as any} />
                  <span className="text-xs text-[#5D697A] font-medium">{colCards.length}</span>
                </div>

                {/* Cards */}
                <div className="flex-1 overflow-y-auto p-2 space-y-2">
                  {colCards.length === 0 ? (
                    <div className="text-center py-8 text-xs text-[#5D697A]">No items</div>
                  ) : colCards.map(card => (
                    <div
                      key={card.id}
                      draggable={!isOffline}
                      onDragStart={() => setDragItem(card.id)}
                      onDragEnd={() => setDragItem(null)}
                      className={`bg-white rounded-[8px] border p-3 cursor-pointer hover:border-[#AAB4C3] transition-all select-none ${
                        dragReject === card.id ? 'border-[#FCA5A5] bg-[#FEE2E2] shake' : 'border-[#CDD5DF]'
                      } ${dragItem === card.id ? 'opacity-50' : ''}`}
                    >
                      {/* Card header */}
                      <div className="flex items-start justify-between gap-1">
                        <button
                          onClick={card.id === 'wi-0018' ? onOpenTask : undefined}
                          disabled={card.id !== 'wi-0018'}
                          title={card.id !== 'wi-0018' ? 'Detail fixture is available for wi-0018 only' : undefined}
                          className="text-xs font-medium text-[#172033] text-left hover:text-[#3659E3] leading-snug line-clamp-2"
                        >
                          {card.title}
                        </button>
                        {card.validActions.length > 0 && (
                          <div className="relative flex-shrink-0">
                            <button
                              onClick={() => setActionMenu(actionMenu === card.id ? null : card.id)}
                              disabled={isOffline}
                              title={isOffline ? 'Reconnect to perform this action' : `Actions for ${card.title}`}
                              aria-label={`Actions for ${card.title}`}
                              className="w-6 h-6 flex items-center justify-center rounded text-[#475569] hover:bg-[#F3F5F8]"
                            ><MoreHorizontal size={14} aria-hidden /></button>
                            {actionMenu === card.id && (
                              <div className="absolute right-0 top-7 z-10 bg-white border border-[#CDD5DF] rounded-[8px] shadow-lg min-w-36 py-1" onClick={e => e.stopPropagation()}>
                                {card.validActions.map(a => (
                                  <button key={a.label} onClick={() => handleAction(card.id, a)} disabled={isOffline}
                                    className="w-full text-left px-3 py-2 text-xs text-[#172033] hover:bg-[#EEF2FF] hover:text-[#3659E3]"
                                  >{a.label}</button>
                                ))}
                              </div>
                            )}
                          </div>
                        )}
                      </div>

                      {/* Repo badges */}
                      <div className="flex flex-wrap gap-1 mt-2">
                        {card.repos.map(r => (
                          <span key={r} className="text-[12px] px-1.5 py-0.5 rounded-[4px] bg-[#F1F5F9] text-[#475569] border border-[#CBD5E1]">{r}</span>
                        ))}
                      </div>

                      {/* Blockers */}
                      {card.blockerCount > 0 && (
                        <div className="flex items-center gap-1 mt-2">
                          <AlertTriangle size={12} aria-hidden className="text-[#92400E]" />
                          <span className="text-[12px] text-[#92400E]">{card.blockerCount} blocker{card.blockerCount > 1 ? 's' : ''}</span>
                        </div>
                      )}

                      {/* Agent claim vs verified */}
                      {card.agentClaim && (
                        <div className="mt-2 space-y-1">
                          <div className="flex items-center gap-1 text-[12px] text-[#5B21B6] bg-[#EDE9FE] rounded-[4px] px-1.5 py-0.5">
                            <Cpu size={12} aria-hidden /> {card.agentClaim}
                          </div>
                          {!card.verifiedDone && (
                            <div className="text-[12px] text-[#92400E] bg-[#FEF3C7] rounded-[4px] px-1.5 py-0.5">Completion not gate-verified</div>
                          )}
                        </div>
                      )}
                      {card.verifiedDone && (
                        <div className="flex items-center gap-1 mt-2 text-[12px] text-[#166534] bg-[#DCFCE7] rounded-[4px] px-1.5 py-0.5">
                          <CheckCircle2 size={12} aria-hidden /> Gate-verified complete
                        </div>
                      )}

                      <div className="flex items-center justify-between mt-2">
                        <CopyableId value={card.id} />
                        <span className="text-[12px] text-[#475569]">{card.freshness}</span>
                      </div>
                      {pendingOps[card.id] && <div className="mt-2"><OperationNotice state={pendingOps[card.id]} message={pendingOps[card.id] === 'Requested' ? 'Named action accepted; waiting for authoritative update.' : 'Authoritative state update received.'} ref={`op-${card.id}`} /></div>}
                    </div>
                  ))}
                </div>

                {/* Load more */}
                {col === 'BACKLOG' && colCards.length > 0 && (
                  <div className="px-3 py-2 border-t border-[#ECEFF4]">
                    <p className="text-center text-xs text-[#5D697A]">All cached items shown</p>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </div>

      {createOpen && (
        <CreateWorkItemDrawer
          project={project}
          isOffline={isOffline}
          onClose={() => setCreateOpen(false)}
          onCreated={draft => {
            setCards(prev => [...prev, {
              id: `wi-local-${project.id}-${prev.length + 1}`, title: draft.title, state: 'BACKLOG',
              repos: draft.repos, component: draft.component, blockerCount: 0,
              verifiedDone: false, freshness: 'just now',
              validActions: draft.ready ? [{ label: 'Mark Ready', targetState: 'READY' }] : [],
            }]);
          }}
        />
      )}
    </div>
  );
}
