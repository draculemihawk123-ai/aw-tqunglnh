import React, { useEffect, useState } from 'react';
import { Badge, Button, Dialog } from '../components/ui';
import { AlertTriangle, CheckCircle2, X } from '../components/icons';
import type { ProjectSummary } from './Projects';

type DefKind = 'all' | 'Skill' | 'Layer' | 'Pack' | 'Agent' | 'Executable';
type DefScope = 'global' | 'project';

interface DefinitionRecord {
  id: string;
  kind: Exclude<DefKind, 'all'>;
  scope: DefScope;
  name: string;
  status: 'ACTIVE';
  version: string;
  dependencies: string[];
  compatible: boolean;
  hash: string;
}

const DEFINITIONS: DefinitionRecord[] = [
  { id: 'def-001', kind: 'Skill', scope: 'global', name: 'code-review-skill', status: 'ACTIVE', version: '2.1.0', dependencies: ['security-layer@1.0.5', 'lint-rules@3.0.0', 'test-runner@3.0.0'], compatible: true, hash: 'sha256:7b2c4e6f' },
  { id: 'def-002', kind: 'Pack', scope: 'global', name: 'backend-pack', status: 'ACTIVE', version: '2.4.1', dependencies: ['code-review-skill@2.1.0', 'security-layer@1.0.5', 'feature-agent@1.3.2', 'test-runner@3.0.0', 'lint-rules@3.0.0'], compatible: true, hash: 'sha256:a3f9e8b1' },
  { id: 'def-003', kind: 'Agent', scope: 'project', name: 'feature-agent', status: 'ACTIVE', version: '1.3.2', dependencies: ['code-review-skill@2.1.0', 'security-layer@1.0.5'], compatible: true, hash: 'sha256:c8d12e4f' },
  { id: 'def-004', kind: 'Layer', scope: 'global', name: 'security-layer', status: 'ACTIVE', version: '1.0.5', dependencies: ['policy-baseline@1.1.0'], compatible: false, hash: 'sha256:f1a2b3c4' },
  { id: 'def-005', kind: 'Executable', scope: 'global', name: 'test-runner', status: 'ACTIVE', version: '3.0.0', dependencies: [], compatible: true, hash: 'sha256:9e8d7c6b' },
];

// Line 11 intentionally uses `context_token` to demonstrate the editor diagnostic.
const YAML_CONTENT = `# feature-agent v1.3.2
kind: Agent
name: feature-agent
version: 1.3.2

dependencies:
  - skill: code-review-skill@^2.0.0
  - layer: security-layer@1.0.5

resources:
  context_token: 200000
  max_attempts: 3

adapter_pins:
  - provider: anthropic
    model: claude-sonnet-4-6
    protocol: AK-Adapter/1.2

workflow: feature-workflow@v3.1.0`;

function yamlForDefinition(definition: DefinitionRecord) {
  if (definition.name === 'feature-agent') return YAML_CONTENT;
  const dependencyLines = definition.dependencies.length > 0
    ? definition.dependencies.map(dependency => `  - ${dependency}`).join('\n')
    : '  []';
  return `kind: ${definition.kind}\nname: ${definition.name}\nversion: ${definition.version}\n\ndependencies:\n${dependencyLines}`;
}

function nextPatchVersion(version: string) {
  const parts = version.split('.');
  const patch = Number(parts[2]);
  if (parts.length !== 3 || !Number.isInteger(patch)) return version;
  return `${parts[0]}.${parts[1]}.${patch + 1}`;
}

interface DefinitionsScreenProps {
  project: ProjectSummary | null;
  initialScope: DefScope;
  isOffline?: boolean;
}

