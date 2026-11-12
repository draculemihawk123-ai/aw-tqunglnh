/**
 * V12-14 (docs/design/15-v12-ui-operations.md, UX-02, UX-03, UX-25, UX-38, UX-42): the logic of the "New task" form, kept pure so
 * it is tested without the dialog — templates by kind of work, "use when" text and a suggestion for the kit's workflows, the
 * checks before creating (a path the contract names that no path scope covers — chochuyennha P2-01 lost 0.65 USD to exactly that —
 * and missing acceptance criteria), and turning a pasted work-item JSON into the form.
 */
import type { AcceptanceCriterion, ScopeGrant, WorkItemContract } from './work';

export type TaskKind = 'bugfix' | 'feature' | 'docs' | 'infra';
export type RiskLevel = 'LOW' | 'MEDIUM' | 'HIGH';

export interface TaskDraft {
  title: string;
  kind: TaskKind;
  behavior: string;
  criteria: AcceptanceCriterion[];
  verificationSpec: string;
  riskLevel: RiskLevel;
  exclusions: string[];
  /** One WRITE scope per repository, path prefixes (empty = the whole repository). */
  scopes: { repositoryId: string; pathScopes: string[]; reason: string }[];
  workflowVersionId: string;
  parentJoinPolicy: string;
}

/** The default join policy of a child: the parent is done when every child is (UX-03: it was required with no default). */
export const DEFAULT_PARENT_JOIN_POLICY = 'ALL_CHILDREN_DONE';

export const TEMPLATES: Record<TaskKind, { label: string; behavior: string; criteria: string[]; verificationSpec: string; riskLevel: RiskLevel }> = {
  bugfix: {
    label: 'Bug fix',
    behavior: 'Bug: <what happens>. Expected: <what should happen>. Reproduce: <steps or the failing request>.',
    criteria: ['A test reproduces the bug and fails before the fix', 'The test passes after the fix and no other test breaks'],
    verificationSpec: 'The repository test command passes; the new test is red before the fix (expect-fail) and green after.',
    riskLevel: 'LOW',
  },
  feature: {
    label: 'Feature',
    behavior: 'Add <capability> so that <who> can <do what>. In scope: <…>. Out of scope: <…>.',
    criteria: ['<observable behaviour 1>', '<observable behaviour 2>', 'Tests cover the new behaviour'],
    verificationSpec: 'The repository test command passes; the new behaviour is covered by tests.',
    riskLevel: 'MEDIUM',
  },
  docs: {
    label: 'Documentation',
    behavior: 'Write or update <document> under docs/ so that <reader> knows <what>.',
    criteria: ['The document covers <topics>', 'Links and commands in it are correct'],
    verificationSpec: 'A reviewer reads the document; no code changes.',
    riskLevel: 'LOW',
  },
  infra: {
    label: 'Infrastructure / configuration',
    behavior: 'Change <build, deployment or configuration> so that <outcome>. Keep <what must not change>.',
    criteria: ['<the configuration works in environment X>', 'The build and the tests still pass'],
    verificationSpec: 'The build and the repository tests pass; <manual check> is done.',
    riskLevel: 'MEDIUM',
  },
};

/** "Use when" for the kit's workflows, matched on the definition id (a project prefixes the kit's ids). */
const WORKFLOW_HELP: { match: RegExp; useWhen: string }[] = [
  { match: /wf-docs-change/, useWhen: 'Only documents change (docs/): a writer agent and a review, no code checks.' },
  { match: /wf-bugfix/, useWhen: 'A bug a test can reproduce: a red test first, then the fix, then the checks and a review.' },
  { match: /wf-task-delivery-plus/, useWhen: 'Code with MEDIUM or HIGH risk: plan check, hygiene, size, quality, an independent review, then your approval.' },
  { match: /wf-task-delivery/, useWhen: 'An ordinary code task with LOW risk: build, checks, quality, then your approval.' },
  { match: /wf-feature-definition/, useWhen: 'Turn an idea into a spec, a design and tasks (tasks.json); no product code.' },
  { match: /wf-retro/, useWhen: 'Draw lessons from finished work into the kit; no product code.' },
];

