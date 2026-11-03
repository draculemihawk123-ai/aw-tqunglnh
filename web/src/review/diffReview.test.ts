import { describe, expect, it } from 'vitest';
import { parseUnifiedDiff, reviewMessage } from './diffReview';

const PATCH = [
  'diff --git a/backend/src/Issue.java b/backend/src/Issue.java',
  'index 1..2 100644',
  '--- a/backend/src/Issue.java',
  '+++ b/backend/src/Issue.java',
  '@@ -10,3 +10,4 @@ class Issue {',
  '   void reopen() {',
  '-    status = CLOSED;',
  '+    status = OPEN;',
  '+    updatedAt = now();',
  '   }',
  'diff --git a/docs/a.md b/docs/a.md',
  'new file mode 100644',
  '--- /dev/null',
  '+++ b/docs/a.md',
  '@@ -0,0 +1 @@',
  '+# A',
].join('\n');

describe('diff review (V12-17)', () => {
  it('splits a unified diff into files and numbered lines', () => {
    const files = parseUnifiedDiff(PATCH);
    expect(files.map(f => [f.path, f.additions, f.deletions])).toEqual([['backend/src/Issue.java', 2, 1], ['docs/a.md', 1, 0]]);
    const lines = files[0].lines;
    expect(lines[0].kind).toBe('hunk');
    expect(lines[1]).toMatchObject({ kind: 'ctx', oldNumber: 10, newNumber: 10 });
    expect(lines[2]).toMatchObject({ kind: 'del', oldNumber: 11, text: '    status = CLOSED;' });
    expect(lines[3]).toMatchObject({ kind: 'add', newNumber: 11 });
    expect(lines[4]).toMatchObject({ kind: 'add', newNumber: 12 });
  });

  it('turns line comments into one message, grouped by file and sorted by line', () => {
    const text = reviewMessage([
      { repositoryId: 'api', path: 'backend/src/Issue.java', line: 12, lineText: '    updatedAt = now();', body: 'Use the injected clock.' },
      { repositoryId: 'api', path: 'backend/src/Issue.java', line: 11, deleted: true, lineText: '    status = CLOSED;', body: 'Keep a test for this case.' },
    ], 'Almost there.');
    expect(text).toContain('Almost there.');
    expect(text.indexOf('line 11')).toBeLessThan(text.indexOf('line 12'));
    expect(text).toContain('line 11 (removed line)');
    expect(text).toContain('Use the injected clock.');
  });
});
