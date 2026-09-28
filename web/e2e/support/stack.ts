/**
 * V7-17C's own real installation harness: builds the real `aw`/`fake-claude`
 * Go binaries, lays out a clean installation directory (fresh sqlite db,
 * artifact root, workspace root, two real local git repositories to
 * onboard), and starts real `aw serve --ui-dist <web/dist>` + `aw worker`
 * child processes — the exact same "public setup" (build the real product,
 * run the real product, exercise it through its own real public surface)
 * `internal/integration/v6accept`'s own `stack_test.go` already establishes
 * for the Go-side acceptance journey, reused here for the browser-driven
 * one. Nothing here touches sqlite or git "by hand": `aw serve`/`aw worker`
 * own the database and workspace lifecycle exactly as they would for a real
 * operator; this harness's only manual git use is creating the SOURCE
 * repositories a real operator would already have on disk before ever
 * running `aw` at all.
 */
import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// web/ has "type": "module" — no __dirname/require here, only import.meta.url.
const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = path.resolve(HERE, '../../..');
const WEB_ROOT = path.resolve(HERE, '../..');
const EXE = process.platform === 'win32' ? '.exe' : '';

/** Shared with playwright.config.ts's own `use.baseURL` — see startStack's own port comment for why this is fixed rather than OS-assigned. */
export const BASE_URL = 'http://127.0.0.1:18765';

export interface Stack {
  root: string;
  binDir: string;
  awBinary: string;
  fakeClaudeBinary: string;
  goVersion: string;
  dbPath: string;
  artifactRoot: string;
  workspaceRoot: string;
  /** A real, already-initialized git repository, ready to register immediately (no BLOCKED detour). The onboarding BLOCKED/retry journey step creates its OWN, separately-timed repository instead — see full-journey.spec.ts. */
  repoReady: string;
  port: number;
  baseURL: string;
  serve: ChildProcess;
  worker: ChildProcess;
}

function run(command: string, args: string[], cwd: string): void {
  const result = spawnSync(command, args, { cwd, stdio: 'pipe', encoding: 'utf-8' });
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(' ')} (cwd=${cwd}) failed:\n${result.stdout}\n${result.stderr}`);
  }
}

/** initRealGitRepo creates a real, minimal, one-commit git repository at dir — exported so full-journey.spec.ts's own onboarding-BLOCKED-then-fixed step can create its later-arriving repository with the identical real recipe. */
export function initRealGitRepo(dir: string): void {
  mkdirSync(dir, { recursive: true });
  run('git', ['init', '-q'], dir);
  run('git', ['config', 'user.email', 'e2e@example.invalid'], dir);
  run('git', ['config', 'user.name', 'e2e'], dir);
  writeFileSync(path.join(dir, 'README.md'), 'e2e fixture repository\n');
  run('git', ['add', '.'], dir);
  run('git', ['commit', '-q', '-m', 'init'], dir);
}

function buildBinaries(binDir: string): { awBinary: string; fakeClaudeBinary: string; goVersion: string } {
  mkdirSync(binDir, { recursive: true });
  const awBinary = path.join(binDir, `aw${EXE}`);
  const fakeClaudeBinary = path.join(binDir, `fake-claude${EXE}`);
  run('go', ['build', '-o', awBinary, './cmd/aw'], REPO_ROOT);
  run('go', ['build', '-o', fakeClaudeBinary, './cmd/fake-claude'], REPO_ROOT);
  // internal/app/adapterbuild.VerifyNoDrift re-measures Toolchain as the
  // real, live `runtime.Version()` of the process running the worker — `go
  // env GOVERSION` reports the exact same string ("go1.27.0", no "go
  // version"/platform suffix) for the SDK that just built these binaries,
  // so an adapter build registered with this value never spuriously drifts
  // the moment a real AGENT node's admission re-check runs.
  const goVersion = spawnSync('go', ['env', 'GOVERSION'], { cwd: REPO_ROOT, encoding: 'utf-8' }).stdout.trim();
  return { awBinary, fakeClaudeBinary, goVersion };
}

async function waitForHTTP(url: string, withinMs: number): Promise<void> {
  const deadline = Date.now() + withinMs;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok || res.status === 404) return;
    } catch (err) {
      lastError = err;
    }
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  throw new Error(`${url} never became reachable within ${withinMs}ms: ${String(lastError)}`);
}

/**
 * startStack builds everything from scratch and starts a real `aw serve` +
 * `aw worker` pair. `web/dist` must already exist (the CI job's own `pnpm
 * build` step, or a local `pnpm build` before running the suite) — this
 * harness never builds the frontend itself, matching the same "build once,
 * reuse" split the Go CI jobs already use for their own binaries.
 */
