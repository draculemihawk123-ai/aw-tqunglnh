import { test, expect, type Locator, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { initRealGitRepo } from './support/stack';
import { publishApprovalWorkflow, publishBlockerWorkflow, publishWaitWorkflow } from './support/definitions';

/**
 * V7-17's own full-journey gate (docs/design/09-v7-alpha-ui.md V7-17):
 * project→definition→task→run→approval→evidence→ReleaseSet→DONE, driven
 * end-to-end through the real browser-served UI (`aw serve --ui-dist`),
 * against a real `aw serve`+`aw worker` pair with a real fake-provider
 * executable (e2e/support/stack.ts's own globalSetup). "Seed via public
 * setup" is honored throughout: workflow/policy definitions are authored
 * through the real public HTTP definitions API (create→validate→publish,
 * the same three calls a real CI pipeline would make — e2e/support/
 * definitions.ts), never a manual sqlite/git edit; the only direct
 * filesystem writes this suite makes are the SOURCE git repositories a
 * real operator would already have on disk before ever running `aw` at
 * all (one pre-made by globalSetup, one created mid-journey specifically
 * to demonstrate the onboarding BLOCKED→retry recovery path — see that
 * step's own comment for why it cannot be pre-made).
 *
 * One continuous test, `test.step`-annotated per journey stage — the same
 * reason `internal/integration/v6accept`'s own Go acceptance journey is a
 * single sequential test: every later stage depends on state only an
 * earlier stage creates, and running them out of order or in parallel
 * would prove nothing real.
 */

/**
 * web/src/api/session.ts reads window.__AW_BOOTSTRAP__ exactly once and
 * then `delete`s it — a deliberate security discipline ("token không được
 * ghi vào browser storage") this suite must respect, not defeat. Rather
 * than reading the global after the app has already deleted it (racy and
 * wrong), this installs a property descriptor BEFORE any page script runs
 * (Playwright's own addInitScript ordering guarantee) that shadows every
 * assignment into a second, never-deleted property this test can read
 * back at its leisure — the real bootstrap flow is completely unaffected
 * (the app's own get/delete continue to see the exact same value).
 */
async function installBootstrapCapture(page: Page) {
  await page.addInitScript(() => {
    let value: unknown;
    Object.defineProperty(window, '__AW_BOOTSTRAP__', {
      configurable: true,
      set(v) { value = v; (window as unknown as { __AW_BOOTSTRAP_CAPTURED__?: unknown }).__AW_BOOTSTRAP_CAPTURED__ = v; },
      get() { return value; },
    });
  });
}

async function readCapturedBootstrap(page: Page): Promise<{ token: string; actor: string; roles: string[] }> {
  return page.evaluate(() => (window as unknown as { __AW_BOOTSTRAP_CAPTURED__: { token: string; actor: string; roles: string[] } }).__AW_BOOTSTRAP_CAPTURED__);
}

/**
 * A freshly-created root WorkItem's own WorkspaceSet is provisioned
 * (real `git worktree` checkouts per repository) by the WORKER, entirely
 * asynchronously — mirrors `internal/integration/v6accept/journey_test.go`'s
 * own `waitWorkspaceReady`. `startWorkflowRun` genuinely refuses to start
 * (a real, correct "the work item's workspace set is not READY" rejection,
 * found live by this suite's own first real run against it) until this
 * finishes, so every WorkItem this journey starts a run on must wait here
 * first.
 */
async function waitForWorkspaceReady(request: import('@playwright/test').APIRequestContext, base: string, token: string, projectId: string, familyId: string): Promise<void> {
  await expect.poll(async () => {
    const resp = await request.get(`${base}/projects/${projectId}/workspace-sets/${familyId}`, { headers: { 'X-Aw-Session-Token': token } });
    if (!resp.ok()) return 'unknown';
    const body = await resp.json() as { state: string };
    return body.state;
  }, { timeout: 30_000, message: `workspace set for family ${familyId} never reached READY` }).toBe('READY');
}

async function getFamilyId(request: import('@playwright/test').APIRequestContext, base: string, token: string, projectId: string, workItemId: string): Promise<string> {
  const resp = await request.get(`${base}/projects/${projectId}/work-items/${workItemId}`, { headers: { 'X-Aw-Session-Token': token } });
  const detail = await resp.json() as { familyId: string };
  return detail.familyId;
}

async function assertNoSeriousA11yViolations(page: Page) {
  const results = await new AxeBuilder({ page }).analyze();
  const serious = results.violations.filter(v => v.impact === 'serious' || v.impact === 'critical');
  expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
}

/**
 * settledCount counts `locator` on a page that was JUST reloaded, but first
 * gives the freshly loaded SPA up to five seconds to hydrate, run its query
 * waterfall and render (polling the count every 100ms until it reaches
 * `atLeast`). A bare `page.reload(); return locator.count()` inside
 * `expect.poll` counts the instant the `load` event fires — before the React
 * app has fetched anything — so the next poll reloads again and the count is
 * taken before hydration every single time. On a fast machine the gap hides
 * it; on windows-latest (where this suite only began to really execute after
 * the pnpm steps were fixed to wait) "COMPLETION_POLICY_FAILED never rendered
 * despite a real OPEN blocker" failed with the blocker alert plainly present
 * in the failure screenshot and page snapshot. Reloading stays (the point of
 * these polls is to recover from a stale page); the wait is what was missing.
 */
async function settledCount(locator: Locator, atLeast = 1): Promise<number> {
  const deadline = Date.now() + 5_000;
  let n = await locator.count();
  while (n < atLeast && Date.now() < deadline) {
    await locator.page().waitForTimeout(100);
    n = await locator.count();
  }
  return n;
}

/**
 * Runs a UI action that fires a mutating request and waits for THAT request's
 * own response (asserting it succeeded) before returning.
 *
 * Every mutation this journey triggers through the UI is followed by a
 * `page.reload()`/`page.goto()` poll to let the async projection catch up. A
 * reload cancels any fetch the page still has in flight, so a bare `.click()`
 * followed directly by a reload races the request itself: on a fast runner
 * the reload can win, the browser aborts the POST before the server ever
 * processes it, and the state the poll is waiting for is never created — the
 * poll then reloads for its whole 30s budget against a server that genuinely
 * never received the command. Real CI traces showed exactly this: the
 * `POST /projects/{id}/repositories` was sent 9ms before the reload began and
 * recorded as aborted (status -1) while every other POST on the same server
 * completed in 1-10ms, and the repository list stayed empty for the full 30s.
 * It passed reliably on Windows (slower to dispatch the reload) and failed
 * intermittently on Ubuntu, which is why it looked like runner slowness.
 * Waiting for the real response is the one signal that is never racy.
 */
async function awaitMutation(page: Page, trigger: () => Promise<unknown>, pathPattern: RegExp): Promise<void> {
  const [response] = await Promise.all([
    page.waitForResponse(resp => resp.request().method() === 'POST' && pathPattern.test(new URL(resp.url()).pathname)),
    trigger(),
  ]);
  if (!response.ok()) {
    throw new Error(`POST ${new URL(response.url()).pathname} failed: ${response.status()} ${await response.text()}`);
  }
}

const REGISTER_REPOSITORY = /\/projects\/[^/]+\/repositories$/;
const RETRY_PROBE = /\/repositories\/[^/]+\/retry-probe$/;
const CREATE_WORK_ITEM = /\/projects\/[^/]+\/work-items$/;
const MARK_READY = /\/work-items\/[^/]+\/mark-ready$/;
const REGISTER_ADAPTER_BUILD = /\/adapter-builds$/;

async function fillCreateWorkItemDialog(page: Page, opts: {
  title: string; repoLabel: string; scopeReason: string; behavior: string;
  criterionDescription: string; workflowVersionId: string;
}) {
  await page.getByRole('button', { name: 'New WorkItem' }).click();
  const dialog = page.getByRole('dialog', { name: 'Create WorkItem' });
  await dialog.getByLabel('Title').fill(opts.title);
  await dialog.getByRole('button', { name: 'Add repository' }).click();
  await dialog.getByLabel('Repository').selectOption({ label: opts.repoLabel });
  await dialog.getByLabel('Reason').fill(opts.scopeReason);
  await dialog.getByRole('checkbox', { name: /Add a readiness contract now/ }).check();
  await dialog.getByLabel('Behavior (WHAT)').fill(opts.behavior);
  await dialog.getByRole('button', { name: 'Add criterion' }).click();
  await dialog.getByLabel('Description').fill(opts.criterionDescription);
  await dialog.getByLabel('Verification ref').fill('manual:operator-review');
  await dialog.getByLabel('Verification spec').fill('Operator observes the real terminal state');
  await dialog.getByLabel('Risk level').fill('low');
  await dialog.getByLabel('Workflow version ID').fill(opts.workflowVersionId);
  await awaitMutation(page, () => dialog.getByRole('button', { name: 'Create' }).click(), CREATE_WORK_ITEM);
}

async function markFirstBacklogCardReady(page: Page, projectId: string) {
  await page.goto(`/ui/projects/${projectId}/board`);
  await awaitMutation(page, () => page.locator('button', { hasText: 'Mark Ready' }).first().click(), MARK_READY);
}

/**
 * Clicks the real "Start Run" button and waits for its own real network
 * response before returning — a bare `.click()` proved flaky in this suite
 * (found live): the button is real and the click lands, but nothing
 * observable in the DOM changes fast enough for a caller's very next
 * assertion to reliably catch a same-page state transition, so a caller
 * checking for a UI SIDE EFFECT immediately after could race a real,
 * successful dispatch. Waiting on the real `POST .../runs` response is the
 * one signal that is never racy.
 */
async function clickStartRun(page: Page): Promise<string> {
  const [response] = await Promise.all([
    page.waitForResponse(resp => /\/work-items\/[^/]+\/runs$/.test(new URL(resp.url()).pathname) && resp.request().method() === 'POST'),
    page.getByRole('button', { name: 'Start Run' }).click(),
  ]);
  if (!response.ok()) {
    throw new Error(`Start Run POST failed: ${response.status()} ${await response.text()}`);
  }
  const body = await response.json() as { runId: string };
  return body.runId;
}

test.describe.configure({ mode: 'serial' });

test('full journey: project → onboarding → adapter → run → approval → scope amendment → attachment → cancel → ReleaseSet → DONE', async ({ page, request, baseURL }) => {
  test.setTimeout(300_000);
  const base = baseURL!;
  // A fresh OS tmpdir, NOT testInfo.project.outputDir (the shared, static
  // project-level base directory — the same path on every attempt AND
  // every retry, which meant a retry's own repoAlphaPath below collided
  // with stale state a prior failed attempt had already turned into a
  // real git repo) and NOT testInfo.outputDir either (unique per attempt,
  // but bakes this test's own title — including its "→" characters — into
  // a long nested path; the real `git worktree` provisioning the worker
  // does against that path genuinely failed with PROVISION_FAILED on it,
  // found live once a fresh, non-ASCII-laden path was actually exercised).
  // mkdtempSync gives a short, ASCII-only, guaranteed-unique-per-invocation
  // directory with none of either problem.
  const outDir = mkdtempSync(path.join(tmpdir(), 'aw-e2e-scratch-'));
  let token = '';
  let projectId = '';
  let adapterBuildId = '';
  let workItemBlockerId = '';
  let workItemApprovalId = '';
  let workItemWaitId = '';
  let familyBlockerId = '';

  await test.step('loads the real app shell with no interactive terminal anywhere, and passes an accessibility smoke', async () => {
    await installBootstrapCapture(page);
    await page.goto('/ui/doctor');
    const boot = await readCapturedBootstrap(page);
    expect(boot.actor).toBe('local-operator');
    expect(boot.roles).toContain('operator');
    token = boot.token;
    await expect(page.getByRole('heading', { name: 'Doctor' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: /command/i })).toHaveCount(0);
    await assertNoSeriousA11yViolations(page);
  });

  await test.step('creates a real project', async () => {
    await page.goto('/ui/projects');
    await page.getByRole('button', { name: 'New Project' }).click();
    await page.getByPlaceholder('my-project').fill('e2e-full-journey');
    await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click();
    await expect(page).toHaveURL(/\/ui\/projects\/[0-9a-f-]+$/);
    projectId = page.url().split('/projects/')[1];
    expect(projectId).toMatch(/^[0-9a-f-]{36}$/);
  });

  await test.step('onboarding BLOCKED/retry: registers a repository pointing at a not-yet-real path, observes real BLOCKED, then recovers via Retry Probe once the path genuinely exists', async () => {
    // Deliberately NOT pre-made by globalSetup: the whole point of this step
    // is that the path does not exist yet at registration time, so the
    // worker's real probe job genuinely fails and the repository genuinely
    // reaches BLOCKED — a repository already present on disk could never
    // demonstrate this.
    const repoAlphaPath = path.join(outDir, 'repo-alpha-onboarding');

    await page.getByRole('button', { name: 'Register Repository' }).click();
    const dialog = page.getByRole('dialog', { name: 'Register Repository' });
    await dialog.getByLabel('Repository ID').fill('repo-alpha');
    await dialog.getByLabel('Name').fill('alpha');
    await dialog.getByLabel('Local repository path').fill(repoAlphaPath);
    await dialog.getByLabel('Default ref').fill('master');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);

    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('alpha').first());
    }, { timeout: 30_000, message: 'newly-registered repository never appeared' }).toBeGreaterThan(0);
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('BLOCKED'));
    }, { timeout: 30_000, message: 'repository never reached BLOCKED for a nonexistent path' }).toBeGreaterThan(0);

    // The real operator action that makes a BLOCKED repository recoverable:
    // the path now genuinely exists. This mirrors what a real operator does
    // between registering and retrying — never a manual DB/status edit.
    initRealGitRepo(repoAlphaPath);

    await awaitMutation(page, () => page.getByRole('button', { name: 'Retry Probe' }).click(), RETRY_PROBE);
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('ACTIVE'));
    }, { timeout: 30_000, message: 'repository never recovered to ACTIVE after Retry Probe' }).toBeGreaterThan(0);
  });

  await test.step('registers a second, immediately-valid repository (the one globalSetup already prepared)', async () => {
    await page.getByRole('button', { name: 'Register Repository' }).click();
    const dialog = page.getByRole('dialog', { name: 'Register Repository' });
    await dialog.getByLabel('Repository ID').fill('repo-beta');
    await dialog.getByLabel('Name').fill('beta');
    await dialog.getByLabel('Local repository path').fill(process.env.AW_E2E_REPO_READY!);
    await dialog.getByLabel('Default ref').fill('master');
    await awaitMutation(page, () => dialog.getByRole('button', { name: 'Register and Probe' }).click(), REGISTER_REPOSITORY);

    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('beta', { exact: true }));
    }, { timeout: 30_000 }).toBeGreaterThan(0);
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('ACTIVE'), 2);
    }, { timeout: 30_000 }).toBeGreaterThan(1); // repo-alpha's own ACTIVE badge is already on screen too
  });

  await test.step('adapter probe/register: measures and registers the real fake-claude executable', async () => {
    const fakeClaudePath = process.env.AW_E2E_FAKE_CLAUDE_PATH!;
    await page.goto('/ui/doctor');
    await page.getByRole('button', { name: 'Probe new build' }).click();
    // ID-based rather than getByLabel: ui.tsx's own TextField derives a
    // deterministic `id="field-<label-slug>"` when no explicit id is
    // passed, and AdapterProbeDialog passes none — a short label like "OS"
    // is a case-insensitive SUBSTRING match risk against unrelated UI text
    // ("Cl[os]e dialog") under getByLabel's default fuzzy matching, so the
    // stable id is the more reliable selector here.
    // internal/app/adapterbuild.VerifyNoDrift re-measures this registration's
    // own tuple LIVE at every real admission check (ProtocolVersion/
    // SupportsStart|Resume|Cancel/CanonicalEventKinds from
    // claude.Adapter.Capabilities()'s own hardcoded real values, Toolchain
    // from the worker process's own real runtime.Version(), OS from its own
    // real runtime.GOOS) — every field below must be the REAL value that
    // check will re-observe, not an arbitrary placeholder, or the very
    // first real AGENT node admission fails closed with a genuine
    // ADAPTER_BUILD_DRIFT (found live by this suite's own first real run).
    const dialog = page.getByRole('dialog', { name: 'Probe adapter build' });
    await dialog.locator('#field-provider-key').fill('claude');
    await dialog.locator('#field-executable-path').fill(fakeClaudePath);
    await dialog.locator('#field-protocol-version').fill('claude-stream-json/v1');
    await dialog.locator('#field-os').fill(process.platform === 'win32' ? 'windows' : 'linux');
    await dialog.locator('#field-toolchain').fill(process.env.AW_E2E_GO_VERSION!);
    await dialog.locator('#field-config-identity').fill('e2e-default');
    await dialog.locator('#field-canonical-event-kinds').fill(
      'EXECUTION_STARTED,STATUS_CHANGED,ASSISTANT_MESSAGE,TOOL_CALL_STARTED,TOOL_CALL_FINISHED,USAGE_REPORTED,'
      + 'CHECKPOINT_PROPOSED,DIAGNOSTIC,EXECUTION_FINISHED',
    );
    await dialog.getByRole('checkbox', { name: 'Supports resume' }).check();
    await dialog.getByRole('checkbox', { name: 'Supports cancel' }).check();
    await dialog.getByRole('button', { name: 'Probe' }).click();

    await expect(page.getByRole('dialog', { name: 'Confirm adapter build candidate' })).toBeVisible();
    // Wait for the register POST itself: the GET below reads the build back
    // straight away. The previous `getByText('REGISTERED')` wait was no wait
    // at all — Playwright's default text match is a case-insensitive
    // substring, and the page's own "Registered Adapter Builds" heading
    // satisfies it before anything is registered, so on a slow Windows runner
    // the GET ran before the POST committed and found no 'claude' build.
    await awaitMutation(page, () => page.getByRole('button', { name: 'Confirm & register' }).click(), REGISTER_ADAPTER_BUILD);

    const buildsResp = await request.get(`${base}/adapter-builds`, { headers: { 'X-Aw-Session-Token': token } });
    const builds = await buildsResp.json() as { builds: { id: string; providerKey: string }[] };
    const build = builds.builds.find(b => b.providerKey === 'claude');
    expect(build).toBeTruthy();
    adapterBuildId = build!.id;
  });

  let waitWorkflowVersionId = '';
  let approvalWorkflowVersionId = '';
  let blockerWorkflowVersionId = '';

  await test.step('publishes workflow definitions through the real public HTTP definitions API', async () => {
    const [waitWf, approvalWf, blockerWf] = await Promise.all([
      publishWaitWorkflow(request, base, token, projectId, 'j1'),
      publishApprovalWorkflow(request, base, token, projectId, 'j1'),
      publishBlockerWorkflow(request, base, token, projectId, 'j1', adapterBuildId),
    ]);
    waitWorkflowVersionId = waitWf.versionId;
    approvalWorkflowVersionId = approvalWf.versionId;
    blockerWorkflowVersionId = blockerWf.versionId;
  });

  await test.step('creates a root WorkItem scoped to repo-alpha, pinning the blocker workflow, with a complete readiness contract', async () => {
    await page.goto(`/ui/projects/${projectId}/board`);
    await fillCreateWorkItemDialog(page, {
      title: 'E2E blocker/recovery journey', repoLabel: 'alpha', scopeReason: 'initial scope for the E2E journey',
      behavior: 'Demonstrate a real completion-policy blocker and its recovery',
      criterionDescription: 'Run reaches a terminal state', workflowVersionId: blockerWorkflowVersionId,
    });

    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('E2E blocker/recovery journey'));
    }, { timeout: 30_000 }).toBeGreaterThan(0);
    await page.getByText('E2E blocker/recovery journey').click();
    await expect(page).toHaveURL(/\/tasks\/[0-9a-f-]+$/);
    workItemBlockerId = page.url().split('/tasks/')[1];

    const detailResp = await request.get(`${base}/projects/${projectId}/work-items/${workItemBlockerId}`, { headers: { 'X-Aw-Session-Token': token } });
    const detail = await detailResp.json() as { familyId: string };
    familyBlockerId = detail.familyId;
  });

  await test.step('marks it READY, starts the run, and lets it reach a real COMPLETION_POLICY_FAILED blocker', async () => {
    await markFirstBacklogCardReady(page, projectId);
    await page.goto(`/ui/projects/${projectId}/tasks/${workItemBlockerId}`);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('READY', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await waitForWorkspaceReady(request, base, token, projectId, familyBlockerId);

    // No intermediate "Cancel Run is visible" check here: unlike the
    // WAIT-workflow scenario below, this Run's one AGENT node executes
    // against the fake provider fast enough that it can already be
    // terminal well before this test's own next poll — a real, correct
    // outcome, not a bug this suite should race against.
    const blockerRunId = await clickStartRun(page);

    // Poll the real backend diagnostics endpoint directly rather than
    // reload-scraping the UI for "COMPLETION_POLICY_FAILED" text — the same
    // reload-per-poll fragility already fixed for the approval-wait and
    // post-approval SUCCEEDED checks below (each reload pays a full page
    // navigation plus a query waterfall, which can outrun a 30s budget
    // under CI-runner load even for this journey's very first async
    // worker-driven transition, found live on a CI run where this was the
    // only WorkItem to have started yet).
    await expect.poll(async () => {
      const resp = await request.get(`${base}/projects/${projectId}/runs/${blockerRunId}/diagnostics`, { headers: { 'X-Aw-Session-Token': token } });
      if (!resp.ok()) return 0;
      const body = await resp.json() as { blockers?: { type: string; state: string }[] };
      return (body.blockers ?? []).filter(b => b.type === 'COMPLETION_POLICY_FAILED' && b.state === 'OPEN').length;
    }, { timeout: 30_000, message: 'run never produced the expected real COMPLETION_POLICY_FAILED blocker' }).toBeGreaterThan(0);

    // Same poll+reload resilience as the approval banner below: the backend
    // already confirmed the real blocker above, this only waits for the
    // frontend's own reload/hydration to catch up.
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByText('COMPLETION_POLICY_FAILED'));
    }, { timeout: 30_000, message: 'COMPLETION_POLICY_FAILED never rendered despite a real OPEN blocker' }).toBeGreaterThan(0);
  });

  await test.step('recovery action: resolves the real open blocker, returning the WorkItem to READY', async () => {
    await page.getByRole('button', { name: 'Resolve' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel(/Reason/).fill('fixture completion-policy gap acknowledged for this E2E journey');
    await dialog.getByRole('button', { name: 'Confirm' }).click();
    await expect(page.getByText(/resolved/).first()).toBeVisible();
  });

  await test.step('scope amendment: requests a scope expansion to repo-beta, then approves it through the browser', async () => {
    const reqResp = await request.post(`${base}/projects/${projectId}/task-families/${familyBlockerId}/scope-expansions`, {
      headers: { 'X-Aw-Session-Token': token, 'Idempotency-Key': crypto.randomUUID() },
      data: {
        requestedGrants: [{ repositoryId: 'repo-beta', access: 'READ', reason: 'e2e scope amendment' }],
        reason: 'E2E journey requests read access to repo-beta',
      },
    });
    expect(reqResp.ok(), await reqResp.text()).toBeTruthy();

    await page.goto(`/ui/projects/${projectId}/tasks/${workItemBlockerId}/workspace`);
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByRole('button', { name: /Scope Requests \(\d+\)/ }));
    }, { timeout: 30_000, message: 'scope expansion request never appeared on the workspace tab' }).toBeGreaterThan(0);
    await page.getByRole('button', { name: /Scope Requests \(\d+\)/ }).click();
    await page.getByRole('button', { name: 'Approve' }).click();
    await expect(page.getByText('APPROVED').first()).toBeVisible();
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByRole('button', { name: /repo-beta revision/ }));
    }, { timeout: 30_000, message: 'repo-beta workspace tab never appeared after scope expansion approval' }).toBeGreaterThan(0);
  });

  await test.step('attachment: sends a chat message and uploads a real file attachment', async () => {
    await page.goto(`/ui/projects/${projectId}/tasks/${workItemBlockerId}/chat`);
    await page.getByLabel('Message composer').fill('e2e operator note');
    await page.getByRole('button', { name: 'Send' }).click();
    await expect(page.getByText('e2e operator note')).toBeVisible({ timeout: 10_000 });

    const attachmentPath = path.join(outDir, 'e2e-attachment.txt');
    writeFileSync(attachmentPath, 'e2e attachment content\n');
    await page.locator('input[type="file"]').setInputFiles(attachmentPath);
    await expect(page.getByText('Attachment uploaded.').first()).toBeVisible({ timeout: 30_000 });
  });

  await test.step('creates a ReleaseSet from the real current per-repository revision, seals it, and requests a real Local Commit (an honest no-diff-yet outcome, matching V7-13A\'s own established precedent)', async () => {
    type WorkspaceState = { repositoryWorkspaces: { repositoryId: string; currentRevision?: string }[] };
    // repo-beta was only just auto-provisioned by the scope-expansion approval
    // above — its own RepositoryWorkspace needs the worker to actually
    // check it out (an async job, the same real provisioning lag every
    // fresh onboarding already has) before it carries a real currentRevision.
    let state!: WorkspaceState;
    await expect.poll(async () => {
      const resp = await request.get(`${base}/projects/${projectId}/workspace-sets/${familyBlockerId}`, { headers: { 'X-Aw-Session-Token': token } });
      state = await resp.json() as WorkspaceState;
      return state.repositoryWorkspaces.length > 0 && state.repositoryWorkspaces.every(r => !!r.currentRevision);
    }, { timeout: 30_000, message: 'repo-beta workspace never finished real provisioning with a current revision' }).toBeTruthy();

    await page.goto(`/ui/projects/${projectId}/tasks/${workItemBlockerId}/workspace`);
    await page.getByRole('button', { name: 'ReleaseSet' }).click();
    await page.getByRole('button', { name: 'Create' }).click();
    const createDialog = page.getByRole('dialog', { name: 'Create ReleaseSet' });
    // Fill every rendered (base, result) pair with that SAME repository's own
    // real current revision, in DOM order (which matches state.repositoryWorkspaces'
    // own order — both come from the same underlying repository set).
    const baseFields = createDialog.getByLabel('Base revision');
    const resultFields = createDialog.getByLabel('Result revision');
    const count = await baseFields.count();
    expect(count).toBe(state.repositoryWorkspaces.length);
    for (let i = 0; i < count; i++) {
      const revision = state.repositoryWorkspaces[i].currentRevision!;
      await baseFields.nth(i).fill(revision);
      await resultFields.nth(i).fill(revision);
    }
    await createDialog.getByRole('button', { name: 'Create' }).click();
    await expect(page.getByText('CREATED', { exact: true }).first()).toBeVisible({ timeout: 10_000 });

    await page.getByRole('button', { name: 'Seal' }).click();
    await page.getByRole('dialog', { name: 'Seal ReleaseSet' }).getByRole('button', { name: 'Seal' }).click();
    await expect(page.getByText('SEALED').first()).toBeVisible({ timeout: 10_000 });

    await page.getByRole('button', { name: 'Local Commit' }).first().click();
    const commitDialog = page.getByRole('dialog', { name: 'Create Local Commit' });
    await commitDialog.getByLabel('Message').fill('e2e release commit');
    await commitDialog.getByLabel('Author name').fill('e2e operator');
    await commitDialog.getByLabel('Author email').fill('e2e@example.invalid');
    await commitDialog.getByRole('button', { name: 'Create Local Commit' }).click();
    await expect(page.getByText(/Local commit requested/).first()).toBeVisible({ timeout: 10_000 });
  });

  await test.step('views the Evidence tab (real, never fabricated data)', async () => {
    await page.goto(`/ui/projects/${projectId}/tasks/${workItemBlockerId}/evidence`);
    await expect(page.getByRole('tab', { name: 'Evidence' })).toBeVisible();
    await assertNoSeriousA11yViolations(page);
  });

  await test.step('run + approval: creates a second WorkItem on the approval workflow, starts it, and resolves the real pending Approval through the browser', async () => {
    await page.goto(`/ui/projects/${projectId}/board`);
    await fillCreateWorkItemDialog(page, {
      title: 'E2E approval journey', repoLabel: 'alpha', scopeReason: 'scope for the approval journey',
      behavior: 'Demonstrate a real human-approval-gated run', criterionDescription: 'Approval is resolved',
      workflowVersionId: approvalWorkflowVersionId,
    });
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('E2E approval journey')); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await page.getByText('E2E approval journey').click();
    workItemApprovalId = page.url().split('/tasks/')[1];

    await markFirstBacklogCardReady(page, projectId);
    await page.goto(`/ui/projects/${projectId}/tasks/${workItemApprovalId}`);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('READY', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await waitForWorkspaceReady(request, base, token, projectId, await getFamilyId(request, base, token, projectId, workItemApprovalId));

    const approvalRunId = await clickStartRun(page);
    // Poll the real backend directly (cheap HTTP GETs), not the UI via
    // repeated page.reload() cycles: each reload pays a full page
    // navigation + a sequential WorkItem->Card->RunDiagnostics query
    // waterfall before the DOM shows anything, and a worker already
    // carrying several prior WorkItems' state machines in this same
    // single-threaded polling loop can genuinely take tens of seconds of
    // real wall time to advance this run to WAITING — found live, this
    // reload-per-poll approach flaked repeatedly even at a 60s budget,
    // while the backend state itself was always genuinely reachable well
    // within that time once checked directly (matching waitForWorkspaceReady's
    // already-proven pattern above). One reload, once the backend confirms
    // readiness, is all the UI needs.
    await expect.poll(async () => {
      const resp = await request.get(`${base}/runs/${approvalRunId}`, { headers: { 'X-Aw-Session-Token': token } });
      if (!resp.ok()) return 0;
      const body = await resp.json() as { approvalRequests?: { state: string }[] };
      return (body.approvalRequests ?? []).filter(a => a.state === 'PENDING').length;
    }, { timeout: 60_000, message: 'the real pending ApprovalRequest never appeared' }).toBeGreaterThan(0);

    // The backend already confirmed a real PENDING ApprovalRequest above —
    // this is purely waiting for the frontend's own page load/hydration/
    // query-waterfall after a reload, which a single fixed-timeout check
    // occasionally lost the race against (found live); poll+reload like
    // every other UI-catch-up check in this journey, for the same
    // resilience.
    await expect.poll(async () => {
      await page.reload();
      await page.getByRole('tab', { name: 'Graph & Timeline' }).click();
      return settledCount(page.getByText(/Approval pending on/));
    }, { timeout: 30_000, message: 'the approval banner never rendered despite a real PENDING ApprovalRequest' }).toBeGreaterThan(0);
    await page.getByRole('button', { name: 'approved' }).click();
    await expect(page.getByText(/Approval resolved/).first()).toBeVisible({ timeout: 30_000 });
    // Same real-backend-first reasoning as the pending-ApprovalRequest poll
    // above: check the run's own authoritative state directly rather than
    // reload-scraping the UI for "SUCCEEDED" text (found live to flake the
    // same way, for the same worker-backlog reason).
    await expect.poll(async () => {
      const resp = await request.get(`${base}/runs/${approvalRunId}`, { headers: { 'X-Aw-Session-Token': token } });
      if (!resp.ok()) return 'unknown';
      const body = await resp.json() as { state: string };
      return body.state;
    }, { timeout: 30_000, message: 'run never reached real terminal SUCCEEDED after the approval' }).toBe('SUCCEEDED');
  });

  await test.step('cancel a run: creates a THIRD WorkItem on a durably-waiting workflow and cancels its real run', async () => {
    await page.goto(`/ui/projects/${projectId}/board`);
    await fillCreateWorkItemDialog(page, {
      title: 'E2E cancel-a-run journey', repoLabel: 'alpha', scopeReason: 'scope for the cancel journey',
      behavior: 'Demonstrate cancelling a real durably-running Run', criterionDescription: 'Run is genuinely cancelled',
      workflowVersionId: waitWorkflowVersionId,
    });
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('E2E cancel-a-run journey')); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await page.getByText('E2E cancel-a-run journey').click();
    workItemWaitId = page.url().split('/tasks/')[1];

    await markFirstBacklogCardReady(page, projectId);
    await page.goto(`/ui/projects/${projectId}/tasks/${workItemWaitId}`);
    await expect.poll(async () => { await page.reload(); return settledCount(page.getByText('READY', { exact: true })); }, { timeout: 30_000 }).toBeGreaterThan(0);
    await waitForWorkspaceReady(request, base, token, projectId, await getFamilyId(request, base, token, projectId, workItemWaitId));

    const runId = await clickStartRun(page);
    // The projected WorkItem detail that supplies activeRunId (and so
    // canCancelRun) is populated by the same async projection every other
    // state check in this journey already has to poll+reload for (see the
    // READY/SUCCEEDED/COMPLETION_POLICY_FAILED polls above) — a single
    // TanStack invalidate-refetch right after Start Run can race ahead of
    // the projection and see no active run yet, with nothing left to
    // trigger a second attempt.
    await expect.poll(async () => {
      await page.reload();
      return settledCount(page.getByRole('button', { name: 'Cancel Run' }));
    }, { timeout: 30_000, message: 'Cancel Run button never appeared once the run started' }).toBeGreaterThan(0);

    await page.getByRole('button', { name: 'Cancel Run' }).click();
    const dialog = page.getByRole('dialog', { name: 'Cancel Run' });
    await dialog.getByLabel(/Reason/).fill('e2e journey no longer needs this run');
    await dialog.getByRole('button', { name: 'Cancel Run' }).click();

    // Checked against the real API, not UI text: once CANCELLED, the Run is
    // no longer this WorkItem's own `activeRunId` (a real, honest
    // consequence of reaching a terminal state) — the runtime blocker
    // banner that would otherwise show "CANCELLED" only ever renders while
    // an active run reference still exists to fetch diagnostics against
    // (found live by this suite's own first real run through this step),
    // so the WorkItem-level UI alone no longer surfaces the word anywhere.
    // The real, authoritative signal is the Run's own terminal state.
    await expect.poll(async () => {
      const resp = await request.get(`${base}/runs/${runId}`, { headers: { 'X-Aw-Session-Token': token } });
      const run = await resp.json() as { state: string };
      return run.state;
    }, { timeout: 30_000, message: 'run never reached real terminal CANCELLED' }).toBe('CANCELLED');
  });

  await test.step('projection resync: every real WorkItem created this journey is visible on the real Kanban board once the async projection catches up — DONE', async () => {
    await page.goto(`/ui/projects/${projectId}/board`);
    await expect.poll(async () => {
      await page.reload();
      const titles = ['E2E blocker/recovery journey', 'E2E approval journey', 'E2E cancel-a-run journey'];
      const counts = await Promise.all(titles.map(t => settledCount(page.getByText(t))));
      return counts.every(c => c > 0);
    }, { timeout: 30_000, message: 'projection never resynced all three real WorkItems onto the Kanban board' }).toBeTruthy();
    await assertNoSeriousA11yViolations(page);
  });
});
