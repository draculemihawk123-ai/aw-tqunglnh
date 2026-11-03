import { test, expect, type Locator, type Page } from '@playwright/test';
import { publishApprovalWorkflow, publishBlockerWorkflow } from './support/definitions';

/**
 * V12-13 (docs/design/15-v12-ui-operations.md, part B) against the real `aw serve` + `aw worker` pair (fake provider):
 *   - a task with two runs: the graph merges both (×2 on the agent node), the history panel lists Run 1 and Run 2, and every node's
 *     panel opens from the keyboard (Tab to it, Enter);
 *   - the banner names the newest run and why it stopped (stopReason), and the old Timeline column is gone;
 *   - an approval waiting: the banner says so, clicking it opens the gate's panel, and the decision is taken there.
 * Not covered here (no fixture workflow with COMMAND loops on this stack yet): a loop run three times, a node blocked then retried.
 */

const CREATE_WORK_ITEM = /\/projects\/[^/]+\/work-items$/;
const MARK_READY = /\/work-items\/[^/]+\/mark-ready$/;
const REGISTER_REPOSITORY = /\/projects\/[^/]+\/repositories$/;

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

async function createProject(page: Page, name: string): Promise<string> {
  await page.goto('/ui/projects');
  await page.getByRole('button', { name: 'New Project' }).click();
  await page.getByPlaceholder('my-project').fill(name);
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/ui\/projects\/[0-9a-f-]+$/);
  return page.url().split('/projects/')[1];
}


async function createWorkItem(page: Page, projectId: string, title: string, versionId: string, repoName: string) {
  await page.goto(`/ui/projects/${projectId}/board`);
  await page.getByRole('button', { name: 'New WorkItem' }).click();
  const wi = page.getByRole('dialog', { name: 'Create WorkItem' });
  await wi.getByLabel('Title').fill(title);
  await wi.getByRole('button', { name: 'Add repository' }).click();
  await wi.getByLabel('Repository').selectOption({ label: repoName });
  await wi.getByLabel('Reason').fill('scope for the V12 journey');
  await wi.getByRole('checkbox', { name: /Add a readiness contract now/ }).check();
  await wi.getByLabel('Behavior (WHAT)').fill('Exercise the run graph');
  await wi.getByRole('button', { name: 'Add criterion' }).click();
  await wi.getByLabel('Description').fill('Run reaches a terminal state');
  await wi.getByLabel('Verification ref').fill('manual:operator-review');
  await wi.getByLabel('Verification spec').fill('Operator observes the real terminal state');
  await wi.getByLabel('Risk level').fill('low');
  await wi.getByLabel('Workflow version ID').fill(versionId);
  await awaitMutation(page, () => wi.getByRole('button', { name: 'Create' }).click(), CREATE_WORK_ITEM);
  await page.goto(`/ui/projects/${projectId}/board`);
  await expect.poll(async () => { await page.reload(); return settledCount(page.locator('button', { hasText: 'Mark Ready' })); }, { timeout: 30_000 }).toBeGreaterThan(0);
  await awaitMutation(page, () => page.locator('button', { hasText: 'Mark Ready' }).first().click(), MARK_READY);
  await expect.poll(async () => { await page.reload(); return settledCount(page.getByText(title)); }, { timeout: 30_000 }).toBeGreaterThan(0);
  await page.getByText(title).first().click();
  await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]+$/);
  return page.url().split('/tasks/')[1];
}

async function startRunWhenReady(page: Page) {
  await expect.poll(async () => { await page.reload(); return settledCount(page.getByRole('button', { name: 'Start Run' })); }, { timeout: 45_000 }).toBeGreaterThan(0);
  await awaitMutation(page, () => page.getByRole('button', { name: 'Start Run' }).click(), /\/work-items\/[^/]+\/runs$/);
}

test.describe.configure({ mode: 'serial' });