export function workflowUseWhen(definitionId: string): string | undefined {
  return WORKFLOW_HELP.find(h => h.match.test(definitionId))?.useWhen;
}

/** The kit workflow that fits a draft: only docs/ in scope → docs-change; a bug fix → bugfix; MEDIUM risk or more → the plus variant. */
export function suggestedWorkflowPattern(draft: Pick<TaskDraft, 'kind' | 'riskLevel' | 'scopes'>): RegExp {
  const paths = draft.scopes.flatMap(s => s.pathScopes);
  const docsOnly = draft.kind === 'docs' || (paths.length > 0 && paths.every(p => /^docs(\/|$)/.test(p)));
  if (docsOnly) return /wf-docs-change/;
  if (draft.kind === 'bugfix') return /wf-bugfix/;
  if (draft.riskLevel === 'MEDIUM' || draft.riskLevel === 'HIGH') return /wf-task-delivery-plus/;
  return /wf-task-delivery(?!-plus)/;
}

export interface WorkflowChoice {
  definitionId: string;
  name: string;
  versionId: string;
  versionNumber: number;
}

/** The workflow the suggestion points at among those published, if any. */
export function suggestWorkflow(draft: Pick<TaskDraft, 'kind' | 'riskLevel' | 'scopes'>, choices: WorkflowChoice[]): WorkflowChoice | undefined {
  const pattern = suggestedWorkflowPattern(draft);
  return choices.find(c => pattern.test(c.definitionId));
}

// Path-like tokens in free text: at least one "/" or a file extension, no spaces, no URL scheme.
const PATH_TOKEN = /(?<![\w:/.-])((?:[\w@.-]+\/)+[\w@.*-]+(?:\.[A-Za-z0-9]+)?|[\w@-]+\.(?:java|kt|ts|tsx|js|jsx|py|go|sql|yml|yaml|json|xml|properties|md|css|scss|html|sh|gradle|toml))(?![\w/])/g;

/** Paths the contract text names. */
export function mentionedPaths(text: string): string[] {
  const out = new Set<string>();
  for (const m of text.matchAll(PATH_TOKEN)) {
    const path = m[1].replace(/[.,;:)]+$/, '');
    if (/^https?:/.test(path) || /^\d/.test(path)) continue;
    out.add(path);
  }
  return [...out];
}

