import React, { useState } from 'react';
import { Badge, Button, Dialog, OperationNotice } from '../components/ui';
import { Wrench, RefreshCw, AlertTriangle, CheckCircle2, XCircle } from '../components/icons';

interface AdapterBuild {
  id: string;
  provider: string;
  registered: boolean;
  observedFingerprint: string;
  registeredFingerprint: string | null;
  protocol: string;
  capabilities: string[];
  os: string;
  probeTime: string;
  drift: boolean;
}

const INITIAL_ADAPTERS: AdapterBuild[] = [
  {
    id: 'go-executor', provider: 'go-executor', registered: false,
    observedFingerprint: 'sha256:a3f9e8b1d4c702fe91a5b3c8e0f2d6a7b9c1e3f5a7d9c012',
    registeredFingerprint: null,
    protocol: 'AK-Adapter/1.2', capabilities: ['exec', 'workspace', 'git-read'],
    os: 'linux/amd64', probeTime: '09:40:55', drift: false,
  },
  {
    id: 'python-executor', provider: 'python-executor', registered: true,
    observedFingerprint: 'sha256:7b2c4e6f8a0d2f4c6e8a0b2d4f6a8c0e2f4d6b8a0c2e89f1',
    registeredFingerprint: 'sha256:7b2c4e6f8a0d2f4c6e8a0b2d4f6a8c0e2f4d6b8a0c2e89f1',
    protocol: 'AK-Adapter/1.2', capabilities: ['exec', 'workspace'],
    os: 'linux/amd64', probeTime: '09:41:02', drift: false,
  },
  {
    id: 'node-executor', provider: 'node-executor', registered: true,
    observedFingerprint: 'sha256:c9d3f7a1b5e2c8d4f6a2b8c4e0f2d6a8b0c4e6f8a2d4c6e8',
    registeredFingerprint: 'sha256:AAAA4e6f8a0d2f4c6e8a0b2d4f6a8c0e2f4d6b8a0c2e89f1',
    protocol: 'AK-Adapter/1.2', capabilities: ['exec', 'workspace', 'git-read'],
    os: 'linux/amd64', probeTime: '09:41:10', drift: true,
  },
];

