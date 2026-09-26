import React, { useState, useRef, useEffect, useCallback, useId } from 'react';
import { Copy, Check, RefreshCw, X, XCircle, AlertTriangle, CheckCircle2, Info, Ban, Circle, CircleDot, Minus, FileText } from './icons';

// ─── Badge ────────────────────────────────────────────────────────────────────

export type BadgeIntent = 'neutral' | 'info' | 'runtime' | 'success' | 'warning' | 'danger';

const BADGE_STYLES: Record<BadgeIntent, string> = {
  neutral: 'bg-[#F1F5F9] text-[#475569] border-[#CBD5E1]',
  info:    'bg-[#DBEAFE] text-[#1E40AF] border-[#93C5FD]',
  runtime: 'bg-[#EDE9FE] text-[#5B21B6] border-[#C4B5FD]',
  success: 'bg-[#DCFCE7] text-[#166534] border-[#86EFAC]',
  warning: 'bg-[#FEF3C7] text-[#92400E] border-[#FCD34D]',
  danger:  'bg-[#FEE2E2] text-[#991B1B] border-[#FCA5A5]',
};

const STATE_ICONS: Record<string, { icon: React.ReactNode; label: string }> = {
  BACKLOG:    { icon: <Circle size={10} aria-hidden />, label: 'Backlog' },
  CANCELLED:  { icon: <Ban size={10} aria-hidden />, label: 'Cancelled' },
  SKIPPED:    { icon: <Ban size={10} aria-hidden />, label: 'Skipped' },
  'N/A':      { icon: <Minus size={10} aria-hidden />, label: 'N/A' },
  NOT_RUN:    { icon: <Minus size={10} aria-hidden />, label: 'Not run' },
  READY:      { icon: <CircleDot size={10} aria-hidden />, label: 'Ready' },
  QUEUED:     { icon: <CircleDot size={10} aria-hidden />, label: 'Queued' },
  REQUESTED:  { icon: <CircleDot size={10} aria-hidden />, label: 'Requested' },
  PROBING:    { icon: <RefreshCw size={10} aria-hidden className="animate-spin" />, label: 'Probing' },
  ACTIVE:     { icon: <CircleDot size={10} aria-hidden />, label: 'Active' },
  RUNNING:    { icon: <RefreshCw size={10} aria-hidden className="animate-spin" />, label: 'Running' },
  VERIFYING:  { icon: <RefreshCw size={10} aria-hidden className="animate-spin" />, label: 'Verifying' },
  CANCELLING: { icon: <RefreshCw size={10} aria-hidden className="animate-spin" />, label: 'Cancelling' },
  BLOCKED:    { icon: <AlertTriangle size={10} aria-hidden />, label: 'Blocked' },
  WAITING:    { icon: <AlertTriangle size={10} aria-hidden />, label: 'Waiting' },
  STALE:      { icon: <AlertTriangle size={10} aria-hidden />, label: 'Stale' },
  PARTIAL:    { icon: <AlertTriangle size={10} aria-hidden />, label: 'Partial' },
  INDETERMINATE:{ icon: <AlertTriangle size={10} aria-hidden />, label: 'Indeterminate' },
  DONE:       { icon: <CheckCircle2 size={10} aria-hidden />, label: 'Done' },
  SUCCEEDED:  { icon: <CheckCircle2 size={10} aria-hidden />, label: 'Succeeded' },
  PASS:       { icon: <CheckCircle2 size={10} aria-hidden />, label: 'Pass' },
  FAILED:     { icon: <X size={10} aria-hidden />, label: 'Failed' },
  ERROR:      { icon: <X size={10} aria-hidden />, label: 'Error' },
  QUARANTINED:{ icon: <X size={10} aria-hidden />, label: 'Quarantined' },
  DRAFT:      { icon: <FileText size={10} aria-hidden />, label: 'Draft' },
  SEALED:     { icon: <CheckCircle2 size={10} aria-hidden />, label: 'Sealed' },
  ABANDONED:  { icon: <Ban size={10} aria-hidden />, label: 'Abandoned' },
  REGISTERED: { icon: <CheckCircle2 size={10} aria-hidden />, label: 'Registered' },
  UNREGISTERED:{ icon: <Circle size={10} aria-hidden />, label: 'Unregistered' },
};

