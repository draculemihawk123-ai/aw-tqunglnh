package clicompose

import (
	"context"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/catalog"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/decision"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/doctor"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/events"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/health"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/noderun"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/projection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/settings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitemblocker"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
)

// runFn is the uniform shape every route's closure takes; the per-leaf
// adapters below bind it to each leaf's own Run* signature (some take stdin,
// some do not) and to that leaf's own Dependencies struct.
type runFn = func(ctx context.Context, d *Deps, args []string, s IO) error

func route(needs Needs, run runFn, path ...string) Route {
	return Route{Path: path, Needs: needs, Run: run}
}

// Routes returns the complete `aw <resource> <action>` routing table: one
// Route per distinct command path any V6-15C..V6-15N leaf registers a
// cli.Descriptor for. TestRoutesCoverEveryDescriptorBothDirections proves
// the table and cli.All() describe exactly the same set of paths, so a leaf
// that registers a descriptor without a route (or a route without a
// descriptor) fails the build instead of shipping an unreachable or an
// undeclared command.
//
// The table is built fresh on every call (a route holds no state), and is
// the only place a leaf's Run* function is bound to a path.
func Routes() []Route {
	var routes []Route
	routes = append(routes, healthRoutes()...)
	routes = append(routes, doctorRoutes()...)
	routes = append(routes, settingsRoutes()...)
	routes = append(routes, catalogRoutes()...)
	routes = append(routes, definitionRoutes()...)
	routes = append(routes, adapterRoutes()...)
	routes = append(routes, workItemRoutes()...)
	routes = append(routes, runRoutes()...)
	routes = append(routes, decisionRoutes()...)
	routes = append(routes, messageRoutes()...)
	routes = append(routes, evidenceRoutes()...)
	routes = append(routes, workspaceRoutes()...)
	routes = append(routes, releaseSetRoutes()...)
	routes = append(routes, projectionRoutes()...)
	return routes
}

// V6-15C ---------------------------------------------------------------

func healthRoutes() []Route {
	return []Route{
		route(0, func(_ context.Context, _ *Deps, a []string, s IO) error {
			return health.RunLive(a, s.Stdout)
		}, "health", "live"),
		route(NeedUoW|NeedArtifactRoot, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return health.RunReady(ctx, health.Dependencies{UnitOfWork: d.UoW, ArtifactRoot: d.ArtifactRoot}, a, s.Stdout)
		}, "health", "ready"),
	}
}

func doctorRoutes() []Route {
	return []Route{
		route(NeedUoW|NeedStore|NeedIsolation|NeedConfig, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return doctor.RunDoctor(ctx, doctor.Dependencies{
				Config: d.Config, Store: d.Store, UnitOfWork: d.UoW, Isolation: d.Isolation,
			}, a, s.Stdout)
		}, "doctor"),
	}
}

func settingsRoutes() []Route {
	deps := func(d *Deps) settings.Dependencies {
		return settings.Dependencies{UnitOfWork: d.UoW, IDs: d.ids(), Clock: d.clock(), Matcher: d.Matcher}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return settings.RunShow(ctx, deps(d), a, s.Stdout)
		}, "settings", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return settings.RunUpdate(ctx, deps(d), a, s.Stdin, s.Stdout)
		}, "settings", "update"),
	}
}

// V6-15D ---------------------------------------------------------------