export async function startStack(): Promise<Stack> {
  const uiDist = path.join(WEB_ROOT, 'dist');
  if (!existsSync(path.join(uiDist, 'index.html'))) {
    throw new Error(`${uiDist} has no index.html — run "pnpm build" in web/ before the E2E suite`);
  }

  const root = mkdtempSync(path.join(tmpdir(), 'aw-e2e-'));
  const binDir = path.join(root, 'bin');
  const dbPath = path.join(root, 'aw.db');
  const artifactRoot = path.join(root, 'artifacts');
  const workspaceRoot = path.join(root, 'workspaces');
  const repoReady = path.join(root, 'src-repos', 'repo-ready');
  mkdirSync(artifactRoot, { recursive: true });
  mkdirSync(workspaceRoot, { recursive: true });
  initRealGitRepo(repoReady);

  const { awBinary, fakeClaudeBinary, goVersion } = buildBinaries(binDir);

  // A fixed port rather than `--port 0`: playwright.config.ts's own
  // `use.baseURL` is read BEFORE globalSetup ever runs, so it cannot learn
  // an OS-assigned ephemeral port after the fact. Every CI runner (and a
  // local dev machine) gets its own fresh, isolated `aw serve` process on
  // this one port, so a fixed value is safe.
  const baseURL = BASE_URL;
  const port = Number(new URL(baseURL).port);

  const serve = spawn(awBinary, [
    'serve', '--db', dbPath, '--artifact-root', artifactRoot, '--workspace-root', workspaceRoot,
    '--host', '127.0.0.1', '--port', String(port), '--claude-executable', fakeClaudeBinary, '--ui-dist', uiDist,
  ], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'], detached: process.platform !== 'win32' });
  serve.unref();
  let serveOutput = '';
  serve.stdout?.on('data', chunk => { serveOutput += String(chunk); });
  serve.stderr?.on('data', chunk => { serveOutput += String(chunk); });
  serve.on('exit', code => {
    if (code !== null && code !== 0) console.error(`aw serve exited with code ${code}:\n${serveOutput}`);
  });

  await waitForHTTP(`${baseURL}/`, 30_000);

  const worker = spawn(awBinary, [
    'worker', '--db', dbPath, '--artifact-root', artifactRoot, '--workspace-root', workspaceRoot,
    '--claude-executable', fakeClaudeBinary, '--poll-interval', '150ms', '--projection-interval', '150ms',
    '--completion-interval', '200ms', '--env-allowlist', 'AGENTKIT_HELPER_MODE,AGENTKIT_HELPER_OUTCOME',
  ], {
    cwd: root, stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, AGENTKIT_HELPER_MODE: 'outcome-success', AGENTKIT_HELPER_OUTCOME: 'done' },
    detached: process.platform !== 'win32',
  });
  worker.unref();
  let workerOutput = '';
  worker.stdout?.on('data', chunk => { workerOutput += String(chunk); });
  worker.stderr?.on('data', chunk => { workerOutput += String(chunk); });
  worker.on('exit', code => {
    if (code !== null && code !== 0) console.error(`aw worker exited with code ${code}:\n${workerOutput}`);
  });

  // aw worker announces `{"workerId":"..."}` once it has successfully
  // registered its own provider adapters and started polling — there is no
  // HTTP endpoint of its own to poll, unlike serve.
  await new Promise<void>((resolve, reject) => {
    const deadline = setTimeout(() => reject(new Error(`aw worker never announced readiness within 15s:\n${workerOutput}`)), 15_000);
    const check = () => {
      if (workerOutput.includes('"workerId"')) { clearTimeout(deadline); resolve(); return; }
      setTimeout(check, 100);
    };
    check();
  });

  return { root, binDir, awBinary, fakeClaudeBinary, goVersion, dbPath, artifactRoot, workspaceRoot, repoReady, port, baseURL, serve, worker };
}

/** stopStack asks both processes to exit gracefully; a no-op for an already-exited process. */
export async function stopStack(stack: Stack): Promise<void> {
  for (const child of [stack.worker, stack.serve]) {
    if (child.exitCode !== null || child.killed) continue;
    child.kill(process.platform === 'win32' ? undefined : 'SIGTERM');
    await new Promise<void>(resolve => {
      const timeout = setTimeout(() => { child.kill('SIGKILL'); resolve(); }, 10_000);
      child.once('exit', () => { clearTimeout(timeout); resolve(); });
    });
  }
}

/**
 * killByPid tree-kills a process started by a PRIOR Node process invocation
 * (globalTeardown's own process is not guaranteed to be the same one
 * globalSetup ran in) — `taskkill /T /F` on Windows, a negative-pid signal
 * to the whole process group on POSIX (only correct because `startStack`
 * spawns with `detached: true` there, making the child its own group
 * leader). A no-op, not an error, if the pid is already gone.
 */
export function killByPid(pid: number): void {
  try {
    if (process.platform === 'win32') {
      spawnSync('taskkill', ['/T', '/F', '/PID', String(pid)], { stdio: 'ignore' });
    } else {
      process.kill(-pid, 'SIGKILL');
    }
  } catch {
    // already exited — nothing to do.
  }
}
