import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getAttemptPrompt } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, InlineError } from '../components/ui';

export interface AttemptPromptView {
  attemptId: string;
  nodeRunId: string;
  runId: string;
  nodeKey: string;
  contextSnapshotId: string;
  prompt: string;
  bytes: number;
  truncated?: boolean;
}

/** The prompt's JSON, indented when it parses (it always does for a real attempt). */
export function formatPrompt(prompt: string): string {
  try {
    return JSON.stringify(JSON.parse(prompt), null, 2);
  } catch {
    return prompt;
  }
}

/**
 * V12-26 (docs/design/15-v12-ui-operations.md, UX-12, ADR-059): the prompt an agent attempt received — the JSON document written to
 * the provider's stdin, rendered again from the attempt's context snapshot, known secrets redacted. Loaded only when asked for: a
 * prompt can be large.
 */
export function AttemptPromptSection({ projectId, attempts }: { projectId: string; attempts: { attemptId: string; attemptNumber?: number }[] }) {
  const [open, setOpen] = useState(false);
  const [attemptId, setAttemptId] = useState(attempts[attempts.length - 1]?.attemptId ?? '');
  const query = useQuery({
    queryKey: ['attemptPrompt', projectId, attemptId],
    queryFn: async () => (await getAttemptPrompt(projectId, attemptId, withSessionToken())) as unknown as AttemptPromptView,
    enabled: open && !!attemptId,
    staleTime: Infinity,
  });
  if (attempts.length === 0) return null;
  return (
    <section className="border border-[#CDD5DF] rounded-[6px] bg-white p-2" aria-label="Prompt" data-testid="node-prompt">
      <div className="flex items-center gap-2">
        <h3 className="text-[12px] font-semibold text-[#172033]">Prompt</h3>
        {attempts.length > 1 && (
          <select aria-label="Attempt" value={attemptId} onChange={e => setAttemptId(e.target.value)} className="border border-[#CDD5DF] rounded-[4px] px-1 text-[12px] bg-white">
            {attempts.map((a, i) => <option key={a.attemptId} value={a.attemptId}>attempt {a.attemptNumber ?? i + 1}</option>)}
          </select>
        )}
        <span className="flex-1" />
        <Button size="compact" intent="quiet" onClick={() => setOpen(o => !o)}>{open ? 'Hide prompt' : 'Show prompt'}</Button>
      </div>
      {open && query.isPending && <p className="text-[12px] text-[#475569]" role="status">Rendering the prompt…</p>}
      {open && query.isError && <InlineError code="PROMPT" message={query.error instanceof Error ? query.error.message : 'Could not read the prompt.'} />}
      {open && query.data && (
        <>
          <p className="text-[11px] text-[#475569] mt-1">
            {Math.round(query.data.bytes / 1024)} KiB · from snapshot <span className="font-mono">{query.data.contextSnapshotId}</span>{query.data.truncated ? ' · cut at 512 KiB' : ''}
            <button type="button" className="ml-2 text-[#3659E3] hover:underline" onClick={() => { void navigator.clipboard?.writeText(query.data!.prompt); }}>Copy</button>
          </p>
          <pre className="mt-1 max-h-80 overflow-auto bg-[#F8FAFC] border border-[#ECEFF4] rounded p-2 text-[11px] whitespace-pre-wrap" data-testid="prompt-text">{formatPrompt(query.data.prompt)}</pre>
        </>
      )}
    </section>
  );
}
