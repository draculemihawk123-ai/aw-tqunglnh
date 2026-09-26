import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Badge, Button, InlineError, Skeleton, TextField, ToastViewport, useToasts } from '../components/ui';
import { ApiError, getSafeSettings } from '../api/generated';
import { withSessionToken, getSessionToken } from '../api/session';
import { SAFE_SETTINGS_FIELDS, updateSafeSettings } from '../api/settings';
import type { SafeSettingsDesired, SafeSettingsDetail } from '../api/settings';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

function fieldSourceBadge(source: string, masked: string) {
  if (masked) {
    return <Badge label={`MASKED BY ${masked.toUpperCase()}`} intent="warning" />;
  }
  if (source === 'sqlite') return <Badge label="THIS VALUE" intent="success" />;
  return <Badge label={source.toUpperCase()} intent="neutral" />;
}

/**
 * blankCredentialRef strips the server's own always-redacted
 * providerCredentialRef (matcher.Tagged(redact.Secret, ...) — real even
 * when the underlying reference is empty) out of a freshly-loaded desired
 * document before it ever becomes editable draft state. Found live, via
 * manual verification: pre-filling the draft with that masked placeholder
 * verbatim means an UNMODIFIED save of that one field always fails real
 * server validation ("contains disallowed character '['... never a raw
 * secret value") — the masked marker is not a valid reference shape either.
 * Since UpdateSafeSettings always replaces the full desired document
 * (never a per-field patch), there is no way for this UI to silently
 * "keep whatever was already configured" for this one field; the operator
 * must always retype the real reference to keep it, and this screen says so.
 */
function blankCredentialRef(desired: SafeSettingsDesired): SafeSettingsDesired {
  return { ...desired, providerCredentialRef: '' };
}

/**
 * SettingsScreen — V7-16's own real "Settings và run diagnostics" (Screen
 * 13 row 1/2, docs/design/09-v7-alpha-ui.md V7-16). Real getSafeSettings/
 * updateSafeSettings — a real, previously-broken generator gap found while
 * building this: updateSafeSettings' own RequestSchema is the real
 * safesettings.SafeSettings domain type, but that type's real wire shape
 * comes entirely from a custom MarshalJSON/UnmarshalJSON pair Go-reflection
 * generation cannot see through (PascalCase field names, EvidenceRetention
 * as a raw number instead of the real duration string) — see
 * web/src/api/settings.ts's own hand-written updateSafeSettings, the real
 * caller in the generated one's place.
 *
 * The full desired document is always submitted together on Save
 * (UpdateSafeSettings' own "Store full desired document" contract — never
 * a per-field patch), gated behind a real If-Match built from the
 * currently-loaded version; a stale save (another session updated first)
 * surfaces as a real 409, shown honestly rather than silently retried.
 * ProviderCredentialRef is never displayed as a real secret: the server
 * always returns it already redacted (matcher.Tagged(redact.Secret, ...),
 * even when the underlying reference is empty). Found live, via manual
 * verification, that pre-filling the draft with that masked marker
 * verbatim makes an unmodified save of this one field always fail real
 * server validation (the marker itself is not a valid reference shape) —
 * blankCredentialRef below keeps this one field out of the draft entirely,
 * and the operator always retypes the real reference to keep it (Save
 * always replaces the full document, never a per-field patch, so leaving
 * it blank really does clear it — this screen says so plainly rather than
 * silently).
 */
