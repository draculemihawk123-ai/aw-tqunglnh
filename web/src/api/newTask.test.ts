import { describe, expect, it } from 'vitest';
import { DEFAULT_PARENT_JOIN_POLICY, TEMPLATES, checkDraft, childRequest, draftFromJSON, isCovered, mentionedPaths, suggestWorkflow, workflowUseWhen } from './newTask';
import type { TaskDraft, WorkflowChoice } from './newTask';

const base: TaskDraft = {
  title: 'P2-01: notification e-mail', kind: 'feature', behavior: 'Send the e-mail from backend/src/main/java/app/Mailer.java',
  criteria: [{ description: 'The e-mail is sent once' }], verificationSpec: 'mvn test passes', riskLevel: 'LOW', exclusions: [],
  scopes: [{ repositoryId: 'api', pathScopes: ['backend/src/main/java'], reason: '' }], workflowVersionId: 'wfv-1', parentJoinPolicy: DEFAULT_PARENT_JOIN_POLICY,
};
const choices: WorkflowChoice[] = [
  { definitionId: 'cc-wf-task-delivery', name: 'Task delivery', versionId: 'wfv-1', versionNumber: 3 },
  { definitionId: 'cc-wf-task-delivery-plus', name: 'Task delivery plus', versionId: 'wfv-2', versionNumber: 2 },
  { definitionId: 'cc-wf-bugfix', name: 'Bug fix', versionId: 'wfv-3', versionNumber: 1 },
  { definitionId: 'cc-wf-docs-change', name: 'Docs change', versionId: 'wfv-4', versionNumber: 1 },
];

describe('new task form logic (V12-14)', () => {
  it('finds the paths a contract names and whether a scope covers them', () => {
    expect(mentionedPaths('Edit backend/src/main/resources/application.yml and frontend/app/page.tsx; see https://x.io/a/b')).toEqual([
      'backend/src/main/resources/application.yml', 'frontend/app/page.tsx',
    ]);
    expect(isCovered('backend/src/main/java/app/Mailer.java', base.scopes)).toBe(true);
    expect(isCovered('backend/src/main/resources/application.yml', base.scopes)).toBe(false);
    expect(isCovered('anything/at/all.ts', [{ repositoryId: 'api', pathScopes: [], reason: '' }])).toBe(true);
  });

  it('warns about a path outside the scope (chochuyennha P2-01) and blocks missing criteria', () => {
    const p201 = { ...base, behavior: base.behavior + ' and set the sender in backend/src/main/resources/application.yml' };
    const problems = checkDraft(p201);
    expect(problems.find(p => p.field === 'scope')).toMatchObject({ severity: 'warning' });
    expect(problems.find(p => p.field === 'scope')!.message).toContain('backend/src/main/resources/application.yml');
    expect(checkDraft({ ...base, criteria: [] }).find(p => p.field === 'criteria')).toMatchObject({ severity: 'error' });
    expect(checkDraft({ ...base, criteria: [{ description: TEMPLATES.feature.criteria[0] }] }).some(p => p.severity === 'error')).toBe(true);
    expect(checkDraft(base).filter(p => p.severity === 'error')).toEqual([]);
  });

  it('suggests a workflow by scope, kind and risk, and warns when another one is chosen', () => {
    expect(suggestWorkflow(base, choices)?.definitionId).toBe('cc-wf-task-delivery');
    expect(suggestWorkflow({ ...base, riskLevel: 'MEDIUM' }, choices)?.definitionId).toBe('cc-wf-task-delivery-plus');
    expect(suggestWorkflow({ ...base, kind: 'bugfix' }, choices)?.definitionId).toBe('cc-wf-bugfix');
    expect(suggestWorkflow({ ...base, scopes: [{ repositoryId: 'api', pathScopes: ['docs/features'], reason: '' }] }, choices)?.definitionId).toBe('cc-wf-docs-change');
    const mismatch = checkDraft({ ...base, kind: 'bugfix' }, choices[0], choices[2]);
    expect(mismatch.find(p => p.field === 'workflow')?.message).toContain('Bug fix');
    expect(workflowUseWhen('cc-wf-bugfix')).toMatch(/red test first/);
  });

  it('builds the createChildWorkItem body with a default join policy', () => {
    const body = childRequest({ ...base, parentJoinPolicy: '' });
    expect(body.parentJoinPolicy).toBe('ALL_CHILDREN_DONE');
    expect(body.effectiveScope).toEqual([{ repositoryId: 'api', access: 'WRITE', reason: 'Feature: P2-01: notification e-mail', pathScopes: ['backend/src/main/java'] }]);
    expect(body.contract).toMatchObject({ schemaVersion: 1, riskLevel: 'LOW', workflowVersionId: 'wfv-1', acceptanceCriteria: [{ description: 'The e-mail is sent once', verificationRef: 'COMMAND_EXECUTION' }] });
  });

  it('reads a pasted run-task.sh work-item JSON', () => {
    const draft = draftFromJSON(JSON.stringify({
      title: 'P1-01', effectiveScope: [{ repositoryId: 'api', access: 'WRITE', pathScopes: ['backend'], reason: 'fix' }, { repositoryId: 'web', access: 'READ', reason: 'read' }],
      contract: { behavior: 'Fix it', acceptanceCriteria: [{ description: 'Fixed' }], riskLevel: 'medium', workflowVersionId: 'WORKFLOW_VERSION_ID' },
    }), base);
    expect(draft).toMatchObject({ title: 'P1-01', behavior: 'Fix it', riskLevel: 'MEDIUM', workflowVersionId: 'wfv-1', scopes: [{ repositoryId: 'api', pathScopes: ['backend'] }] });
    expect(() => draftFromJSON('{not json', base)).toThrow(/Not valid JSON/);
  });
});
