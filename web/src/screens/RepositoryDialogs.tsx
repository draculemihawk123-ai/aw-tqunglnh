import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import {
  ApiError, projectRepositoriesCheck, repositoriesDelete, repositoriesDisable, repositoriesOnboarding, repositoriesUpdate,
} from '../api/generated';
import type { OnboardingView, RepositoryView } from '../api/catalog';
import { withSessionToken } from '../api/session';
import { Button, CopyableId, Dialog, InlineError, TextField } from '../components/ui';

/**
 * V11-12 (ADR-043) — the repository lifecycle surfaces: say what is wrong with a path before it is registered (and after), correct a BLOCKED
 * registration, retire a repository, remove one that never became ACTIVE. Every message comes from the server (failureKind / message / hint), never
 * guessed here; the hint is a command the operator can paste.
 */

export interface CheckOutcome {
  ok: boolean;
  failureKind?: string;
  message?: string;
  path?: string;
  resolvedPath?: string;
  detail?: string;
  hint?: string;
  canonicalPath?: string;
  baseCommit?: string;
  dirty?: boolean;
  components?: { name: string }[];
}

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) {
    const items = (err.details ?? []).filter(d => d.field === 'items').map(d => d.message);
    const code = (err.details ?? []).find(d => d.field === 'repository')?.message ?? err.code;
    return { code, message: items.length ? `${err.message}` : err.message };
  }
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

function refusalItems(err: unknown): string[] {
  return err instanceof ApiError ? (err.details ?? []).filter(d => d.field === 'items').map(d => d.message) : [];
}

/** Runs the server-side probe without registering anything; used on leaving the path field. */
export async function checkRepositoryPath(projectId: string, path: string, ref: string): Promise<CheckOutcome> {
  const query: Record<string, string> = { path };
  if (ref.trim()) query.ref = ref.trim();
  return (await projectRepositoriesCheck(projectId, withSessionToken({ query }))) as unknown as CheckOutcome;
}

/** The result of a path check, in the same words everywhere. */
export function CheckResult({ outcome }: { outcome: CheckOutcome }) {
  if (outcome.ok) {
    return (
      <div role="status" className="text-[12px] text-[#166534] bg-[#DCFCE7] border border-[#86EFAC] rounded-[6px] px-3 py-2" data-testid="check-ok">
        Looks good: a Git repository at <span className="font-mono">{outcome.canonicalPath}</span>
        {outcome.dirty ? ' (it has uncommitted changes)' : ''}
        {outcome.components && outcome.components.length > 0 ? `, ${outcome.components.length} folder${outcome.components.length === 1 ? '' : 's'} found` : ''}.
      </div>
    );
  }
  return <FailureNote kind={outcome.failureKind} message={outcome.message ?? 'The check failed.'} hint={outcome.hint} detail={outcome.detail} testId="check-failure" />;
}

/** A failure in four parts: what, which kind, what to do (copyable), and Git's own words behind a toggle. */
export function FailureNote({ kind, message, hint, detail, testId }: { kind?: string; message: string; hint?: string; detail?: string; testId?: string }) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  function copy() {
    try {
      void navigator.clipboard?.writeText(hint ?? '');
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div role="alert" data-testid={testId} className="text-[12px] text-[#92400E] bg-[#FEF3C7] border border-[#FCD34D] rounded-[6px] px-3 py-2 space-y-1">
      <p><span className="font-mono">{kind ?? 'PROBE_FAILED'}</span> — {message}</p>
      {hint && (
        <p className="flex items-start gap-2">
          <span className="flex-1">{hint}</span>
          <button type="button" className="text-[#3659E3] hover:underline flex-shrink-0" onClick={copy} aria-label="Copy the suggested fix">{copied ? 'Copied' : 'Copy'}</button>
        </p>
      )}
      {detail && (
        <>
          <button type="button" className="text-[#3659E3] hover:underline" aria-expanded={open} onClick={() => setOpen(o => !o)}>{open ? 'Hide Git output' : 'Show Git output'}</button>
          {open && <pre className="whitespace-pre-wrap font-mono text-[11px] bg-white/60 rounded p-2" data-testid="failure-detail">{detail}</pre>}
        </>
      )}
    </div>
  );
}

/** The BLOCKED card's reason; the Git output is fetched (onboarding) only when asked for. */
export function RepositoryFailure({ repository }: { repository: RepositoryView }) {
  const error = repository.lastProbeError;
  const [wantDetail, setWantDetail] = useState(false);
  const onboarding = useQuery({
    queryKey: ['repositoryOnboarding', repository.id, repository.version],
    queryFn: async () => (await repositoriesOnboarding(repository.id, withSessionToken())) as unknown as OnboardingView,
    enabled: wantDetail,
  });
  if (!error) return null;
  const detail = onboarding.data?.lastProbeError?.detail;
  return (
    <div className="mt-1" data-testid={`repo-error-${repository.id}`}>
      <FailureNote kind={error.failureKind} message={error.message} hint={error.hint} detail={detail} />
      {!wantDetail && (
        <button type="button" className="text-[12px] text-[#3659E3] hover:underline mt-1" onClick={() => setWantDetail(true)}>Load Git output</button>
      )}
    </div>
  );
}

