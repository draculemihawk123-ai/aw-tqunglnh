import { describe, expect, it } from 'vitest';
import { childRequestForTask, parseTasksJson, workflowForTask } from './tasksJson';

const TASKS = JSON.stringify([
  { id: 'T-01', title: 'Order status API', pathScopes: ['backend'], riskLevel: 'LOW', lane: 'fast', behavior: 'GET /orders/{id}/status', acceptanceCriteria: ['AC-1: returns 200'] },
  { id: 'T-02', title: 'Status badge', pathScopes: ['src'], riskLevel: 'MEDIUM', lane: 'full', behavior: 'Show the badge', acceptanceCriteria: ['AC-2: badge'], repository: 'web' },
  { id: 'T-03', title: 'broken' },
]);

describe('tasks.json (V12-22)', () => {
  it('reads good tasks and reports bad ones', () => {
    const { tasks, problems } = parseTasksJson(TASKS);
    expect(tasks.map(t => t.id)).toEqual(['T-01', 'T-02']);
    expect(problems).toHaveLength(1);
    expect(problems[0].message).toMatch(/T-03: missing or invalid behavior, acceptanceCriteria, riskLevel/);
    expect(parseTasksJson('{}').problems[0].message).toMatch(/JSON array/);
  });

  it('builds the same child request as split-tasks.sh', () => {
    const [first, second] = parseTasksJson(TASKS).tasks;
    const ctx = { slug: 'order-status', docsRepository: 'api', repositories: ['api', 'web'], workflowVersionId: 'wf-v3' };
    const one = childRequestForTask(first, ctx);
    expect(one.title).toBe('T-01: Order status API');
    expect(one.effectiveScope).toEqual([
      { repositoryId: 'api', access: 'WRITE', reason: 'Task T-01 của tính năng order-status', pathScopes: ['backend', 'docs'] },
      { repositoryId: 'web', access: 'READ', reason: 'Đọc hợp đồng và code liên quan' },
    ]);
    expect(one.contract.behavior).toBe('Thư mục tài liệu: docs/features/order-status (02-spec.md, 03-design.md, tasks.json). Task T-01, lane đề xuất: fast. GET /orders/{id}/status');
    expect(one.contract.acceptanceCriteria).toEqual([{ description: 'AC-1: returns 200', verificationRef: 'COMMAND_EXECUTION' }]);
    const two = childRequestForTask(second, ctx);
    expect(two.effectiveScope[0].repositoryId).toBe('web');
    expect(two.contract.behavior).toContain('Tài liệu tính năng nằm ở repository api (chỉ đọc)');

    const choices = [
      { definitionId: 'p-wf-task-delivery', name: 'Task delivery', versionId: 'v1', versionNumber: 1 },
      { definitionId: 'p-wf-task-delivery-plus', name: 'Task delivery plus', versionId: 'v2', versionNumber: 2 },
    ];
    expect(workflowForTask(first, choices)?.versionId).toBe('v1');
    expect(workflowForTask(second, choices)?.versionId).toBe('v2');
  });
});