export function Badge({ label, intent }: { label: string; intent: BadgeIntent }) {
  const si = STATE_ICONS[label];
  return (
    <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded-[4px] border text-[12px] font-medium leading-none ${BADGE_STYLES[intent]}`}
      role="status" aria-label={si?.label ?? label}>
      <span className="flex-shrink-0">{si?.icon ?? <Info size={10} aria-hidden />}</span>
      <span aria-hidden>{label}</span>
    </span>
  );
}

export function StatusBadge({ state, entity = 'workitem' }: { state: string; entity?: string }) {
  let intent: BadgeIntent = 'neutral';
  if (['BACKLOG', 'CANCELLED', 'SKIPPED', 'N/A', 'NOT_RUN'].includes(state)) intent = 'neutral';
  else if (['READY', 'QUEUED', 'REQUESTED', 'PROBING'].includes(state)) intent = 'info';
  else if (['ACTIVE', 'RUNNING', 'VERIFYING', 'CANCELLING'].includes(state)) {
    intent = entity === 'repository' ? 'success' : 'runtime';
  }
  else if (['BLOCKED', 'WAITING', 'STALE', 'PARTIAL', 'INDETERMINATE'].includes(state)) intent = 'warning';
  else if (['DONE', 'SUCCEEDED', 'PASS'].includes(state)) intent = 'success';
  else if (['FAILED', 'ERROR', 'QUARANTINED'].includes(state)) intent = 'danger';
  return <Badge label={state} intent={intent} />;
}

export function VerdictBadge({ verdict }: { verdict: 'PASS' | 'FAIL' | 'ERROR' | 'N/A' | 'NOT_RUN' }) {
  const map: Record<string, BadgeIntent> = { PASS: 'success', FAIL: 'danger', ERROR: 'danger', 'N/A': 'neutral', NOT_RUN: 'neutral' };
  return <Badge label={verdict} intent={map[verdict] ?? 'neutral'} />;
}

// ─── Button ───────────────────────────────────────────────────────────────────

export type ButtonIntent = 'primary' | 'secondary' | 'quiet' | 'destructive';

const BTN_STYLES: Record<ButtonIntent, string> = {
  primary:     'bg-[#3659E3] text-white border-[#3659E3] hover:bg-[#2845B8] hover:border-[#2845B8]',
  secondary:   'bg-white text-[#172033] border-[#CDD5DF] hover:bg-[#F3F5F8] hover:border-[#AAB4C3]',
  quiet:       'bg-transparent text-[#3659E3] border-transparent hover:bg-[#EEF2FF]',
  destructive: 'bg-white text-[#991B1B] border-[#FCA5A5] hover:bg-[#FEE2E2]',
};

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  intent?: ButtonIntent;
  size?: 'compact' | 'default';
  loading?: boolean;
  icon?: React.ReactNode;
  iconEnd?: React.ReactNode;
}

export function Button({ intent = 'secondary', size = 'default', loading, icon, iconEnd, children, disabled, className = '', ...rest }: ButtonProps) {
  const h = size === 'compact' ? 'h-8 px-3 text-[13px]' : 'h-9 px-4 text-sm';
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      aria-disabled={disabled || loading}
      className={`inline-flex items-center gap-1.5 font-medium rounded-[6px] border transition-colors ${h} ${BTN_STYLES[intent]} disabled:opacity-50 disabled:cursor-not-allowed ${className}`}
    >
      {loading ? <RefreshCw size={13} className="animate-spin" aria-hidden /> : icon}
      {children}
      {iconEnd && <span aria-hidden>{iconEnd}</span>}
    </button>
  );
}

// ─── IconButton ───────────────────────────────────────────────────────────────

export function IconButton({ label, onClick, children, className = '', active }: {
  label: string; onClick?: () => void; children: React.ReactNode; className?: string; active?: boolean;
}) {
  return (
    <button
      aria-label={label}
      title={label}
      onClick={onClick}
      className={`w-8 h-8 flex items-center justify-center rounded-[6px] transition-colors ${active ? 'bg-[#EEF2FF] text-[#3659E3] border border-[#C7D2FE]' : 'text-[#475569] border border-transparent hover:bg-[#ECEFF4] hover:text-[#172033]'} ${className}`}
    >
      {children}
    </button>
  );
}

// ─── Tabs ─────────────────────────────────────────────────────────────────────

export function Tabs({ tabs, active, onChange }: {
  tabs: { id: string; label: string; count?: number; error?: boolean }[];
  active: string;
  onChange: (id: string) => void;
}) {
  const tabRefs = useRef<Record<string, HTMLButtonElement | null>>({});

  // WAI-ARIA APG "Tabs" pattern, automatic activation: Left/Right move
  // (and select, since content already switches with focus) with
  // wraparound, Home/End jump to the first/last tab. Roving tabindex
  // (only the active tab is in the Tab order) is what makes Home/End/
  // arrow keys meaningful — a screen reader user tabs INTO the tablist
  // once, then arrows between tabs, never Tabs past every single one.
  const handleKeyDown = (e: React.KeyboardEvent, index: number) => {
    let nextIndex: number | null = null;
    if (e.key === 'ArrowRight') nextIndex = (index + 1) % tabs.length;
    else if (e.key === 'ArrowLeft') nextIndex = (index - 1 + tabs.length) % tabs.length;
    else if (e.key === 'Home') nextIndex = 0;
    else if (e.key === 'End') nextIndex = tabs.length - 1;
    if (nextIndex === null) return;
    e.preventDefault();
    const next = tabs[nextIndex];
    onChange(next.id);
    tabRefs.current[next.id]?.focus();
  };

  return (
    <div role="tablist" className="flex border-b border-[#CDD5DF]">
      {tabs.map((tab, index) => (
        <button
          key={tab.id}
          ref={el => { tabRefs.current[tab.id] = el; }}
          role="tab"
          aria-selected={active === tab.id}
          tabIndex={active === tab.id ? 0 : -1}
          onClick={() => onChange(tab.id)}
          onKeyDown={e => handleKeyDown(e, index)}
          className={`px-4 py-2.5 text-[13px] font-medium border-b-2 -mb-px transition-colors ${
            active === tab.id
              ? 'border-[#3659E3] text-[#3659E3] bg-white'
              : 'border-transparent text-[#475569] hover:text-[#172033] hover:border-[#CDD5DF]'
          }`}
        >
          {tab.label}
          {tab.count !== undefined && (
            <span className={`ml-1.5 px-1.5 py-0.5 rounded-full text-[12px] font-medium ${active === tab.id ? 'bg-[#EEF2FF] text-[#3659E3]' : 'bg-[#ECEFF4] text-[#475569]'}`}>
              {tab.count}
            </span>
          )}
          {tab.error && <XCircle size={12} className="ml-1 inline text-[#991B1B]" aria-label="error" />}
        </button>
      ))}
    </div>
  );
}

// ─── InlineError ──────────────────────────────────────────────────────────────

export function InlineError({ code, message, field, correlation, onRetry }: {
  code: string; message: string; field?: string; correlation?: string; onRetry?: () => void;
}) {
  return (
    <div role="alert" aria-live="assertive" className="flex gap-3 p-3 rounded-[8px] bg-[#FEE2E2] border border-[#FCA5A5]">
      <X size={14} className="text-[#991B1B] flex-shrink-0 mt-0.5" aria-hidden />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="font-mono text-[12px] text-[#991B1B] font-medium">{code}</span>
          {field && <span className="text-[12px] text-[#991B1B] opacity-75">at {field}</span>}
        </div>
        <p className="text-[13px] text-[#991B1B] mt-0.5">{message}</p>
        {correlation && (
          <button onClick={() => navigator.clipboard?.writeText(correlation)} className="font-mono text-[12px] text-[#991B1B] opacity-60 hover:opacity-100 mt-1 truncate block">
            correlation: {correlation}
          </button>
        )}
        {onRetry && <button onClick={onRetry} className="text-[12px] text-[#991B1B] underline mt-1">Retry</button>}
      </div>
    </div>
  );
}

// ─── ProjectionBanner ─────────────────────────────────────────────────────────

export function ProjectionBanner({
  state, watermark, journalPosition, onRefresh, onRebuild, onDiagnostics,
}: {
  state: 'Fresh' | 'Stale' | 'Degraded' | 'Resync required';
  watermark?: string;
  /** The real, server-reported AsOfJournalPosition this banner's own state was computed from (httpapi.Freshness) — never shown unless a caller has a real one; there is no installation-wide "the" journal position to invent when one is omitted. */
  journalPosition?: number;
  onRefresh?: () => void;
  onRebuild?: () => void;
  onDiagnostics?: () => void;
}) {
  if (state === 'Fresh') return null;
  const styles = {
    Stale:           'bg-[#FEF3C7] border-[#FCD34D] text-[#92400E]',
    Degraded:        'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]',
    'Resync required':'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]',
  };
  return (
    <div role="status" aria-live="polite" className={`mx-2 mt-2 flex flex-wrap items-center gap-2 rounded-[8px] border px-4 py-2 text-[13px] ${styles[state]}`}>
      <AlertTriangle size={13} aria-hidden className="flex-shrink-0" />
      <span className="font-medium">Projection {state}</span>
      {watermark && <span className="text-[12px]">— last updated {watermark}</span>}
      <span className="text-[12px]">
        {journalPosition !== undefined && <>JournalPosition {journalPosition} · </>}
        {state === 'Stale' ? 'Consumer lag; awaiting projection catch-up.' : 'Projection interrupted by poison event; cached data remains readable.'}
      </span>
      <div className="flex-1" />
      {onRefresh && <button onClick={onRefresh} className="text-[12px] underline font-medium">Refresh</button>}
      {onRebuild && <button onClick={onRebuild} className="text-[12px] underline font-medium ml-2">Rebuild Projection</button>}
      {onDiagnostics && <button onClick={onDiagnostics} className="text-[12px] underline font-medium">Run Diagnostics</button>}
    </div>
  );
}

// ─── ConnectionBanner ─────────────────────────────────────────────────────────

export function ConnectionBanner({ state }: { state: 'Live' | 'Reconnecting' | 'Offline' | 'Degraded' | 'Stale' }) {
  // Stale/Degraded are projection conditions and already have the dedicated
  // ProjectionBanner. Keep this banner for transport connectivity only.
  if (state === 'Live' || state === 'Stale' || state === 'Degraded') return null;
  const cfg = ({
    Reconnecting: { bg: 'bg-[#FEF3C7] border-[#FCD34D] text-[#92400E]', msg: 'Reconnecting — retrying in 5 s…', Icon: RefreshCw, spin: true },
    Offline:      { bg: 'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]', msg: 'Offline — cached data shown as of last sync. Mutations unavailable.', Icon: X, spin: false },
  } as Record<string, { bg: string; msg: string; Icon: React.ComponentType<any>; spin: boolean }>)[state];
  const { bg, msg, Icon, spin } = cfg;
  return (
    <div role="alert" aria-live="assertive" className={`mx-2 mt-2 flex items-center gap-2 rounded-[8px] border px-4 py-2 text-[13px] font-medium ${bg}`}>
      <Icon size={13} aria-hidden className={`flex-shrink-0 ${spin ? 'animate-spin' : ''}`} />
      <span>{msg}</span>
    </div>
  );
}

// ─── ConnectionIndicator ──────────────────────────────────────────────────────

export function ConnectionIndicator({ state }: { state: 'Live' | 'Reconnecting' | 'Offline' | 'Degraded' | 'Stale' }) {
  const cfg = ({
    Live:        { color: 'text-[#166534]', dot: 'bg-[#4ADE80]', label: 'Live' },
    Reconnecting:{ color: 'text-[#92400E]', dot: 'bg-[#FCD34D] animate-pulse', label: 'Reconnecting' },
    Stale:       { color: 'text-[#92400E]', dot: 'bg-[#FCD34D]', label: 'Stale' },
    Offline:     { color: 'text-[#991B1B]', dot: 'bg-[#FCA5A5]', label: 'Offline' },
    Degraded:    { color: 'text-[#991B1B]', dot: 'bg-[#FCA5A5] animate-pulse', label: 'Degraded' },
  } as Record<string, { color: string; dot: string; label: string }>)[state];
  return (
    <span className={`inline-flex items-center gap-1.5 text-[12px] font-medium ${cfg.color}`} role="status" aria-label={`Connection: ${cfg.label}`}>
      <span className={`w-2 h-2 rounded-full flex-shrink-0 ${cfg.dot}`} aria-hidden />
      {cfg.label}
    </span>
  );
}

// ─── OperationNotice ──────────────────────────────────────────────────────────

export function OperationNotice({ state, ref: opRef, message }: {
  state: 'Requested' | 'Queued' | 'Running' | 'Completed' | 'Failed'; ref?: string; message?: string;
}) {
  const styles = {
    Requested: 'bg-[#DBEAFE] border-[#93C5FD] text-[#1E40AF]',
    Queued:    'bg-[#DBEAFE] border-[#93C5FD] text-[#1E40AF]',
    Running:   'bg-[#EDE9FE] border-[#C4B5FD] text-[#5B21B6]',
    Completed: 'bg-[#DCFCE7] border-[#86EFAC] text-[#166534]',
    Failed:    'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]',
  };
  return (
    <div role="status" aria-live="polite" className={`flex items-center gap-2 px-3 py-2 rounded-[6px] border text-[13px] ${styles[state]}`}>
      {state === 'Running' && <RefreshCw size={13} className="animate-spin" aria-hidden />}
      <span className="font-medium">{state}</span>
      {message && <span className="opacity-75">{message}</span>}
      {opRef && <span className="font-mono text-[12px] opacity-60 ml-auto">{opRef}</span>}
    </div>
  );
}

// ─── BlockerCard ──────────────────────────────────────────────────────────────

export function BlockerCard({ type, target, reason, opened, actions }: {
  type: string; target: string; reason: string; opened: string; actions: string[];
}) {
  return (
    <div role="alert" className="rounded-[8px] bg-[#FEF3C7] border border-[#FCD34D] p-4">
      <div className="flex items-start gap-3">
        <AlertTriangle size={16} className="text-[#92400E] flex-shrink-0 mt-0.5" aria-hidden />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-mono text-[12px] font-medium text-[#92400E]">{type}</span>
            <span className="text-[12px] text-[#92400E] opacity-75">on {target}</span>
            <span className="text-[12px] text-[#92400E] opacity-50 ml-auto">since {opened}</span>
          </div>
          <p className="text-[13px] text-[#78350F] mt-1">{reason}</p>
          {actions.length > 0 && (
            <div className="flex gap-2 mt-3">
              {actions.map(a => (
                <Button key={a} size="compact" intent="quiet" className="text-[#92400E] border-[#FCD34D] hover:bg-[#FDE68A]">{a}</Button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ─── EmptyState ───────────────────────────────────────────────────────────────

export function EmptyState({ icon, title, description, action }: {
  icon?: React.ReactNode; title: string; description: string;
  action?: { label: string; onClick: () => void };
}) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center px-8">
      {icon && <div className="mb-4 opacity-20 text-[#5D697A]">{icon}</div>}
      <p className="text-sm font-medium text-[#172033] mb-1">{title}</p>
      <p className="text-[13px] text-[#475569] max-w-sm">{description}</p>
      {action && (
        <Button intent="primary" size="compact" className="mt-4" onClick={action.onClick}>{action.label}</Button>
      )}
    </div>
  );
}

// ─── Skeleton ─────────────────────────────────────────────────────────────────

export function Skeleton({ className = '' }: { className?: string }) {
  return <div className={`bg-[#ECEFF4] rounded animate-pulse ${className}`} aria-hidden />;
}

// ─── CopyableId ───────────────────────────────────────────────────────────────

export function CopyableId({ value, short }: { value: string; short?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  const display = short ?? value;
  return (
    <button
      onClick={copy}
      title={`Copy ${value}`}
      aria-label={`Copy ID: ${value}`}
      className="inline-flex items-center gap-1 font-mono text-[12px] text-[#475569] hover:text-[#172033] bg-[#ECEFF4] hover:bg-[#E2E8F0] px-1.5 py-0.5 rounded-[4px] transition-colors"
    >
      {copied
        ? <><Check size={10} aria-hidden className="text-[#166534]" /><span>copied</span></>
        : <><Copy size={10} aria-hidden /><span>{display}</span></>}
    </button>
  );
}

// ─── ValidActionBar ───────────────────────────────────────────────────────────

export function ValidActionBar({ actions }: {
  actions: { label: string; intent?: ButtonIntent; onClick: () => void; disabled?: boolean }[];
}) {
  if (actions.length === 0) return null;
  const primary     = actions.filter(a => !a.intent || a.intent === 'primary');
  const secondary   = actions.filter(a => a.intent === 'secondary' || a.intent === 'quiet');
  const destructive = actions.filter(a => a.intent === 'destructive');
  return (
    <div role="group" aria-label="Available actions" className="flex items-center gap-2 flex-wrap">
      {primary.map(a => <Button key={a.label} intent="primary" size="compact" onClick={a.onClick} disabled={a.disabled}>{a.label}</Button>)}
      {secondary.map(a => <Button key={a.label} intent={a.intent ?? 'secondary'} size="compact" onClick={a.onClick} disabled={a.disabled}>{a.label}</Button>)}
      {destructive.length > 0 && <div className="w-px h-5 bg-[#CDD5DF] mx-1" aria-hidden />}
      {destructive.map(a => <Button key={a.label} intent="destructive" size="compact" onClick={a.onClick} disabled={a.disabled}>{a.label}</Button>)}
    </div>
  );
}

// ─── Dialog with focus trap ───────────────────────────────────────────────────

export function Dialog({ title, description, children, onClose, actions, isDurable = false }: {
  title: string; description?: string; children?: React.ReactNode;
  onClose: () => void; actions?: React.ReactNode;
  /** If true, Escape key does NOT close (durable operations). */
  isDurable?: boolean;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previousFocusRef = useRef<HTMLElement | null>(null);
  const titleId = useId();

  useEffect(() => {
    previousFocusRef.current = document.activeElement as HTMLElement;
    // Move focus to first focusable element in dialog
    const firstFocusable = dialogRef.current?.querySelector<HTMLElement>(
      'h2, button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
    );
    firstFocusable?.focus();
    return () => {
      previousFocusRef.current?.focus();
    };
  }, []);

  // Focus trap
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Escape' && !isDurable) { onClose(); return; }
    if (e.key !== 'Tab') return;
    const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
      'h2[tabindex], button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
    );
    if (!focusable || focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (e.shiftKey) { if (document.activeElement === first) { last.focus(); e.preventDefault(); } }
    else { if (document.activeElement === last) { first.focus(); e.preventDefault(); } }
  }, [onClose, isDurable]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/25 p-4"
      onClick={e => { if (e.target === e.currentTarget && !isDurable) onClose(); }}>
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onKeyDown={handleKeyDown}
        className="bg-white rounded-[12px] border border-[#CDD5DF] shadow-xl w-full max-w-lg max-h-[calc(100vh-32px)] overflow-y-auto island-shadow"
      >
        <div className="flex items-start justify-between p-6 pb-0">
          <div>
            <h2 id={titleId} tabIndex={-1} className="text-base font-semibold text-[#172033] outline-none">{title}</h2>
            {description && <p className="text-[13px] text-[#475569] mt-1">{description}</p>}
          </div>
          {!isDurable && (
            <button onClick={onClose} aria-label="Close dialog" className="ml-4 flex-shrink-0 w-7 h-7 flex items-center justify-center rounded-[6px] text-[#475569] hover:bg-[#ECEFF4]">
              <X size={14} aria-hidden />
            </button>
          )}
        </div>
        {children && <div className="px-6 pt-4">{children}</div>}
        {actions && <div className="flex justify-end gap-2 p-6 pt-4">{actions}</div>}
      </div>
    </div>
  );
}

// ─── Drawer ───────────────────────────────────────────────────────────────────

export function Drawer({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  const previousFocusRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    previousFocusRef.current = document.activeElement as HTMLElement;
    return () => { previousFocusRef.current?.focus(); };
  }, []);
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [onClose]);
  return (
    <div className="flex flex-col h-full bg-[#F3F5F8] border-l border-[#CDD5DF] w-96">
      <div className="flex items-center justify-between px-4 py-3 border-b border-[#CDD5DF]">
        <span className="text-sm font-semibold text-[#172033]">{title}</span>
        <IconButton label="Close panel" onClick={onClose}><X size={14} aria-hidden /></IconButton>
      </div>
      <div className="flex-1 overflow-y-auto">{children}</div>
    </div>
  );
}

