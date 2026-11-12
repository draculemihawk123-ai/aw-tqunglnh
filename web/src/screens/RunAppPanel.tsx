import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getRunAppStatus, setRunAppProfile, startRunApp, stopRunApp } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';
import { Button, Dialog, InlineError } from '../components/ui';

export interface RunAppProcess { name: string; argv: string[]; dir?: string; port?: number; env?: Record<string, string>; secretNames?: string[] }
export interface RunAppProfile { processes: RunAppProcess[]; previewUrl?: string; stopAfterMinutes?: number }
interface ProfileView { repositoryId: string; profile?: RunAppProfile; version: number }
interface Session { id: string; state: string; active: boolean; requestedBy: string; workerId?: string; deadlineAt: string; startedAt?: string; endedAt?: string; problem?: string }
export interface RunAppStatus {
  repositoryWorkspaceId: string;
  repositoryId: string;
  workspaceState: string;
  profile: ProfileView;
  session?: Session;
  logs: { name: string; port?: number; tail: string }[];
  startBlockers: string[];
}

const key = () => withSessionToken({ idempotencyKey: crypto.randomUUID() });

export const EXAMPLE_PROFILE: RunAppProfile = {
  processes: [
    { name: 'backend', argv: ['./mvnw', 'spring-boot:run'], dir: 'backend', port: 8080, secretNames: ['DB_PASSWORD'] },
    { name: 'frontend', argv: ['pnpm', 'dev'], dir: 'frontend', port: 3000, env: { NEXT_PUBLIC_API_URL: 'http://localhost:8080' } },
  ],
  previewUrl: 'http://localhost:3000',
  stopAfterMinutes: 120,
};

/** Parses the profile editor's text; returns the profile or the reason it is not one. */
export function parseProfile(text: string): { profile?: RunAppProfile; error?: string } {
  let value: unknown;
  try { value = JSON.parse(text); } catch (e) { return { error: e instanceof Error ? e.message : 'Not JSON.' }; }
  const p = value as RunAppProfile;
  if (!p || !Array.isArray(p.processes) || p.processes.length === 0) return { error: 'A profile needs at least one process.' };
  for (const proc of p.processes) {
    if (!proc || typeof proc.name !== 'string' || !Array.isArray(proc.argv) || proc.argv.length === 0 || proc.argv.some(a => typeof a !== 'string')) {
      return { error: 'Each process needs a name and an argv list of strings (no shell string).' };
    }
  }
  return { profile: p };
}

const ACTIVE_LABEL: Record<string, string> = { REQUESTED: 'starting (waiting for a worker)', RUNNING: 'running', STOP_REQUESTED: 'stopping' };

/**
 * V12-30 (docs/design/15-v12-ui-operations.md, UX-32, ADR-062): run the worktree's application to try it by hand. `aw worker` runs the
 * repository's Run app profile (argv per process, ports checked free, secrets from the repository's store), keeps a log per process
 * (secrets redacted), and kills every process tree on Stop, at the automatic stop, when the worktree is released and when an attempt
 * takes its write lease. One application per repository at a time.
 */
