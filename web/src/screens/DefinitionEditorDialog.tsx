import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import {
  validateDefinitionDraft, validateProjectDefinitionDraft,
  publishDefinitionVersion, publishProjectDefinitionVersion, ApiError,
} from '../api/generated';
import { DEFINITION_KINDS } from '../api/definitions';
import type { DefinitionKind, DefinitionView, VersionFieldsView } from '../api/definitions';
import { withSessionToken } from '../api/session';
import { Button, CopyableId, Dialog, InlineError, Select } from '../components/ui';

interface PinDraft {
  key: string;
  kind: DefinitionKind;
  definitionId: string;
  versionId: string;
}

function newPinDraft(): PinDraft {
  return { key: `pin-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`, kind: 'BLOCK', definitionId: '', versionId: '' };
}

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/** A stable string identity of exactly the content this request body would publish — used to tell whether the operator has edited anything since the last successful Validate, matching V7-08's own "confirm SourceHash + CompiledSnapshotHash/dependency pins" line: Publish must confirm the SAME content Validate already proved compiles, never a silently-drifted one. */
function requestSignature(content: string, format: string, pins: PinDraft[]): string {
  return JSON.stringify({ content, format, pins: pins.map(p => ({ kind: p.kind, definitionId: p.definitionId, versionId: p.versionId })) });
}

/**
 * DefinitionEditorDialog — V7-08's own "bounded editor, format-preserving
 * draft local-only... validate locations, dependency pins, confirm
 * SourceHash + CompiledSnapshotHash/dependency pins" line. The draft
 * (content/format/dependency pins) lives ONLY in this component's own
 * state — never auto-saved anywhere, never sent to the server until the
 * operator explicitly clicks Validate or Publish, and entirely discarded
 * if the dialog is closed without publishing. No visual graph editing (the
 * task's own explicit "Không làm" line): content is a plain bounded text
 * area, the document's own raw JSON/YAML text.
 */