// ─── TextField ───────────────────────────────────────────────────────────────

export function TextField({ label, value, onChange, placeholder, error, helper, mono, readOnly, required }: {
  label: string; value: string; onChange?: (v: string) => void;
  placeholder?: string; error?: string; helper?: string; mono?: boolean; readOnly?: boolean; required?: boolean;
}) {
  const id = `field-${label.replace(/\s+/g, '-').toLowerCase()}`;
  const errId = `${id}-error`;
  const helpId = `${id}-helper`;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-[12px] font-medium text-[#172033]">
        {label}{required && <span aria-hidden className="ml-0.5 text-[#991B1B]">*</span>}
      </label>
      <input
        id={id}
        type="text"
        value={value}
        onChange={e => onChange?.(e.target.value)}
        placeholder={placeholder}
        readOnly={readOnly}
        required={required}
        aria-required={required}
        aria-describedby={error ? errId : helper ? helpId : undefined}
        aria-invalid={!!error}
        className={`h-9 px-3 rounded-[6px] border text-[13px] transition-colors ${mono ? 'font-mono' : ''} ${readOnly ? 'bg-[#F3F5F8] cursor-default' : 'bg-white'} ${error ? 'border-[#FCA5A5] focus:border-[#FCA5A5]' : 'border-[#CDD5DF] focus:border-[#3659E3]'} outline-none`}
      />
      {error && <p id={errId} role="alert" className="text-[12px] text-[#991B1B]">{error}</p>}
      {helper && !error && <p id={helpId} className="text-[12px] text-[#475569]">{helper}</p>}
    </div>
  );
}

