import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { doctor, listAdapterBuilds, type GetAdapterBuildResponse } from '../api/generated';
import { Badge, Button, EmptyState, Skeleton, ToastViewport, useToasts, type BadgeIntent } from '../components/ui';
import { Plus, RefreshCw } from '../components/icons';
import { AdapterProbeDialog } from './AdapterProbeDialog';

/**
 * The real per-check wire shape GET /doctor's own `checks[]` carries
 * (internal/delivery/httpapi/doctor/dto.go's own `checkResultWire`,
 * internal/app/doctor/doctor.go's own `Status`/`Category` enums) — hand-
 * declared here because apicontract's generator only reflects ONE level of
 * a top-level response's own fields; a slice element's own shape is never
 * expanded (apicontract's own "why a custom JSON contract, not full
 * OpenAPI 3.x" doc comment), so `DoctorResponse.checks` comes back from
 * the generated client as `unknown[]`.
 */
export interface DoctorCheck {
  name: string;
  category: 'LIVENESS' | 'READINESS' | 'CAPABILITY';
  status: 'HEALTHY' | 'DEGRADED' | 'BLOCKED';
  detail?: string;
  remediation?: string;
}

// internal/app/doctor's own package doc comment names these three
// questions verbatim ("Liveness: is this process running...", "Readiness:
// can this instance actually do its job...", "Capability: what optional
// capability did we OBSERVE") — the exact grouping this screen renders,
// not an invented taxonomy.
const CATEGORY_ORDER: DoctorCheck['category'][] = ['LIVENESS', 'READINESS', 'CAPABILITY'];
const CATEGORY_LABEL: Record<DoctorCheck['category'], string> = {
  LIVENESS: 'Liveness',
  READINESS: 'Readiness',
  CAPABILITY: 'Capability',
};

const STATUS_INTENT: Record<DoctorCheck['status'], BadgeIntent> = {
  HEALTHY: 'success',
  DEGRADED: 'warning',
  BLOCKED: 'danger',
};

/** "provider:anthropic" -> "provider anthropic"; "app_config" -> "app config" — never a hardcoded name-to-label table, since the real check set is installation-dependent (one provider: check per configured provider). */
function formatCheckName(name: string): string {
  return name.replace(':', ' ').replace(/_/g, ' ');
}

