import { writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startStack } from './stack';

const HANDOFF_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '.stack-handoff.json');

/**
 * Playwright's globalSetup and globalTeardown are not guaranteed to share
 * process memory, so the real installation's baseURL/pids/repo paths are
 * handed off through a plain JSON file rather than a module-level variable
 * — the same reasoning `internal/integration/v6accept`'s own Go harness
 * never needed (a single `go test` process owns setup AND teardown via
 * `t.Cleanup`), but Playwright's two-file convention does.
 */
export default async function globalSetup(): Promise<void> {
  const stack = await startStack();
  process.env.AW_E2E_BASE_URL = stack.baseURL;
  process.env.AW_E2E_REPO_READY = stack.repoReady;
  process.env.AW_E2E_FAKE_CLAUDE_PATH = stack.fakeClaudeBinary;
  process.env.AW_E2E_GO_VERSION = stack.goVersion;
  writeFileSync(HANDOFF_PATH, JSON.stringify({
    baseURL: stack.baseURL,
    root: stack.root,
    repoReady: stack.repoReady,
    fakeClaudeBinary: stack.fakeClaudeBinary,
    servePid: stack.serve.pid,
    workerPid: stack.worker.pid,
  }, null, 2));
}
