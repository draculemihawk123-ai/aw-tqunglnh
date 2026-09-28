import type { APIRequestContext } from '@playwright/test';

/**
 * Publishes workflow-graph definitions through the REAL public HTTP
 * definitions API (create → validate → publish) — "seed fake-provider
 * environment qua public setup" (docs/design/09-v7-alpha-ui.md V7-17's own
 * line): a real operator/CI-pipeline would author a workflow this same
 * way, never by writing rows into sqlite directly. Field shapes mirror
 * internal/integration/v6accept's own definitions_test.go byte-for-byte
 * (same Go domain structs, hand-transcribed to JSON since this file has no
 * Go struct to import from).
 */

export interface PublishedDefinition {
  definitionId: string;
  versionId: string;
  compiledHash: string;
}

export function pin(p: PublishedDefinition, kind: string) {
  return { kind, definitionId: p.definitionId, versionId: p.versionId };
}

export async function publishDefinition(
  request: APIRequestContext, baseURL: string, token: string,
  scopePrefix: string, kind: string, definitionId: string, name: string, document: unknown,
): Promise<PublishedDefinition> {
  const headers = { 'X-Aw-Session-Token': token, 'Idempotency-Key': crypto.randomUUID() };
  const base = `${baseURL}${scopePrefix}/definitions/${kind}`;

  const created = await request.post(base, { headers, data: { definitionId, name } });
  if (!created.ok()) throw new Error(`create ${kind}/${definitionId}: ${created.status()} ${await created.text()}`);

  const body = { content: JSON.stringify(document), format: 'json', schemaVersion: 1 };
  const validated = await request.post(`${base}/${definitionId}/validate`, { headers, data: body });
  if (!validated.ok()) throw new Error(`validate ${kind}/${definitionId}: ${validated.status()} ${await validated.text()}`);

  const published = await request.post(`${base}/${definitionId}/publish`, { headers, data: body });
  if (!published.ok()) throw new Error(`publish ${kind}/${definitionId}: ${published.status()} ${await published.text()}`);
  const version = await published.json() as { id: string; compiledHash: string };
  return { definitionId, versionId: version.id, compiledHash: version.compiledHash };
}

/** publishWaitWorkflow: START -> WAIT(SIGNAL, never sent, long timeout) -> END — a Run that sits durably RUNNING/WAITING until cancelled, never on its own. Mirrors internal/integration/v6accept/stage_fault_cancel_test.go's own waitSignalOnlyWorkflowDocument exactly. */
export async function publishWaitWorkflow(
  request: APIRequestContext, baseURL: string, token: string, projectId: string, suffix: string,
): Promise<PublishedDefinition> {
  const document = {
    schemaVersion: '1',
    nodes: [
      { key: 'start', type: 'START', outcomes: ['next'] },
      {
        key: 'pause', type: 'WAIT', outcomes: ['resumed', 'expired'],
        wait: { mode: 'SIGNAL', signalName: 'never-sent', timeoutSeconds: 3600, completionOutcome: 'resumed', timeoutOutcome: 'expired' },
      },
      { key: 'end_resumed', type: 'END' },
      { key: 'end_expired', type: 'END' },
    ],
    edges: [
      { key: 'start-pause', from: 'start', outcome: 'next', to: 'pause' },
      { key: 'pause-resumed-end', from: 'pause', outcome: 'resumed', to: 'end_resumed' },
      { key: 'pause-expired-end', from: 'pause', outcome: 'expired', to: 'end_expired' },
    ],
  };
  return publishDefinition(request, baseURL, token, `/projects/${projectId}`, 'WORKFLOW', `e2e-wait-workflow-${suffix}`, 'e2e wait workflow', document);
}

