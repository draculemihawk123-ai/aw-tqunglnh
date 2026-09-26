import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { createRootWorkItem, projectRepositoriesList, ApiError } from '../api/generated';
import type { RepositoryView } from '../api/catalog';
import type { AcceptanceCriterion, CreateRootWorkItemResult, RepositoryAccess, ScopeGrant, WorkItemContract } from '../api/work';
import { withSessionToken } from '../api/session';
import { Button, Checkbox, Dialog, InlineError, Select, TextField } from '../components/ui';
import { Plus, X } from '../components/icons';

interface ScopeGrantDraft {
  key: string;
  repositoryId: string;
  access: RepositoryAccess;
  pathScopes: string;
  reason: string;
}

interface AcceptanceCriterionDraft {
  key: string;
  description: string;
  verificationRef: string;
}

function newKey(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function apiErrorMessage(err: unknown): { code: string; message: string; details?: { field: string; message: string }[] } {
  if (err instanceof ApiError) return { code: err.code, message: err.message, details: err.details };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * CreateWorkItemDialog — V7-10's own real "Create root WorkItem" form.
 * docs/design/09-v7-alpha-ui.md's own line: "nhập đủ WHAT/DONE/scope/
 * out-of-scope/workflow version" — mapped directly onto
 * internal/app/work.WorkItemContractRequest's own real fields: WHAT =
 * Behavior, DONE = AcceptanceCriteria, scope = InitialScope grants,
 * out-of-scope = Exclusions, workflow version = WorkflowVersionID. Also
 * exposes SchemaVersion (a bare positive integer, no defined migration
 * semantics — work.ValidateReadinessGate only requires it be positive)
 * since a real Mark Ready manual run against this form's own output
 * (schema version omitted) surfaced "schema version must be positive" as
 * a real readiness blocker with no field in the form to ever cure it. The
 * contract as a whole is optional (a WorkItem may start BACKLOG with no
 * contract at all, per CreateRootWorkItemRequest's own doc comment) —
 * completeness is judged later, fresh, by Kanban's own Mark Ready flow
 * (V7-09A), never here; this form only enforces the same STRUCTURAL
 * checks the server itself enforces before ever dispatching (non-blank
 * description per criterion, non-blank exclusion, etc.) so a malformed
 * draft never even reaches the network.
 *
 * Child WorkItem creation (`createChildWorkItem`, whose own "effective
 * scope must be a subset of the parent's" constraint needs a real parent
 * task's own already-loaded scope to build a meaningful "subset" picker
 * against) is deliberately deferred to V7-11, where a real Task Detail
 * screen will actually have that context — building it here against no
 * real parent view would either invent one or leave the subset constraint
 * unenforceable in the UI.
 */
export function CreateWorkItemDialog({ projectId, onClose, onCreated }: {
  projectId: string; onClose: () => void; onCreated: (result: CreateRootWorkItemResult) => void;
}) {
  const [title, setTitle] = useState('');
  const [grants, setGrants] = useState<ScopeGrantDraft[]>([]);
  const [includeContract, setIncludeContract] = useState(false);
  const [schemaVersion, setSchemaVersion] = useState('1');
  const [behavior, setBehavior] = useState('');
  const [criteria, setCriteria] = useState<AcceptanceCriterionDraft[]>([]);
  const [verificationSpec, setVerificationSpec] = useState('');
  const [riskLevel, setRiskLevel] = useState('');
  const [exclusions, setExclusions] = useState('');
  const [workflowVersionId, setWorkflowVersionId] = useState('');
  const [submitted, setSubmitted] = useState(false);

  const reposQuery = useQuery({
    queryKey: ['projectRepositories', projectId],
    queryFn: async () => (await projectRepositoriesList(projectId, withSessionToken())) as unknown as { repositories: RepositoryView[] },
  });
  const activeRepos = (reposQuery.data?.repositories ?? []).filter(r => r.status === 'ACTIVE');

  const mutation = useMutation({
    mutationFn: () => {
      const initialScope: ScopeGrant[] = grants.map(g => ({
        repositoryId: g.repositoryId, access: g.access, reason: g.reason.trim(),
        pathScopes: g.pathScopes.split(',').map(s => s.trim()).filter(Boolean),
      }));
      const parsedSchemaVersion = Number(schemaVersion.trim());
      const contract: WorkItemContract | undefined = includeContract ? {
        schemaVersion: Number.isInteger(parsedSchemaVersion) && parsedSchemaVersion > 0 ? parsedSchemaVersion : undefined,
        behavior: behavior.trim() || undefined,
        acceptanceCriteria: criteria.filter(c => c.description.trim()).map<AcceptanceCriterion>(c => ({
          description: c.description.trim(), verificationRef: c.verificationRef.trim() || undefined,
        })),
        verificationSpec: verificationSpec.trim() || undefined,
        riskLevel: riskLevel.trim() || undefined,
        exclusions: exclusions.split(',').map(s => s.trim()).filter(Boolean),
        workflowVersionId: workflowVersionId.trim() || undefined,
      } : undefined;
      return createRootWorkItem(projectId, { title: title.trim(), initialScope: initialScope as unknown[], contract: contract as unknown }, withSessionToken()) as unknown as Promise<CreateRootWorkItemResult>;
    },
    onSuccess: result => onCreated(result),
  });

  const errors = {
    title: !title.trim(),
    grants: grants.length === 0,
    grantFields: grants.some(g => !g.repositoryId || !g.reason.trim()),
  };
  const valid = !errors.title && !errors.grants && !errors.grantFields;

  function addGrant() {
    setGrants(current => [...current, { key: newKey(), repositoryId: activeRepos[0]?.id ?? '', access: 'WRITE', pathScopes: '', reason: '' }]);
  }
  function updateGrant(key: string, patch: Partial<ScopeGrantDraft>) {
    setGrants(current => current.map(g => (g.key === key ? { ...g, ...patch } : g)));
  }
  function removeGrant(key: string) {
    setGrants(current => current.filter(g => g.key !== key));
  }
  function addCriterion() {
    setCriteria(current => [...current, { key: newKey(), description: '', verificationRef: '' }]);
  }
  function updateCriterion(key: string, patch: Partial<AcceptanceCriterionDraft>) {
    setCriteria(current => current.map(c => (c.key === key ? { ...c, ...patch } : c)));
  }
  function removeCriterion(key: string) {
    setCriteria(current => current.filter(c => c.key !== key));
  }

  function handleCreate() {
    setSubmitted(true);
    if (!valid) return;
    mutation.mutate();
  }

  const errorDetails = apiErrorMessage(mutation.error).details;

  return (
    <Dialog
      title="Create WorkItem"
      description="A root WorkItem starts its own TaskFamily and WorkspaceSet. Scope grants name exactly which repositories it may read or write."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} onClick={handleCreate}>Create</Button>
        </>
      }
    >
      <div className="space-y-4">
        <TextField label="Title" required value={title} error={submitted && errors.title ? 'required' : undefined}
          placeholder="Add distributed tracing to API gateway" onChange={setTitle} />

        <div>
          <div className="flex items-center justify-between mb-1.5">
            <span className="text-xs font-medium text-[#5D697A]">Initial scope</span>
            <Button size="compact" intent="quiet" icon={<Plus size={12} aria-hidden />} onClick={addGrant} disabled={activeRepos.length === 0}>Add repository</Button>
          </div>
          {activeRepos.length === 0 && !reposQuery.isPending && (
            <p className="text-[12px] text-[#5D697A]">No ACTIVE repositories in this project yet — register and probe one first.</p>
          )}
          {submitted && errors.grants && <p role="alert" className="text-[12px] text-[#991B1B] mb-1.5">At least one repository scope grant is required.</p>}
          <div className="space-y-2">
            {grants.map(grant => (
              <div key={grant.key} className="flex items-start gap-2 bg-[#F8FAFC] border border-[#CDD5DF] rounded-[6px] p-2">
                <div className="w-32 flex-shrink-0">
                  <Select label="Repository" value={grant.repositoryId} onChange={v => updateGrant(grant.key, { repositoryId: v })}
                    options={activeRepos.map(r => ({ value: r.id, label: r.name }))} required />
                </div>
                <div className="w-24 flex-shrink-0">
                  <Select label="Access" value={grant.access} onChange={v => updateGrant(grant.key, { access: v as RepositoryAccess })}
                    options={[{ value: 'READ', label: 'READ' }, { value: 'WRITE', label: 'WRITE' }]} required />
                </div>
                <div className="flex-1 min-w-0">
                  <TextField label="Path scopes" value={grant.pathScopes} placeholder="src/**" mono onChange={v => updateGrant(grant.key, { pathScopes: v })} />
                </div>
                <div className="flex-1 min-w-0">
                  <TextField label="Reason" required value={grant.reason} error={submitted && !grant.reason.trim() ? 'required' : undefined}
                    placeholder="implement tracing" onChange={v => updateGrant(grant.key, { reason: v })} />
                </div>
                <Button size="compact" intent="quiet" className="mt-5" onClick={() => removeGrant(grant.key)} aria-label="Remove scope grant"><X size={12} aria-hidden /></Button>
              </div>
            ))}
          </div>
        </div>

        <Checkbox label="Add a readiness contract now (can also be left for later)" checked={includeContract} onChange={setIncludeContract} />

        {includeContract && (
          <div className="space-y-3 border-t border-[#ECEFF4] pt-3">
            <TextField label="Schema version" value={schemaVersion} onChange={setSchemaVersion} mono
              helper="A positive integer identifying this contract's own schema (no defined migration semantics yet — ValidateReadinessGate only requires it be positive)." />
            <TextField label="Behavior (WHAT)" value={behavior} onChange={setBehavior} placeholder="What this WorkItem must accomplish" />

            <div>
              <div className="flex items-center justify-between mb-1.5">
                <span className="text-xs font-medium text-[#5D697A]">Acceptance criteria (DONE)</span>
                <Button size="compact" intent="quiet" icon={<Plus size={12} aria-hidden />} onClick={addCriterion}>Add criterion</Button>
              </div>
              <div className="space-y-2">
                {criteria.map(c => (
                  <div key={c.key} className="flex items-start gap-2">
                    <div className="flex-1 min-w-0">
                      <TextField label="Description" value={c.description} onChange={v => updateCriterion(c.key, { description: v })} placeholder="Tracing spans appear in Jaeger" />
                    </div>
                    <div className="flex-1 min-w-0">
                      <TextField label="Verification ref" value={c.verificationRef} onChange={v => updateCriterion(c.key, { verificationRef: v })} placeholder="optional" mono />
                    </div>
                    <Button size="compact" intent="quiet" className="mt-5" onClick={() => removeCriterion(c.key)} aria-label="Remove criterion"><X size={12} aria-hidden /></Button>
                  </div>
                ))}
                {criteria.length === 0 && <p className="text-[12px] text-[#5D697A]">No acceptance criteria yet.</p>}
              </div>
            </div>

            <TextField label="Verification spec" value={verificationSpec} onChange={setVerificationSpec} placeholder="How completion will be checked" />
            <TextField label="Risk level" value={riskLevel} onChange={setRiskLevel} placeholder="low / medium / high — no fixed vocabulary" />
            <TextField label="Exclusions (out-of-scope)" value={exclusions} onChange={setExclusions} placeholder="comma-separated" helper="What this WorkItem explicitly does not cover" />
            <TextField label="Workflow version ID" value={workflowVersionId} onChange={setWorkflowVersionId} mono placeholder="optional — pin an exact published WorkflowVersion" />
          </div>
        )}

        {mutation.isError && (
          errorDetails && errorDetails.length > 0 ? (
            <div role="alert" className="space-y-1.5">
              {errorDetails.map((d, i) => (
                <div key={i} className="flex gap-3 p-2.5 rounded-[6px] bg-[#FEE2E2] border border-[#FCA5A5] text-[12px]">
                  <span className="font-mono text-[#991B1B] font-medium flex-shrink-0">{d.field}</span>
                  <span className="text-[#991B1B]">{d.message}</span>
                </div>
              ))}
            </div>
          ) : <InlineError {...apiErrorMessage(mutation.error)} />
        )}
      </div>
    </Dialog>
  );
}