export function SettingsScreen({ isOffline = false }: { isOffline?: boolean }) {
  const queryClient = useQueryClient();
  const { toasts, show, dismiss } = useToasts();
  const [draft, setDraft] = useState<SafeSettingsDesired | null>(null);

  const settingsQuery = useQuery({
    queryKey: ['safeSettings'],
    queryFn: async () => (await getSafeSettings(withSessionToken())) as unknown as SafeSettingsDetail,
    enabled: !isOffline,
  });

  useEffect(() => {
    if (settingsQuery.data && draft === null) setDraft(blankCredentialRef(settingsQuery.data.desired));
  }, [settingsQuery.data, draft]);

  const saveMutation = useMutation({
    mutationFn: async () => {
      // Fresh recheck immediately before dispatch — the same "never trust a
      // version read at page-load time" discipline every other mutation in
      // this app follows (V7-11's own Cancel Run pattern): reload the real
      // current version right before submitting, rather than the one this
      // screen loaded whenever it first mounted.
      const fresh = (await getSafeSettings(withSessionToken())) as unknown as SafeSettingsDetail;
      return updateSafeSettings(draft!, {
        token: getSessionToken(), idempotencyKey: crypto.randomUUID(), ifMatch: `"${fresh.version}"`,
      });
    },
    onSuccess: result => {
      queryClient.setQueryData(['safeSettings'], result);
      setDraft(blankCredentialRef(result.desired));
      show({ intent: 'success', message: result.restartRequired ? 'Settings saved — a restart is required for the new values to take effect.' : 'Settings saved.' });
    },
    // A failure is shown ONLY via the real inline InlineError below (with
    // its own Retry) — never also as a toast for the identical message,
    // which would just report the same failure twice.
  });

  if (isOffline) {
    return <div className="flex-1 p-6"><p className="text-[13px] text-[#475569]">Reconnect to view and edit settings.</p></div>;
  }
  if (settingsQuery.isPending || draft === null) {
    return <div className="flex-1 p-6 space-y-2" aria-hidden><Skeleton className="h-24 w-full" /></div>;
  }
  if (settingsQuery.isError) {
    return <div className="flex-1 p-6"><InlineError {...apiErrorMessage(settingsQuery.error)} onRetry={() => settingsQuery.refetch()} /></div>;
  }
  const detail = settingsQuery.data!;
  const dirty = JSON.stringify(draft) !== JSON.stringify(detail.desired);

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-3xl mx-auto space-y-6">
        <div>
          <h1 className="text-xl font-semibold text-[#172033]">Settings</h1>
          <p className="text-sm text-[#5D697A] mt-0.5">Safe, allow-listed settings. Secret values are never stored or displayed — use a reference only.</p>
        </div>

        {detail.restartRequired && (
          <div role="status" className="p-3 rounded-[8px] bg-[#FEF3C7] border border-[#FCD34D] text-[13px] text-[#92400E]">
            A restart is required for the currently-saved settings to take effect — Alpha has no live-reload path.
          </div>
        )}

        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between">
            <div>
              <h2 className="text-sm font-semibold text-[#172033]">Desired values</h2>
              <p className="text-[12px] text-[#5D697A] mt-0.5">version <span className="font-mono">{detail.version}</span> · last updated by <span className="font-mono">{detail.updatedBy || '—'}</span></p>
            </div>
          </div>
          <div className="divide-y divide-[#ECEFF4]">
            {SAFE_SETTINGS_FIELDS.map(({ key, label }) => {
              const effective = detail.effective[key];
              return (
                <div key={key} className="px-5 py-4">
                  <div className="flex items-center gap-2 mb-1.5">
                    <label htmlFor={key} className="text-sm font-medium text-[#172033]">{label}</label>
                    {fieldSourceBadge(effective.source, effective.maskedByStartupSource)}
                  </div>
                  <TextField
                    id={key}
                    label={label}
                    mono={key !== 'processOutputLimit'}
                    value={String(draft[key])}
                    onChange={v => setDraft(cur => (cur ? ({
                      ...cur, [key]: key === 'processOutputLimit' ? (Number(v) || 0) : v,
                    } as SafeSettingsDesired) : cur))}
                  />
                  <p className="text-[12px] text-[#5D697A] mt-1">
                    effective next restart: <span className="font-mono">{String(effective.effective)}</span> (from {effective.source})
                  </p>
                  {key === 'providerCredentialRef' && (
                    <p className="text-[12px] text-[#92400E] mt-1">
                      Never round-trips a real secret value — always blank here on load. Saving with this field left blank clears any
                      currently-configured reference (Save always replaces the full document); retype the real reference to keep it.
                    </p>
                  )}
                </div>
              );
            })}
          </div>
        </div>

        {saveMutation.isError && <InlineError {...apiErrorMessage(saveMutation.error)} onRetry={() => saveMutation.mutate()} />}

        <div className="flex justify-end gap-2 pb-4">
          <Button intent="secondary" disabled={!dirty} onClick={() => setDraft(detail.desired)}>Discard Changes</Button>
          <Button intent="primary" disabled={!dirty} loading={saveMutation.isPending} onClick={() => saveMutation.mutate()}>Save Settings</Button>
        </div>
      </div>
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}
