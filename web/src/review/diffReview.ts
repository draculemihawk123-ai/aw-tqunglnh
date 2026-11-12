/**
 * V12-17 (docs/design/15-v12-ui-operations.md, UX-31): the approver's diff review — a unified diff split into files and numbered
 * lines, and line comments turned into the message the maker receives with the `revise` outcome (also the decision's reason, so
 * the comments are recorded with the decision as evidence).
 */
export type DiffLineKind = 'add' | 'del' | 'ctx' | 'hunk';

export interface DiffLine {
  kind: DiffLineKind;
  text: string;
  oldNumber?: number;
  newNumber?: number;
}

export interface DiffFile {
  path: string;
  lines: DiffLine[];
  additions: number;
  deletions: number;
}

export interface LineComment {
  repositoryId: string;
  path: string;
  /** The new-side line number, or the old-side one for a deleted line (then `deleted` is true). */
  line: number;
  deleted?: boolean;
  lineText: string;
  body: string;
}

const HUNK = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

export function parseUnifiedDiff(patch: string): DiffFile[] {
  const files: DiffFile[] = [];
  let current: DiffFile | undefined;
  let oldNo = 0;
  let newNo = 0;
  for (const raw of patch.split('\n')) {
    if (raw.startsWith('diff --git ')) {
      const m = /^diff --git a\/(.+?) b\/(.+)$/.exec(raw);
      current = { path: m ? m[2] : raw.slice(11), lines: [], additions: 0, deletions: 0 };
      files.push(current);
      continue;
    }
    if (!current) continue;
    if (raw.startsWith('+++ ')) {
      const p = raw.slice(4);
      if (p !== '/dev/null') current.path = p.replace(/^b\//, '');
      continue;
    }
    if (raw.startsWith('--- ') || raw.startsWith('index ') || raw.startsWith('new file') || raw.startsWith('deleted file') ||
      raw.startsWith('similarity ') || raw.startsWith('rename ') || raw.startsWith('old mode') || raw.startsWith('new mode') || raw.startsWith('Binary ')) {
      continue;
    }
    const hunk = HUNK.exec(raw);
    if (hunk) {
      oldNo = Number(hunk[1]);
      newNo = Number(hunk[2]);
      current.lines.push({ kind: 'hunk', text: raw });
      continue;
    }
    if (raw.startsWith('+')) {
      current.lines.push({ kind: 'add', text: raw.slice(1), newNumber: newNo++ });
      current.additions++;
    } else if (raw.startsWith('-')) {
      current.lines.push({ kind: 'del', text: raw.slice(1), oldNumber: oldNo++ });
      current.deletions++;
    } else if (raw.startsWith(' ')) {
      current.lines.push({ kind: 'ctx', text: raw.slice(1), oldNumber: oldNo++, newNumber: newNo++ });
    } else if (raw === '\\ No newline at end of file') {
      continue;
    }
  }
  return files;
}

/** The message (and decision reason) for "Request changes". */
export function reviewMessage(comments: LineComment[], summary = ''): string {
  const lines: string[] = ['Changes requested at the review gate:'];
  if (summary.trim()) lines.push('', summary.trim());
  const byFile = new Map<string, LineComment[]>();
  for (const c of comments) byFile.set(`${c.repositoryId}:${c.path}`, [...(byFile.get(`${c.repositoryId}:${c.path}`) ?? []), c]);
  for (const [, list] of byFile) {
    lines.push('', `${list[0].path} (${list[0].repositoryId}):`);
    for (const c of [...list].sort((a, b) => a.line - b.line)) {
      lines.push(`- line ${c.line}${c.deleted ? ' (removed line)' : ''}: \`${c.lineText.trim().slice(0, 120)}\``, `  ${c.body.trim()}`);
    }
  }
  return lines.join('\n');
}
