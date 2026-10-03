import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  repositoriesReadiness, repositoriesReadinessAcceptException, repositoriesReadinessProfileSet, repositoriesReadinessVerify, ApiError,
} from '../api/generated';
import type { RepositoryView } from '../api/catalog';
import type { BaselineStateName, ReadinessCommandView, RepositoryReadinessView, WorkspaceBaselineView } from '../api/readiness';
import { withSessionToken } from '../api/session';
import { Badge, Button, CopyableId, Dialog, InlineError, Skeleton, TextField } from '../components/ui';
import type { BadgeIntent } from '../components/ui';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

const STATE_INTENT: Record<BaselineStateName, BadgeIntent> = {
  NOT_REQUIRED: 'neutral', PENDING: 'info', PASS: 'success', FAIL: 'danger', EXCEPTION_ACCEPTED: 'warning',
};

function commandText(command: ReadinessCommandView): string {
  return [command.executable, ...(command.argv ?? [])].join(' ');
}

/** One command field group: executable, arguments (space separated) and timeout. */
interface CommandDraft { executable: string; args: string; timeout: string }

function toDraft(command?: ReadinessCommandView): CommandDraft {
  return { executable: command?.executable ?? '', args: (command?.argv ?? []).join(' '), timeout: String(command?.timeoutSeconds ?? 600) };
}

function toCommand(draft: CommandDraft): ReadinessCommandView {
  return {
    executable: draft.executable.trim(),
    argv: draft.args.trim() === '' ? [] : draft.args.trim().split(/\s+/),
    timeoutSeconds: Number(draft.timeout),
  };
}

function validTimeout(value: string): boolean {
  const n = Number(value);
  return Number.isInteger(n) && n > 0;
}

/**
 * RepositoryReadinessDialog — V9-08's operator surface for a repository's
 * readiness profile and baseline (gap G8). It shows the profile and, for every
 * workspace, whether its baseline admits a writer; lets the operator declare the
 * profile, run the baseline again, and — for a failed baseline — accept the
 * failure with a reason (recorded with their name). Every action is one of the
 * `aw repository readiness ...` commands (set, verify, accept-exception).
 */
