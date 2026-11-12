import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { deleteRepositorySecret, listRepositorySecrets, setRepositorySecrets } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, Dialog, InlineError } from '../components/ui';

interface SecretName { name: string; version: number; updatedBy: string; updatedAt: string }
interface SecretList { repositoryId: string; protection: string; secrets: SecretName[] }

const NAME = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;

/** The variable names of a .env text (the values are never shown), as the server reads it. */
export function dotEnvNames(text: string): { names: string[]; badLines: number[] } {
  const names: string[] = [];
  const badLines: number[] = [];
  text.split(/\r?\n/).forEach((raw, i) => {
    const line = raw.trim();
    if (!line || line.startsWith('#')) return;
    const body = line.startsWith('export ') ? line.slice(7) : line;
    const eq = body.indexOf('=');
    const name = eq > 0 ? body.slice(0, eq).trim() : '';
    if (!NAME.test(name)) { badLines.push(i + 1); return; }
    if (body.slice(eq + 1).trim() === '') return;
    if (!names.includes(name)) names.push(name);
  });
  return { names: names.sort(), badLines };
}

const PROTECTION: Record<string, string> = {
  dpapi: 'Windows DPAPI', keychain: 'macOS Keychain', 'secret-service': 'Secret Service', file: 'a key file readable only by this user',
};

/**
 * V12-29 (docs/design/15-v12-ui-operations.md, UX-34, ADR-061): the secrets of a repository — values its gates, commands and QA app
 * need — stored encrypted; this dialog never shows a value. Add or replace one, import a .env file once (only its names are shown to
 * confirm; the file is not kept), delete.
 */
export function RepositorySecretsDialog({ projectId, repositoryId, repositoryName, onClose }: { projectId: string; repositoryId: string; repositoryName: string; onClose: () => void }) {
  const query = useQuery({
    queryKey: ['repositorySecrets', projectId, repositoryId],
    queryFn: async () => (await listRepositorySecrets(projectId, repositoryId, withSessionToken())) as unknown as SecretList,
  });
  const [name, setName] = useState('');
  const [value, setValue] = useState('');
  const [dotEnv, setDotEnv] = useState('');
  const parsed = dotEnvNames(dotEnv);
  const save = useMutation({
    mutationFn: (body: { values?: Record<string, string>; dotEnv?: string }) =>
      setRepositorySecrets(projectId, repositoryId, body, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { setName(''); setValue(''); setDotEnv(''); void query.refetch(); },
  });
  const remove = useMutation({
    mutationFn: (secret: string) => deleteRepositorySecret(projectId, repositoryId, secret, {}, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { void query.refetch(); },
  });
  const existing = new Set((query.data?.secrets ?? []).map(s => s.name));
  const error = save.error ?? remove.error ?? query.error;
  return (
    <Dialog title={`Secrets of ${repositoryName}`} onClose={onClose}
      description="Given to this repository's gates, commands and QA app as environment variables; never shown, never put in a prompt, evidence or log.">
      {query.data && <p className="text-[12px] text-[#475569]">Encrypted; the key is protected by {PROTECTION[query.data.protection] ?? query.data.protection}.</p>}
      {error && <InlineError code="SECRETS" message={error instanceof Error ? error.message : 'The secret store failed.'} />}
      <ul className="mt-2 space-y-1" aria-label="Secrets">
        {(query.data?.secrets ?? []).map(s => (
          <li key={s.name} className="flex items-center gap-2 text-[13px]">
            <span className="font-mono text-[#172033]">{s.name}</span>
            <span className="text-[12px] text-[#475569]">•••••• · {s.updatedBy} · {new Date(s.updatedAt).toLocaleString()}</span>
            <span className="flex-1" />
            <Button size="compact" intent="quiet" disabled={remove.isPending} onClick={() => remove.mutate(s.name)} aria-label={`Delete ${s.name}`}>Delete</Button>
          </li>
        ))}
        {query.data && query.data.secrets.length === 0 && <li className="text-[13px] text-[#475569]">No secret yet.</li>}
      </ul>
      <div className="mt-3 flex items-end gap-2 flex-wrap">
        <label className="text-[12px] text-[#475569]">Name
          <input aria-label="Secret name" value={name} onChange={e => setName(e.target.value)} placeholder="DB_PASSWORD"
            className="block mt-1 border border-[#CDD5DF] rounded-[6px] px-2 py-1 font-mono text-[12px]" />
        </label>
        <label className="text-[12px] text-[#475569]">Value
          <input aria-label="Secret value" type="password" autoComplete="off" value={value} onChange={e => setValue(e.target.value)}
            className="block mt-1 border border-[#CDD5DF] rounded-[6px] px-2 py-1 text-[12px]" />
        </label>
        <Button size="compact" intent="primary" disabled={!NAME.test(name) || !value || save.isPending}
          onClick={() => save.mutate({ values: { [name]: value } })}>{existing.has(name) ? 'Replace' : 'Add'}</Button>
      </div>
      <details className="mt-3">
        <summary className="text-[12px] text-[#3659E3] cursor-pointer">Import a .env file</summary>
        <textarea aria-label="Pasted .env" rows={4} value={dotEnv} onChange={e => setDotEnv(e.target.value)} spellCheck={false}
          className="w-full mt-1 border border-[#CDD5DF] rounded-[6px] p-2 font-mono text-[12px]" style={{ WebkitTextSecurity: 'disc' } as React.CSSProperties} />
        {parsed.names.length > 0 && (
          <p className="text-[12px] text-[#475569]" data-testid="dotenv-names">
            Will store {parsed.names.length}: <span className="font-mono">{parsed.names.join(', ')}</span>
            {parsed.names.some(n => existing.has(n)) && ' (existing ones are replaced)'}
          </p>
        )}
        {parsed.badLines.length > 0 && <p className="text-[12px] text-[#991B1B]">Lines {parsed.badLines.join(', ')} are not NAME=value.</p>}
        <Button size="compact" intent="secondary" disabled={parsed.names.length === 0 || parsed.badLines.length > 0 || save.isPending}
          onClick={() => save.mutate({ dotEnv })}>Import</Button>
      </details>
    </Dialog>
  );
}
