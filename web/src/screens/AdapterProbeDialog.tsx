import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  probeAdapterBuild,
  registerAdapterBuild,
  ApiError,
  type ProbeAdapterBuildResponse,
} from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, Checkbox, Dialog, InlineError, TextField } from '../components/ui';
import type { ToastItem } from '../components/ui';

/**
 * The real wire shape of a domainadapterbuild.CapabilityManifest
 * (internal/domain/adapterbuild/adapterbuild.go) — apicontract's generator
 * only reflects one level of a top-level request/response, so a nested
 * field like ProbeAdapterBuildRequest.capabilityManifest comes back as
 * `unknown` on the generated client; this dialog owns the one place that
 * shape is hand-declared, on both the probe request and the register
 * request (kept byte-identical between the two calls — see the "Confirm &
 * register" step below for why).
 */
interface CapabilityManifest {
  supportsStart: boolean;
  supportsResume: boolean;
  supportsCancel: boolean;
  canonicalEventKinds: string[];
}

/** ProbeAdapterBuildResponse.tuple is `unknown` for the same one-level-deep-contract reason; this is domainadapterbuild.CandidateTuple's real shape (internal/domain/adapterbuild/adapterbuild.go). */
interface CandidateTuple {
  providerKey: string;
  executablePath: string;
  executableContentHash: string;
  protocolVersion: string;
  capabilityManifestHash: string;
  os: string;
  toolchain: string;
  configIdentity: string;
}

interface FormState {
  providerKey: string;
  executablePath: string;
  protocolVersion: string;
  os: string;
  toolchain: string;
  configIdentity: string;
  supportsStart: boolean;
  supportsResume: boolean;
  supportsCancel: boolean;
  canonicalEventKinds: string;
}

const EMPTY_FORM: FormState = {
  providerKey: '', executablePath: '', protocolVersion: '', os: '', toolchain: '', configIdentity: '',
  supportsStart: true, supportsResume: false, supportsCancel: false, canonicalEventKinds: '',
};

function manifestFrom(form: FormState): CapabilityManifest {
  const kinds = Array.from(new Set(form.canonicalEventKinds.split(',').map(s => s.trim()).filter(Boolean)));
  return {
    supportsStart: form.supportsStart, supportsResume: form.supportsResume, supportsCancel: form.supportsCancel,
    canonicalEventKinds: kinds,
  };
}

