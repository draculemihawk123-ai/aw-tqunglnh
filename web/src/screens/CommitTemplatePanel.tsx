import { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getProjectCommitTemplate, setProjectCommitTemplate } from '../api/generated';
import { withSessionToken } from '../api/session';
import { renderCommitTemplate } from '../api/commitTemplate';
import { Button, InlineError } from '../components/ui';

export interface CommitTemplateView { projectId: string; template: string; default: string; version?: number }

export function commitTemplateQueryOptions(projectId: string, isOffline?: boolean) {
  return {
    queryKey: ['projectCommitTemplate', projectId],
    queryFn: async () => (await getProjectCommitTemplate(projectId, withSessionToken())) as unknown as CommitTemplateView,
    enabled: !isOffline && !!projectId,
    staleTime: 60_000,
  };
}

/**
 * V12-25 (docs/design/15-v12-ui-operations.md, UX-13, ADR-058): the project's commit message template, the default message of the
 * commit an approval asks for — written in the repository's convention ("feat: {title}"). The release marker is added by aw as a
 * trailer at the end of the body, never on the subject line.
 */
export function CommitTemplatePanel({ projectId, isOffline, readOnly }: { projectId: string; isOffline?: boolean; readOnly?: boolean }) {
  const query = useQuery(commitTemplateQueryOptions(projectId, isOffline));
  const [draft, setDraft] = useState('');
  useEffect(() => { if (query.data) setDraft(query.data.template); }, [query.data]);
  const save = useMutation({
    mutationFn: (template: string) => setProjectCommitTemplate(projectId, { template }, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { void query.refetch(); },
  });
  if (!query.data) return null;
  const preview = renderCommitTemplate(draft, 'T-01: Order status API (lần 2)', 'wi-…');
  return (
    <section className="bg-white border border-[#CDD5DF] rounded-[8px] p-4" aria-label="Commit message template">
      <h2 className="text-[14px] font-semibold text-[#172033]">Commit message template</h2>
      <p className="text-[12px] text-[#475569] mt-0.5">
        The default message of a commit an approval asks for. Placeholders: <code>{'{title}'}</code>, <code>{'{taskId}'}</code>, <code>{'{workItemId}'}</code>.
        aw adds its release marker as a trailer at the end.
      </p>
      <textarea aria-label="Template" rows={2} value={draft} placeholder={query.data.default} disabled={isOffline || readOnly}
        onChange={e => setDraft(e.target.value)} className="w-full mt-2 border border-[#CDD5DF] rounded-[6px] p-2 font-mono text-[12px]" />
      <p className="text-[12px] text-[#475569]">Example: <span className="font-mono text-[#172033]" data-testid="template-preview">{preview}</span></p>
      {save.isError && <InlineError code="TEMPLATE" message={save.error instanceof Error ? save.error.message : 'Saving failed.'} />}
      <div className="flex gap-2 mt-2">
        <Button size="compact" intent="primary" disabled={isOffline || readOnly || draft.trim() === query.data.template.trim()} loading={save.isPending}
          onClick={() => save.mutate(draft)}>Save template</Button>
        {query.data.template && <Button size="compact" intent="secondary" disabled={isOffline || readOnly} onClick={() => save.mutate('')}>Use the default</Button>}
      </div>
    </section>
  );
}