/** A path is covered when some WRITE scope has no path prefixes (the whole repository) or one is a prefix of it. */
export function isCovered(path: string, scopes: TaskDraft['scopes']): boolean {
  const clean = path.replace(/^\.\//, '');
  return scopes.some(s => s.pathScopes.length === 0 || s.pathScopes.some(p => {
    const prefix = p.replace(/^\.\//, '').replace(/\/\*\*$/, '').replace(/\/+$/, '');
    return prefix === '' || prefix === '**' || clean === prefix || clean.startsWith(prefix + '/');
  }));
}

export interface DraftProblem {
  severity: 'error' | 'warning';
  field: string;
  message: string;
}

/** The checks before creating. Errors block the create; warnings ask for a second look. */
export function checkDraft(draft: TaskDraft, chosen?: WorkflowChoice, suggested?: WorkflowChoice): DraftProblem[] {
  const problems: DraftProblem[] = [];
  if (!draft.title.trim()) problems.push({ severity: 'error', field: 'title', message: 'A title is required.' });
  if (!draft.behavior.trim()) problems.push({ severity: 'error', field: 'behavior', message: 'Describe the behaviour (what the task must do).' });
  const criteria = draft.criteria.filter(c => c.description.trim());
  if (criteria.length === 0) problems.push({ severity: 'error', field: 'criteria', message: 'Add at least one acceptance criterion.' });
  if (draft.criteria.some(c => /^<.*>$/.test(c.description.trim()))) {
    problems.push({ severity: 'error', field: 'criteria', message: 'Replace the template placeholders (<…>) in the acceptance criteria.' });
  }
  if (/<[^>]+>/.test(draft.behavior)) problems.push({ severity: 'warning', field: 'behavior', message: 'The behaviour still has template placeholders (<…>).' });
  if (draft.scopes.length === 0) problems.push({ severity: 'error', field: 'scope', message: 'Choose at least one repository the task may change.' });
  if (!draft.workflowVersionId) problems.push({ severity: 'error', field: 'workflow', message: 'Choose a workflow.' });
  const text = [draft.behavior, ...draft.criteria.map(c => c.description), draft.verificationSpec].join('\n');
  // A bare file name ("application.yml") cannot be placed in a directory, so only paths with a directory are checked.
  const outside = mentionedPaths(text).filter(p => p.includes('/') && !isCovered(p, draft.scopes));
  if (outside.length > 0) {
    problems.push({
      severity: 'warning', field: 'scope',
      message: `The contract names ${outside.join(', ')}, which no path scope covers: an agent that edits ${outside.length === 1 ? 'it' : 'them'} fails with SCOPE_VIOLATION. Widen the scope or change the text.`,
    });
  }
  if (chosen && suggested && chosen.definitionId !== suggested.definitionId) {
    problems.push({ severity: 'warning', field: 'workflow', message: `For this task ${suggested.name} is suggested (${workflowUseWhen(suggested.definitionId) ?? ''}).` });
  }
  return problems;
}

/** The body of createChildWorkItem for a draft. */
export function childRequest(draft: TaskDraft): { title: string; parentJoinPolicy: string; effectiveScope: ScopeGrant[]; contract: WorkItemContract } {
  return {
    title: draft.title.trim(),
    parentJoinPolicy: draft.parentJoinPolicy || DEFAULT_PARENT_JOIN_POLICY,
    effectiveScope: draft.scopes.map(s => ({
      repositoryId: s.repositoryId, access: 'WRITE', reason: s.reason.trim() || `${TEMPLATES[draft.kind].label}: ${draft.title.trim()}`,
      ...(s.pathScopes.length > 0 ? { pathScopes: s.pathScopes } : {}),
    })),
    contract: {
      schemaVersion: 1, behavior: draft.behavior.trim(),
      acceptanceCriteria: draft.criteria.filter(c => c.description.trim()).map(c => ({ description: c.description.trim(), verificationRef: c.verificationRef?.trim() || 'COMMAND_EXECUTION' })),
      verificationSpec: draft.verificationSpec.trim(), riskLevel: draft.riskLevel,
      ...(draft.exclusions.length > 0 ? { exclusions: draft.exclusions } : {}),
      workflowVersionId: draft.workflowVersionId,
    },
  };
}

/** A draft from a pasted work-item JSON (the file `run-task.sh` takes). Throws with a readable message when it is not one. */
export function draftFromJSON(text: string, base: TaskDraft): TaskDraft {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    throw new Error(`Not valid JSON: ${e instanceof Error ? e.message : String(e)}`);
  }
  if (!raw || typeof raw !== 'object') throw new Error('The JSON must be an object with title, effectiveScope and contract.');
  const item = raw as { title?: string; parentJoinPolicy?: string; effectiveScope?: ScopeGrant[]; contract?: WorkItemContract };
  const contract = item.contract ?? {};
  const risk = String(contract.riskLevel ?? base.riskLevel).toUpperCase();
  return {
    ...base,
    title: item.title ?? base.title,
    behavior: contract.behavior ?? base.behavior,
    criteria: contract.acceptanceCriteria?.length ? contract.acceptanceCriteria : base.criteria,
    verificationSpec: contract.verificationSpec ?? base.verificationSpec,
    riskLevel: (['LOW', 'MEDIUM', 'HIGH'].includes(risk) ? risk : base.riskLevel) as RiskLevel,
    exclusions: contract.exclusions ?? base.exclusions,
    scopes: (item.effectiveScope ?? []).filter(s => s.access === 'WRITE').map(s => ({ repositoryId: s.repositoryId, pathScopes: s.pathScopes ?? [], reason: s.reason ?? '' })),
    workflowVersionId: contract.workflowVersionId && contract.workflowVersionId !== 'WORKFLOW_VERSION_ID' ? contract.workflowVersionId : base.workflowVersionId,
    parentJoinPolicy: item.parentJoinPolicy ?? base.parentJoinPolicy,
  };
}