// ─── Select ───────────────────────────────────────────────────────────────────

export function Select({ label, value, onChange, options, required }: {
  label: string; value: string; onChange: (v: string) => void;
  options: { value: string; label: string }[]; required?: boolean;
}) {
  const id = `sel-${label.replace(/\s+/g, '-').toLowerCase()}`;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-[12px] font-medium text-[#172033]">
        {label}{required && <span aria-hidden className="ml-0.5 text-[#991B1B]">*</span>}
      </label>
      <select id={id} value={value} onChange={e => onChange(e.target.value)}
        className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] bg-white text-[13px] focus:border-[#3659E3] outline-none">
        {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    </div>
  );
}

// ─── Checkbox ────────────────────────────────────────────────────────────────

export function Checkbox({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label className="flex items-center gap-2 cursor-pointer">
      <input type="checkbox" checked={checked} onChange={e => onChange(e.target.checked)} className="w-4 h-4 rounded border-[#CDD5DF] accent-[#3659E3]" />
      <span className="text-[13px] text-[#172033]">{label}</span>
    </label>
  );
}

// ─── Table ────────────────────────────────────────────────────────────────────

export interface TableColumn<T> {
  key: string;
  header: string;
  align?: 'left' | 'right' | 'center';
  /** Rendered as a visually-hidden column header for screen readers when
   * the header itself has no useful text (e.g. a trailing actions column). */
  headerLabel?: string;
  render: (row: T) => React.ReactNode;
}

const ALIGN_CLASS: Record<'left' | 'right' | 'center', string> = {
  left: 'text-left',
  right: 'text-right',
  center: 'text-center',
};

/**
 * The one locked table pattern every screen with tabular data should
 * converge on (V7-03's own "Thực hiện" line) — a real `<table>` with
 * `<th scope="col">` on every header (screen readers announce which
 * column a cell belongs to; the 4 screens' own raw hand-rolled `<table>`
 * markup this replaces never set `scope`) and one `EmptyState` for the
 * zero-rows case instead of every screen inventing its own empty table
 * body.
 *
 * onRowClick is a MOUSE-ONLY convenience (a larger click target, exactly
 * the existing "row + inner button both call the same handler" pattern
 * already used in `Definitions.tsx`/`Settings.tsx`): it deliberately does
 * NOT give `<tr>` a `role="button"` (that would misrepresent
 * real table structure to assistive tech) — callers that need row
 * activation to be keyboard-reachable put a real interactive element
 * (e.g. a `<button>`) in at least one cell's own `render`, exactly like
 * every existing screen already does.
 */
export function Table<T>({ columns, rows, getRowKey, onRowClick, isRowSelected, emptyState }: {
  columns: TableColumn<T>[];
  rows: T[];
  getRowKey: (row: T) => string;
  onRowClick?: (row: T) => void;
  isRowSelected?: (row: T) => boolean;
  emptyState?: React.ReactNode;
}) {
  if (rows.length === 0 && emptyState) {
    return <>{emptyState}</>;
  }
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="border-b border-[#ECEFF4] bg-[#F8FAFC]">
          {columns.map(col => (
            <th key={col.key} scope="col" className={`px-5 py-2.5 text-xs font-semibold text-[#5D697A] ${ALIGN_CLASS[col.align ?? 'left']}`}>
              {col.headerLabel ? <span className="sr-only">{col.headerLabel}</span> : col.header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody className="divide-y divide-[#ECEFF4]">
        {rows.map(row => {
          const selected = isRowSelected?.(row) ?? false;
          return (
            <tr
              key={getRowKey(row)}
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={`transition-colors ${onRowClick ? 'hover:bg-[#F8FAFC] cursor-pointer' : ''} ${selected ? 'bg-[#EEF2FF]' : ''}`}
            >
              {columns.map(col => (
                <td key={col.key} className={`px-5 py-3 ${ALIGN_CLASS[col.align ?? 'left']}`}>{col.render(row)}</td>
              ))}
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

// ─── Toast ────────────────────────────────────────────────────────────────────

export type ToastIntent = 'success' | 'danger' | 'warning' | 'info';

export interface ToastItem {
  id: string;
  intent: ToastIntent;
  message: string;
  /** Auto-dismiss after this many ms. 0 disables auto-dismiss (the operator must close it). Default 5000. */
  duration?: number;
}

const TOAST_STYLES: Record<ToastIntent, { classes: string; Icon: React.ComponentType<{ size?: number; className?: string; 'aria-hidden'?: boolean }> }> = {
  success: { classes: 'bg-[#DCFCE7] border-[#86EFAC] text-[#166534]', Icon: CheckCircle2 },
  danger:  { classes: 'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]', Icon: XCircle },
  warning: { classes: 'bg-[#FEF3C7] border-[#FCD34D] text-[#92400E]', Icon: AlertTriangle },
  info:    { classes: 'bg-[#DBEAFE] border-[#93C5FD] text-[#1E40AF]', Icon: Info },
};

const DEFAULT_TOAST_DURATION = 5000;

/**
 * useToasts is the one toast queue every screen shares — V7-03's own
 * "toast" primitive, transient and dismissible (unlike `OperationNotice`,
 * which is a persistent banner for an operation still in progress). A
 * `danger` toast never auto-dismisses regardless of `duration`: an error
 * the operator has not yet acknowledged must not silently disappear.
 */
export function useToasts() {
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const dismiss = useCallback((id: string) => {
    setToasts(current => current.filter(t => t.id !== id));
  }, []);

  const show = useCallback((toast: Omit<ToastItem, 'id'> & { id?: string }) => {
    const id = toast.id ?? `toast-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    setToasts(current => [...current, { ...toast, id }]);
    return id;
  }, []);

  return { toasts, show, dismiss };
}

/** Renders the current toast stack — mount exactly one of these near the app root, fed by useToasts(). */
export function ToastViewport({ toasts, onDismiss }: { toasts: ToastItem[]; onDismiss: (id: string) => void }) {
  if (toasts.length === 0) return null;
  return (
    <div className="fixed bottom-4 right-4 z-50 flex flex-col gap-2 w-80" aria-label="Notifications">
      {toasts.map(toast => <Toast key={toast.id} toast={toast} onDismiss={() => onDismiss(toast.id)} />)}
    </div>
  );
}

function Toast({ toast, onDismiss }: { toast: ToastItem; onDismiss: () => void }) {
  const { classes, Icon } = TOAST_STYLES[toast.intent];
  const duration = toast.duration ?? DEFAULT_TOAST_DURATION;

  useEffect(() => {
    if (toast.intent === 'danger' || duration <= 0) return;
    const timer = window.setTimeout(onDismiss, duration);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [toast.id]);

  return (
    <div
      role="status"
      aria-live={toast.intent === 'danger' ? 'assertive' : 'polite'}
      className={`flex items-start gap-2.5 rounded-[8px] border px-4 py-3 text-[13px] shadow-lg island-shadow ${classes}`}
    >
      <Icon size={15} aria-hidden className="flex-shrink-0 mt-0.5" />
      <p className="flex-1 min-w-0">{toast.message}</p>
      <button onClick={onDismiss} aria-label="Dismiss notification" className="flex-shrink-0 opacity-60 hover:opacity-100">
        <X size={13} aria-hidden />
      </button>
    </div>
  );
}