/** publishApprovalWorkflow: START -> APPROVAL(operator) -> END, completion gated on a real human decision (policy.AssuranceLevel HUMAN) — no COMMAND/AGENT node needed at all. */
export async function publishApprovalWorkflow(
  request: APIRequestContext, baseURL: string, token: string, projectId: string, suffix: string,
): Promise<PublishedDefinition> {
  const completionPolicy = await publishDefinition(request, baseURL, token, '', 'POLICY', `e2e-approval-completion-${suffix}`, 'e2e approval completion policy', {
    category: 'COMPLETION',
    completion: { requiredAssurance: [{ level: 'HUMAN', requiredApprovals: [{ authorizedRoles: ['operator'] }] }] },
  });
  const document = {
    schemaVersion: '1',
    completionPolicyRef: pin(completionPolicy, 'POLICY'),
    nodes: [
      { key: 'start', type: 'START', outcomes: ['go'] },
      { key: 'review', type: 'APPROVAL', outcomes: ['approved', 'rejected'], approval: { authorizedRoles: ['operator'], timeoutSeconds: 3600, escalationOutcome: 'rejected' } },
      { key: 'end', type: 'END' },
    ],
    edges: [
      { key: 'start-review', from: 'start', outcome: 'go', to: 'review' },
      { key: 'review-approved-end', from: 'review', outcome: 'approved', to: 'end' },
      { key: 'review-rejected-end', from: 'review', outcome: 'rejected', to: 'end' },
    ],
  };
  return publishDefinition(request, baseURL, token, `/projects/${projectId}`, 'WORKFLOW', `e2e-approval-workflow-${suffix}`, 'e2e approval workflow', document);
}

/**
 * publishBlockerWorkflow: START -> AGENT(maker, real fake-provider
 * execution) -> END, with a completion policy requiring COMMAND_EXECUTION
 * evidence a bare AGENT node never produces — a deliberate, deterministic
 * GC-INV-12/13 completion-policy mismatch that always yields a real
 * COMPLETION_POLICY_FAILED WorkItemBlocker on a successful run, the exact
 * same honest fixture-workflow consequence V7-11's own manual verification
 * already relied on for its real recovery-action journey.
 */
export async function publishBlockerWorkflow(
  request: APIRequestContext, baseURL: string, token: string, projectId: string, suffix: string, adapterBuildId: string,
): Promise<PublishedDefinition> {
  const contextPolicy = await publishDefinition(request, baseURL, token, '', 'POLICY', `e2e-context-${suffix}`, 'e2e context policy', {
    category: 'CONTEXT', context: { selector: ['e2e-agent-context'], budget: { maxTokens: 4096 } },
  });
  const agentProfile = await publishDefinition(request, baseURL, token, '', 'AGENT_PROFILE', `e2e-agent-profile-${suffix}`, 'e2e agent profile', {
    providerKey: 'claude', model: 'fake-model', toolRefs: ['read_file'],
    contextPolicyRef: pin(contextPolicy, 'POLICY'),
    compatibility: { os: [process.platform === 'win32' ? 'windows' : 'linux'] },
    budget: { maxTokens: 4096 },
  });
  const attemptPolicy = await publishDefinition(request, baseURL, token, '', 'POLICY', `e2e-attempt-${suffix}`, 'e2e attempt policy', {
    category: 'ATTEMPT', attempt: { maxAttempts: 1, backoffSeconds: 1, timeoutSeconds: 60 },
  });
  const permissionPolicy = await publishDefinition(request, baseURL, token, '', 'POLICY', `e2e-permission-${suffix}`, 'e2e permission policy', {
    category: 'PERMISSION', permission: { isolationTier: 'OPERATOR_TRUSTED_LOCAL', grantedCapabilities: [] },
  });
  // Deliberately NO completionPolicyRef: internal/app/runtime/completion_policy.go's
  // own resolveCompletionPolicy treats a nil ref as a real, waivable
  // CompletionOutcomeFail ("no completion policy pinned"), which is what
  // actually produces a resolvable work.BlockerCompletionPolicyFailed
  // WorkItemBlocker — a completion policy that merely reports unsatisfied
  // REQUIREMENTS (e.g. requiredEvidenceKinds never produced) instead
  // resolves to CompletionOutcomeBlock, which deliberately opens no
  // WorkItemBlocker at all (found live by this suite's own first real run
  // against a workflow that DID set one).
  const attemptRefs = [pin(attemptPolicy, 'POLICY'), pin(permissionPolicy, 'POLICY')];
  const document = {
    schemaVersion: '1',
    nodes: [
      { key: 'start', type: 'START', outcomes: ['next'] },
      { key: 'maker', type: 'AGENT', outcomes: ['done'], agent: { profileRef: pin(agentProfile, 'AGENT_PROFILE'), policyRefs: attemptRefs, adapterBuildId, role: 'MAKER' } },
      { key: 'end', type: 'END' },
    ],
    edges: [
      { key: 'start-maker', from: 'start', outcome: 'next', to: 'maker' },
      { key: 'maker-end', from: 'maker', outcome: 'done', to: 'end' },
    ],
  };
  return publishDefinition(request, baseURL, token, `/projects/${projectId}`, 'WORKFLOW', `e2e-blocker-workflow-${suffix}`, 'e2e blocker workflow', document);
}
