import { existsSync, readFileSync, rmSync, unlinkSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { killByPid } from './stack';

const HANDOFF_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '.stack-handoff.json');

export default async function globalTeardown(): Promise<void> {
  if (!existsSync(HANDOFF_PATH)) return;
  const handoff = JSON.parse(readFileSync(HANDOFF_PATH, 'utf-8')) as {
    root: string; servePid?: number; workerPid?: number;
  };
  // Worker first — the same "no job claimed while the API is already gone"
  // ordering `internal/integration/v6accept`'s own stack.go stop() uses.
  if (handoff.workerPid) killByPid(handoff.workerPid);
  if (handoff.servePid) killByPid(handoff.servePid);
  // Windows can hold a file-lock on a just-killed process's own
  // working-directory files (the sqlite db, its own binary) for a moment
  // after taskkill returns — wait it out, then retry the removal itself;
  // this cleanup is best-effort (a CI runner's disk is thrown away anyway)
  // so a lock that STILL hasn't cleared is logged, never thrown, rather
  // than reporting a real, green test run as a failure over housekeeping.
  await new Promise(resolve => setTimeout(resolve, 1000));
  try {
    rmSync(handoff.root, { recursive: true, force: true, maxRetries: 10, retryDelay: 500 });
  } catch (err) {
    console.error(`best-effort cleanup of ${handoff.root} failed (leaving it for the OS/CI runner to reclaim):`, err);
  }
  unlinkSync(HANDOFF_PATH);
}
