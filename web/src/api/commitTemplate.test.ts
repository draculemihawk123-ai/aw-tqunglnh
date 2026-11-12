import { describe, expect, it } from 'vitest';
import { renderCommitTemplate } from './commitTemplate';

describe('renderCommitTemplate (V12-25)', () => {
  it('follows the same rules as the server', () => {
    expect(renderCommitTemplate(undefined, 'P1-01: reopen keeps the status (lần 3)', 'wi-9')).toBe('P1-01: reopen keeps the status');
    expect(renderCommitTemplate('feat: {title}', 'Add tracing (lần 2)', 'wi-9')).toBe('feat: Add tracing');
    expect(renderCommitTemplate('feat({taskId}): {title}', 'T-01: Order status API', 'wi-9')).toBe('feat(T-01): Order status API');
    expect(renderCommitTemplate('{taskId} {title}\n\nWork item {workItemId}', 'Docs only', 'wi-9')).toBe('Docs only\n\nWork item wi-9');
  });
});
