import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { createDefinition, createProjectDefinition, ApiError } from '../api/generated';
import { DEFINITION_KINDS } from '../api/definitions';
import type { DefinitionKind } from '../api/definitions';
import { withSessionToken } from '../api/session';
import { Button, Dialog, InlineError, Select, TextField } from '../components/ui';

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * CreateDefinitionDialog — the first half of V7-08's own "author YAML/JSON"
 * flow: a brand-new Definition always starts empty (always DRAFT, always
 * generation 1 — internal/domain/definition/lifecycle.go's own Create doc
 * comment), created before any document content exists at all. Authoring
 * the actual document happens in a separate step (DefinitionEditorDialog,
 * opened on the resulting Definition), matching the real two-command shape
 * (CreateDefinition, then ValidateDraft/PublishDefinitionVersion) rather
 * than pretending they are one action.
 */
export function CreateDefinitionDialog({ scope, projectId, onClose, onCreated }: {
  scope: 'global' | 'project'; projectId?: string;
  onClose: () => void; onCreated: (kind: DefinitionKind, id: string) => void;
}) {
  const [kind, setKind] = useState<DefinitionKind>('BLOCK');
  const [definitionId, setDefinitionId] = useState('');
  const [name, setName] = useState('');
  const [submitted, setSubmitted] = useState(false);

  const mutation = useMutation({
    mutationFn: () => {
      const body = { definitionId: definitionId.trim(), name: name.trim() };
      return scope === 'project' && projectId
        ? createProjectDefinition(projectId, kind, body, withSessionToken())
        : createDefinition(kind, body, withSessionToken());
    },
  });

  const errors = { definitionId: !definitionId.trim(), name: !name.trim() };
  const valid = !errors.definitionId && !errors.name;

  function handleCreate() {
    setSubmitted(true);
    if (!valid) return;
    mutation.mutate(undefined, { onSuccess: () => onCreated(kind, definitionId.trim()) });
  }

  return (
    <Dialog
      title="New Definition"
      description={scope === 'project' ? 'Create a new draft definition in this project.' : 'Create a new draft definition at the global / installation scope.'}
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} onClick={handleCreate}>Create</Button>
        </>
      }
    >
      <div className="space-y-3">
        <Select label="Kind" value={kind} onChange={v => setKind(v as DefinitionKind)}
          options={DEFINITION_KINDS.map(k => ({ value: k, label: k }))} required />
        <TextField label="Definition ID" required mono value={definitionId} error={submitted && errors.definitionId ? 'required' : undefined}
          placeholder="my-block" onChange={setDefinitionId} />
        <TextField label="Name" required value={name} error={submitted && errors.name ? 'required' : undefined}
          placeholder="My Block" onChange={setName} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}