export function DefinitionsScreen({ project, initialScope, isOffline = false }: DefinitionsScreenProps) {
  const [scope, setScope] = useState<DefScope>(initialScope);
  const [kindFilter, setKindFilter] = useState<DefKind>('all');
  const [selectedDef, setSelectedDef] = useState(DEFINITIONS[2]);
  const [editorMode, setEditorMode] = useState(false);
  const [publishDialog, setPublishDialog] = useState(false);
  const [draftYaml, setDraftYaml] = useState(YAML_CONTENT);
  const [draftSaved, setDraftSaved] = useState(false);

  useEffect(() => {
    setEditorMode(false);
    setScope(initialScope);
    const firstInScope = DEFINITIONS.find(def => initialScope === 'global' ? def.scope === 'global' : def.scope === 'project');
    if (firstInScope) {
      setSelectedDef(firstInScope);
      setDraftYaml(yamlForDefinition(firstInScope));
      setDraftSaved(false);
    }
  }, [initialScope, project?.id]);

  const changeScope = (nextScope: DefScope) => {
    setScope(nextScope);
    const firstInScope = DEFINITIONS.find(def => nextScope === 'global' ? def.scope === 'global' : def.scope === 'project');
    if (firstInScope) {
      setSelectedDef(firstInScope);
      setDraftYaml(yamlForDefinition(firstInScope));
      setDraftSaved(false);
    }
  };

  const selectDefinition = (definition: DefinitionRecord) => {
    setSelectedDef(definition);
    setDraftYaml(yamlForDefinition(definition));
    setDraftSaved(false);
  };

  const openEditor = () => {
    setDraftYaml(yamlForDefinition(selectedDef));
    setDraftSaved(false);
    setEditorMode(true);
  };

  const hasContextTokenDiagnostic = /^\s*context_token\s*:/m.test(draftYaml);

  const filtered = DEFINITIONS.filter(d => {
    if (d.scope !== scope) return false;
    if (scope === 'project' && project?.id !== 'proj-alpha-001') return false;
    if (kindFilter !== 'all' && d.kind !== kindFilter) return false;
    return true;
  });

  const kinds: DefKind[] = ['all', 'Skill', 'Layer', 'Pack', 'Agent', 'Executable'];

  if (editorMode) {
    return (
      <div className="flex-1 flex flex-col overflow-hidden">
        <div className="px-6 py-3 border-b border-[#CDD5DF] bg-white flex items-center gap-3">
          <button onClick={() => setEditorMode(false)} className="text-xs text-[#5D697A] hover:text-[#3659E3]">← Definitions</button>
          <span className="text-sm font-semibold text-[#172033]">{selectedDef.name}</span>
          <Badge label="LOCAL DRAFT" intent="warning" />
          <span className="text-[12px] text-[#475569]">{draftSaved ? 'saved locally · not published' : 'unsaved changes · not published'}</span>
          <div className="flex-1" />
          <Button intent="secondary" size="compact" onClick={() => setEditorMode(false)}>Discard</Button>
          <Button intent="secondary" size="compact" onClick={() => setDraftSaved(true)}>Save Local Draft</Button>
          <Button intent="primary" size="compact" disabled={isOffline || hasContextTokenDiagnostic}
            title={isOffline ? 'Reconnect to publish' : hasContextTokenDiagnostic ? 'Resolve validation errors before publishing' : undefined}
            onClick={() => setPublishDialog(true)}>Publish…</Button>
        </div>
        <div className="flex flex-1 overflow-hidden">
          {/* Editor */}
          <div className="flex-1 flex flex-col overflow-hidden">
            <div className="px-4 py-2 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center gap-2">
              <span className="text-xs font-medium text-[#5D697A]">YAML</span>
              <div className="h-4 w-px bg-[#CDD5DF]" />
              <span className="text-[12px] text-[#475569]">{selectedDef.name}.yaml</span>
            </div>
            <textarea
              value={draftYaml}
              onChange={e => { setDraftYaml(e.target.value); setDraftSaved(false); }}
              className={`flex-1 p-4 font-mono text-[12px] bg-[#FBFCFE] text-[#172033] resize-none outline-none leading-6 border-l-4 ${hasContextTokenDiagnostic ? 'border-l-[#FCA5A5]' : 'border-l-transparent'}`}
              spellCheck={false}
            />
            {hasContextTokenDiagnostic && (
              <div role="alert" className="px-4 py-2 border-t border-[#CDD5DF] bg-[#FEE2E2] text-[12px] text-[#991B1B] flex items-center gap-2">
                <X size={12} aria-hidden className="flex-shrink-0" />
                <span className="font-mono font-medium">line 11, col 3</span>
                <span>Unknown field <span className="font-mono">context_token</span> — did you mean <span className="font-mono">context_tokens</span>?</span>
              </div>
            )}
          </div>
          {/* Graph preview */}
          <div className="w-72 border-l border-[#CDD5DF] bg-[#F3F5F8] flex flex-col">
            <div className="px-4 py-2.5 border-b border-[#CDD5DF]">
              <span className="text-xs font-semibold text-[#5D697A]">Graph Preview (read-only)</span>
            </div>
            <div className="flex-1 flex items-center justify-center text-[12px] text-[#475569]">
              Save draft to preview graph.
            </div>
          </div>
        </div>

        {publishDialog && (
          <Dialog
            title={`Publish ${selectedDef.name}`}
            description="Publishing creates an immutable version. Review both hashes and dependency pins before confirming."
            onClose={() => setPublishDialog(false)}
            actions={
              <>
                <Button intent="secondary" onClick={() => setPublishDialog(false)}>Cancel</Button>
                <Button intent="primary" disabled={isOffline} onClick={() => { setPublishDialog(false); setEditorMode(false); }}>Confirm Publish</Button>
              </>
            }
          >
            <div className="space-y-3 text-sm">
              <div className="space-y-1.5">
                <div className="flex gap-3"><span className="text-[#5D697A] w-40">Name</span><span className="font-medium">{selectedDef.name}</span></div>
                <div className="flex gap-3"><span className="text-[#5D697A] w-40">Version</span><span className="font-mono text-xs">{nextPatchVersion(selectedDef.version)}</span></div>
                <div className="flex gap-3"><span className="text-[#5D697A] w-40">SourceHash</span><span className="font-mono text-xs break-all">sha256:a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2</span></div>
                <div className="flex gap-3"><span className="text-[#5D697A] w-40">CompiledSnapshotHash</span><span className="font-mono text-xs break-all">sha256:f6e5d4c3b2a1f6e5d4c3b2a1f6e5d4c3b2a1f6e5</span></div>
              </div>
              <div className="pt-2 border-t border-[#ECEFF4]">
                <div className="text-xs font-medium text-[#172033] mb-1.5">Dependency Pins</div>
                <div className="space-y-1 font-mono text-xs text-[#5D697A]">
                  {selectedDef.dependencies.map(dependency => (
                    <div key={dependency} className="flex items-center gap-1.5">
                      <CheckCircle2 size={12} className="text-[#166534]" aria-hidden />
                      <span>{dependency}</span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </Dialog>
        )}
      </div>
    );
  }

  return (
    <div className="flex-1 flex overflow-hidden">
      {/* Catalog */}
      <div className="flex-1 flex flex-col overflow-hidden border-r border-[#CDD5DF]">
        <div className="px-6 py-4 border-b border-[#CDD5DF] bg-white">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-xl font-semibold text-[#172033]">Definitions</h1>
              <p className="text-[12px] text-[#475569] mt-0.5">{scope === 'project' ? `Project scope · ${project?.name ?? 'Select a project'} · ${project?.id ?? '—'}` : 'Global / Installation scope'}</p>
            </div>
          </div>
          <div className="flex items-center gap-3 mt-3">
            <div className="flex rounded-[6px] border border-[#CDD5DF] overflow-hidden">
              {(['global', 'project'] as const).map(s => (
                <button key={s} onClick={() => changeScope(s)} aria-pressed={scope === s} disabled={s === 'project' && !project}
                  className={`px-3 py-1.5 text-xs font-medium capitalize transition-colors ${scope === s ? 'bg-[#3659E3] text-white' : 'bg-white text-[#5D697A] hover:bg-[#F3F5F8]'}`}>
                  {s === 'global' ? 'Global / Installation' : 'Project'}
                </button>
              ))}
            </div>
            <div className="flex gap-1">
              {kinds.map(k => (
                <button key={k} onClick={() => setKindFilter(k)}
                  className={`px-2.5 py-1 text-xs rounded-[6px] border transition-colors ${kindFilter === k ? 'bg-[#EEF2FF] text-[#3659E3] border-[#C7D2FE] font-medium' : 'bg-white text-[#5D697A] border-[#CDD5DF] hover:border-[#AAB4C3]'}`}>
                  {k}
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="flex-1 overflow-y-auto bg-[#E9EDF3] p-4">
          <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[#ECEFF4] bg-[#F8FAFC]">
                  <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Name</th>
                  <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Kind</th>
                  <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Version</th>
                  <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Deps</th>
                  <th className="text-left px-5 py-2.5 text-xs font-semibold text-[#5D697A]">Compatible</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#ECEFF4]">
                {filtered.map(def => (
                  <tr key={def.id}
                    onClick={() => selectDefinition(def)}
                    className={`hover:bg-[#F8FAFC] cursor-pointer transition-colors ${selectedDef.id === def.id ? 'bg-[#EEF2FF]' : ''}`}>
                    <td className="px-5 py-3">
                      <button onClick={() => selectDefinition(def)} className="font-medium text-[#172033]">{def.name}</button>
                    </td>
                    <td className="px-5 py-3"><Badge label={def.kind} intent="neutral" /></td>
                    <td className="px-5 py-3 font-mono text-xs">{def.version}</td>
                    <td className="px-5 py-3 text-[#5D697A]">{def.dependencies.length}</td>
                    <td className="px-5 py-3">
                      {def.compatible
                        ? <span className="inline-flex items-center gap-1 text-[#166534] text-xs"><CheckCircle2 size={12} aria-hidden /> Compatible</span>
                        : <span className="inline-flex items-center gap-1 text-[#991B1B] text-xs"><AlertTriangle size={12} aria-hidden /> Incompatible pin</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {/* Version detail */}
      {filtered.some(def => def.id === selectedDef.id) ? <div className="w-80 xl:w-96 flex flex-col bg-[#F3F5F8] flex-shrink-0 overflow-y-auto">
        <div className="px-5 py-4 border-b border-[#CDD5DF] bg-white">
          <div className="flex items-center justify-between">
            <span className="text-sm font-semibold text-[#172033]">{selectedDef.name}</span>
            <Badge label={selectedDef.kind} intent="neutral" />
          </div>
          <div className="flex items-center gap-2 mt-1">
            <span className="font-mono text-xs text-[#5D697A]">v{selectedDef.version}</span>
            <span className="text-[12px] text-[#475569]">·</span>
            <span className="text-[12px] text-[#475569]">immutable</span>
          </div>
        </div>
        <div className="p-5 space-y-5">
          {/* Hashes */}
          <div>
            <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Hashes</h3>
            <div className="space-y-2 bg-white rounded-[8px] border border-[#CDD5DF] p-3">
              <div>
                <div className="text-[12px] text-[#475569] mb-0.5">SourceHash</div>
                <div className="font-mono text-xs text-[#172033] break-all">{selectedDef.hash}…a1b2c3d4</div>
              </div>
              <div className="h-px bg-[#ECEFF4]" />
              <div>
                <div className="text-[12px] text-[#475569] mb-0.5">CompiledSnapshotHash</div>
                <div className="font-mono text-xs text-[#172033] break-all">sha256:9e8d7c6b…f5e4d3c2</div>
              </div>
            </div>
          </div>
          {/* Dependencies */}
          <div>
            <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Dependencies ({selectedDef.dependencies.length})</h3>
            <div className="space-y-1">
              {selectedDef.dependencies.length > 0 ? (
                selectedDef.dependencies.map(d => (
                  <div key={d} className="font-mono text-xs text-[#172033] bg-white rounded-[6px] border border-[#CDD5DF] px-3 py-2">{d}</div>
                ))
              ) : <div className="text-[12px] text-[#475569]">No dependencies.</div>}
            </div>
          </div>
          {/* Adapter pins */}
          <div>
            <h3 className="text-xs font-semibold text-[#5D697A] mb-2">Adapter Pins</h3>
            <div className="font-mono text-xs text-[#172033] bg-white rounded-[6px] border border-[#CDD5DF] px-3 py-2">
              {selectedDef.kind === 'Agent' || selectedDef.kind === 'Executable'
                ? 'anthropic / claude-sonnet-4-6 / AK-Adapter/1.2'
                : 'No direct adapter pin'}
            </div>
          </div>
          {/* Actions */}
          <div className="flex gap-2">
            <Button intent="secondary" size="compact" onClick={openEditor}>Open Editor</Button>
            <Button intent="quiet" size="compact">View Raw</Button>
          </div>
        </div>
      </div> : <p className="p-5 text-[13px] text-[#475569]">No selected definition in this scope. Select an available definition to inspect it.</p>}
    </div>
  );
}
