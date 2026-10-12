import { test, expect, type Locator, type Page } from '@playwright/test';
import { publishBlockerWorkflow } from './support/definitions';

/**
 * Opt-in (AW_E2E_CLAUDE_EXECUTABLE=<path to the real claude CLI>): one run of the V11-11 surfaces against the REAL Claude CLI instead of the fake provider —
 * what the node pane, the Chat milestones and a failed or finished attempt look like with real tool calls and real words. It spends real money (the worker
 * is started with --claude-max-budget-usd, default 0.4) and is skipped everywhere else, so the normal suite never touches the provider.
 */
test.skip(!process.env.AW_E2E_CLAUDE_EXECUTABLE, 'set AW_E2E_CLAUDE_EXECUTABLE to run against the real Claude CLI');

async function awaitMutation(page: Page, trigger: () => Promise<unknown>, pathPattern: RegExp): Promise<void> {
  const [response] = await Promise.all([
    page.waitForResponse(resp => resp.request().method() === 'POST' && pathPattern.test(new URL(resp.url()).pathname)),
    trigger(),
  ]);
  if (!response.ok()) throw new Error(`POST ${new URL(response.url()).pathname} failed: ${response.status()} ${await response.text()}`);
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

test('real Claude: a node that really ran, seen through the node pane and the Chat', async ({ page, request, baseURL }, testInfo) => {
  test.setTimeout(600_000);
  const base = baseURL!;
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
  const headers = { 'X-Aw-Session-Token': token };

  await page.goto('/ui/projects');
  await page.getByRole('button', { name: 'New Project' }).click();
  await page.getByPlaceholder('my-project').fill('e2e-real-claude');
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click();
  await expect(page).toHaveURL(/\/ui\/projects\/[0-9a-f-]+$/);
  const projectId = page.url().split('/projects/')[1];

  await page.getByRole('button', { name: 'Register Repository' }).click();
  const reg = page.getByRole('dialog', { name: 'Register Repository' });
  await reg.getByLabel('Repository ID').fill('repo-real');
  await reg.locator('#field-name').fill('real-repo');
  await reg.getByLabel('Local repository path').fill(process.env.AW_E2E_REPO_READY!);
  await reg.getByLabel('Default ref').fill('master');
  await awaitMutation(page, () => reg.getByRole('button', { name: 'Register and Probe' }).click(), /\/projects\/[^/]+\/repositories$/);
  await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('ACTIVE', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);

  await page.goto('/ui/doctor');
  await page.getByRole('button', { name: 'Probe new build' }).click();
  const probe = page.getByRole('dialog', { name: 'Probe adapter build' });
  await probe.locator('#field-provider-key').fill('claude');
  await probe.locator('#field-executable-path').fill(process.env.AW_E2E_CLAUDE_EXECUTABLE!);
  await probe.locator('#field-protocol-version').fill('claude-stream-json/v1');
  await probe.locator('#field-os').fill(process.platform === 'win32' ? 'windows' : 'linux');
  await probe.locator('#field-toolchain').fill(process.env.AW_E2E_GO_VERSION!);
  await probe.locator('#field-config-identity').fill('e2e-real');
  await probe.locator('#field-canonical-event-kinds').fill('EXECUTION_STARTED,STATUS_CHANGED,ASSISTANT_MESSAGE,TOOL_CALL_STARTED,TOOL_CALL_FINISHED,USAGE_REPORTED,CHECKPOINT_PROPOSED,DIAGNOSTIC,EXECUTION_FINISHED');
  await probe.getByRole('checkbox', { name: 'Supports resume' }).check();
  await probe.getByRole('checkbox', { name: 'Supports cancel' }).check();
  await probe.getByRole('button', { name: 'Probe' }).click();
  await awaitMutation(page, () => page.getByRole('button', { name: 'Confirm & register' }).click(), /\/adapter-builds$/);
  const builds = await (await request.get(`${base}/adapter-builds`, { headers })).json() as { builds: { id: string; providerKey: string }[] };
  const build = builds.builds.find(b => b.providerKey === 'claude')!;
  const workflow = await publishBlockerWorkflow(request, base, token, projectId, 'real', build.id);

  await page.goto(`/ui/projects/${projectId}/board`);
  await page.getByRole('button', { name: 'New WorkItem' }).click();
  const wi = page.getByRole('dialog', { name: 'Create WorkItem' });
  await wi.getByLabel('Title').fill('Real Claude: add a hello file');
  await wi.getByRole('button', { name: 'Add repository' }).click();
  await wi.getByLabel('Repository').selectOption({ label: 'real-repo' });
  await wi.getByLabel('Reason').fill('a tiny real task');
  await wi.getByRole('checkbox', { name: /Add a readiness contract now/ }).check();
  await wi.getByLabel('Behavior (WHAT)').fill('Create a file hello.txt containing the single line: hello from aw. Do nothing else.');
  await wi.getByRole('button', { name: 'Add criterion' }).click();
  await wi.getByLabel('Description').fill('hello.txt exists');
  await wi.getByLabel('Verification ref').fill('manual:operator-review');
  await wi.getByLabel('Verification spec').fill('Operator reads the file');
  await wi.getByLabel('Risk level').fill('low');
  await wi.getByLabel('Workflow version ID').fill(workflow.versionId);
  await awaitMutation(page, () => wi.getByRole('button', { name: 'Create' }).click(), /\/projects\/[^/]+\/work-items$/);

  await expect.poll(async () => { await page.reload(); return settledCount(page.locator('button', { hasText: 'Mark Ready' })); }, { timeout: 30_000 }).toBeGreaterThan(0);
  await awaitMutation(page, () => page.locator('button', { hasText: 'Mark Ready' }).first().click(), /\/work-items\/[^/]+\/mark-ready$/);
  await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('Real Claude: add a hello file')); }, { timeout: 30_000 }).toBeGreaterThan(0);
  await page.getByText('Real Claude: add a hello file').click();
  const taskId = page.url().split('/tasks/')[1];
  const detail = await (await request.get(`${base}/projects/${projectId}/work-items/${taskId}`, { headers })).json() as { familyId: string };
  await expect.poll(async () => {
    const r = await request.get(`${base}/projects/${projectId}/workspace-sets/${detail.familyId}`, { headers });
    return r.ok() ? ((await r.json()) as { state: string }).state : 'unknown';
  }, { timeout: 30_000 }).toBe('READY');
  await page.reload();
  await awaitMutation(page, () => page.getByRole('button', { name: 'Start Run' }).click(), /\/work-items\/[^/]+\/runs$/);

  // Wait for the run to end one way or the other (a completion-policy blocker, or a failed attempt).
  await expect.poll(async () => {
    await page.reload();
    return (await settledCount(page.getByText('COMPLETION_POLICY_FAILED'))) + (await settledCount(page.getByText('RUN_FAILED')));
  }, { timeout: 420_000, intervals: [5_000], message: 'the real run never ended' }).toBeGreaterThan(0);

  await page.getByRole('tab', { name: 'Graph & Timeline' }).click();
  await page.getByRole('list', { name: /nodes/i }).getByRole('button', { name: /maker/ }).first().click().catch(async () => { await page.getByText('maker', { exact: true }).first().click(); });
  const pane = page.getByTestId('node-detail');
  await expect(pane).toBeVisible();
  await expect(pane.getByTestId('node-progress')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('node-pane.png'), fullPage: true });
  await testInfo.attach('node-pane.txt', { body: await pane.innerText(), contentType: 'text/plain' });
  console.log('NODE PANE >>>\n' + await pane.innerText());

  await page.getByRole('tab', { name: 'Chat' }).click();
  await expect(page.getByText(/maker started/)).toBeVisible();
  const chat = await page.getByRole('log', { name: 'Conversation messages' }).innerText();
  await testInfo.attach('chat.txt', { body: chat, contentType: 'text/plain' });
  console.log('CHAT >>>\n' + chat);
});