function requiredFieldErrors(form: FormState): Record<string, string> {
  const errors: Record<string, string> = {};
  for (const [field, value] of Object.entries({
    providerKey: form.providerKey, executablePath: form.executablePath, protocolVersion: form.protocolVersion,
    os: form.os, toolchain: form.toolchain, configIdentity: form.configIdentity,
  })) {
    if (value.trim() === '') errors[field] = 'required';
  }
  if (!form.supportsStart) errors.supportsStart = 'a build that cannot start a run is never a valid candidate';
  return errors;
}

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * AdapterProbeDialog is V7-05B's own real implementation of ADR-022's
 * "action probe -> xác nhận -> đăng ký" (docs/design/09-v7-alpha-ui.md
 * V7-05's own line) — a two-step flow, never a single combined submit,
 * because the underlying protocol itself is two separate commands
 * (ProbeAdapterBuild, then RegisterAdapterBuild) with a real operator
 * decision point between them: the operator reviews the SERVER-MEASURED
 * candidate fingerprint (executableContentHash/capabilityManifestHash) —
 * never a client-side guess — before confirming registration.
 *
 * Step 1 ("probe"): the operator supplies every field
 * appadapterbuild.ProbeRequest needs (mirroring `aw adapter probe`'s own
 * flag set exactly, internal/delivery/cli/adapterbuild/probe.go) and this
 * dialog calls POST /adapter-builds/probe.
 *
 * Step 2 ("candidate"): the server's response (a signed CandidateToken) is
 * shown for review, and "Confirm & register" calls POST /adapter-builds
 * with {token, capabilityManifest} — capabilityManifest is the EXACT SAME
 * object step 1 already built, read-only in this step, never re-typed:
 * RegisterAdapterBuild re-hashes it and rejects on any mismatch against
 * what the token bound (ErrCapabilityManifestDrift), so offering a second
 * editable copy here would only invite an operator to accidentally trigger
 * that rejection.
 */
export function AdapterProbeDialog({ onClose, onRegistered }: { onClose: () => void; onRegistered: (toast: Omit<ToastItem, 'id'>) => void }) {
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [submitted, setSubmitted] = useState(false);
  const [candidate, setCandidate] = useState<{ token: ProbeAdapterBuildResponse; manifest: CapabilityManifest } | null>(null);
  const queryClient = useQueryClient();

  const probeMutation = useMutation({
    mutationFn: (body: { form: FormState; manifest: CapabilityManifest }) =>
      probeAdapterBuild(
        {
          providerKey: body.form.providerKey, executablePath: body.form.executablePath, protocolVersion: body.form.protocolVersion,
          capabilityManifest: body.manifest, os: body.form.os, toolchain: body.form.toolchain, configIdentity: body.form.configIdentity,
        },
        withSessionToken(),
      ),
  });

  const registerMutation = useMutation({
    mutationFn: (body: { token: ProbeAdapterBuildResponse; manifest: CapabilityManifest }) =>
      registerAdapterBuild({ token: body.token as unknown, capabilityManifest: body.manifest }, withSessionToken()),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['adapterBuilds'] });
      onRegistered({ intent: 'success', message: `Adapter build for "${form.providerKey}" registered.` });
      onClose();
    },
  });

  const errors = submitted ? requiredFieldErrors(form) : {};

  function handleProbe() {
    setSubmitted(true);
    if (Object.keys(requiredFieldErrors(form)).length > 0) return;
    const manifest = manifestFrom(form);
    probeMutation.mutate(
      { form, manifest },
      { onSuccess: token => setCandidate({ token, manifest }) },
    );
  }

  if (candidate) {
    const tuple = candidate.token.tuple as unknown as CandidateTuple;
    return (
      <Dialog
        title="Confirm adapter build candidate"
        description="Review what was actually measured on this installation before registering it as an immutable AdapterBuildVersion."
        onClose={onClose}
        actions={
          <>
            <Button intent="secondary" onClick={() => setCandidate(null)} disabled={registerMutation.isPending}>Back</Button>
            <Button
              intent="primary"
              loading={registerMutation.isPending}
              onClick={() => registerMutation.mutate({ token: candidate.token, manifest: candidate.manifest })}
            >
              Confirm &amp; register
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <dl className="grid grid-cols-[140px_1fr] gap-x-3 gap-y-2 text-[13px]">
            <dt className="text-[#5D697A]">Provider key</dt><dd className="text-[#172033] font-medium">{tuple.providerKey}</dd>
            <dt className="text-[#5D697A]">Executable path</dt><dd className="font-mono text-[12px] text-[#172033] break-all">{tuple.executablePath}</dd>
            <dt className="text-[#5D697A]">Content hash</dt><dd className="font-mono text-[12px] text-[#172033] break-all">{tuple.executableContentHash}</dd>
            <dt className="text-[#5D697A]">Protocol version</dt><dd className="text-[#172033]">{tuple.protocolVersion}</dd>
            <dt className="text-[#5D697A]">Capability hash</dt><dd className="font-mono text-[12px] text-[#172033] break-all">{tuple.capabilityManifestHash}</dd>
            <dt className="text-[#5D697A]">OS / toolchain</dt><dd className="text-[#172033]">{tuple.os} / {tuple.toolchain}</dd>
            <dt className="text-[#5D697A]">Config identity</dt><dd className="text-[#172033]">{tuple.configIdentity}</dd>
            <dt className="text-[#5D697A]">Candidate expires</dt><dd className="text-[#172033]">{new Date(candidate.token.expiresAt).toLocaleTimeString()}</dd>
          </dl>
          <p className="text-[12px] text-[#5D697A]">
            This fingerprint was measured by this installation just now — not entered by you. Registering pins it as an immutable build; running workflows never auto-repin to it.
          </p>
          {registerMutation.isError && (
            <InlineError {...apiErrorMessage(registerMutation.error)} onRetry={() => setCandidate(null)} />
          )}
        </div>
      </Dialog>
    );
  }

  return (
    <Dialog
      title="Probe adapter build"
      description="Measure a configured provider executable's real fingerprint before registering it (ADR-022)."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={probeMutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={probeMutation.isPending} onClick={handleProbe}>Probe</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Provider key" required value={form.providerKey} error={errors.providerKey && 'required'}
          placeholder="claude" onChange={v => setForm(f => ({ ...f, providerKey: v }))} />
        <TextField label="Executable path" required mono value={form.executablePath} error={errors.executablePath && 'required'}
          placeholder="/usr/local/bin/claude" onChange={v => setForm(f => ({ ...f, executablePath: v }))} />
        <TextField label="Protocol version" required value={form.protocolVersion} error={errors.protocolVersion && 'required'}
          placeholder="AK-Adapter/1.2" onChange={v => setForm(f => ({ ...f, protocolVersion: v }))} />
        <div className="grid grid-cols-2 gap-3">
          <TextField label="OS" required value={form.os} error={errors.os && 'required'}
            placeholder="linux/amd64" onChange={v => setForm(f => ({ ...f, os: v }))} />
          <TextField label="Toolchain" required value={form.toolchain} error={errors.toolchain && 'required'}
            placeholder="node-20.11" onChange={v => setForm(f => ({ ...f, toolchain: v }))} />
        </div>
        <TextField label="Config identity" required value={form.configIdentity} error={errors.configIdentity && 'required'}
          helper="operator-assigned identity for this executable's permission mode / env profile"
          placeholder="isolated-default" onChange={v => setForm(f => ({ ...f, configIdentity: v }))} />
        <TextField label="Canonical event kinds" value={form.canonicalEventKinds}
          helper="comma-separated, optional" placeholder="agent.turn.started, agent.turn.completed"
          onChange={v => setForm(f => ({ ...f, canonicalEventKinds: v }))} />
        <div className="flex flex-col gap-2 pt-1">
          <Checkbox label="Supports start" checked={form.supportsStart} onChange={v => setForm(f => ({ ...f, supportsStart: v }))} />
          {errors.supportsStart && <p role="alert" className="text-[12px] text-[#991B1B] -mt-1">{errors.supportsStart}</p>}
          <Checkbox label="Supports resume" checked={form.supportsResume} onChange={v => setForm(f => ({ ...f, supportsResume: v }))} />
          <Checkbox label="Supports cancel" checked={form.supportsCancel} onChange={v => setForm(f => ({ ...f, supportsCancel: v }))} />
        </div>
        {probeMutation.isError && <InlineError {...apiErrorMessage(probeMutation.error)} />}
      </div>
    </Dialog>
  );
}
