import { test, expect, type Locator, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { initRealGitRepo } from './support/stack';
import { publishBlockerWorkflow } from './support/definitions';

/**
 * V11 operator journeys (docs/design/14-v11-bugfix.md) against the same real `aw serve` + `aw worker` pair the V7 full journey uses (fake provider):
 *   V11-12  a folder that was never `git init`ed: the register dialog says so BEFORE registering, the BLOCKED card says why and what to do, the
 *           operator fixes it from the card (Edit), removes a wrong registration (Delete) and retires a repository (Disable);
 *   V11-13  rename, archive (refused while work is open, with the reasons), read-only banner, restore;
 *   V11-10/11  a real failed run: click a node and see what it did, the Chat shows the engine's milestones, Run Again.
 * Nothing here edits sqlite or git "by hand" except creating the source folders an operator would already have on disk.
 */

const REGISTER_REPOSITORY = /\/projects\/[^/]+\/repositories$/;
const CREATE_PROJECT = /\/projects$/;
const CREATE_WORK_ITEM = /\/projects\/[^/]+\/work-items$/;
const MARK_READY = /\/work-items\/[^/]+\/mark-ready$/;
const REGISTER_ADAPTER_BUILD = /\/adapter-builds$/;

async function awaitMutation(page: Page, trigger: () => Promise<unknown>, pathPattern: RegExp, method = 'POST'): Promise<void> {
  const [response] = await Promise.all([
    page.waitForResponse(resp => resp.request().method() === method && pathPattern.test(new URL(resp.url()).pathname)),
    trigger(),
  ]);
  if (!response.ok()) throw new Error(`${method} ${new URL(response.url()).pathname} failed: ${response.status()} ${await response.text()}`);
}

async function settledCount(locator: Locator, atLeast = 1): Promise<number> {
  const deadline = Date.now() + 5_000;
  let n = await locator.count();
  while (n < atLeast && Date.now() < deadline) {
    await locator.page().waitForTimeout(100);
    n = await locator.count();
  }
  return n;
}

function git(dir: string, ...args: string[]) {
  execFileSync('git', args, { cwd: dir, stdio: 'pipe' });
}

async function createProject(page: Page, name: string): Promise<string> {
  await page.goto('/ui/projects');
  await page.getByRole('button', { name: 'New Project' }).click();
  await page.getByPlaceholder('my-project').fill(name);
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/ui\/projects\/[0-9a-f-]+$/);
  return page.url().split('/projects/')[1];
}

async function fillRegisterDialog(page: Page, id: string, name: string, repoPath: string, ref: string) {
  await page.getByRole('button', { name: 'Register Repository' }).click();
  const dialog = page.getByRole('dialog', { name: 'Register Repository' });
  await dialog.getByLabel('Repository ID').fill(id);
  await dialog.locator('#field-name').fill(name);
  await dialog.getByLabel('Local repository path').fill(repoPath);
  await dialog.getByLabel('Default ref').fill(ref);
  await dialog.getByLabel('Default ref').blur();
  return dialog;
}

/** The repository card by its name (the card is the nearest container holding that name and its buttons). */
function repoCard(page: Page, name: string): Locator {
  return page.locator('div.px-5.py-4', { has: page.getByText(name, { exact: true }) });
}

test.describe.configure({ mode: 'serial' });