export function EditRepositoryDialog({ repository, onClose, onDone }: { repository: RepositoryView; onClose: () => void; onDone: (message: string) => void }) {
  const [name, setName] = useState(repository.name);
  const [remoteLocator, setRemoteLocator] = useState(repository.remoteLocator);
  const [defaultRef, setDefaultRef] = useState(repository.defaultRef);
  const [outcome, setOutcome] = useState<CheckOutcome | null>(null);
  const [checking, setChecking] = useState(false);
  const mutation = useMutation({
    mutationFn: () => repositoriesUpdate(repository.id, {
      name: name.trim() === repository.name ? null : name.trim(),
      remoteLocator: remoteLocator.trim(), defaultRef: defaultRef.trim(),
    }, withSessionToken({ ifMatch: `"${repository.version}"` })),
  });
  async function check() {
    if (!remoteLocator.trim()) return;
    setChecking(true);
    try {
      setOutcome(await checkRepositoryPath(repository.projectId, remoteLocator.trim(), defaultRef));
    } catch (err) {
      setOutcome({ ok: false, message: apiErrorMessage(err).message });
    } finally {
      setChecking(false);
    }
  }
  const valid = !!remoteLocator.trim() && !!defaultRef.trim() && !!name.trim();
  return (
    <Dialog
      title={`Edit ${repository.name}`}
      description="Correct this registration. aw probes it again; the repository becomes ACTIVE only when the probe passes."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} disabled={!valid}
            onClick={() => mutation.mutate(undefined, { onSuccess: () => onDone(`${repository.name} updated — probing again.`) })}>Save and Probe</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Name" required value={name} onChange={setName} />
        <TextField label="Local repository path" required mono value={remoteLocator} onChange={v => { setRemoteLocator(v); setOutcome(null); }} onBlur={check}
          helper="aw checks the path when you leave this field" />
        <TextField label="Default ref" required mono value={defaultRef} onChange={v => { setDefaultRef(v); setOutcome(null); }} onBlur={check} />
        {/* Fixed height, so the answer cannot move the buttons under the pointer. */}
        <div className="h-28 overflow-y-auto" aria-live="polite">
          {checking && <p className="text-[12px] text-[#475569]" role="status">Checking the path…</p>}
          {outcome && <CheckResult outcome={outcome} />}
        </div>
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}

export function DisableRepositoryDialog({ repository, onClose, onDone }: { repository: RepositoryView; onClose: () => void; onDone: (message: string) => void }) {
  const [reason, setReason] = useState('');
  const mutation = useMutation({
    mutationFn: () => repositoriesDisable(repository.id, { reason: reason.trim() }, withSessionToken({ ifMatch: `"${repository.version}"` })),
  });
  const items = refusalItems(mutation.error);
  return (
    <Dialog
      title={`Disable ${repository.name}`}
      description="A disabled repository can no longer be used by new work. Its history and evidence are kept, and its name can be registered again."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="destructive" loading={mutation.isPending}
            onClick={() => mutation.mutate(undefined, { onSuccess: () => onDone(`${repository.name} disabled.`) })}>Disable</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Reason (optional)" value={reason} onChange={setReason} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
        {items.length > 0 && (
          <ul className="text-[12px] text-[#172033] list-disc pl-5 space-y-0.5" aria-label="Still using this repository" data-testid="holders">
            {items.map(item => <li key={item}>{item}</li>)}
          </ul>
        )}
      </div>
    </Dialog>
  );
}

export function DeleteRepositoryDialog({ repository, onClose, onDone }: { repository: RepositoryView; onClose: () => void; onDone: (message: string) => void }) {
  const [reason, setReason] = useState('');
  const mutation = useMutation({
    mutationFn: () => repositoriesDelete(repository.id, { reason: reason.trim() }, withSessionToken({ ifMatch: `"${repository.version}"` })),
  });
  const items = refusalItems(mutation.error);
  return (
    <Dialog
      title={`Delete ${repository.name}`}
      description="This removes the registration permanently. The id is never used again. Only a repository that never became ACTIVE and that nothing references can be deleted."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="destructive" loading={mutation.isPending} disabled={!reason.trim()}
            onClick={() => mutation.mutate(undefined, { onSuccess: () => onDone(`${repository.name} deleted.`) })}>Delete permanently</Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-[12px] text-[#475569]">Repository <CopyableId value={repository.id} /></p>
        <TextField label="Reason" required value={reason} onChange={setReason} helper="kept in the event log" />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
        {items.length > 0 && (
          <ul className="text-[12px] text-[#172033] list-disc pl-5 space-y-0.5" aria-label="References" data-testid="references">
            {items.map(item => <li key={item}>{item}</li>)}
          </ul>
        )}
      </div>
    </Dialog>
  );
}
