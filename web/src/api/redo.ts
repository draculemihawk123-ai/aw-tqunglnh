/**
 * Hand-declared wire shapes and pure helpers for V11-10's run-again / redo-with-the-newest-version actions (ADR-041).
 *
 * `getWorkItemRedoPreview` and the results of `retryWorkItem` / `recreateWorkItem` nest one level deeper than apicontract's shallow
 * generator expands, so the generated client types those fields `unknown`; these mirror internal/app/workitemredo and
 * internal/app/runtime field-for-field, the same "narrow `unknown` at the call site" convention every sibling API-gap file uses.
 */

import { ApiError } from './generated';

export interface RedoRefusal {
  code: string;
  message: string;
}

export interface WorkflowVersionView {
  workflowVersionId: string;
  versionNumber: number;
}

export interface RedoBlocker {
  blockerId: string;
  type: string;
  reason: string;
}

export interface WorkItemRef {
  workItemId: string;
  title?: string;
}

export interface RedoFile {
  repositoryId: string;
  repositoryWorkspaceId: string;
  path: string;
  status: string;
  /** No WRITE scope of the work item covers the path: a run that finds it fails with SCOPE_VIOLATION. */
  outOfScope: boolean;
}

export interface RedoPreview {
  workItemId: string;
  projectId: string;
  title: string;
  status: string;
  isChild: boolean;
  canRetry: boolean;
  retryRefusal?: RedoRefusal | null;
  canRecreate: boolean;
  recreateRefusal?: RedoRefusal | null;
  pinnedWorkflowVersion: WorkflowVersionView;
  latestWorkflowVersion: WorkflowVersionView;
  newerVersionAvailable: boolean;
  blockersToResolve: RedoBlocker[];
  operatorMessageCount: number;
  redoNumber: number;
  priorRunCount: number;
  priorCostUsd: number;
  costKnown: boolean;
  supersedes?: WorkItemRef | null;
  supersededBy?: WorkItemRef | null;
  files: RedoFile[];
  outOfScopeCount: number;
  filesUnavailableReason?: string;
}

export interface ParkedSet {
  repositoryId: string;
  repositoryWorkspaceId: string;
  ref: string;
  paths: string[];
}

export type OutOfScopeChoice = 'KEEP' | 'PARK';

/** Plain-language explanation of a typed refusal, so a dialog never shows a bare code. */
const REFUSAL_TEXT: Record<string, string> = {
  NOT_RETRYABLE: 'This work item is not waiting on a failed or cancelled run, so there is nothing to run again.',
  NEVER_RAN: 'This work item has not run yet. Use Start Run instead.',
  RUN_IN_PROGRESS: 'A run of this work item has not finished. Wait for it, or cancel it first.',
  LATEST_RUN_SUCCEEDED: 'The latest run succeeded, so there is nothing to run again.',
  STILL_BLOCKED: 'The work item is blocked by something other than the failed run (a scope expansion, an admission blocker or a failed release). Resolve that first.',
  WORKSPACE_QUARANTINED: 'A repository workspace of this task family is QUARANTINED. Reconcile it in the Workspace tab first.',
  ROOT_UNSUPPORTED: 'A root work item cannot be redone with a newer version; only a child task can.',
  NOT_RECREATABLE: 'This work item cannot be redone in its current status.',
  ALREADY_SUPERSEDED: 'This work item was already replaced by a newer one. Redo the newer work item instead.',
  NO_PINNED_WORKFLOW_VERSION: 'This work item has no pinned workflow version, so there is no workflow to take the newest version of.',
  NO_PINNED_VERSION: 'This work item has no pinned workflow version, so there is no workflow to take the newest version of.',
};

export function explainRefusal(refusal: RedoRefusal | null | undefined): string | undefined {
  if (!refusal) return undefined;
  return REFUSAL_TEXT[refusal.code] ?? refusal.message;
}

/** The typed redo refusal code an error carries in its details (`field: "redo"`), if any. */
export function redoRefusalOf(err: unknown): string | undefined {
  if (!(err instanceof ApiError)) return undefined;
  return err.details?.find(d => d.field === 'redo')?.message;
}

/** True when the server refused because files outside the scope need a KEEP/PARK decision. */
export function needsOutOfScopeDecision(err: unknown): boolean {
  return err instanceof ApiError && !!err.details?.some(d => d.message.startsWith('OUT_OF_SCOPE_FILES_NEED_DECISION'));
}

/** One sentence for an error from retryWorkItem / recreateWorkItem. */
export function describeRedoError(err: unknown): { code: string; message: string } {
  const refusal = redoRefusalOf(err);
  if (refusal) return { code: refusal, message: REFUSAL_TEXT[refusal] ?? (err as ApiError).message };
  if (needsOutOfScopeDecision(err)) {
    return { code: 'OUT_OF_SCOPE_FILES', message: 'A previous run left files outside the work item\'s scope. Choose to keep them or park them, then try again.' };
  }
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/** Cost in US dollars as the dialogs show it. */
export function formatCost(preview: Pick<RedoPreview, 'priorCostUsd' | 'costKnown'>): string {
  if (!preview.costKnown) return 'not reported';
  return `$${preview.priorCostUsd.toFixed(2)}`;
}

/** From the third try on, the dialog warns: repeating the same task is a signal worth stopping on. */
export function redoWarning(redoNumber: number, cost: string): string | undefined {
  if (redoNumber < 3) return undefined;
  return `This would be attempt ${redoNumber} of this task (earlier attempts cost ${cost}). Check that the cause is fixed before running it again.`;
}

export function outOfScopeFiles(preview: RedoPreview): RedoFile[] {
  return preview.files.filter(f => f.outOfScope);
}

export function defaultOutOfScopeChoice(preview: RedoPreview): OutOfScopeChoice | undefined {
  return outOfScopeFiles(preview).length > 0 ? 'KEEP' : undefined;
}