export function AdapterBuildsScreen({ isOffline = false }: { isOffline?: boolean }) {
  const [adapters, setAdapters] = useState(INITIAL_ADAPTERS);
  const [probing, setProbing] = useState<string | null>(null);
  const [registerTarget, setRegisterTarget] = useState<AdapterBuild | null>(null);
  const [opState, setOpState] = useState<Record<string, 'requested' | 'done'>>({});

  const runProbe = (id: string) => {
    if (isOffline) return;
    setProbing(id);
    window.setTimeout(() => {
      setProbing(null);
      setAdapters(prev => prev.map(a => id === 'all' || a.id === id ? { ...a, probeTime: '09:58:20' } : a));
    }, 1200);
  };

  const confirmRegister = (adapter: AdapterBuild) => {
    setRegisterTarget(null);
    setOpState(prev => ({ ...prev, [adapter.id]: 'requested' }));
    window.setTimeout(() => {
      setOpState(prev => ({ ...prev, [adapter.id]: 'done' }));
      setAdapters(prev => prev.map(item => item.id === adapter.id ? {
        ...item,
        registered: true,
        registeredFingerprint: item.observedFingerprint,
        drift: false,
      } : item));
    }, 1200);
  };

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-xl font-semibold text-[#172033]">Adapter Builds</h1>
            <p className="text-[13px] text-[#475569] mt-0.5">
              Installation-scoped adapter registry. Observed fingerprints must be explicitly admitted before they are trusted.
            </p>
          </div>
          <Button intent="secondary" icon={<RefreshCw size={13} aria-hidden />} loading={probing === 'all'} disabled={isOffline} onClick={() => runProbe('all')}>Re-probe all</Button>
        </div>

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between">
            <h2 className="text-sm font-semibold text-[#172033]">Registry</h2>
            <span className="text-[12px] text-[#475569]">
              {adapters.filter(a => a.registered).length}/{adapters.length} registered
            </span>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {adapters.map(adapter => {
              const isRegistered = adapter.registered;
              const isProbing = probing === adapter.id || probing === 'all';
              const op = opState[adapter.id];
              const hasDrift = adapter.drift && isRegistered;

              return (
                <div key={adapter.id} className="px-5 py-5">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex-1 min-w-0">
                      {/* Header row */}
                      <div className="flex items-center gap-2 flex-wrap">
                        <Wrench size={15} className="text-[#475569]" aria-hidden />
                        <span className="text-sm font-semibold text-[#172033]">{adapter.provider}</span>
                        <Badge
                          label={isRegistered ? 'REGISTERED' : 'UNREGISTERED'}
                          intent={isRegistered ? 'success' : 'danger'}
                        />
                        {hasDrift && <Badge label="DRIFT" intent="warning" />}
                        <span className="text-[12px] text-[#475569]">{adapter.os} · {adapter.protocol}</span>
                      </div>

                      {/* Health text — never dot-only */}
                      <p className="flex items-center gap-1.5 text-[12px] mt-1.5 font-medium" style={{ color: isRegistered && !hasDrift ? '#166534' : hasDrift ? '#92400E' : '#991B1B' }}>
                        {isRegistered && !hasDrift
                          ? <><CheckCircle2 size={12} aria-hidden /> Registered — fingerprint admitted and matching</>
                          : hasDrift
                            ? <><AlertTriangle size={12} aria-hidden /> Warning — observed fingerprint differs from registered</>
                            : <><XCircle size={12} aria-hidden /> Unregistered — observed fingerprint is not admitted</>}
                      </p>

                      {/* Fingerprints */}
                      <div className="mt-3 space-y-2 font-mono text-[12px]">
                        <div className="flex items-start gap-2">
                          <span className="text-[#475569] w-40 flex-shrink-0 text-[12px]">Observed fingerprint</span>
                          <span className={`text-[#172033] break-all ${!isRegistered ? 'italic text-[#991B1B]' : ''}`}>
                            {adapter.observedFingerprint}
                          </span>
                        </div>
                        <div className="flex items-start gap-2">
                          <span className="text-[#475569] w-40 flex-shrink-0 text-[12px]">Registered fingerprint</span>
                          {isRegistered && adapter.registeredFingerprint ? (
                            <span className={`break-all ${hasDrift ? 'text-[#92400E]' : 'text-[#166534]'}`}>
                              {adapter.registeredFingerprint}
                            </span>
                          ) : isRegistered ? (
                            <span className="text-[#166534]">{adapter.observedFingerprint}</span>
                          ) : (
                            <span className="text-[#991B1B] italic not-italic">— not registered; observed is not admitted</span>
                          )}
                        </div>
                      </div>

                      {/* Capabilities */}
                      <div className="flex items-center gap-1.5 mt-2 flex-wrap">
                        <span className="text-[12px] text-[#475569]">Capabilities:</span>
                        {adapter.capabilities.map(c => (
                          <span key={c} className="text-[12px] px-1.5 py-0.5 bg-[#F1F5F9] text-[#475569] rounded-[4px] border border-[#CBD5E1] font-mono">{c}</span>
                        ))}
                        <span className="text-[12px] text-[#475569] ml-auto">probed {isProbing ? 'now…' : adapter.probeTime}</span>
                      </div>

                      {/* Operation notice */}
                      {op === 'requested' && <div className="mt-2"><OperationNotice state="Requested" message="Registration submitted" ref={`op-reg-${adapter.id}`} /></div>}
                      {op === 'done' && <div className="mt-2"><OperationNotice state="Completed" message="New immutable build registered; existing registrations were not overwritten." ref={`op-reg-${adapter.id}`} /></div>}
                    </div>

                    {/* Actions */}
                    <div className="flex flex-col gap-2 flex-shrink-0">
                      <Button intent="secondary" size="compact" loading={isProbing} disabled={isOffline} onClick={() => runProbe(adapter.id)}
                        icon={!isProbing ? <RefreshCw size={12} aria-hidden /> : undefined}>
                        Probe
                      </Button>
                      {!isRegistered && !op && (
                        <Button intent="primary" size="compact" disabled={isOffline} onClick={() => setRegisterTarget(adapter)}>Register</Button>
                      )}
                      {hasDrift && (
                        <Button intent="quiet" size="compact" disabled={isOffline} onClick={() => setRegisterTarget(adapter)}>Register Observed Build</Button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>

      {/* Registration confirmation dialog */}
      {registerTarget && (
        <Dialog
          title={registerTarget.registered ? `Register Observed Build: ${registerTarget.provider}` : `Register Adapter: ${registerTarget.provider}`}
          description="Review the observed candidate. An observed fingerprint is not trusted until explicitly admitted here."
          onClose={() => setRegisterTarget(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setRegisterTarget(null)}>Cancel</Button>
              <Button intent="primary" disabled={isOffline} onClick={() => confirmRegister(registerTarget)}>
                Confirm Registration
              </Button>
            </>
          }
        >
          <div className="space-y-3 text-[13px]">
            <div className="p-3 rounded-[6px] bg-[#FEF3C7] border border-[#FCD34D] text-[12px] text-[#92400E]">
              <AlertTriangle size={12} aria-hidden className="inline mr-1" />
              Verify the fingerprint matches the expected artifact. Registration creates a new immutable build; execution is eligible only when a workflow pins that exact build.
            </div>
            <dl className="space-y-2">
              {[
                ['Provider',    registerTarget.provider],
                ['Protocol',    registerTarget.protocol],
                ['OS / Arch',   registerTarget.os],
              ].map(([k, v]) => (
                <div key={k} className="flex gap-3"><dt className="text-[#475569] w-28">{k}</dt><dd className="font-mono text-[12px]">{v}</dd></div>
              ))}
              <div className="flex gap-3 items-start">
                <dt className="text-[#475569] w-28 flex-shrink-0">Observed fp</dt>
                <dd className="font-mono text-[12px] break-all">{registerTarget.observedFingerprint}</dd>
              </div>
            </dl>
          </div>
        </Dialog>
      )}
    </div>
  );
}
