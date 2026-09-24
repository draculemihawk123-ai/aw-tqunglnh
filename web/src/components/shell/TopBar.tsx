import React from 'react';
import { ConnectionIndicator } from '../ui';
import { HelpCircle, ChevronDown, User } from '../icons';

export type ConnectionState = 'Live' | 'Reconnecting' | 'Offline' | 'Degraded' | 'Stale';

interface TopBarProps {
  project: string | null;
  connectionState: ConnectionState;
  freshness: string;
  onProjectSwitch: () => void;
}

export function TopBar({ project, connectionState, freshness, onProjectSwitch }: TopBarProps) {
  return (
    <header className="h-12 flex items-center px-4 gap-4 bg-[#F3F5F8] border border-[#CDD5DF] rounded-[12px] island-shadow flex-shrink-0 mx-2 mt-2">
      {/* Mark */}
      <div className="flex items-center gap-2 mr-2">
        <div className="w-7 h-7 rounded-[6px] bg-[#3659E3] flex items-center justify-center text-white text-[11px] font-bold select-none" aria-hidden>AK</div>
        <span className="text-sm font-semibold text-[#172033] hidden lg:block">Agent Kit</span>
      </div>

      {/* Project switcher */}
      <button
        onClick={onProjectSwitch}
        aria-label={project ? `Switch project (current: ${project})` : 'Select project'}
        className="flex items-center gap-1.5 h-8 px-3 rounded-[6px] bg-white border border-[#CDD5DF] hover:border-[#AAB4C3] text-sm text-[#172033] transition-colors"
      >
        <ChevronDown size={12} aria-hidden className="text-[#475569]" />
        {project ?? <span className="text-[#475569]">Select project…</span>}
      </button>

      <div className="flex-1" />

      {/* Freshness */}
      <span className="text-[12px] text-[#475569] hidden lg:block" aria-label={`Data last updated: ${freshness}`}>
        Updated <span className="font-medium">{freshness}</span>
      </span>

      {/* Connection */}
      <ConnectionIndicator state={connectionState} />

      {/* Actor */}
      <div className="flex items-center gap-2 pl-3 border-l border-[#CDD5DF]">
        <div className="w-6 h-6 rounded-full bg-[#ECEFF4] border border-[#CDD5DF] flex items-center justify-center text-[#475569]" aria-hidden>
          <User size={12} aria-hidden />
        </div>
        <span className="text-[12px] text-[#475569] hidden lg:block">local-operator</span>
      </div>

      {/* Help */}
      <button aria-label="Help" title="Help" className="w-7 h-7 rounded-full border border-[#CDD5DF] flex items-center justify-center text-[#475569] hover:bg-[#ECEFF4] transition-colors">
        <HelpCircle size={14} aria-hidden />
      </button>
    </header>
  );
}
