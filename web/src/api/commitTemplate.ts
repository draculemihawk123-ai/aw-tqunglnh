/**
 * V12-25 (docs/design/15-v12-ui-operations.md, UX-13, ADR-058): the default commit message of an approval-bound commit, from the
 * project's template — the same rules as internal/app/catalog.RenderCommitTemplate: {title} is the work item title without the redo
 * suffix " (lần N)" (and without a leading "T-01: " when the template places {taskId} itself), {taskId} that id, {workItemId} the id.
 * The release marker is never part of it: the server appends it as a trailer.
 */
export const DEFAULT_COMMIT_TEMPLATE = '{title}';

const REDO_SUFFIX = /\s*\(lần \d+\)\s*$/;
const TASK_ID_PREFIX = /^([A-Z][A-Z0-9]*-\d+[a-z]?):\s+/;

export function renderCommitTemplate(template: string | undefined, title: string, workItemId: string): string {
  const t = template?.trim() ? template : DEFAULT_COMMIT_TEMPLATE;
  let clean = title.replace(REDO_SUFFIX, '').trim();
  let taskId = '';
  const m = TASK_ID_PREFIX.exec(clean);
  if (m) {
    taskId = m[1];
    if (t.includes('{taskId}')) clean = clean.slice(m[0].length).trim();
  }
  return t.split('{title}').join(clean).split('{taskId}').join(taskId).split('{workItemId}').join(workItemId).trim();
}