export function DefinitionEditorDialog({ definition, scope, projectId, onClose, onPublished }: {
  definition: DefinitionView; scope: 'global' | 'project'; projectId?: string;
  onClose: () => void; onPublished: (version: VersionFieldsView) => void;
}) {
  const isWorkflow = definition.kind === 'WORKFLOW';
  const [content, setContent] = useState('');
  const [format, setFormat] = useState<'json' | 'yaml'>('json');
  const [pins, setPins] = useState<PinDraft[]>([]);
  const [step, setStep] = useState<'editing' | 'confirm'>('editing');
  const [validated, setValidated] = useState<{ signature: string; result: VersionFieldsView } | null>(null);
  const [diagnostics, setDiagnostics] = useState<{ field: string; message: string }[] | null>(null);

  const currentSignature = requestSignature(content, format, pins);
  const isValidatedCurrent = validated?.signature === currentSignature;

  function requestBody() {
    return {
      content,
      format: isWorkflow ? undefined : format,
      dependencies: isWorkflow ? undefined : pins.filter(p => p.definitionId.trim() && p.versionId.trim())
        .map(p => ({ kind: p.kind, definitionId: p.definitionId.trim(), versionId: p.versionId.trim() })),
    };
  }

  const validateMutation = useMutation({
    mutationFn: () => {
      const body = requestBody();
      return (scope === 'project' && projectId
        ? validateProjectDefinitionDraft(projectId, definition.kind, definition.id, body, withSessionToken())
        : validateDefinitionDraft(definition.kind, definition.id, body, withSessionToken())) as unknown as Promise<VersionFieldsView>;
    },
    onSuccess: result => { setValidated({ signature: currentSignature, result }); setDiagnostics(null); },
    onError: err => {
      setValidated(null);
      setDiagnostics(err instanceof ApiError && err.details ? err.details : [{ field: '', message: apiErrorMessage(err).message }]);
    },
  });

  const publishMutation = useMutation({
    mutationFn: () => {
      const body = requestBody();
      return (scope === 'project' && projectId
        ? publishProjectDefinitionVersion(projectId, definition.kind, definition.id, body, withSessionToken())
        : publishDefinitionVersion(definition.kind, definition.id, body, withSessionToken())) as unknown as Promise<VersionFieldsView>;
    },
    onSuccess: onPublished,
  });

  function addPin() { setPins(current => [...current, newPinDraft()]); }
  function removePin(key: string) { setPins(current => current.filter(p => p.key !== key)); }
  function updatePin(key: string, patch: Partial<PinDraft>) {
    setPins(current => current.map(p => (p.key === key ? { ...p, ...patch } : p)));
  }

  if (step === 'confirm' && validated && isValidatedCurrent) {
    const v = validated.result;
    return (
      <Dialog
        title={`Publish ${definition.name}`}
        description="Publishing creates a new, immutable Version. Review both hashes and dependency pins before confirming."
        onClose={onClose}
        isDurable={publishMutation.isPending}
        actions={
          <>
            <Button intent="secondary" onClick={() => setStep('editing')} disabled={publishMutation.isPending}>Back</Button>
            <Button intent="primary" loading={publishMutation.isPending} onClick={() => publishMutation.mutate()}>Confirm Publish</Button>
          </>
        }
      >
        <div className="space-y-3 text-sm">
          <div className="space-y-1.5">
            <div className="flex gap-3"><span className="text-[#5D697A] w-40">Definition</span><CopyableId value={definition.id} short={definition.name} /></div>
            <div className="flex gap-3"><span className="text-[#5D697A] w-40">SourceHash</span><span className="font-mono text-xs break-all">{v.sourceHash}</span></div>
            <div className="flex gap-3"><span className="text-[#5D697A] w-40">CompiledSnapshotHash</span><span className="font-mono text-xs break-all">{v.compiledHash}</span></div>
          </div>
          <div className="pt-2 border-t border-[#ECEFF4]">
            <div className="text-xs font-medium text-[#172033] mb-1.5">Dependency Pins ({v.dependencies.pins?.length ?? 0})</div>
            <div className="space-y-1 font-mono text-xs text-[#5D697A]">
              {(v.dependencies.pins?.length ?? 0) > 0
                ? v.dependencies.pins!.map(p => <div key={`${p.kind}:${p.definitionId}:${p.versionId}`}>{p.kind}:{p.definitionId}@{p.versionId}</div>)
                : <div>No dependency pins.</div>}
            </div>
          </div>
          {publishMutation.isError && <InlineError {...apiErrorMessage(publishMutation.error)} />}
        </div>
      </Dialog>
    );
  }

  return (
    <Dialog
      title={`Edit ${definition.name}`}
      description={`${definition.kind} · draft is local to this dialog only, never auto-saved.`}
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose}>Close</Button>
          <Button intent="secondary" loading={validateMutation.isPending} onClick={() => validateMutation.mutate()}>Validate</Button>
          <Button intent="primary" disabled={!isValidatedCurrent} title={isValidatedCurrent ? undefined : 'Validate the current draft first'} onClick={() => setStep('confirm')}>Publish…</Button>
        </>
      }
    >
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <span className="text-xs font-medium text-[#5D697A]">Content</span>
          {!isWorkflow && (
            <div className="flex rounded-[6px] border border-[#CDD5DF] overflow-hidden ml-auto">
              {(['json', 'yaml'] as const).map(f => (
                <button key={f} onClick={() => setFormat(f)} aria-pressed={format === f}
                  className={`px-2.5 py-1 text-xs font-medium uppercase transition-colors ${format === f ? 'bg-[#3659E3] text-white' : 'bg-white text-[#5D697A] hover:bg-[#F3F5F8]'}`}>
                  {f}
                </button>
              ))}
            </div>
          )}
          {isWorkflow && <span className="text-[11px] text-[#5D697A] ml-auto">json only</span>}
        </div>
        <textarea
          value={content}
          onChange={e => setContent(e.target.value)}
          rows={12}
          spellCheck={false}
          aria-label="Document content"
          placeholder={isWorkflow ? '{ "schemaVersion": "1", "nodes": [...], "edges": [...] }' : '{ ... }'}
          className="w-full font-mono text-[12px] bg-[#FBFCFE] text-[#172033] border border-[#CDD5DF] rounded-[6px] p-3 resize-y outline-none focus:border-[#3659E3]"
        />

        {!isWorkflow && (
          <div>
            <div className="flex items-center justify-between mb-1.5">
              <span className="text-xs font-medium text-[#5D697A]">Dependency Pins</span>
              <Button size="compact" intent="quiet" onClick={addPin}>+ Add pin</Button>
            </div>
            <div className="space-y-2">
              {pins.map(pin => (
                <div key={pin.key} className="flex items-center gap-2">
                  <Select label="Kind" value={pin.kind} onChange={v => updatePin(pin.key, { kind: v as DefinitionKind })}
                    options={DEFINITION_KINDS.map(k => ({ value: k, label: k }))} />
                  <input aria-label="Pin definition ID" value={pin.definitionId} onChange={e => updatePin(pin.key, { definitionId: e.target.value })}
                    placeholder="definitionId" className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px] font-mono flex-1 min-w-0" />
                  <input aria-label="Pin version ID" value={pin.versionId} onChange={e => updatePin(pin.key, { versionId: e.target.value })}
                    placeholder="versionId" className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px] font-mono flex-1 min-w-0" />
                  <Button size="compact" intent="quiet" onClick={() => removePin(pin.key)} aria-label="Remove pin">✕</Button>
                </div>
              ))}
              {pins.length === 0 && <p className="text-[12px] text-[#5D697A]">No dependency pins declared.</p>}
            </div>
          </div>
        )}

        {diagnostics && (
          <div role="alert" className="space-y-1.5">
            {diagnostics.map((d, i) => (
              <div key={i} className="flex gap-3 p-2.5 rounded-[6px] bg-[#FEE2E2] border border-[#FCA5A5] text-[12px]">
                {d.field && <span className="font-mono text-[#991B1B] font-medium flex-shrink-0">{d.field}</span>}
                <span className="text-[#991B1B]">{d.message}</span>
              </div>
            ))}
          </div>
        )}
        {validated && isValidatedCurrent && !diagnostics && (
          <div role="status" className="p-2.5 rounded-[6px] bg-[#DCFCE7] border border-[#86EFAC] text-[12px] text-[#166534]">
            Valid — SourceHash {validated.result.sourceHash.slice(0, 19)}… ready to publish.
          </div>
        )}
      </div>
    </Dialog>
  );
}