test('V11 operator journeys: repository mistakes, project lifecycle, a failed run and its nodes', async ({ page, request, baseURL }) => {
  test.setTimeout(240_000);
  const base = baseURL!;
  const scratch = mkdtempSync(path.join(tmpdir(), 'aw-e2e-v11-'));
  let token = '';

  await page.addInitScript(() => {
    let value: unknown;
    Object.defineProperty(window, '__AW_BOOTSTRAP__', {
      configurable: true,
      set(v) { value = v; (window as unknown as { __AW_BOOTSTRAP_CAPTURED__?: unknown }).__AW_BOOTSTRAP_CAPTURED__ = v; },
      get() { return value; },
    });
  });
  await page.goto('/ui/doctor');
  token = await page.evaluate(() => (window as unknown as { __AW_BOOTSTRAP_CAPTURED__: { token: string } }).__AW_BOOTSTRAP_CAPTURED__.token);

  // ---------------------------------------------------------------- V11-12
  const reposProject = await createProject(page, 'e2e-v11-repositories');
  const folder = path.join(scratch, 'bpm-gl');
  mkdirSync(folder, { recursive: true });

  await test.step('V11-12: a folder that was never `git init`ed is caught before registering, with the command to fix it', async () => {
    const dialog = await fillRegisterDialog(page, 'r-wrong', 'bpm-gl', folder, 'main');
    const failure = dialog.getByTestId('check-failure');
    await expect(failure).toContainText('NOT_A_GIT_REPOSITORY');
    await expect(failure).toContainText('init -b main');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);
  });

  await test.step('V11-12: the BLOCKED card says what is wrong and what to do — no terminal needed to find the reason', async () => {
    const note = page.getByTestId('repo-error-r-wrong');
    await expect.poll(async () => { await page.reload(); return settledCount(note); }, { timeout: 30_000, message: 'the repository never reached BLOCKED with a reason' }).toBeGreaterThan(0);
    await expect(note).toContainText('NOT_A_GIT_REPOSITORY');
    await expect(note).toContainText('is not a Git repository yet');
    await expect(note).toContainText('init -b main');
    await note.getByRole('button', { name: 'Load Git output' }).click();
    await note.getByRole('button', { name: 'Show Git output' }).click();
    await expect(note.getByTestId('failure-detail')).toContainText(/not a git repository/i);
  });

  await test.step('V11-12: the operator runs git init (branch trunk), retries, and the card now says the ref is wrong and lists the branches', async () => {
    git(folder, 'init', '-q', '-b', 'trunk');
    git(folder, 'config', 'user.email', 'e2e@example.invalid');
    git(folder, 'config', 'user.name', 'e2e');
    git(folder, 'commit', '-q', '--allow-empty', '-m', 'init');
    await page.reload();
    await page.getByRole('button', { name: 'Retry Probe' }).click();
    const note = page.getByTestId('repo-error-r-wrong');
    await expect.poll(async () => { await page.reload(); return settledCount(note.getByText('REF_NOT_FOUND')); }, { timeout: 30_000, message: 'the retried probe never reported REF_NOT_FOUND' }).toBeGreaterThan(0);
    await note.getByRole('button', { name: 'Load Git output' }).click();
    await note.getByRole('button', { name: 'Show Git output' }).click();
    await expect(note.getByTestId('failure-detail')).toContainText('trunk');
  });

  await test.step('V11-12: Edit corrects the default ref from the card and the repository becomes ACTIVE', async () => {
    await page.getByRole('button', { name: 'Edit bpm-gl' }).click();
    const dialog = page.getByRole('dialog', { name: 'Edit bpm-gl' });
    await dialog.getByLabel('Default ref').fill('trunk');
    await dialog.getByLabel('Default ref').blur();
    await expect(dialog.getByTestId('check-ok')).toBeVisible();
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Save and Probe' }).click(), /\/repositories\/[^/]+$/, 'PATCH');
    await expect.poll(async () => { await page.reload(); return settledCount(repoCard(page, 'bpm-gl').getByText('ACTIVE', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await expect(page.getByRole('button', { name: 'Edit bpm-gl' })).toHaveCount(0); // an ACTIVE repository cannot be edited
  });

  await test.step('V11-12: a wrong registration that never became ACTIVE is deleted from the card, and does not come back', async () => {
    const missing = path.join(scratch, 'does-not-exist');
    const dialog = await fillRegisterDialog(page, 'r-typo', 'typo', missing, 'main');
    await expect(dialog.getByTestId('check-failure')).toContainText('PATH_NOT_FOUND');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByTestId('repo-error-r-typo')); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await page.getByRole('button', { name: 'Delete typo' }).click();
    const confirm = page.getByRole('dialog', { name: 'Delete typo' });
    await expect(confirm.getByRole('button', { name: 'Delete permanently' })).toBeDisabled();
    await confirm.getByLabel('Reason').fill('typo in the path');
    await awaitMutation(page, () => confirm.getByRole('button', { name: 'Delete permanently' }).click(), /\/repositories\/[^/]+$/, 'DELETE');
    await page.reload();
    await expect(page.getByText('typo', { exact: true })).toHaveCount(0);
  });

  await test.step('V11-12: an ACTIVE repository is disabled, hidden from the list, and shown again by "Show disabled"', async () => {
    await page.getByRole('button', { name: 'Disable bpm-gl' }).click();
    const dialog = page.getByRole('dialog', { name: 'Disable bpm-gl' });
    await dialog.getByLabel(/Reason/).fill('moved to another path');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Disable', exact: true }).click(), /\/repositories\/[^/]+\/disable$/);
    await expect(page.getByText('bpm-gl', { exact: true })).toHaveCount(0);
    await page.getByLabel('Show disabled').check();
    await expect(page.getByText('bpm-gl', { exact: true })).toBeVisible();
    await expect(page.getByText('DISABLED').first()).toBeVisible();
    // the name is free again
    const again = await fillRegisterDialog(page, 'r-again', 'bpm-gl', folder, 'trunk');
    await expect(again.getByTestId('check-ok')).toBeVisible();
    await again.getByRole('button', { name: 'Cancel' }).click();
  });

  // ---------------------------------------------------------------- V11-13 (empty project)
  await test.step('V11-13: rename, archive an idle project, see it read-only, restore it', async () => {
    await page.goto('/ui/projects');
    await page.getByRole('button', { name: 'Rename e2e-v11-repositories' }).click();
    const rename = page.getByRole('dialog');
    await rename.getByLabel(/Project name/).fill('e2e-v11-renamed');
    await awaitMutation(page, () => rename.getByRole('button', { name: 'Rename' }).click(), /\/projects\/[^/]+$/, 'PATCH');
    await expect(page.getByText('e2e-v11-renamed', { exact: true })).toBeVisible();

    await page.getByRole('button', { name: 'Archive e2e-v11-renamed' }).click();
    const archive = page.getByRole('dialog');
    await expect(archive.getByText(/does not delete anything/)).toBeVisible();
    await awaitMutation(page, () => archive.getByRole('button', { name: 'Archive', exact: true }).click(), /\/projects\/[^/]+\/archive$/);
    await expect(page.getByText('e2e-v11-renamed', { exact: true })).toHaveCount(0); // hidden from the default list

    await page.getByLabel('Show archived').check();
    await page.getByText('e2e-v11-renamed', { exact: true }).click();
    await expect(page.getByTestId('archived-banner')).toContainText('archived and read-only');
    await expect(page.getByRole('button', { name: 'Register Repository' })).toBeDisabled();

    // the API says the same thing the banner does
    const refused = await request.post(`${base}/projects/${reposProject}/repositories`, {
      headers: { 'X-Aw-Session-Token': token, 'Idempotency-Key': crypto.randomUUID() },
      data: { repositoryId: 'late', name: 'late', remoteLocator: folder, defaultRef: 'trunk' },
    });
    expect(refused.status()).toBe(409);
    expect(await refused.text()).toContain('PROJECT_ARCHIVED');

    await awaitMutation(page, () => page.getByTestId('archived-banner').getByRole('button', { name: 'Restore' }).click(), /\/projects\/[^/]+\/restore$/);
    await expect(page.getByTestId('archived-banner')).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Register Repository' })).toBeEnabled();
  });

  // ---------------------------------------------------------------- V11-10 / V11-11 / V11-13 (busy project)
  const runsProject = await createProject(page, 'e2e-v11-runs');
  let childId = '';
  let pinnedVersion = '';
  let republishWorkflow: () => Promise<{ versionId: string }> = async () => { throw new Error('workflow not published yet'); };

  await test.step('setup: a ready repository, the fake provider build, the blocker workflow', async () => {
    const dialog = await fillRegisterDialog(page, 'repo-v11', 'runs-repo', process.env.AW_E2E_REPO_READY!, 'master');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('ACTIVE', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);

    const existing = await (await request.get(`${base}/adapter-builds`, { headers: { 'X-Aw-Session-Token': token } })).json() as { builds: { id: string; providerKey: string }[] };
    let build = existing.builds.find(b => b.providerKey === 'claude');
    if (!build) {
      await page.goto('/ui/doctor');
      await page.getByRole('button', { name: 'Probe new build' }).click();
      const probe = page.getByRole('dialog', { name: 'Probe adapter build' });
      await probe.locator('#field-provider-key').fill('claude');
      await probe.locator('#field-executable-path').fill(process.env.AW_E2E_FAKE_CLAUDE_PATH!);
      await probe.locator('#field-protocol-version').fill('claude-stream-json/v1');
      await probe.locator('#field-os').fill(process.platform === 'win32' ? 'windows' : 'linux');
      await probe.locator('#field-toolchain').fill(process.env.AW_E2E_GO_VERSION!);
      await probe.locator('#field-config-identity').fill('e2e-default');
      await probe.locator('#field-canonical-event-kinds').fill('EXECUTION_STARTED,STATUS_CHANGED,ASSISTANT_MESSAGE,TOOL_CALL_STARTED,TOOL_CALL_FINISHED,USAGE_REPORTED,CHECKPOINT_PROPOSED,DIAGNOSTIC,EXECUTION_FINISHED');
      await probe.getByRole('checkbox', { name: 'Supports resume' }).check();
      await probe.getByRole('checkbox', { name: 'Supports cancel' }).check();
      await probe.getByRole('button', { name: 'Probe' }).click();
      await awaitMutation(page, () => page.getByRole('button', { name: 'Confirm & register' }).click(), REGISTER_ADAPTER_BUILD);
      const after = await (await request.get(`${base}/adapter-builds`, { headers: { 'X-Aw-Session-Token': token } })).json() as { builds: { id: string; providerKey: string }[] };
      build = after.builds.find(b => b.providerKey === 'claude');
    }
    expect(build).toBeTruthy();
    const workflow = await publishBlockerWorkflow(request, base, token, runsProject, 'v11', build!.id);
    republishWorkflow = workflow.republish;
    pinnedVersion = workflow.versionId;

    await page.goto(`/ui/projects/${runsProject}/board`);
    await page.getByRole('button', { name: 'New WorkItem' }).click();
    const wi = page.getByRole('dialog', { name: 'Create WorkItem' });
    await wi.getByLabel('Title').fill('V11 failed run');
    await wi.getByRole('button', { name: 'Add repository' }).click();
    await wi.getByLabel('Repository').selectOption({ label: 'runs-repo' });
    await wi.getByLabel('Reason').fill('scope for the V11 journey');
    await wi.getByRole('checkbox', { name: /Add a readiness contract now/ }).check();
    await wi.getByLabel('Behavior (WHAT)').fill('Fail the completion policy on purpose');
    await wi.getByRole('button', { name: 'Add criterion' }).click();
    await wi.getByLabel('Description').fill('Run reaches a terminal state');
    await wi.getByLabel('Verification ref').fill('manual:operator-review');
    await wi.getByLabel('Verification spec').fill('Operator observes the real terminal state');
    await wi.getByLabel('Risk level').fill('low');
    await wi.getByLabel('Workflow version ID').fill(workflow.versionId);
    await awaitMutation(page, () => wi.getByRole('button', { name: 'Create' }).click(), CREATE_WORK_ITEM);
  });

  let taskId = '';
  await test.step('the run starts and fails its completion policy for real', async () => {
    await page.goto(`/ui/projects/${runsProject}/board`);
    await expect.poll(async () => { await page.reload(); return settledCount(page.locator('button', { hasText: 'Mark Ready' })); }, { timeout: 30_000, message: 'the work item never reached the board' }).toBeGreaterThan(0);
    await awaitMutation(page, () => page.locator('button', { hasText: 'Mark Ready' }).first().click(), MARK_READY);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('V11 failed run')); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await page.getByText('V11 failed run').click();
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]+$/);
    taskId = page.url().split('/tasks/')[1];
    const detail = await (await request.get(`${base}/projects/${runsProject}/work-items/${taskId}`, { headers: { 'X-Aw-Session-Token': token } })).json() as { familyId: string };
    await expect.poll(async () => {
      const resp = await request.get(`${base}/projects/${runsProject}/workspace-sets/${detail.familyId}`, { headers: { 'X-Aw-Session-Token': token } });
      return resp.ok() ? ((await resp.json()) as { state: string }).state : 'unknown';
    }, { timeout: 30_000 }).toBe('READY');
    await page.reload();
    await awaitMutation(page, () => page.getByRole('button', { name: 'Start Run' }).click(), /\/work-items\/[^/]+\/runs$/);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('COMPLETION_POLICY_FAILED')); }, { timeout: 45_000, message: 'no COMPLETION_POLICY_FAILED blocker' }).toBeGreaterThan(0);
  });

  await test.step('V11-11: clicking the node shows what it was asked, what it did, what came of it', async () => {
    await page.getByRole('tab', { name: 'Graph & Timeline' }).click();
    await page.getByRole('list', { name: /nodes/i }).getByRole('button', { name: /maker/ }).first().click().catch(async () => {
      await page.getByText('maker', { exact: true }).first().click();
    });
    const pane = page.getByTestId('node-detail');
    await expect(pane).toBeVisible();
    await expect(pane.getByTestId('node-input')).toContainText('MAKER');
    await expect(pane.getByTestId('node-progress')).toBeVisible();
    await expect(pane.getByTestId('node-result')).toBeVisible();
  });

  await test.step('V11-11: the Chat tab carries the engine\'s milestones and "only my messages" hides them', async () => {
    await page.getByRole('tab', { name: 'Chat' }).click();
    await expect(page.getByText(/maker started/)).toBeVisible();
    await expect(page.getByText(/maker finished|maker failed/)).toBeVisible();
    await page.getByRole('button', { name: 'only my messages' }).click();
    await expect(page.getByText(/maker started/)).toHaveCount(0);
  });

  await test.step('V11-11: the Evidence tab names the node', async () => {
    await page.getByRole('tab', { name: 'Evidence' }).click();
    const filter = page.getByLabel('Filter evidence by node');
    await expect(filter).toContainText('maker');
    await filter.selectOption('maker');
    await expect(page.locator('span', { hasText: /^maker$/ }).first()).toBeVisible(); // each row names its node
    await expect(page.getByText(/No evidence recorded for node/)).toHaveCount(0);
  });

  await test.step('V11-10: Run Again resolves the blocker and starts a new run in one action', async () => {
    await page.getByRole('tab', { name: 'Overview' }).click();
    await page.getByRole('button', { name: 'Run Again' }).click();
    const dialog = page.getByRole('dialog', { name: 'Run Again' });
    await expect(dialog.getByText('Runs with (pinned)')).toBeVisible();
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Run Again' }).click(), /\/work-items\/[^/]+\/retry$/);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByRole('list', { name: 'Runs of this WorkItem' }).getByRole('listitem'), 2); },
      { timeout: 45_000, message: 'the second run never appeared in the Runs list' }).toBeGreaterThanOrEqual(2);
    await expect(page.getByRole('list', { name: 'Runs of this WorkItem' })).toContainText('Run 2');
  });

  await test.step('V11-10: a CHILD work item is redone on the newest version of its workflow (a root cannot be redone)', async () => {
    // The root above has no Redo button: a root work item owns its family, so only a child can be redone (ADR-041).
    await expect(page.getByRole('button', { name: 'Redo with Latest Version' })).toHaveCount(0);

    const headers = { 'X-Aw-Session-Token': token };
    const mutate = { ...headers, 'Idempotency-Key': crypto.randomUUID() };
    const created = await request.post(`${base}/projects/${runsProject}/work-items/${taskId}/children`, {
      headers: mutate,
      data: {
        title: 'V11 child', parentJoinPolicy: 'e2e-child',
        effectiveScope: [{ repositoryId: 'repo-v11', access: 'WRITE', pathScopes: ['**'], reason: 'child scope for the redo journey' }],
        contract: {
          schemaVersion: 1, behavior: 'Fail the completion policy on purpose', verificationSpec: 'Operator observes the real terminal state', riskLevel: 'low',
          acceptanceCriteria: [{ description: 'Run reaches a terminal state', verificationRef: 'manual:operator-review' }], workflowVersionId: pinnedVersion,
        },
      },
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    childId = ((await created.json()) as { workItemId: string }).workItemId;
    const current = await (await request.get(`${base}/projects/${runsProject}/work-items/${childId}`, { headers })).json() as { version: number };
    const ready = await request.post(`${base}/work-items/${childId}/mark-ready`, { headers: { ...headers, 'Idempotency-Key': crypto.randomUUID(), 'If-Match': `"${current.version}"` }, data: {} });
    expect(ready.ok(), await ready.text()).toBeTruthy();

    await page.goto(`/ui/projects/${runsProject}/tasks/${childId}`);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByRole('button', { name: 'Start Run' })); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await awaitMutation(page, () => page.getByRole('button', { name: 'Start Run' }).click(), /\/work-items\/[^/]+\/runs$/);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('COMPLETION_POLICY_FAILED')); }, { timeout: 45_000, message: 'the child run never failed its completion policy' }).toBeGreaterThan(0);

    // The workflow is fixed and published again: version 2 of the SAME definition.
    const newer = await republishWorkflow();
    await page.reload();
    await page.getByRole('button', { name: 'Redo with Latest Version' }).click();
    const dialog = page.getByRole('dialog', { name: 'Redo with Latest Version' });
    await expect(dialog.getByText('Pinned workflow version', { exact: true })).toBeVisible();
    await expect(dialog.getByText('Newest version', { exact: true })).toBeVisible();
    await expect(dialog).toContainText(newer.versionId.slice(0, 8));
    await dialog.getByLabel('Reason').fill('workflow fixed in e2e');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Cancel and Redo' }).click(), /\/work-items\/[^/]+\/recreate$/);
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]+$/);
    expect(page.url().split('/tasks/')[1]).not.toBe(childId);
    await expect(page.getByText(/replaces/i).first()).toBeVisible();
  });

  await test.step('V11-13: a project with open work cannot be archived, and the dialog lists what is open', async () => {
    await page.goto('/ui/projects');
    await page.getByRole('button', { name: 'Archive e2e-v11-runs' }).click();
    const archive = page.getByRole('dialog');
    await archive.getByRole('button', { name: 'Archive', exact: true }).click();
    await expect(archive.getByRole('alert')).toContainText('PROJECT_BUSY');
    await expect(archive.getByTestId('project-holders')).toContainText(/WORK_ITEM|WORKFLOW_RUN|JOB/);
  });
});
