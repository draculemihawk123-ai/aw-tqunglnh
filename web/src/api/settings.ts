/**
 * Hand-declared wire shapes for GET/PUT /settings/safe — apicontract's
 * generator has no way to describe `desired`/`effective` beyond `unknown`
 * (internal/delivery/httpapi/safesettings/dto.go's own `desiredWire`/
 * `effectiveWire` are free-form allow-listed documents, not a schema the
 * shallow generator inspects), the same "narrow `unknown` at the call site"
 * convention every sibling API-gap file this session already established.
 * Field-for-field mirror of that package's own DTOs.
 *
 * `updateSafeSettings` is entirely excluded from the generated client
 * (`tsclient.go`'s own `customJSONShapeOperations` set): its real request
 * body comes from `internal/domain/safesettings.SafeSettings`'s own custom
 * `MarshalJSON`/`UnmarshalJSON` (camelCase field names, `evidenceRetention`
 * as a Go-syntax duration STRING), which Go-reflection-based generation
 * cannot see through — a generated function would send PascalCase field
 * names with a raw nanosecond number instead. `updateSafeSettings` below is
 * the real hand-written caller in its place, using the actual wire
 * contract read directly from the server's own source.
 */

import { ApiError } from './generated';

/** internal/app/safesettings/startup.go's own FieldSource — which precedence layer ultimately won. */
export type FieldSource = 'default' | 'file' | 'sqlite' | 'environment' | 'flag';

export interface SafeSettingsDesired {
  managedWorkspaceRoot: string;
  managedArtifactRoot: string;
  /** Go-syntax duration string (e.g. "168h0m0s") — round-trips through time.ParseDuration on the server. */
  evidenceRetention: string;
  processOutputLimit: number;
  providerExecutablePath: string;
  providerDefaultModel: string;
  /**
   * ALWAYS server-side redacted on the way out (matcher.Tagged(redact.Secret, ...)
   * — internal/delivery/httpapi/safesettings/dto.go's own maskedDesiredWire
   * doc comment: "this task's own 'never echoed back in cleartext' line
   * applies to every response"). Never re-display this value as if it were
   * a real credential — treat it as an opaque, already-masked reference on
   * read; a real, operator-typed reference (e.g. "env:MY_KEY") is the only
   * thing this UI ever sends back on write.
   */
  providerCredentialRef: string;
}

export interface StringFieldEffective {
  effective: string;
  source: FieldSource;
  maskedByStartupSource: FieldSource | '';
}

export interface DurationFieldEffective {
  effective: string;
  source: FieldSource;
  maskedByStartupSource: FieldSource | '';
}

export interface IntFieldEffective {
  effective: number;
  source: FieldSource;
  maskedByStartupSource: FieldSource | '';
}

export interface SafeSettingsEffective {
  managedWorkspaceRoot: StringFieldEffective;
  managedArtifactRoot: StringFieldEffective;
  evidenceRetention: DurationFieldEffective;
  processOutputLimit: IntFieldEffective;
  providerExecutablePath: StringFieldEffective;
  providerDefaultModel: StringFieldEffective;
  providerCredentialRef: StringFieldEffective;
}

export interface SafeSettingsDetail {
  desired: SafeSettingsDesired;
  version: number;
  updatedAt: string;
  updatedBy: string;
  restartRequired: boolean;
  effective: SafeSettingsEffective;
}

export interface UpdateSafeSettingsOptions {
  token: string;
  idempotencyKey: string;
  /** The server's ETagFromVersion-wrapped version string, e.g. `"3"` (with the surrounding quotes). */
  ifMatch: string;
}

/**
 * updateSafeSettings calls PUT /settings/safe directly (never through the
 * generated client — see this file's own doc comment), sending exactly
 * `desired`'s own 7 fields with the real camelCase wire tags and
 * `evidenceRetention` left as whatever Go-duration-syntax string the caller
 * supplies (this function never re-validates or reformats it — the
 * server's own strict decoder is the real authority, and its real error
 * message is what a caller should show, never a fabricated client-side
 * rule).
 */
export async function updateSafeSettings(desired: SafeSettingsDesired, opts: UpdateSafeSettingsOptions): Promise<SafeSettingsDetail> {
  const url = new URL('/settings/safe', window.location.origin);
  const res = await fetch(url, {
    method: 'PUT',
    headers: {
      'X-Aw-Session-Token': opts.token,
      'Idempotency-Key': opts.idempotencyKey,
      'If-Match': opts.ifMatch,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(desired),
  });
  if (!res.ok) {
    const errBody = await res.json().catch(() => null);
    throw new ApiError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? res.statusText, errBody?.error?.details);
  }
  return res.json();
}

export const SAFE_SETTINGS_FIELDS: { key: keyof SafeSettingsDesired; label: string }[] = [
  { key: 'managedWorkspaceRoot', label: 'Managed workspace root' },
  { key: 'managedArtifactRoot', label: 'Managed artifact root' },
  { key: 'evidenceRetention', label: 'Evidence retention' },
  { key: 'processOutputLimit', label: 'Process output limit' },
  { key: 'providerExecutablePath', label: 'Provider executable path' },
  { key: 'providerDefaultModel', label: 'Provider default model' },
  { key: 'providerCredentialRef', label: 'Provider credential reference' },
];
