import React from 'react';
import {
  Stethoscope, LayoutGrid, ListTree, Columns3, Layers, Settings2,
  GanttChartSquare, MessageSquare, Package, Boxes, Wrench,
  Eye, FileCode2, Network, ClipboardList, SquareTerminal,
} from '../icons';

export type NavRoute =
  | 'doctor'
  | 'projects'
  | 'project-overview'
  | 'project-board'
  | 'project-components'
  | 'project-definitions'
  | 'task-overview'
  | 'task-graph'
  | 'task-workspace'
  | 'task-evidence'
  | 'task-chat'
  | 'global-definitions'
  | 'system-adapters'
  | 'system-diagnostics'
  | 'system-settings';

type NavIcon = React.ComponentType<{ size?: number; className?: string; 'aria-hidden'?: boolean }>;

interface NavItem {
  id: NavRoute;
  label: string;
  Icon: NavIcon;
  group: 'top' | 'project' | 'task' | 'global' | 'system';
}

const NAV_ITEMS: NavItem[] = [
  { id: 'doctor',              label: 'Doctor',          Icon: Stethoscope,     group: 'top' },
  { id: 'projects',            label: 'Projects',        Icon: LayoutGrid,      group: 'top' },
  { id: 'project-overview',    label: 'Overview',        Icon: Eye,             group: 'project' },
  { id: 'project-board',       label: 'Board',           Icon: Columns3,        group: 'project' },
  { id: 'project-components',  label: 'Components',      Icon: Boxes,           group: 'project' },
  { id: 'project-definitions', label: 'Definitions',     Icon: FileCode2,       group: 'project' },
  { id: 'task-overview',       label: 'Overview',        Icon: ClipboardList,   group: 'task' },
  { id: 'task-graph',          label: 'Graph & Timeline',Icon: Network,         group: 'task' },
  { id: 'task-workspace',      label: 'Workspace',       Icon: SquareTerminal,  group: 'task' },
  { id: 'task-evidence',       label: 'Evidence',        Icon: ListTree,        group: 'task' },
  { id: 'task-chat',           label: 'Chat',            Icon: MessageSquare,   group: 'task' },
  { id: 'global-definitions',  label: 'Global Definitions', Icon: FileCode2,    group: 'global' },
  { id: 'system-adapters',     label: 'Adapter Builds',  Icon: Wrench,          group: 'system' },
  { id: 'system-diagnostics',  label: 'Run Diagnostics', Icon: GanttChartSquare,group: 'system' },
  { id: 'system-settings',     label: 'Settings',        Icon: Settings2,       group: 'system' },
];

interface LeftNavProps {
  route: NavRoute;
  onNavigate: (route: NavRoute) => void;
  projectSelected: boolean;
  taskSelected: boolean;
  collapsed?: boolean;
}

export function LeftNav({ route, onNavigate, projectSelected, taskSelected, collapsed }: LeftNavProps) {
  const renderGroup = (label: string | null, items: NavItem[]) => (
    <div className="mb-1" key={label ?? items.map(item => item.id).join('-')}>
      {label && !collapsed && (
        <p className="px-3 py-1.5 text-[12px] font-semibold text-[#475569] uppercase tracking-wider" role="presentation">
          {label}
        </p>
      )}
      {items.map(item => {
        const isActive = route === item.id;
        return (
          <button
            key={item.id}
            onClick={() => onNavigate(item.id)}
            aria-current={isActive ? 'page' : undefined}
            title={collapsed ? item.label : undefined}
            className={`w-full flex items-center gap-2.5 px-3 py-2 rounded-[6px] text-sm transition-colors ${
              isActive
                ? 'bg-[#EEF2FF] text-[#3659E3] font-medium'
                : 'text-[#475569] hover:bg-[#ECEFF4] hover:text-[#172033]'
            } ${collapsed ? 'justify-center' : ''}`}
          >
            <item.Icon
              size={16}
              aria-hidden
              className={`flex-shrink-0 ${isActive ? 'text-[#3659E3]' : 'text-[#475569]'}`}
            />
            {!collapsed && <span>{item.label}</span>}
          </button>
        );
      })}
    </div>
  );

  return (
    <nav
      aria-label="Primary navigation"
      className={`flex flex-col bg-[#F3F5F8] border border-[#CDD5DF] rounded-[12px] island-shadow flex-shrink-0 overflow-y-auto transition-all ${collapsed ? 'w-[64px]' : 'w-[232px]'}`}
    >
      <div className="p-2 flex-1">
        {renderGroup(null, NAV_ITEMS.filter(i => i.group === 'top'))}

        {projectSelected && (
          <>
            <div className={`my-1 ${collapsed ? 'mx-2' : 'mx-1'} h-px bg-[#CDD5DF]`} />
            {renderGroup('Project', NAV_ITEMS.filter(i => i.group === 'project'))}
          </>
        )}

        {taskSelected && (
          <>
            <div className={`my-1 ${collapsed ? 'mx-2' : 'mx-1'} h-px bg-[#CDD5DF]`} />
            {renderGroup('WorkItem', NAV_ITEMS.filter(i => i.group === 'task'))}
          </>
        )}

        <div className={`my-2 ${collapsed ? 'mx-2' : 'mx-1'} h-px bg-[#CDD5DF]`} />
        {renderGroup(null, NAV_ITEMS.filter(i => i.group === 'global'))}

        <div className={`my-1 ${collapsed ? 'mx-2' : 'mx-1'} h-px bg-[#CDD5DF]`} />
        {renderGroup('System', NAV_ITEMS.filter(i => i.group === 'system'))}
      </div>
    </nav>
  );
}
