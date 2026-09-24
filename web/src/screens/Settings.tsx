import React, { useState } from 'react';
import { Badge, Button, Dialog } from '../components/ui';

interface SafeField {
  key: string;
  label: string;
  value: string;
  restartRequired: boolean;
  validation?: string;
  conflict?: boolean;
}

const SETTING_GROUPS = [
  {
    id: 'provider',
    name: 'Provider Defaults',
    settings: [
      { key: 'default_provider', label: 'Default provider', value: 'anthropic', restartRequired: false },
      { key: 'default_model', label: 'Default model', value: 'claude-sonnet-4-6', restartRequired: false },
      { key: 'max_retries', label: 'Max retries', value: '3', restartRequired: false },
      { key: 'request_timeout_s', label: 'Request timeout (s)', value: '120', restartRequired: false },
      { key: 'min_retry_delay_s', label: 'Min retry delay (s)', value: '10', restartRequired: false, validation: 'Value 10 is below the minimum of 30. Set to 30 or higher.' },
    ] as SafeField[],
  },
  {
    id: 'execution',
    name: 'Execution',
    settings: [
      { key: 'workspace_root', label: 'Workspace root', value: '/var/ak/workspaces', restartRequired: true },
      { key: 'artifact_root', label: 'Artifact root', value: '/var/ak/artifacts', restartRequired: true },
      { key: 'max_parallel_runs', label: 'Max parallel runs', value: '4', restartRequired: false, conflict: true },
    ] as SafeField[],
  },
  {
    id: 'secrets',
    name: 'Secret References',
    settings: [
      { key: 'openai_api_key_ref', label: 'OpenAI API key reference', value: 'env:OPENAI_API_KEY', restartRequired: false },
      { key: 'anthropic_api_key_ref', label: 'Anthropic API key reference', value: 'env:ANTHROPIC_API_KEY', restartRequired: false },
    ] as SafeField[],
  },
];

const RETENTION_CLASSES = [
  { id: 'standard', name: 'standard', duration: '30 days', hold: false, contentType: 'Artifacts, logs' },
  { id: 'extended', name: 'extended', duration: '90 days', hold: false, contentType: 'Evidence, ReleaseSet manifests' },
  { id: 'hold', name: 'hold', duration: 'Until released', hold: true, contentType: 'Quarantined, tampered artifacts' },
];

export function SettingsScreen({ isOffline = false }: { isOffline?: boolean }) {
  const [values, setValues] = useState<Record<string, string>>(
    Object.fromEntries(SETTING_GROUPS.flatMap(g => g.settings.map(s => [s.key, s.value])))
  );
  const [conflictDialog, setConflictDialog] = useState<string | null>(null);

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-5xl mx-auto space-y-6">
        <div>
          <h1 className="text-xl font-semibold text-[#172033]">Settings</h1>
          <p className="text-sm text-[#5D697A] mt-0.5">Safe, allow-listed settings. Secret values are never stored or displayed — use references only.</p>
        </div>

        {SETTING_GROUPS.map(group => (
          <div key={group.id} className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
              <h2 className="text-sm font-semibold text-[#172033]">{group.name}</h2>
            </div>
            <div className="divide-y divide-[#ECEFF4]">
              {group.settings.map(setting => (
                <div key={setting.key} className="px-5 py-4">
                  <div className="flex items-start gap-4">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 mb-1.5">
                        <label htmlFor={setting.key} className="text-sm font-medium text-[#172033]">{setting.label}</label>
                        {setting.restartRequired && (
                          <Badge label="Restart required" intent="warning" />
                        )}
                        {setting.conflict && (
                          <Badge label="Version conflict" intent="danger" />
                        )}
                      </div>
                      {group.id === 'secrets' ? (
                        <div className="flex items-center gap-2">
                          <input
                            id={setting.key}
                            value={values[setting.key]}
                            onChange={e => setValues(prev => ({ ...prev, [setting.key]: e.target.value }))}
                            className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-sm font-mono bg-white focus:border-[#3659E3] outline-none flex-1"
                            placeholder="env:MY_SECRET or secret:name"
                          />
                          <span className="text-xs text-[#5D697A]">reference only</span>
                        </div>
                      ) : (
                        <input
                          id={setting.key}
                          value={values[setting.key]}
                          onChange={e => setValues(prev => ({ ...prev, [setting.key]: e.target.value }))}
                          className={`h-9 px-3 rounded-[6px] border text-sm bg-white focus:outline-none w-full max-w-sm ${setting.validation ? 'border-[#FCA5A5] focus:border-[#FCA5A5]' : 'border-[#CDD5DF] focus:border-[#3659E3]'}`}
                        />
                      )}
                      {setting.validation && (
                        <p className="text-xs text-[#991B1B] mt-1">{setting.validation}</p>
                      )}
                      {setting.conflict && (
                        <div className="mt-2 p-2 rounded-[6px] bg-[#FEE2E2] border border-[#FCA5A5] text-xs text-[#991B1B]">
                          Version conflict: your local value is <span className="font-mono">4</span> but the server expects <span className="font-mono">3</span>.
                          <button className="underline ml-1" onClick={() => setConflictDialog(setting.key)}>Resolve…</button>
                        </div>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        ))}

        {/* Retention classes */}
        <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
          <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC]">
            <h2 className="text-sm font-semibold text-[#172033]">Retention Classes</h2>
            <p className="text-xs text-[#5D697A] mt-0.5">Canonical data is not subject to blanket TTL. These classes apply to artifacts and logs.</p>
          </div>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#ECEFF4] bg-[#F8FAFC]">
                <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Class</th>
                <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Duration</th>
                <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Hold policy</th>
                <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Content type</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#ECEFF4]">
              {RETENTION_CLASSES.map(rc => (
                <tr key={rc.id}>
                  <td className="px-5 py-3 font-mono text-xs font-medium">{rc.name}</td>
                  <td className="px-5 py-3 text-[#172033]">{rc.duration}</td>
                  <td className="px-5 py-3">{rc.hold ? <Badge label="HOLD" intent="info" /> : <span className="text-[#5D697A] text-xs">—</span>}</td>
                  <td className="px-5 py-3 text-[#5D697A]">{rc.contentType}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="flex justify-end gap-2 pb-4">
          <Button intent="secondary">Discard Changes</Button>
          <Button intent="primary" disabled={isOffline} title={isOffline ? 'Reconnect to save settings' : undefined}>Save Settings</Button>
        </div>
      </div>

      {conflictDialog && (
        <Dialog
          title="Resolve Version Conflict"
          description="Your local value was based on an older settings version. Refresh before submitting another mutation."
          onClose={() => setConflictDialog(null)}
          actions={
            <>
              <Button intent="secondary" onClick={() => setConflictDialog(null)}>Cancel</Button>
              <Button intent="quiet" onClick={() => { setValues(prev => ({ ...prev, max_parallel_runs: '3' })); setConflictDialog(null); }}>Use Server Value (3)</Button>
              <Button intent="primary" onClick={() => setConflictDialog(null)}>Refresh Latest Version</Button>
            </>
          }
        >
          <div className="text-sm text-[#5D697A] space-y-1">
            <div><strong className="text-[#172033]">Setting:</strong> max_parallel_runs</div>
            <div><strong className="text-[#172033]">Server value:</strong> <span className="font-mono">3</span></div>
            <div><strong className="text-[#172033]">Your value:</strong> <span className="font-mono">4</span></div>
          </div>
        </Dialog>
      )}
    </div>
  );
}
