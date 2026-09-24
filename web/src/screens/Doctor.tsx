import React, { useState } from 'react';
import { Badge, Button, OperationNotice, Dialog } from '../components/ui';
import { AlertTriangle, CheckCircle2, RefreshCw, XCircle } from '../components/icons';

type CheckState = 'PASS' | 'FAIL' | 'WARN' | 'PROBING';

interface ReadinessCheck {
  id: string;
  name: string;
  state: CheckState;
  detail: string;
  remediation?: string;
  observedAt: string;
}

const INITIAL_CHECKS: ReadinessCheck[] = [
  { id: 'db', name: 'Database', state: 'PASS', detail: 'PostgreSQL 16.2 reachable; migrations applied.', observedAt: '09:41:03' },
  { id: 'artifact', name: 'Artifact Root', state: 'PASS', detail: 'Configured artifact root is writable; 184 GB free.', observedAt: '09:41:04' },
  { id: 'workspace', name: 'Workspace Root', state: 'PASS', detail: 'Configured workspace root is writable; 512 GB free.', observedAt: '09:41:04' },
  { id: 'git', name: 'Git', state: 'PASS', detail: 'git 2.44.0 detected; credential helper configured.', observedAt: '09:41:05' },
  { id: 'provider-openai', name: 'Provider / openai', state: 'FAIL', detail: 'Probe returned HTTP 401. API key may be expired or invalid.', remediation: 'Verify OPENAI_API_KEY in provider defaults and re-probe.', observedAt: '09:40:58' },
  { id: 'provider-anthropic', name: 'Provider / anthropic', state: 'PASS', detail: 'claude-sonnet-4-6 accessible; rate limit OK.', observedAt: '09:41:06' },
  { id: 'adapter-go', name: 'Adapter Build / go-executor', state: 'FAIL', detail: 'Adapter not registered. Observed fingerprint sha256:a3f9…c012 is not in the registry.', remediation: 'Run adapter probe and confirm registration.', observedAt: '09:40:55' },
  { id: 'isolation', name: 'Isolation Enforcement', state: 'WARN', detail: 'ENFORCED_ISOLATED is unavailable on this installation. OPERATOR_TRUSTED_LOCAL is the only declared tier.', remediation: 'Do not auto-downgrade a pinned isolation tier. Runs requiring ENFORCED_ISOLATED remain blocked.', observedAt: '09:41:07' },
];

interface AdapterBuild {
  id: string;
  provider: string;
  registered: boolean;
  observedFingerprint: string;
  registeredFingerprint: string | null;
  protocol: string;
  os: string;
  probeTime: string;
}

const ADAPTERS: AdapterBuild[] = [
  {
    id: 'go-executor', provider: 'go-executor', registered: false,
    observedFingerprint: 'sha256:a3f9e8b1d4c702fe91a5b3c8e0f2d6a7b9c1e3f5a7d9…c012',
    registeredFingerprint: null,
    protocol: 'AK-Adapter/1.2', os: 'linux/amd64', probeTime: '09:40:55',
  },
  {
    id: 'python-executor', provider: 'python-executor', registered: true,
    observedFingerprint: 'sha256:7b2c4e6f8a0d2f4c6e8a0b2d4f6a8c0e2f4d6b8a0c2e…89f1',
    registeredFingerprint: 'sha256:7b2c4e6f8a0d2f4c6e8a0b2d4f6a8c0e2f4d6b8a0c2e…89f1',
    protocol: 'AK-Adapter/1.2', os: 'linux/amd64', probeTime: '09:41:02',
  },
];