export function RepositoryReadinessDialog({ repository, onClose, onDone }: {
  repository: RepositoryView; onClose: () => void; onDone: (message: string) => void;
}) {
  const queryClient = useQueryClient();
  const queryKey = ['repositoryReadiness', repository.id];
  const readinessQuery = useQuery({
    queryKey,
    queryFn: async () => (await repositoriesReadiness(repository.id, withSessionToken())) as unknown as RepositoryReadinessView,
  });

  const [editing, setEditing] = useState(false);
  const [setup, setSetup] = useState<CommandDraft>(toDraft());
  const [verification, setVerification] = useState<CommandDraft>(toDraft());
  const [useSetup, setUseSetup] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [accepting, setAccepting] = useState<WorkspaceBaselineView | null>(null);
  const [reason, setReason] = useState('');

  function startEditing() {
    const profile = readinessQuery.data?.profile;
    setSetup(toDraft(profile?.setup));
    setVerification(toDraft(profile?.verification));
    setUseSetup(!!profile?.setup);
    setSubmitted(false);
    setEditing(true);
  }

  const saveMutation = useMutation({
    mutationFn: () => repositoriesReadinessProfileSet(repository.id, {
      setup: useSetup ? toCommand(setup) : undefined,
      verification: toCommand(verification),
    }, withSessionToken()),
  });
  const verifyMutation = useMutation({
    mutationFn: () => repositoriesReadinessVerify(repository.id, {}, withSessionToken()),
  });
  const acceptMutation = useMutation({
    mutationFn: (attemptId: string) => repositoriesReadinessAcceptException(repository.id, { baselineAttemptId: attemptId, reason: reason.trim() }, withSessionToken()),
  });

  const verificationValid = verification.executable.trim() !== '' && validTimeout(verification.timeout);
  const setupValid = !useSetup || (setup.executable.trim() !== '' && validTimeout(setup.timeout));

  function handleSave() {
    setSubmitted(true);
    if (!verificationValid || !setupValid) return;
    saveMutation.mutate(undefined, {
      onSuccess: result => {
        queryClient.invalidateQueries({ queryKey });
        setEditing(false);
        onDone(`Readiness profile v${result.profileVersion} saved for ${repository.name}; ${result.baselineJobsEnqueued} baseline run(s) started.`);
      },
    });
  }

  function handleVerify() {
    verifyMutation.mutate(undefined, {
      onSuccess: result => {
        queryClient.invalidateQueries({ queryKey });
        onDone(`Baseline started on ${result.baselineJobsEnqueued} workspace(s) of ${repository.name}.`);
      },
    });
  }

  function handleAccept() {
    const attemptId = accepting?.attempt?.attemptId;
    if (!attemptId || reason.trim() === '') return;
    acceptMutation.mutate(attemptId, {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey });
        setAccepting(null);
        setReason('');
        onDone(`Baseline failure accepted for ${repository.name}.`);
      },
    });
  }

  const data = readinessQuery.data;

  return (
    <Dialog
      title={`Readiness — ${repository.name}`}
      description="The repository's setup/verification recipe, and whether its baseline lets an agent write. A WorkItem that may write here is READY only when the baseline passed, or an operator accepted its failure."
      onClose={onClose}
      actions={<Button intent="secondary" onClick={onClose}>Close</Button>}
    >
      <div className="space-y-5">
        {readinessQuery.isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : readinessQuery.isError ? (
          <InlineError {...apiErrorMessage(readinessQuery.error)} onRetry={() => readinessQuery.refetch()} />
        ) : (
          <>
            <section aria-label="Readiness profile">
              <div className="flex items-center justify-between mb-1.5">
                <h3 className="text-[12px] font-semibold text-[#172033] uppercase tracking-wide">Profile</h3>
                {!editing && <Button size="compact" intent="secondary" onClick={startEditing}>{data?.profile ? 'Edit profile' : 'Set profile'}</Button>}
              </div>
              {!editing && (data?.profile ? (
                <dl className="grid grid-cols-[110px_1fr] gap-x-3 gap-y-1 text-[13px]">
                  <dt className="text-[#5D697A]">Version</dt><dd>{data.profile.version}</dd>
                  {data.profile.setup && (<><dt className="text-[#5D697A]">Setup</dt><dd className="font-mono text-[12px]">{commandText(data.profile.setup)}</dd></>)}
                  <dt className="text-[#5D697A]">Verification</dt><dd className="font-mono text-[12px]">{commandText(data.profile.verification)}</dd>
                </dl>
              ) : (
                <p className="text-[13px] text-[#5D697A]">No readiness profile: no baseline is required, writers are not gated.</p>
              ))}
              {editing && (
                <div className="space-y-3">
                  <label className="flex items-center gap-2 text-[13px]">
                    <input type="checkbox" checked={useSetup} onChange={e => setUseSetup(e.target.checked)} />
                    Run a setup command first
                  </label>
                  {useSetup && (
                    <CommandFields idPrefix="setup" title="Setup command" draft={setup} onChange={setSetup} submitted={submitted} />
                  )}
                  <CommandFields idPrefix="verification" title="Verification command" draft={verification} onChange={setVerification} submitted={submitted} />
                  {saveMutation.isError && <InlineError {...apiErrorMessage(saveMutation.error)} />}
                  <div className="flex gap-2">
                    <Button intent="primary" size="compact" loading={saveMutation.isPending} onClick={handleSave}>Save profile</Button>
                    <Button intent="secondary" size="compact" disabled={saveMutation.isPending} onClick={() => setEditing(false)}>Cancel</Button>
                  </div>
                </div>
              )}
            </section>

            <section aria-label="Baseline">
              <div className="flex items-center justify-between mb-1.5">
                <h3 className="text-[12px] font-semibold text-[#172033] uppercase tracking-wide">Baseline</h3>
                {data?.profile && <Button size="compact" intent="secondary" loading={verifyMutation.isPending} onClick={handleVerify}>Verify again</Button>}
              </div>
              {verifyMutation.isError && <InlineError {...apiErrorMessage(verifyMutation.error)} />}
              {(data?.workspaces.length ?? 0) === 0 ? (
                <p className="text-[13px] text-[#5D697A]">No workspace has been provisioned for this repository yet.</p>
              ) : (
                <ul className="space-y-3">
                  {data?.workspaces.map(ws => (
                    <li key={ws.repositoryWorkspaceId} className="border border-[#CDD5DF] rounded-[8px] p-3 space-y-1.5">
                      <div className="flex items-center gap-2 flex-wrap">
                        <CopyableId value={ws.repositoryWorkspaceId} />
                        <span className="text-[12px] text-[#5D697A]">generation {ws.generation} · {ws.workspaceState}</span>
                        <Badge label={ws.baselineState} intent={STATE_INTENT[ws.baselineState]} />
                        <span className="text-[12px] text-[#475569]">{ws.admitsWriters ? 'writers admitted' : 'writers blocked'}</span>
                      </div>
                      {ws.reason && <p className="text-[12px] text-[#475569]">{ws.reason}</p>}
                      {ws.attempt && (
                        <div className="text-[12px] text-[#475569] space-y-1">
                          <p>
                            {ws.attempt.stage} {ws.attempt.outcome}
                            {ws.attempt.failureKind ? ` — ${ws.attempt.failureKind}` : ''}
                            {ws.attempt.exitCode !== undefined ? `, exit ${ws.attempt.exitCode}` : ''} · profile v{ws.attempt.profileVersion}
                          </p>
                          {ws.attempt.errorMessage && <p className="font-mono">{ws.attempt.errorMessage}</p>}
                          {ws.attempt.stderrExcerpt && <pre className="font-mono text-[11px] bg-[#F3F5F8] rounded-[4px] p-2 max-h-28 overflow-auto whitespace-pre-wrap">{ws.attempt.stderrExcerpt}</pre>}
                        </div>
                      )}
                      {ws.exception && (
                        <p className="text-[12px] text-[#92400E]">Accepted by {ws.exception.acceptedBy} on {new Date(ws.exception.acceptedAt).toLocaleString()}: {ws.exception.reason}</p>
                      )}
                      {ws.baselineState === 'FAIL' && (
                        <Button size="compact" intent="secondary" onClick={() => { setAccepting(ws); setReason(''); }}>Accept exception…</Button>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {accepting && (
                <div className="mt-3 space-y-2 border-t border-[#ECEFF4] pt-3">
                  <TextField label="Reason for accepting this failure" required value={reason} onChange={setReason}
                    helper="recorded with your name; covers this failed attempt only" />
                  {acceptMutation.isError && <InlineError {...apiErrorMessage(acceptMutation.error)} />}
                  <div className="flex gap-2">
                    <Button intent="primary" size="compact" loading={acceptMutation.isPending} disabled={reason.trim() === ''} onClick={handleAccept}>Accept failure</Button>
                    <Button intent="secondary" size="compact" disabled={acceptMutation.isPending} onClick={() => setAccepting(null)}>Cancel</Button>
                  </div>
                </div>
              )}
            </section>
          </>
        )}
      </div>
    </Dialog>
  );
}

function CommandFields({ idPrefix, title, draft, onChange, submitted }: {
  idPrefix: string; title: string; draft: CommandDraft; onChange: (draft: CommandDraft) => void; submitted: boolean;
}) {
  return (
    <fieldset className="space-y-2 border border-[#ECEFF4] rounded-[8px] p-3">
      <legend className="text-[12px] font-semibold px-1">{title}</legend>
      <TextField id={`${idPrefix}-executable`} label={`${title} executable`} required mono value={draft.executable}
        error={submitted && draft.executable.trim() === '' ? 'required' : undefined} placeholder="npm"
        onChange={executable => onChange({ ...draft, executable })} />
      <TextField id={`${idPrefix}-args`} label={`${title} arguments`} mono value={draft.args} placeholder="test --silent"
        helper="separated by spaces; each word is one argument, there is no shell"
        onChange={args => onChange({ ...draft, args })} />
      <TextField id={`${idPrefix}-timeout`} label={`${title} timeout (seconds)`} required mono value={draft.timeout}
        error={submitted && !validTimeout(draft.timeout) ? 'a positive whole number' : undefined}
        onChange={timeout => onChange({ ...draft, timeout })} />
    </fieldset>
  );
}