export function RunAppPanel({ projectId, repo, isOffline }: { projectId: string; repo: RepositoryWorkspaceState; isOffline?: boolean }) {
  const query = useQuery({
    queryKey: ['runApp', projectId, repo.repositoryWorkspaceId],
    queryFn: async () => (await getRunAppStatus(projectId, repo.repositoryWorkspaceId, withSessionToken())) as unknown as RunAppStatus,
    enabled: !isOffline,
    retry: false,
    refetchInterval: q => (q.state.data?.session?.active ? 2_000 : false),
  });
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState('');
  const [showLogs, setShowLogs] = useState(false);
  const refetch = () => { void query.refetch(); };
  const start = useMutation({ mutationFn: () => startRunApp(projectId, repo.repositoryWorkspaceId, {}, key()), onSuccess: refetch });
  const stop = useMutation({ mutationFn: () => stopRunApp(projectId, repo.repositoryWorkspaceId, {}, key()), onSuccess: refetch });
  const st = query.data;
  const parsed = parseProfile(text);
  const save = useMutation({
    mutationFn: () => setRunAppProfile(projectId, st!.repositoryId, { profile: parsed.profile!, expectedVersion: st!.profile.version }, key()),
    onSuccess: () => { setEditing(false); refetch(); },
  });
  if (!st) return null;
  const s = st.session;
  if (repo.state !== 'READY' && !s?.active) return null;
  const profile = st.profile.profile;
  const error = start.error ?? stop.error;
  const openEditor = () => { setText(JSON.stringify(profile ?? EXAMPLE_PROFILE, null, 2)); setEditing(true); };

  return (
    <div className="px-4 py-2 bg-white border-b border-[#CDD5DF] text-[12px] text-[#475569] flex-shrink-0 space-y-1" data-testid="run-app">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-medium text-[#172033]">Run app</span>
        {!profile && <span>no profile for this repository yet</span>}
        {s?.active && <span className="text-[#166534]" data-testid="run-app-state">{ACTIVE_LABEL[s.state] ?? s.state} · stops at {new Date(s.deadlineAt).toLocaleTimeString()}</span>}
        {s && !s.active && <span data-testid="run-app-state" className={s.state === 'FAILED' ? 'text-[#991B1B]' : ''}>{s.state.toLowerCase()}{s.problem ? `: ${s.problem}` : ''}</span>}
        {s?.state === 'RUNNING' && profile?.previewUrl && (
          <a className="text-[#3659E3] hover:underline" href={profile.previewUrl} target="_blank" rel="noreferrer">Open preview</a>
        )}
        <span className="flex-1" />
        {s && <Button size="compact" intent="quiet" onClick={() => setShowLogs(v => !v)}>{showLogs ? 'Hide logs' : 'Logs'}</Button>}
        <Button size="compact" intent="quiet" disabled={isOffline} onClick={openEditor}>{profile ? 'Profile…' : 'Set up…'}</Button>
        {s?.active ? (
          <Button size="compact" intent="secondary" disabled={isOffline || stop.isPending || s.state === 'STOP_REQUESTED'} onClick={() => stop.mutate()}>Stop</Button>
        ) : (
          <Button size="compact" intent="primary" disabled={isOffline || start.isPending || st.startBlockers.length > 0} onClick={() => start.mutate()}
            title={st.startBlockers.join('\n') || undefined}>Start</Button>
        )}
      </div>
      {!s?.active && profile && st.startBlockers.length > 0 && (
        <ul className="text-[#92400E]" aria-label="Why it cannot start">{st.startBlockers.map(b => <li key={b}>{b}</li>)}</ul>
      )}
      {error && <InlineError code="RUN_APP" message={error instanceof Error ? error.message : 'The Run app action failed.'} />}
      {showLogs && st.logs.map(l => (
        <details key={l.name} open className="mt-1">
          <summary className="cursor-pointer font-mono text-[#172033]">{l.name}{l.port ? ` :${l.port}` : ''}</summary>
          <pre className="max-h-48 overflow-auto bg-[#0F172A] text-[#E2E8F0] p-2 rounded-[6px] text-[11px] whitespace-pre-wrap" data-testid={`run-app-log-${l.name}`}>{l.tail || '(empty)'}</pre>
        </details>
      ))}

      {editing && (
        <Dialog title="Run app profile" onClose={() => setEditing(false)}
          description="Each process is an argv list (never a shell string) run in a sub-directory of the worktree, with its port, plain variables and the names of repository secrets. Used by every worktree of this repository; a running application keeps the profile it started with."
          actions={<>
            <Button intent="secondary" onClick={() => setEditing(false)} disabled={save.isPending}>Cancel</Button>
            <Button intent="primary" loading={save.isPending} disabled={!parsed.profile} onClick={() => save.mutate()}>Save</Button>
          </>}>
          <textarea aria-label="Profile JSON" rows={16} value={text} onChange={e => setText(e.target.value)} spellCheck={false}
            className="w-full border border-[#CDD5DF] rounded-[6px] p-2 font-mono text-[12px]" />
          {parsed.error && <p className="text-[12px] text-[#991B1B]">{parsed.error}</p>}
          {save.isError && <InlineError code="PROFILE" message={save.error instanceof Error ? save.error.message : 'Saving the profile failed.'} />}
        </Dialog>
      )}
    </div>
  );
}
