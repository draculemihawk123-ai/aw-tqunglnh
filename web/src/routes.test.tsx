import { describe, expect, it } from 'vitest';
import { renderHook } from '@testing-library/react';
import { Router, useRouter } from 'wouter';
import { memoryLocation } from 'wouter/memory-location';
import { FIXTURE_TASK_ID, matchPath, pathFor } from './routes';

/** A real wouter Parser — matchPath's own doc comment says to get one from useRouter().parser, so tests do exactly that rather than reaching into regexparam directly. */
function useRealParser() {
  const { result } = renderHook(() => useRouter(), {
    wrapper: ({ children }) => <Router hook={memoryLocation({ path: '/doctor' }).hook}>{children}</Router>,
  });
  return result.current.parser;
}

describe('pathFor', () => {
  it('builds every non-scoped route as a fixed path', () => {
    expect(pathFor('doctor')).toBe('/doctor');
    expect(pathFor('projects')).toBe('/projects');
    expect(pathFor('global-definitions')).toBe('/definitions');
    expect(pathFor('system-adapters')).toBe('/system/adapters');
    expect(pathFor('system-diagnostics')).toBe('/system/diagnostics');
    expect(pathFor('system-settings')).toBe('/system/settings');
  });

  it('builds every project-scoped route with the given projectId', () => {
    expect(pathFor('project-overview', { projectId: 'proj-1' })).toBe('/projects/proj-1');
    expect(pathFor('project-board', { projectId: 'proj-1' })).toBe('/projects/proj-1/board');
    expect(pathFor('project-components', { projectId: 'proj-1' })).toBe('/projects/proj-1/components');
    expect(pathFor('project-definitions', { projectId: 'proj-1' })).toBe('/projects/proj-1/definitions');
  });

  it('builds every task-scoped route with both projectId and taskId', () => {
    expect(pathFor('task-overview', { projectId: 'proj-1', taskId: 'wi-1' })).toBe('/projects/proj-1/tasks/wi-1');
    expect(pathFor('task-graph', { projectId: 'proj-1', taskId: 'wi-1' })).toBe('/projects/proj-1/tasks/wi-1/graph');
    expect(pathFor('task-workspace', { projectId: 'proj-1', taskId: 'wi-1' })).toBe('/projects/proj-1/tasks/wi-1/workspace');
    expect(pathFor('task-evidence', { projectId: 'proj-1', taskId: 'wi-1' })).toBe('/projects/proj-1/tasks/wi-1/evidence');
    expect(pathFor('task-chat', { projectId: 'proj-1', taskId: 'wi-1' })).toBe('/projects/proj-1/tasks/wi-1/chat');
  });

  it('throws rather than silently building a broken URL when a required id is missing', () => {
    expect(() => pathFor('project-overview')).toThrow(/projectId/);
    expect(() => pathFor('task-overview', { projectId: 'proj-1' })).toThrow(/taskId/);
  });
});

describe('matchPath', () => {
  it('matches every route pathFor can build, round-trip, with the right params', () => {
    const parser = useRealParser();
    const cases: Array<[ReturnType<typeof pathFor>, string, Record<string, string>]> = [
      [pathFor('doctor'), 'doctor', {}],
      [pathFor('projects'), 'projects', {}],
      [pathFor('project-overview', { projectId: 'proj-1' }), 'project-overview', { projectId: 'proj-1' }],
      [pathFor('project-board', { projectId: 'proj-1' }), 'project-board', { projectId: 'proj-1' }],
      [pathFor('task-graph', { projectId: 'proj-1', taskId: FIXTURE_TASK_ID }), 'task-graph', { projectId: 'proj-1', taskId: FIXTURE_TASK_ID }],
      [pathFor('global-definitions'), 'global-definitions', {}],
      [pathFor('system-settings'), 'system-settings', {}],
    ];
    for (const [path, expectedRoute, expectedIds] of cases) {
      const match = matchPath(parser, path);
      expect(match?.route).toBe(expectedRoute);
      if (expectedIds.projectId) expect(match?.projectId).toBe(expectedIds.projectId);
      if (expectedIds.taskId) expect(match?.taskId).toBe(expectedIds.taskId);
    }
  });

  it('matches a task route before its own project route (more specific first)', () => {
    const parser = useRealParser();
    // "/projects/proj-1/tasks/wi-1" must resolve to task-overview, never be
    // mistaken for project-overview with a literal projectId of "tasks".
    const match = matchPath(parser, '/projects/proj-1/tasks/wi-1');
    expect(match?.route).toBe('task-overview');
    expect(match?.projectId).toBe('proj-1');
    expect(match?.taskId).toBe('wi-1');
  });

  it('returns null for a path with no matching route', () => {
    const parser = useRealParser();
    expect(matchPath(parser, '/nothing-here')).toBeNull();
    expect(matchPath(parser, '/')).toBeNull();
  });
});