test('V12 run graph: two runs merged, keyboard opens every node, banner and approval', async ({ page, request, baseURL }) => {
  test.setTimeout(240_000);
  const base = baseURL!;
  // The session token the shell is handed (the same capture the V11 journey uses).
  await page.addInitScript(() => {
    let value: unknown;
    Object.defineProperty(window, '__AW_BOOTSTRAP__', {
      configurable: true,
      set(v) { value = v; (window as unknown as { __AW_BOOTSTRAP_CAPTURED__?: unknown }).__AW_BOOTSTRAP_CAPTURED__ = v; },
      get() { return value; },
    });
  });
  await page.goto('/ui/doctor');
  const token = await page.evaluate(() => (window as unknown as { __AW_BOOTSTRAP_CAPTURED__: { token: string } }).__AW_BOOTSTRAP_CAPTURED__.token);
  expect(token, 'no session token').not.toBe('');
  const headers = { 'X-Aw-Session-Token': token };

  const projectId = await createProject(page, 'e2e-v12-graph');
  await page.getByRole('button', { name: 'Register Repository' }).click();
  const reg = page.getByRole('dialog', { name: 'Register Repository' });
  await reg.getByLabel('Repository ID').fill('repo-v12');
  await reg.locator('#field-name').fill('graph-repo');
  await reg.getByLabel('Local repository path').fill(process.env.AW_E2E_REPO_READY!);
  await reg.getByLabel('Default ref').fill('master');
  await reg.getByLabel('Default ref').blur();
  await awaitMutation(page, () => reg.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);
  await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('ACTIVE', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);

  const builds = await (await request.get(`${base}/adapter-builds`, { headers })).json() as { builds: { id: string; providerKey: string }[] };
  const build = builds.builds.find(b => b.providerKey === 'claude');
  test.skip(!build, 'the V11 journey registers the fake provider build; run this spec after it');

  await test.step('a task with two runs: both merged in the graph and in the node history', async () => {
    const workflow = await publishBlockerWorkflow(request, base, token, projectId, 'v12', build!.id);
    await createWorkItem(page, projectId, 'V12 two runs', workflow.versionId, 'graph-repo');
    await startRunWhenReady(page);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('COMPLETION_POLICY_FAILED')); }, { timeout: 45_000 }).toBeGreaterThan(0);
    await page.getByRole('button', { name: 'Run Again' }).click();
    const dialog = page.getByRole('dialog', { name: 'Run Again' });
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Run Again' }).click(), /\/work-items\/[^/]+\/retry$/);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByRole('list', { name: 'Runs of this WorkItem' }).getByRole('listitem'), 2); },
      { timeout: 45_000 }).toBeGreaterThanOrEqual(2);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('COMPLETION_POLICY_FAILED')); }, { timeout: 45_000 }).toBeGreaterThan(0);

    await page.getByRole('tab', { name: 'Graph & Timeline' }).click();
    const graph = page.getByRole('group', { name: /Workflow graph for run/ });
    await expect(graph).toBeVisible();
    await expect(page.getByTestId('run-banner')).toContainText('Run 2 of 2');
    await expect(page.getByRole('list', { name: 'Run timeline' })).toHaveCount(0); // the old Timeline column is gone
    await expect(page.getByTestId('runs-maker')).toContainText('×2');

    // Keyboard: Tab onto each node and press Enter — its panel opens.
    for (const key of ['start', 'maker', 'end']) {
      const node = graph.getByRole('button', { name: new RegExp(`^${key},`) });
      await node.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('complementary', { name: `History of ${key}` })).toBeVisible();
    }
    const panel = page.getByRole('complementary', { name: 'History of end' });
    await expect(panel).toBeVisible();
    await graph.getByRole('button', { name: /^maker,/ }).focus();
    await page.keyboard.press('Enter');
    const maker = page.getByRole('complementary', { name: 'History of maker' });
    await expect(maker.getByText('Run 1')).toBeVisible();
    await expect(maker.getByText('Run 2')).toBeVisible();
  });

  await test.step('an approval waiting: the banner says so and the decision is taken in the gate\'s panel', async () => {
    const workflow = await publishApprovalWorkflow(request, base, token, projectId, 'v12');
    await createWorkItem(page, projectId, 'V12 approval', workflow.versionId, 'graph-repo');
    await startRunWhenReady(page);
    await page.getByRole('tab', { name: 'Graph & Timeline' }).click();
    const reason = page.getByTestId('stop-reason');
    await expect.poll(async () => { await page.reload(); await page.getByRole('tab', { name: 'Graph & Timeline' }).click(); return settledCount(reason); },
      { timeout: 45_000, message: 'the banner never said the approval is waiting' }).toBeGreaterThan(0);
    await expect(reason).toContainText('review is waiting for a decision');
    await reason.click();
    const gate = page.getByRole('complementary', { name: 'History of review' });
    await expect(gate).toBeVisible();
    await gate.getByRole('button', { name: 'approved' }).click();
    await expect(page.getByText(/Approval resolved/)).toBeVisible();
  });
});
