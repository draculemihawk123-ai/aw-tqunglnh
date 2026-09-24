import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Button, TextField } from '../components/ui';
import { Plus, X, AlertTriangle, CheckCircle2 } from '../components/icons';
import type { ProjectSummary } from './Projects';

interface ScopeRow { repo: string; access: 'READ' | 'WRITE'; path: string }

interface Props {
  project: ProjectSummary;
  onClose: () => void;
  onCreated?: (draft: { title: string; repos: string[]; component: string; ready: boolean }) => void;
  isOffline?: boolean;
}

export function CreateWorkItemDrawer({ project, onClose, onCreated, isOffline = false }: Props) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const [mode, setMode] = useState<'root' | 'child'>('root');
  const [title, setTitle] = useState('');
  const [what, setWhat] = useState('');
  const [criteria, setCriteria] = useState(['']);
  const [verificationRecipe, setVerificationRecipe] = useState('');
  const [exclusions, setExclusions] = useState('');
  const [risk, setRisk] = useState('');
  const [workflowVersion, setWorkflowVersion] = useState('feature-workflow@v3.1.0');
  const [scope, setScope] = useState<ScopeRow[]>([
    ...(project.repositories[0] ? [{ repo: project.repositories[0].name, access: 'WRITE' as const, path: 'src/' }] : []),
  ]);
  const [titleError, setTitleError] = useState('');
  const [scopeError, setScopeError] = useState('');
  const [readinessChecked, setReadinessChecked] = useState(false);

  useEffect(() => {
    previousFocusRef.current = document.activeElement as HTMLElement;
    dialogRef.current?.querySelector<HTMLElement>('h2, input, textarea, select, button')?.focus();
    return () => previousFocusRef.current?.focus();
  }, []);

  const handleKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === 'Escape') { onClose(); return; }
    if (event.key !== 'Tab') return;
    const focusable = dialogRef.current?.querySelectorAll<HTMLElement>('h2[tabindex], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])');
    if (!focusable?.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { last.focus(); event.preventDefault(); }
    if (!event.shiftKey && document.activeElement === last) { first.focus(); event.preventDefault(); }
  }, [onClose]);

  const familyScope: ScopeRow[] = [
    { repo: 'core-api', access: 'WRITE', path: 'src/' },
    { repo: 'worker-service', access: 'WRITE', path: 'src/worker/' },
  ];

  const addCriterion = () => setCriteria(prev => [...prev, '']);
  const removeCriterion = (i: number) => setCriteria(prev => prev.filter((_, idx) => idx !== i));

  const addScopeRow = () => { if (project.repositories[0]) setScope(prev => [...prev, { repo: project.repositories[0].name, access: 'READ', path: '' }]); };
  const removeScopeRow = (i: number) => setScope(prev => prev.filter((_, idx) => idx !== i));

  const validateChildScope = (row: ScopeRow): string | null => {
    if (!row.path || row.path.startsWith('/') || row.path.includes('\\') || row.path.split('/').some(part => part === '..' || part === '.') || row.path.includes('//') || row.path.includes(':')) return 'Enter a normalized relative path without traversal.';
    if (mode !== 'child') return null;
    const parent = familyScope.find(f => f.repo === row.repo);
    if (!parent) return `${row.repo} is not in the family scope.`;
    if (row.access === 'WRITE' && parent.access === 'READ') return `Family scope grants only READ for ${row.repo}.`;
    if (row.path && !row.path.startsWith(parent.path)) return `Path must be within ${parent.path} (family scope).`;
    return null;
  };

  const childScopeErrors = scope.map(validateChildScope);
  const hasChildScopeError = childScopeErrors.some(Boolean);
  const readinessItems = [
    { label: 'Acceptance criteria defined', pass: criteria.some(item => item.trim().length > 0), detail: criteria.some(item => item.trim().length > 0) ? 'At least one criterion is present' : 'Add at least one acceptance criterion' },
    { label: 'Verification recipe defined', pass: verificationRecipe.trim().length > 0, detail: verificationRecipe.trim().length > 0 ? 'Runnable verification instructions are present' : 'Add a command or deterministic verification recipe' },
    { label: 'Repositories accessible', pass: scope.length > 0 && scope.every(row => project.repositories.some(repo => repo.name === row.repo && repo.state === 'ACTIVE')), detail: 'Selected repositories must have an ACTIVE probe state' },
    { label: 'Required pack compatible', pass: true, detail: 'backend-pack@2.4.1 is compatible with the selected workflow' },
    { label: 'Scope is valid', pass: scope.length > 0 && !hasChildScopeError, detail: scope.length === 0 ? 'Add at least one repository scope' : hasChildScopeError ? 'Child scope exceeds family scope' : 'No family-scope violations detected' },
  ];
  const isReady = readinessItems.every(item => item.pass);

  const submit = () => {
    if (isOffline) return;
    let ok = true;
    if (!title.trim()) { setTitleError('Title is required.'); ok = false; }
    else setTitleError('');
    if (scope.length === 0) { setScopeError('At least one repository scope is required.'); ok = false; }
    else if (hasChildScopeError) { setScopeError('Fix child scope violations before creating.'); ok = false; }
    else setScopeError('');
    if (ok) {
      onCreated?.({
        title: title.trim(),
        repos: Array.from(new Set(scope.map(row => row.repo))),
        component: scope[0]?.path || 'src/',
        ready: isReady,
      });
      onClose();
    }
  };

  return (
    <div className="fixed inset-0 z-40 flex items-stretch justify-end bg-black/20" onClick={e => { if (e.target === e.currentTarget) onClose(); }}>
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="cwi-title"
        onKeyDown={handleKeyDown}
        className="w-full max-w-2xl bg-white border-l border-[#CDD5DF] shadow-xl flex flex-col h-full overflow-hidden"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-[#CDD5DF] flex-shrink-0">
          <h2 id="cwi-title" tabIndex={-1} className="text-base font-semibold text-[#172033] outline-none">Create WorkItem</h2>
          <button onClick={onClose} aria-label="Close" className="w-7 h-7 flex items-center justify-center rounded-[6px] text-[#475569] hover:bg-[#ECEFF4]">
            <X size={14} aria-hidden />
          </button>
        </div>

        {/* Mode toggle */}
        <div className="px-6 py-3 border-b border-[#ECEFF4] bg-[#F8FAFC] flex-shrink-0">
          <div className="flex items-center gap-2">
            <span className="text-[12px] text-[#475569] font-medium">Mode</span>
            <div className="flex rounded-[6px] border border-[#CDD5DF] overflow-hidden">
              {(['root', 'child'] as const).map(m => (
                <button key={m} onClick={() => setMode(m)} aria-pressed={mode === m} disabled={m === 'child' && project.id !== 'proj-alpha-001'} title={m === 'child' && project.id !== 'proj-alpha-001' ? 'No parent WorkItem family fixture in this project' : undefined}
                  className={`px-3 py-1.5 text-[12px] font-medium capitalize transition-colors ${mode === m ? 'bg-[#3659E3] text-white' : 'bg-white text-[#475569] hover:bg-[#F3F5F8]'}`}>
                  {m === 'root' ? 'Root WorkItem' : 'Child WorkItem'}
                </button>
              ))}
            </div>
            {mode === 'child' && (
              <span className="text-[12px] text-[#475569]">scope must be a subset of family scope</span>
            )}
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-6 py-5 space-y-5">
          {isOffline && (
            <div role="alert" className="rounded-[8px] border border-[#FCA5A5] bg-[#FEE2E2] px-3 py-2 text-[12px] text-[#991B1B]">
              Offline — local draft editing is available, but reconnect to create this WorkItem.
            </div>
          )}
          {/* Title */}
          <TextField
            label="Title"
            value={title}
            onChange={v => { setTitle(v); if (v) setTitleError(''); }}
            placeholder="Short, specific description of what to accomplish"
            error={titleError}
            required
          />

          {/* WHAT */}
          <div className="flex flex-col gap-1">
            <label htmlFor="cwi-what" className="text-[12px] font-medium text-[#172033]">WHAT <span className="text-[#475569] font-normal">(full description)</span></label>
            <textarea id="cwi-what" value={what} onChange={e => setWhat(e.target.value)}
              className="h-24 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none resize-none"
              placeholder="Describe what the agent should produce or achieve." />
          </div>

          {/* Acceptance criteria */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <label className="text-[12px] font-medium text-[#172033]">Acceptance Criteria (ordered)</label>
              <Button size="compact" intent="quiet" onClick={addCriterion} icon={<Plus size={12} aria-hidden />}>Add</Button>
            </div>
            <ol className="space-y-2">
              {criteria.map((c, i) => (
                <li key={i} className="flex items-center gap-2">
                  <span className="text-[12px] text-[#475569] w-5 text-right flex-shrink-0">{i + 1}.</span>
                  <input
                    value={c}
                    onChange={e => setCriteria(prev => prev.map((v, idx) => idx === i ? e.target.value : v))}
                    placeholder="Criterion…"
                    className="flex-1 h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none"
                    aria-label={`Acceptance criterion ${i + 1}`}
                  />
                  {criteria.length > 1 && (
                    <button onClick={() => removeCriterion(i)} aria-label={`Remove criterion ${i + 1}`}
                      className="w-7 h-7 flex items-center justify-center rounded-[6px] text-[#475569] hover:bg-[#FEE2E2] hover:text-[#991B1B]">
                      <X size={12} aria-hidden />
                    </button>
                  )}
                </li>
              ))}
            </ol>
          </div>

          {/* Verification recipe */}
          <div className="flex flex-col gap-1">
            <label htmlFor="cwi-verification" className="text-[12px] font-medium text-[#172033]">Verification Recipe</label>
            <textarea id="cwi-verification" value={verificationRecipe} onChange={event => setVerificationRecipe(event.target.value)}
              className="h-20 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] font-mono bg-white focus:border-[#3659E3] outline-none resize-none"
              placeholder="Example: pnpm test --filter tracing && pnpm lint" />
            <p className="text-[12px] text-[#475569]">Required before this WorkItem can transition to READY.</p>
          </div>

          {/* Exclusions + risk */}
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-1">
              <label htmlFor="cwi-excl" className="text-[12px] font-medium text-[#172033]">Exclusions</label>
              <textarea id="cwi-excl" value={exclusions} onChange={e => setExclusions(e.target.value)}
                className="h-16 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none resize-none"
                placeholder="What is out of scope." />
            </div>
            <div className="flex flex-col gap-1">
              <label htmlFor="cwi-risk" className="text-[12px] font-medium text-[#172033]">Risk</label>
              <textarea id="cwi-risk" value={risk} onChange={e => setRisk(e.target.value)}
                className="h-16 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] bg-white focus:border-[#3659E3] outline-none resize-none"
                placeholder="Known risks or concerns." />
            </div>
          </div>

          {/* Workflow version (immutable selector) */}
          <div className="flex flex-col gap-1">
            <label className="text-[12px] font-medium text-[#172033]">Workflow version <span className="text-[#475569] font-normal">(immutable)</span></label>
            <div className="flex items-center gap-2">
              <select aria-label="Workflow version" value={workflowVersion} onChange={e => setWorkflowVersion(e.target.value)}
                className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] bg-white text-[13px] font-mono focus:border-[#3659E3] outline-none">
                <option value="feature-workflow@v3.1.0">feature-workflow@v3.1.0</option>
                <option value="feature-workflow@v3.0.2">feature-workflow@v3.0.2</option>
                <option value="bugfix-workflow@v1.4.0">bugfix-workflow@v1.4.0</option>
              </select>
              <span className="text-[12px] text-[#475569]">Published · immutable</span>
            </div>
          </div>

          {/* Scope builder */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <label className="text-[12px] font-medium text-[#172033]">Repository Scope</label>
              <Button size="compact" intent="quiet" disabled={!project.repositories.length} onClick={addScopeRow} icon={<Plus size={12} aria-hidden />}>Add repository</Button>
            </div>
            {mode === 'child' && (
              <div className="p-2 rounded-[6px] bg-[#DBEAFE] border border-[#93C5FD] text-[12px] text-[#1E40AF]">
                Family scope: {familyScope.map(f => `${f.access} ${f.repo}/${f.path}`).join(', ')}. Child scope must be a subset.
              </div>
            )}
            <div className="space-y-2" role="list" aria-label="Repository scope rows">
              {scope.map((row, i) => {
                const err = childScopeErrors[i];
                return (
                  <div key={i} className={`flex items-center gap-2 p-3 rounded-[8px] border ${err ? 'border-[#FCA5A5] bg-[#FEF2F2]' : 'border-[#CDD5DF] bg-[#F8FAFC]'}`} role="listitem">
                    <select value={row.repo} onChange={e => setScope(prev => prev.map((r, idx) => idx === i ? { ...r, repo: e.target.value } : r))}
                      className="h-8 px-2 rounded-[6px] border border-[#CDD5DF] bg-white text-[12px] focus:border-[#3659E3] outline-none"
                      aria-label={`Repository for row ${i + 1}`}>
                      {project.repositories.map(repository => <option key={repository.id} value={repository.name}>{repository.name}</option>)}
                    </select>
                    <div className="flex rounded-[6px] border border-[#CDD5DF] overflow-hidden">
                      {(['READ', 'WRITE'] as const).map(a => (
                        <button key={a} onClick={() => setScope(prev => prev.map((r, idx) => idx === i ? { ...r, access: a } : r))}
                          className={`px-2 py-1 text-[12px] font-semibold transition-colors ${row.access === a ? (a === 'WRITE' ? 'bg-[#DCFCE7] text-[#166534]' : 'bg-[#DBEAFE] text-[#1E40AF]') : 'bg-white text-[#475569] hover:bg-[#F3F5F8]'}`}
                          aria-pressed={row.access === a} aria-label={`${a} access`}>
                          {a}
                        </button>
                      ))}
                    </div>
                    <input value={row.path} onChange={e => setScope(prev => prev.map((r, idx) => idx === i ? { ...r, path: e.target.value } : r))}
                      aria-invalid={!!err} aria-describedby={err ? `scope-error-${i}` : undefined}
                      placeholder="src/" className="min-w-0 flex-1 h-8 px-2 rounded-[6px] border border-[#CDD5DF] bg-white text-[12px] font-mono focus:border-[#3659E3] outline-none"
                      aria-label={`Path for ${row.repo} row ${i + 1}`} />
                    {err && <span id={`scope-error-${i}`} role="alert" className="text-[12px] text-[#991B1B]">{err}</span>}
                    <button onClick={() => removeScopeRow(i)} aria-label={`Remove scope row ${i + 1}`}
                      className="w-6 h-6 flex items-center justify-center rounded text-[#475569] hover:bg-[#FEE2E2] hover:text-[#991B1B]">
                      <X size={11} aria-hidden />
                    </button>
                  </div>
                );
              })}
            </div>
            {scopeError && <p role="alert" className="text-[12px] text-[#991B1B]">{scopeError}</p>}
          </div>

          {/* Readiness diagnostics */}
          <div className="rounded-[8px] border border-[#CDD5DF] overflow-hidden">
            <div className="px-4 py-3 bg-[#F8FAFC] border-b border-[#CDD5DF] flex items-center justify-between">
              <span className="text-[12px] font-semibold text-[#172033]">Readiness Diagnostics</span>
              <Button size="compact" intent="quiet" onClick={() => setReadinessChecked(true)} icon={<CheckCircle2 size={12} aria-hidden />}>
                Check Readiness
              </Button>
            </div>
            {readinessChecked ? (
              <div className="divide-y divide-[#ECEFF4]">
                {readinessItems.map(item => (
                  <div key={item.label} className="flex items-center gap-3 px-4 py-2.5">
                    {item.pass
                      ? <CheckCircle2 size={14} className="text-[#166534] flex-shrink-0" aria-label="Pass" />
                      : <AlertTriangle size={14} className="text-[#991B1B] flex-shrink-0" aria-label="Fail" />}
                    <span className="text-[13px] text-[#172033]">{item.label}</span>
                    <span className="text-[12px] text-[#475569] ml-auto">{item.detail}</span>
                  </div>
                ))}
                <div className={`px-4 py-3 text-[12px] font-medium ${isReady ? 'bg-[#DCFCE7] text-[#166534]' : 'bg-[#FEF3C7] text-[#92400E]'}`}>
                  {isReady ? 'Ready to transition after creation.' : 'May be saved as BACKLOG; requirements for READY are not yet satisfied.'}
                </div>
              </div>
            ) : (
              <div className="px-4 py-3 text-[12px] text-[#475569]">Run diagnostics to verify scope and workflow compatibility.</div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-[#CDD5DF] bg-[#F8FAFC] flex-shrink-0">
          <Button intent="secondary" onClick={onClose}>Cancel</Button>
          <Button intent="primary" onClick={submit} disabled={isOffline} title={isOffline ? 'Reconnect to create this WorkItem' : undefined}>Create WorkItem</Button>
        </div>
      </div>
    </div>
  );
}
