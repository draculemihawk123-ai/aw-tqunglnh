import { matchRoute, type Parser } from 'wouter';
import type { NavRoute } from './components/shell/LeftNav';

/**
 * The one place every NavRoute maps to and from a real, bookmarkable URL —
 * V7-04's own "route guards by selected project only" requirement needs a
 * REAL url (so "which project" survives a refresh) instead of the
 * in-memory `route`/`project` useState pair V7-03 and earlier left behind.
 *
 * A project- or task-scoped NavRoute always needs a `projectId` (and task-
 * scoped ones a `taskId`) baked into the URL; App.tsx's own route guard
 * redirects to `/projects` whenever the URL's projectId does not resolve
 * to a real project — see App.tsx's own doc comment on that effect.
 */

export interface RouteMatch {
  route: NavRoute;
  projectId?: string;
  taskId?: string;
}

interface RouteDef {
  pattern: string;
  route: NavRoute;
}

// Longest/most specific patterns first purely for readability — matchRoute
// itself matches each pattern independently (no longest-prefix-wins
// behavior to depend on), so order does not change correctness here, only
// which pattern a reader meets first.
const ROUTE_TABLE: RouteDef[] = [
  { pattern: '/doctor', route: 'doctor' },
  { pattern: '/projects', route: 'projects' },
  { pattern: '/projects/:projectId/tasks/:taskId/graph', route: 'task-graph' },
  { pattern: '/projects/:projectId/tasks/:taskId/workspace', route: 'task-workspace' },
  { pattern: '/projects/:projectId/tasks/:taskId/evidence', route: 'task-evidence' },
  { pattern: '/projects/:projectId/tasks/:taskId/chat', route: 'task-chat' },
  { pattern: '/projects/:projectId/tasks/:taskId', route: 'task-overview' },
  { pattern: '/projects/:projectId/board', route: 'project-board' },
  { pattern: '/projects/:projectId/components', route: 'project-components' },
  { pattern: '/projects/:projectId/definitions', route: 'project-definitions' },
  { pattern: '/projects/:projectId', route: 'project-overview' },
  { pattern: '/definitions', route: 'global-definitions' },
  { pattern: '/system/adapters', route: 'system-adapters' },
  { pattern: '/system/diagnostics', route: 'system-diagnostics' },
  { pattern: '/system/settings', route: 'system-settings' },
];

/** The single real fixture TaskDetail can actually render (Kanban.tsx's own documented limitation — every other card's open button is disabled). */
export const FIXTURE_TASK_ID = 'wi-0018';

export const PROJECT_SCOPED_ROUTES = new Set<NavRoute>([
  'project-overview', 'project-board', 'project-components', 'project-definitions',
  'task-overview', 'task-graph', 'task-workspace', 'task-evidence', 'task-chat',
]);

/** matchRoute needs a Parser instance — get one from `useRouter().parser` (wouter's own recipe for matching a path against several patterns outside of JSX). */
export function matchPath(parser: Parser, path: string): RouteMatch | null {
  for (const { pattern, route } of ROUTE_TABLE) {
    const [matched, params] = matchRoute(parser, pattern, path);
    if (matched) {
      return { route, projectId: (params as Record<string, string> | null)?.projectId, taskId: (params as Record<string, string> | null)?.taskId };
    }
  }
  return null;
}

/** The inverse of matchPath: build the real URL for a NavRoute, given whatever projectId/taskId it needs. Throws if a required id is missing — a caller navigating to a project/task route always has one, per PROJECT_SCOPED_ROUTES's own contract. */
export function pathFor(route: NavRoute, ids: { projectId?: string; taskId?: string } = {}): string {
  switch (route) {
    case 'doctor': return '/doctor';
    case 'projects': return '/projects';
    case 'project-overview': return `/projects/${requireId(route, 'projectId', ids.projectId)}`;
    case 'project-board': return `/projects/${requireId(route, 'projectId', ids.projectId)}/board`;
    case 'project-components': return `/projects/${requireId(route, 'projectId', ids.projectId)}/components`;
    case 'project-definitions': return `/projects/${requireId(route, 'projectId', ids.projectId)}/definitions`;
    case 'task-overview': return `/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}`;
    case 'task-graph': return `/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/graph`;
    case 'task-workspace': return `/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/workspace`;
    case 'task-evidence': return `/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/evidence`;
    case 'task-chat': return `/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/chat`;
    case 'global-definitions': return '/definitions';
    case 'system-adapters': return '/system/adapters';
    case 'system-diagnostics': return '/system/diagnostics';
    case 'system-settings': return '/system/settings';
  }
}

function requireId(route: NavRoute, field: 'projectId' | 'taskId', value: string | undefined): string {
  if (!value) {
    throw new Error(`pathFor(${route}): missing required ${field}`);
  }
  return value;
}
