/**
 * V12-22 (docs/design/15-v12-ui-operations.md, UX-09): the tasks a feature design wrote into docs/features/<slug>/tasks.json, turned
 * into child work items exactly as kit/scripts/split-tasks.sh does — the same title, scope (the task's directories plus docs, the
 * other repositories read-only), contract text and acceptance criteria — so the UI replaces the script.
 */
import type { ScopeGrant, WorkItemContract } from './work';
import { suggestWorkflow } from './newTask';
import type { RiskLevel, WorkflowChoice } from './newTask';

export interface TaskSpec {
  id: string;
  title: string;
  pathScopes: string[];
  riskLevel: RiskLevel;
  lane?: string;
  behavior: string;
  acceptanceCriteria: string[];
  repository?: string;
}

export interface TasksProblem {
  index: number;
  message: string;
}

/** Reads tasks.json: a JSON array of tasks. Each bad entry is reported, the good ones are returned. */
export function parseTasksJson(text: string): { tasks: TaskSpec[]; problems: TasksProblem[] } {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch (e) {
    return { tasks: [], problems: [{ index: -1, message: `Not valid JSON: ${e instanceof Error ? e.message : String(e)}` }] };
  }
  if (!Array.isArray(raw)) return { tasks: [], problems: [{ index: -1, message: 'tasks.json must be a JSON array of tasks.' }] };
  const tasks: TaskSpec[] = [];
  const problems: TasksProblem[] = [];
  const seen = new Set<string>();
  raw.forEach((entry, index) => {
    const t = (entry ?? {}) as Record<string, unknown>;
    const id = typeof t.id === 'string' ? t.id.trim() : '';
    const missing = ['id', 'title', 'behavior'].filter(k => typeof t[k] !== 'string' || !(t[k] as string).trim());
    if (!Array.isArray(t.acceptanceCriteria) || t.acceptanceCriteria.length === 0) missing.push('acceptanceCriteria');
    const risk = String(t.riskLevel ?? '').toUpperCase();
    if (!['LOW', 'MEDIUM', 'HIGH'].includes(risk)) missing.push('riskLevel (LOW, MEDIUM or HIGH)');
    if (missing.length > 0) {
      problems.push({ index, message: `Task ${id || `#${index + 1}`}: missing or invalid ${missing.join(', ')}.` });
      return;
    }
    if (seen.has(id)) {
      problems.push({ index, message: `Task ${id} appears twice.` });
      return;
    }
    seen.add(id);
    tasks.push({
      id, title: String(t.title).trim(), behavior: String(t.behavior).trim(), riskLevel: risk as RiskLevel,
      pathScopes: Array.isArray(t.pathScopes) ? t.pathScopes.map(String) : [],
      acceptanceCriteria: (t.acceptanceCriteria as unknown[]).map(String),
      ...(typeof t.lane === 'string' ? { lane: t.lane } : {}),
      ...(typeof t.repository === 'string' && t.repository.trim() ? { repository: t.repository.trim() } : {}),
    });
  });
  return { tasks, problems };
}

/** The workflow a task runs under: by its risk (LOW → task delivery, MEDIUM/HIGH → the plus variant), as the New task form suggests. */
export function workflowForTask(task: TaskSpec, choices: WorkflowChoice[]): WorkflowChoice | undefined {
  return suggestWorkflow({ kind: 'feature', riskLevel: task.riskLevel, scopes: [] }, choices);
}

export interface SplitContext {
  slug: string;
  /** The repository the feature documents live in (split-tasks.sh's $repo). */
  docsRepository: string;
  /** Every repository of the family. */
  repositories: string[];
  workflowVersionId: string;
  /** V12-33 (ADR-065): where the task's frame.md and plan.md go — REPO (docs/features, the default) or AW (.aw/features, kept out of git). */
  taskDocuments?: 'REPO' | 'AW' | string;
}

/** The createChildWorkItem body for one task — split-tasks.sh's jq program, field for field. */
export function childRequestForTask(task: TaskSpec, ctx: SplitContext): { title: string; parentJoinPolicy: string; effectiveScope: ScopeGrant[]; contract: WorkItemContract } {
  const target = task.repository ?? ctx.docsRepository;
  const pathScopes = [...new Set([...task.pathScopes, 'docs'])].sort();
  const others = ctx.repositories.filter(r => r !== target);
  const docsRoot = ctx.taskDocuments === 'AW' ? '.aw/features' : 'docs/features';
  const inAw = docsRoot !== 'docs/features'
    ? ` Thư mục tài liệu của task (frame.md, plan.md): ${docsRoot}/${ctx.slug}/tasks/${task.id}/ (chỉ trong aw, git bỏ qua, không commit).`
    : '';
  const crossRepo = target !== ctx.docsRepository
    ? ` Tài liệu tính năng nằm ở repository ${ctx.docsRepository} (chỉ đọc); file frame.md, plan.md của task này ghi vào ${docsRoot}/${ctx.slug}/tasks/${task.id}/ trong repository ${target}.`
    : '';
  return {
    title: `${task.id}: ${task.title}`,
    parentJoinPolicy: 'ALL_CHILDREN_DONE',
    effectiveScope: [
      { repositoryId: target, access: 'WRITE', reason: `Task ${task.id} của tính năng ${ctx.slug}`, pathScopes },
      ...others.map(r => ({ repositoryId: r, access: 'READ' as const, reason: 'Đọc hợp đồng và code liên quan' })),
    ],
    contract: {
      schemaVersion: 1,
      behavior: `Thư mục tài liệu: docs/features/${ctx.slug} (02-spec.md, 03-design.md, tasks.json). Task ${task.id}, lane đề xuất: ${task.lane ?? ''}. ${task.behavior}${inAw}${crossRepo}`,
      verificationSpec: 'GATE 1 (build + test của phần code bị đổi) và bước kiểm tra tĩnh đều thoát mã 0 với evidence COMMAND_EXECUTION; GATE 2: người vận hành duyệt diff.',
      riskLevel: task.riskLevel,
      acceptanceCriteria: task.acceptanceCriteria.map(description => ({ description, verificationRef: 'COMMAND_EXECUTION' })),
      workflowVersionId: ctx.workflowVersionId,
    },
  };
}
