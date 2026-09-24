import type { NavRoute } from '../components/shell/LeftNav';

const checks: { step: string; status: 'PASS' | 'NEEDS REVIEW'; detail: string; route: NavRoute }[] = [
  { step: '1. Project identity and onboarding', status: 'PASS', detail: 'Project-specific repositories/components; Create Project and Register Repository focus return.', route: 'project-overview' },
  { step: '2. Components', status: 'PASS', detail: 'Components heading, identities, paths and exact packs.', route: 'project-components' },
  { step: '3. Definition scopes', status: 'PASS', detail: 'Global and Project filters are disjoint; selected project identity is preserved.', route: 'project-definitions' },
  { step: '4. Adapter Builds', status: 'PASS', detail: 'Installation registry, fingerprint drift, capabilities and probe actions.', route: 'system-adapters' },
  { step: '5. Create WorkItem', status: 'PASS', detail: 'Root/Child, READ/WRITE, normalized path validation and Cancel focus return. Board state is held per project for this session.', route: 'project-board' },
  { step: '6. Workspace and ReleaseSet', status: 'PASS', detail: 'worker-service Source/Diff/Log and quarantine; partial seal and local-only commit first show Requested.', route: 'task-workspace' },
  { step: '7. Definition diagnostic', status: 'PASS', detail: 'Open feature-agent editor: context_token at line 11 matches the diagnostic.', route: 'project-definitions' },
  { step: '8. Settings', status: 'PASS', detail: '120 is valid; min retry 10 has a linked error; version-conflict dialog is separate.', route: 'system-settings' },
  { step: '9. Approval', status: 'NEEDS REVIEW', detail: 'Target dialog, evidence, focus return and Requested operation pass. Authoritative event-stream completion and entity-version contract remain unspecified in this fixture.', route: 'task-overview' },
  { step: '10. Connection and projection', status: 'PASS', detail: 'Distinct Stale/Reconnecting/Offline/Degraded UI, cached age, journal position, reasons and rebuild Requested state.', route: 'system-diagnostics' },
  { step: '11. Keyboard accessibility', status: 'PASS', detail: 'Dialog trap/Escape/return, named icon controls, keyboard tooltips, aria-current, actionable graph list and definition selection.', route: 'task-graph' },
  { step: '12. Responsive Islands Light', status: 'PASS', detail: '1440×1024 and 1024×768 checks; 64 px rail, internal Board scrolling and 12 px metadata floor.', route: 'system-settings' },
  { step: '13. Safe authority boundaries', status: 'PASS', detail: 'Read-only code/logs, no remote Git or terminal controls; secret references only. All operations are prototype simulations.', route: 'task-workspace' },
];

export function RepairAudit({ onOpen }: { onOpen: (route: NavRoute) => void }) {
  return <div className="flex-1 overflow-auto p-6">
    <h1 className="text-xl font-semibold">Repair audit</h1>
    <p className="text-[13px] text-[#475569] my-3">Browser QA · 2026-09-09 · local Vite prototype. PASS describes the demonstrated fixture, not backend integration. Open links select platform-core for project/task fixtures.</p>
    <table className="w-full text-[13px] border-collapse">
      <thead><tr className="text-left bg-[#F3F5F8]"><th className="p-3">Acceptance step</th><th className="p-3">Result</th><th className="p-3">Evidence / limitation</th></tr></thead>
      <tbody>{checks.map(check => <tr key={check.step} className="border-b border-[#CDD5DF]">
        <td className="p-3"><button className="text-[#3659E3] underline text-left" onClick={() => onOpen(check.route)}>{check.step}</button></td>
        <td className={`p-3 whitespace-nowrap font-semibold ${check.status === 'PASS' ? 'text-[#166534]' : 'text-[#92400E]'}`}>{check.status}</td>
        <td className="p-3 text-[#475569]">{check.detail}</td>
      </tr>)}</tbody>
    </table>
    <p className="mt-4 text-[13px] text-[#475569]">Open API questions: authoritative approval entity version and SSE outcome; valid actions after cancellation; durable storage and backend admission for locally created records. No remote operation or permission is inferred from this prototype.</p>
  </div>;
}