export function DoctorScreen({ isOffline = false }: { isOffline?: boolean }) {
  const [checks, setChecks] = useState(INITIAL_CHECKS);
  const [probing, setProbing] = useState<string | null>(null);
  const [registerDialog, setRegisterDialog] = useState<AdapterBuild | null>(null);
  const [registered, setRegistered] = useState(false);

  const runProbe = (id: string) => {
    if (isOffline) return;
    setProbing(id);
    setTimeout(() => {
      setProbing(null);
      setChecks(prev => prev.map(c => {
        if (c.id !== id) return c;
        if (id === 'isolation') {
          return { ...c, state: 'WARN', observedAt: '09:58:20' };
        }
        if (id === 'adapter-go' && !registered) {
          return { ...c, state: 'FAIL', detail: 'Probe succeeded, but the observed build is still unregistered.', observedAt: '09:58:20' };
        }
        return { ...c, state: 'PASS', detail: 'Probe succeeded.', remediation: undefined, observedAt: '09:58:20' };
      }));
    }, 1200);
  };

  const stateStyle: Record<CheckState, string> = {
    PASS: 'text-[#166534]', FAIL: 'text-[#991B1B]', WARN: 'text-[#92400E]', PROBING: 'text-[#1E40AF]',
  };
  const stateIcon: Record<CheckState, React.ReactNode> = {
    PASS: <CheckCircle2 size={16} aria-hidden />,
    FAIL: <XCircle size={16} aria-hidden />,
    WARN: <AlertTriangle size={16} aria-hidden />,
    PROBING: <RefreshCw size={16} aria-hidden className="animate-spin" />,
  };

  const failCount = checks.filter(c => c.state === 'FAIL').length;

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-6">
        {/* Header */}
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-xl font-semibold text-[#172033]">Doctor</h1>
            <p className="text-sm text-[#5D697A] mt-0.5">System readiness report for this installation.</p>
          </div>
          <Button intent="secondary" disabled={isOffline} onClick={() => {
            setChecks(prev => prev.map(c => ({ ...c, state: 'PROBING' as CheckState })));
            setTimeout(() => setChecks(INITIAL_CHECKS), 2500);
          }} icon={<RefreshCw size={13} aria-hidden />}>Re-run all checks</Button>
        </div>

        {/* Summary banner */}
        {failCount > 0 ? (
          <div className="flex items-center gap-3 p-3 rounded-[8px] bg-[#FEE2E2] border border-[#FCA5A5]">
            <XCircle size={16} aria-hidden className="text-[#991B1B]" />
            <span className="text-sm font-medium text-[#991B1B]">{failCount} check{failCount > 1 ? 's' : ''} failing — system is not ready for production workloads.</span>
          </div>
        ) : (
          <div className="flex items-center gap-3 p-3 rounded-[8px] bg-[#DCFCE7] border border-[#86EFAC]">
            <CheckCircle2 size={16} aria-hidden className="text-[#166534]" />
            <span className="text-sm font-medium text-[#166534]">All checks passing — installation is ready.</span>
          </div>
        )}

        {/* Readiness grid */}
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Readiness Checks</h2>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {checks.map(check => {
              const isProbing = probing === check.id;
              return (
                <div key={check.id} className="flex items-start gap-4 px-5 py-4">
                  <span className={`mt-0.5 text-base flex-shrink-0 ${isProbing ? 'animate-pulse text-[#1E40AF]' : stateStyle[check.state]}`}>
                    {isProbing ? stateIcon.PROBING : stateIcon[check.state]}
                  </span>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium text-[#172033]">{check.name}</span>
                      <span className="text-[12px] text-[#475569]">observed {isProbing ? 'now' : check.observedAt}</span>
                    </div>
                    <p className="text-sm text-[#5D697A] mt-0.5">{check.detail}</p>
                    {check.remediation && !isProbing && check.state !== 'PASS' && (
                      <p className="text-xs text-[#92400E] mt-1 bg-[#FEF3C7] border border-[#FCD34D] rounded-[4px] px-2 py-1">{check.remediation}</p>
                    )}
                  </div>
                  <div className="flex items-center gap-2 flex-shrink-0">
                    {isProbing ? (
                      <OperationNotice state="Running" message="Probing…" />
                    ) : check.state === 'FAIL' ? (
                      <Button intent="primary" size="compact" disabled={isOffline} onClick={() => runProbe(check.id)} icon={<RefreshCw size={12} aria-hidden />}>Probe</Button>
                    ) : check.state === 'WARN' ? (
                      <Button intent="secondary" size="compact" disabled={isOffline} onClick={() => runProbe(check.id)}>Re-probe</Button>
                    ) : null}
                  </div>
                </div>
              );
            })}
          </div>
        </div>

        {/* Adapter Builds */}
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between">
            <h2 className="text-sm font-semibold text-[#172033]">Adapter Builds</h2>
            <span className="text-[12px] text-[#475569]">{ADAPTERS.filter(a => a.registered || (a.id === 'go-executor' && registered)).length}/{ADAPTERS.length} registered</span>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {ADAPTERS.map(adapter => {
              const isRegistered = adapter.id === 'go-executor' ? registered : adapter.registered;
              const registeredFingerprint = adapter.id === 'go-executor' && registered
                ? adapter.observedFingerprint
                : adapter.registeredFingerprint;
              return (
                <div key={adapter.id} className="px-5 py-4">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="text-sm font-medium text-[#172033]">{adapter.provider}</span>
                        <Badge label={isRegistered ? 'REGISTERED' : 'UNREGISTERED'} intent={isRegistered ? 'success' : 'danger'} />
                        <span className="text-[12px] text-[#475569]">{adapter.os} · {adapter.protocol}</span>
                      </div>
                      <div className="mt-2 space-y-1">
                        <div className="flex items-center gap-2">
                          <span className="text-xs text-[#5D697A] w-36 flex-shrink-0">Observed fingerprint</span>
                          <span className="font-mono text-xs text-[#172033] truncate">{adapter.observedFingerprint.slice(0, 48)}…</span>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="text-xs text-[#5D697A] w-36 flex-shrink-0">Registered fingerprint</span>
                          {isRegistered && registeredFingerprint ? (
                            <span className="font-mono text-xs text-[#166534] truncate">{registeredFingerprint.slice(0, 48)}…</span>
                          ) : (
                            <span className="text-xs text-[#991B1B] italic">not registered — observed fingerprint is not admitted</span>
                          )}
                        </div>
                      </div>
                    </div>
                    {!isRegistered && (
                      <Button intent="primary" size="compact" disabled={isOffline} onClick={() => setRegisterDialog(adapter)}>Register</Button>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      {/* Registration dialog */}
      {registerDialog && (
        <Dialog
          title={`Register Adapter: ${registerDialog.provider}`}
          description="Review the observed candidate before admission. An observed fingerprint is not yet trusted."
          onClose={() => setRegisterDialog(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setRegisterDialog(null)}>Cancel</Button>
              <Button intent="primary" onClick={() => {
                setRegistered(true);
                setRegisterDialog(null);
                setChecks(prev => prev.map(c => c.id === 'adapter-go' ? { ...c, state: 'PASS', detail: 'go-executor build registered. Workflows must still pin this exact build.', observedAt: '09:58:24' } : c));
              }}>Confirm Registration</Button>
            </>
          }
        >
          <div className="space-y-3 text-sm">
            <div className="p-3 rounded-[6px] bg-[#FEF3C7] border border-[#FCD34D] text-[#92400E] text-xs">
              Registering creates an immutable build record. Verify this fingerprint before confirming; execution still requires an exact workflow pin.
            </div>
            <div className="space-y-2">
              <div className="flex gap-2">
                <span className="text-[#5D697A] w-32 flex-shrink-0">Provider</span>
                <span className="font-medium">{registerDialog.provider}</span>
              </div>
              <div className="flex gap-2">
                <span className="text-[#5D697A] w-32 flex-shrink-0">Protocol</span>
                <span className="font-mono text-xs">{registerDialog.protocol}</span>
              </div>
              <div className="flex gap-2">
                <span className="text-[#5D697A] w-32 flex-shrink-0">OS/Arch</span>
                <span className="font-mono text-xs">{registerDialog.os}</span>
              </div>
              <div className="flex gap-2">
                <span className="text-[#5D697A] w-32 flex-shrink-0 mt-0.5">Observed fp</span>
                <span className="font-mono text-xs break-all text-[#172033]">{registerDialog.observedFingerprint}</span>
              </div>
            </div>
          </div>
        </Dialog>
      )}
    </div>
  );
}