export function DoctorScreen({ isOffline = false }: { isOffline?: boolean }) {
  const [probeOpen, setProbeOpen] = useState(false);
  const { toasts, show, dismiss } = useToasts();
  const doctorQuery = useQuery({
    queryKey: ['doctor'],
    queryFn: () => doctor(),
    enabled: !isOffline,
  });
  const buildsQuery = useQuery({
    queryKey: ['adapterBuilds'],
    queryFn: () => listAdapterBuilds(),
    enabled: !isOffline,
  });

  const checks = (doctorQuery.data?.checks ?? []) as unknown as DoctorCheck[];
  const builds = (buildsQuery.data?.builds ?? []) as unknown as GetAdapterBuildResponse[];
  const status = doctorQuery.data?.status as DoctorCheck['status'] | undefined;

  const grouped = CATEGORY_ORDER.map(category => ({
    category,
    checks: checks.filter(c => c.category === category),
  })).filter(group => group.checks.length > 0);

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-4xl mx-auto space-y-6">
        <div className="flex items-start justify-between">
          <div>
            <h1 className="text-xl font-semibold text-[#172033]">Doctor</h1>
            <p className="text-sm text-[#5D697A] mt-0.5">System readiness report for this installation.</p>
          </div>
          <Button
            intent="secondary"
            disabled={isOffline}
            loading={doctorQuery.isFetching}
            onClick={() => { doctorQuery.refetch(); buildsQuery.refetch(); }}
            icon={<RefreshCw size={13} aria-hidden />}
          >
            Re-run all checks
          </Button>
        </div>

        {doctorQuery.isPending && (
          <div className="space-y-2" aria-hidden>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        )}

        {doctorQuery.isError && (
          <div role="alert" className="flex items-center gap-3 p-3 rounded-[8px] bg-[#FEE2E2] border border-[#FCA5A5]">
            <span className="text-sm font-medium text-[#991B1B]">
              Could not reach this installation's own Doctor endpoint. {doctorQuery.error instanceof Error ? doctorQuery.error.message : ''}
            </span>
          </div>
        )}

        {status && (
          <div
            role="status"
            className={`flex items-center gap-3 p-3 rounded-[8px] border ${
              status === 'HEALTHY' ? 'bg-[#DCFCE7] border-[#86EFAC] text-[#166534]'
              : status === 'DEGRADED' ? 'bg-[#FEF3C7] border-[#FCD34D] text-[#92400E]'
              : 'bg-[#FEE2E2] border-[#FCA5A5] text-[#991B1B]'
            }`}
          >
            <span className="text-sm font-medium">
              {status === 'HEALTHY' && 'All checks passing — installation is ready.'}
              {status === 'DEGRADED' && 'Some checks are degraded — review before relying on affected capabilities.'}
              {status === 'BLOCKED' && 'A check is blocked — this installation cannot fully operate until resolved.'}
            </span>
          </div>
        )}

        {doctorQuery.data?.restartRequired && (
          <div role="status" className="flex items-center gap-3 p-3 rounded-[8px] bg-[#DBEAFE] border border-[#93C5FD] text-[#1E40AF]">
            <span className="text-sm font-medium">A configuration change is pending — restart this installation for it to take effect.</span>
          </div>
        )}

        {grouped.map(group => (
          <div key={group.category} className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
              <h2 className="text-sm font-semibold text-[#172033]">{CATEGORY_LABEL[group.category]}</h2>
            </div>
            <div className="divide-y divide-[#ECEFF4]">
              {group.checks.map(check => (
                <div key={check.name} className="flex items-start gap-4 px-5 py-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium text-[#172033]">{formatCheckName(check.name)}</span>
                      <Badge label={check.status} intent={STATUS_INTENT[check.status]} />
                    </div>
                    {check.detail && <p className="text-sm text-[#5D697A] mt-0.5">{check.detail}</p>}
                    {check.remediation && (
                      <p className="text-xs text-[#92400E] mt-1 bg-[#FEF3C7] border border-[#FCD34D] rounded-[4px] px-2 py-1">{check.remediation}</p>
                    )}
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between">
            <h2 className="text-sm font-semibold text-[#172033]">Registered Adapter Builds</h2>
            <div className="flex items-center gap-3">
              <span className="text-[12px] text-[#475569]">{builds.length} registered</span>
              <Button intent="secondary" size="compact" disabled={isOffline} icon={<Plus size={13} aria-hidden />} onClick={() => setProbeOpen(true)}>
                Probe new build
              </Button>
            </div>
          </div>
          {builds.length === 0 ? (
            <EmptyState
              title="No adapter builds registered yet"
              description={'A configured provider\'s observed executable appears above under Capability. Use "Probe new build" to review its real fingerprint and register it (ADR-022).'}
            />
          ) : (
            <div className="divide-y divide-[#ECEFF4]">
              {builds.map(build => (
                <div key={build.id} className="px-5 py-4">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-medium text-[#172033]">{build.providerKey}</span>
                    <Badge label="REGISTERED" intent="success" />
                    <span className="text-[12px] text-[#475569]">{build.os} · {build.protocolVersion}</span>
                  </div>
                  <div className="mt-2 flex items-center gap-2">
                    <span className="text-xs text-[#5D697A] w-36 flex-shrink-0">Executable hash</span>
                    <span className="font-mono text-xs text-[#172033] truncate">{build.executableContentHash}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {probeOpen && (
        <AdapterProbeDialog
          onClose={() => setProbeOpen(false)}
          onRegistered={toast => show(toast)}
        />
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}