func catalogRoutes() []Route {
	deps := func(d *Deps) catalog.Dependencies { return catalog.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now} }
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunProjectList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "project", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunProjectCreate(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "project", "create"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunProjectShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "project", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunRepositoryList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunRepositoryRegister(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "repository", "register"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunRepositoryOnboarding(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository", "onboarding"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunRepositoryRetryProbe(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository", "retry-probe"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunComponentList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "component", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunPackAssignmentList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "pack-assignment", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return catalog.RunPackAssignmentAssign(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "pack-assignment", "assign"),
	}
}

// V6-15E ---------------------------------------------------------------

func definitionRoutes() []Route {
	deps := func(d *Deps) definitions.Dependencies {
		return definitions.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "definition", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionCreate(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "definition", "create"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "definition", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionVersions(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "definition", "versions"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionValidate(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "definition", "validate"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunDefinitionPublish(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "definition", "publish"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunVersionShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "version", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return definitions.RunVersionDiff(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "version", "diff"),
	}
}

// V6-15F ---------------------------------------------------------------

func adapterRoutes() []Route {
	deps := func(d *Deps) adapterbuild.Dependencies {
		return adapterbuild.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return adapterbuild.RunList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "adapter", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return adapterbuild.RunShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "adapter", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return adapterbuild.RunProbe(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "adapter", "probe"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return adapterbuild.RunRegister(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "adapter", "register"),
	}
}

// V6-15G ---------------------------------------------------------------

func workItemRoutes() []Route {
	deps := func(d *Deps) workitem.Dependencies {
		return workitem.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "work-item", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "work-item", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemCreate(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "work-item", "create"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemCreateChild(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "work-item", "create-child"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemReadiness(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "work-item", "readiness"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemMarkReady(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "work-item", "mark-ready"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitem.RunWorkItemCancel(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "work-item", "cancel"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workitemblocker.Resolve(ctx, workitemblocker.Dependencies{UoW: d.UoW, IDs: d.ids()}, a, s.Stdout, s.Stderr)
		}, "blocker", "resolve"),
	}
}

// V6-15H ---------------------------------------------------------------

func runRoutes() []Route {
	deps := func(d *Deps) run.Dependencies {
		return run.Dependencies{
			UOW: d.UoW, IDs: d.ids(), Isolation: d.Isolation, Agents: d.Agents,
			Matcher: d.Matcher, Now: d.Now, Sleep: d.Sleep,
		}
	}
	const runNeeds = NeedUoW | NeedIsolation | NeedAgents
	return []Route{
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Start(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "start"),
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Show(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "show"),
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Cancel(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "cancel"),
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Graph(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "graph"),
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Timeline(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "timeline"),
		route(runNeeds, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return run.Diagnostics(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "run", "diagnostics"),
		route(NeedUoW|NeedIsolation|NeedAgents, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return noderun.RetryBlocked(ctx, noderun.Dependencies{
				UOW: d.UoW, IDs: d.ids(), Isolation: d.Isolation, Agents: d.Agents,
			}, a, s.Stdout, s.Stderr)
		}, "node-run", "retry-blocked"),
	}
}

// V6-15I ---------------------------------------------------------------

func decisionRoutes() []Route {
	scopeDeps := func(d *Deps) scopeexpansion.Dependencies {
		return scopeexpansion.Dependencies{UOW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	decisionDeps := func(d *Deps) decision.Dependencies {
		return decision.Dependencies{UOW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return scopeexpansion.Request(ctx, scopeDeps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "scope-expansion", "request"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return scopeexpansion.Approve(ctx, scopeDeps(d), a, s.Stdout, s.Stderr)
		}, "scope-expansion", "approve"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return scopeexpansion.Reject(ctx, scopeDeps(d), a, s.Stdout, s.Stderr)
		}, "scope-expansion", "reject"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return scopeexpansion.Withdraw(ctx, scopeDeps(d), a, s.Stdout, s.Stderr)
		}, "scope-expansion", "withdraw"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return decision.ResolveApproval(ctx, decisionDeps(d), a, s.Stdout, s.Stderr)
		}, "approval", "resolve"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return decision.SignalWait(ctx, decisionDeps(d), a, s.Stdout, s.Stderr)
		}, "wait", "signal"),
	}
}

// V6-15J ---------------------------------------------------------------

func messageRoutes() []Route {
	deps := func(d *Deps) message.Dependencies {
		return message.Dependencies{
			UnitOfWork: d.UoW, ArtifactStore: d.ArtifactStore, IDs: d.ids(), Clock: d.clock(), Matcher: d.Matcher,
		}
	}
	const needs = NeedUoW | NeedArtifactStore
	return []Route{
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return message.RunMessageList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "message", "list"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return message.RunMessageAppend(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "message", "append"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return message.RunMessageUploadAttachment(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "message", "upload-attachment"),
	}
}

// V6-15K ---------------------------------------------------------------

func evidenceRoutes() []Route {
	deps := func(d *Deps) evidence.Dependencies {
		return evidence.Dependencies{UnitOfWork: d.UoW, ArtifactStore: d.ArtifactStore}
	}
	const needs = NeedUoW | NeedArtifactStore
	return []Route{
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return evidence.RunEvidenceList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "evidence", "list"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return evidence.RunEvidenceVerify(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "evidence", "verify"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return evidence.RunContextSnapshotShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "context-snapshot", "show"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return evidence.RunArtifactGet(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "artifact", "get"),
	}
}

// V6-15L ---------------------------------------------------------------

func workspaceRoutes() []Route {
	deps := func(d *Deps) workspace.Dependencies {
		return workspace.Dependencies{
			UOW: d.UoW, IDs: d.ids(), Authority: workapp.NewEligibilityAuthority(d.UoW),
			Reader: d.WorkspaceReader, Now: d.Now,
		}
	}
	const needs = NeedUoW | NeedWorkspace
	return []Route{
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunWorkspaceSetShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "workspace-set", "show"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunWorkspaceSetRelease(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "workspace-set", "release"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunRepositoryWorkspaceSource(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository-workspace", "source"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunRepositoryWorkspaceDiff(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository-workspace", "diff"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunRepositoryWorkspaceLog(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository-workspace", "log"),
		route(needs, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return workspace.RunRepositoryWorkspaceReconcile(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "repository-workspace", "reconcile"),
	}
}

// V6-15M ---------------------------------------------------------------

func releaseSetRoutes() []Route {
	deps := func(d *Deps) releaseset.Dependencies {
		return releaseset.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now, Sleep: d.Sleep}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunList(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "release-set", "list"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunCreate(ctx, deps(d), a, s.Stdin, s.Stdout, s.Stderr)
		}, "release-set", "create"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunShow(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "release-set", "show"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunSeal(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "release-set", "seal"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunAbandon(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "release-set", "abandon"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return releaseset.RunLocalCommit(ctx, deps(d), a, s.Stdout, s.Stderr)
		}, "release-set", "local-commit"),
	}
}

// V6-15N ---------------------------------------------------------------

func projectionRoutes() []Route {
	projDeps := func(d *Deps) projection.Dependencies {
		return projection.Dependencies{UoW: d.UoW, IDs: d.ids(), Now: d.Now}
	}
	return []Route{
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return projection.RunStatus(ctx, projDeps(d), a, s.Stdout, s.Stderr)
		}, "projection", "status"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return projection.RunRebuild(ctx, projDeps(d), a, s.Stdout, s.Stderr)
		}, "projection", "rebuild"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return projection.RunRebuildStatus(ctx, projDeps(d), a, s.Stdout, s.Stderr)
		}, "projection", "rebuild-status"),
		route(NeedUoW, func(ctx context.Context, d *Deps, a []string, s IO) error {
			return events.RunWatch(ctx, events.Dependencies{UoW: d.UoW, Matcher: d.Matcher}, a, s.Stdout, s.Stderr)
		}, "events", "watch"),
	}
}
