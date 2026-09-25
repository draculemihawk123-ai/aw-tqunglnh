import { matchRoute, type Parser } from 'wouter';
import type { NavRoute } from './components/shell/LeftNav';

/**
 * The one place every NavRoute maps to and from a real, bookmarkable URL —
 * V7-04's own "route guards by selected project only" requirement needs a
 * REAL url (so "which project" survives a refresh) instead of the
 * in-memory `route`/`project` useState pair V7-03 and earlier left behind.
 *
 * Every path here lives under UI_PREFIX ("/ui") — a real bug, not a design
 * up front: earlier versions of this table used bare paths like "/doctor"
 * and "/projects", which collide with real REST API paths this same
 * `aw serve` process ALSO serves at the identical GET verb+path (found via
 * V7-05A's own real-browser verification against a real backend — a
 * browser tab that never leaves the SPA never round-trips these, but a
 * direct deep-link or a refresh does, and the server has no way to tell
 * "the browser wants the HTML shell" from "the SPA's own fetch wants the
 * JSON resource" for an identical request). No REST resource in this
 * codebase has ever used "ui" as a top-level path segment (every one is a
 * domain noun — projects/definitions/repositories/runs/work-items/
 * adapter-builds/settings/health), and `httpcompose/compose.go`'s own
 * `GET /ui/{path...}` route (OperationID `uiShell`) is the server-side
 * counterpart that serves the SPA shell for any path under this prefix.
 *
 * A project- or task-scoped NavRoute always needs a `projectId` (and task-
 * scoped ones a `taskId`) baked into the URL; App.tsx's own route guard
 * redirects to `/ui/projects` whenever the URL's projectId does not
 * resolve to a real project — see App.tsx's own doc comment on that
 * effect.
 */

const UI_PREFIX = '/ui';

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
  { pattern: `${UI_PREFIX}/doctor`, route: 'doctor' },
  { pattern: `${UI_PREFIX}/projects`, route: 'projects' },
  { pattern: `${UI_PREFIX}/projects/:projectId/tasks/:taskId/graph`, route: 'task-graph' },
  { pattern: `${UI_PREFIX}/projects/:projectId/tasks/:taskId/workspace`, route: 'task-workspace' },
  { pattern: `${UI_PREFIX}/projects/:projectId/tasks/:taskId/evidence`, route: 'task-evidence' },
  { pattern: `${UI_PREFIX}/projects/:projectId/tasks/:taskId/chat`, route: 'task-chat' },
  { pattern: `${UI_PREFIX}/projects/:projectId/tasks/:taskId`, route: 'task-overview' },
  { pattern: `${UI_PREFIX}/projects/:projectId/board`, route: 'project-board' },
  { pattern: `${UI_PREFIX}/projects/:projectId/components`, route: 'project-components' },
  { pattern: `${UI_PREFIX}/projects/:projectId/definitions`, route: 'project-definitions' },
  { pattern: `${UI_PREFIX}/projects/:projectId`, route: 'project-overview' },
  { pattern: `${UI_PREFIX}/definitions`, route: 'global-definitions' },
  { pattern: `${UI_PREFIX}/system/adapters`, route: 'system-adapters' },
  { pattern: `${UI_PREFIX}/system/diagnostics`, route: 'system-diagnostics' },
  { pattern: `${UI_PREFIX}/system/settings`, route: 'system-settings' },
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
    case 'doctor': return `${UI_PREFIX}/doctor`;
    case 'projects': return `${UI_PREFIX}/projects`;
    case 'project-overview': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}`;
    case 'project-board': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/board`;
    case 'project-components': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/components`;
    case 'project-definitions': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/definitions`;
    case 'task-overview': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}`;
    case 'task-graph': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/graph`;
    case 'task-workspace': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/workspace`;
    case 'task-evidence': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/evidence`;
    case 'task-chat': return `${UI_PREFIX}/projects/${requireId(route, 'projectId', ids.projectId)}/tasks/${requireId(route, 'taskId', ids.taskId)}/chat`;
    case 'global-definitions': return `${UI_PREFIX}/definitions`;
    case 'system-adapters': return `${UI_PREFIX}/system/adapters`;
    case 'system-diagnostics': return `${UI_PREFIX}/system/diagnostics`;
    case 'system-settings': return `${UI_PREFIX}/system/settings`;
  }
}

function requireId(route: NavRoute, field: 'projectId' | 'taskId', value: string | undefined): string {
  if (!value) {
    throw new Error(`pathFor(${route}): missing required ${field}`);
  }
  return value;
}
