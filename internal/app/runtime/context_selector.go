// V9-04 — "Selector theo component, path và block có hiệu lực lúc chạy"
// (docs/design/12-v9-harness-alignment.md V9-04; gap G4 in
// docs/harness-engineering/15-doi-chieu-v9.md; HE-04-M02, HE-03-M04).
//
// A Skill or Layer resource may declare componentTags, pathTags and blockKinds
// next to taskKinds and riskClasses, and the publish-time validators accept
// all five. Until V9-04 the scheduler resolved a context route against a
// contextassembler.ResolutionContext that carried only TaskKind and RiskClass,
// so under AND-across-declared-dimensions (contextassembler.Selector
// .MatchesContext) every resource declaring one of the other three was
// excluded as NOT_APPLICABLE for every task, and the only way to give each
// code area its own knowledge was a separate agent profile per area.
//
// This file projects what the scheduler already knows when it schedules an
// AGENT node onto the rest of the context. The projection lives here, not in
// internal/domain/contextassembler, because that package must not import the
// work or project packages (internal/archtest); the only piece of it that is
// pure path logic, the overlap rule, is the domain function
// contextassembler.PathsOverlap so the Component rule and the pathTags rule
// can never disagree.
//
// The rules (the orchestrator's decisions for V9-04, restated in the operator
// documentation, docs/operator/04-authoring-workflows.md):
//
//  1. ComponentTags are the NAMES of the project's Components whose repository
//     appears in the WorkItem's effective scope (READ and WRITE entries both
//     count — knowledge about code the agent may read is as relevant as
//     knowledge about code it may write) and whose Path overlaps a path scope
//     of that repository's scope entry. A scope entry with no path scopes
//     covers the whole repository and so overlaps every Component of it.
//     Name, not ID: a Component's ID is random (the repository probe mints
//     one per discovered directory) while the name is what an author can write
//     into a selector. Kind is not added: it names a classification, not a
//     piece of the code base. Sorted, deduplicated.
//  2. PathTags are the normalized path scopes of every effective scope entry,
//     sorted and deduplicated; WholeRepositoryScope is true when at least one
//     entry has no path scopes. The selector side of the match is path overlap
//     rather than string equality (contextassembler.MatchesContext).
//  3. BlockKind is the AGENT node's effective role, MAKER or CHECKER
//     (workflow.AgentNodeConfig.EffectiveRole). Only AGENT nodes resolve a
//     context route (resolveExecutionProfile returns a zero ContextPolicyRef
//     for COMMAND and MACHINE_GATE), so the node TYPE would be the same value
//     for every route that exists; the role is the distinction an author can
//     use ("the checker also gets the review checklist, the maker does not").
//     A blockKinds entry other than MAKER or CHECKER is not rejected at
//     publish time — it is a valid selector that simply never matches — so
//     definitions published before V9-04 keep publishing and keep selecting
//     exactly what they did.
//
// Timing. The effective scope is read when the NodeRun is scheduled (it is the
// same list pinned on the NodeRun, loaded fresh inside the scheduling
// transaction), and the resulting ContextSnapshot pins the selected resources.
// A NodeRun scheduled after a scope expansion therefore sees the expanded
// scope, which is intended: the agent that runs after the expansion works in
// the larger area and should get that area's knowledge. A snapshot that has
// already been created is never re-resolved: a retry or a recovery attempt
// clones it with its pinned ResourceRefs.
package runtime

import (
	"context"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextassembler"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// buildContextResolutionContext is the ResolutionContext ScheduleExecutableNodeRun
// resolves node's context route against: the WorkItem's own TaskKind and
// RiskClass (unchanged since V5-08B0) plus the V9-04 projection of
// effectiveScope and of node itself — see this file's doc comment for the
// rules.
//
// Components are read only when the scope names at least one repository, so a
// WorkItem with no effective scope costs no extra read.
func buildContextResolutionContext(
	ctx context.Context, tx ports.Tx, projectID project.ProjectID, workItem workdomain.WorkItem,
	node workflow.Node, effectiveScope []workdomain.RepositoryScope,
) (contextassembler.ResolutionContext, error) {
	resolutionCtx := contextassembler.ResolutionContext{
		TaskKind:  string(workItem.Kind),
		RiskClass: string(workItem.RiskLevel),
		BlockKind: contextBlockKind(node),
	}
	if len(effectiveScope) == 0 {
		return resolutionCtx, nil
	}
	components, err := tx.Catalog().ListComponents(ctx, string(projectID))
	if err != nil {
		return contextassembler.ResolutionContext{}, err
	}
	resolutionCtx.ComponentTags, resolutionCtx.PathTags, resolutionCtx.WholeRepositoryScope =
		projectEffectiveScope(effectiveScope, components)
	return resolutionCtx, nil
}

// contextBlockKind is ResolutionContext.BlockKind for node: the effective
// role of an AGENT node (MAKER or CHECKER), empty for any other node, which
// never resolves a context route.
func contextBlockKind(node workflow.Node) string {
	if node.Type == workflow.NodeAgent && node.Agent != nil {
		return string(node.Agent.EffectiveRole())
	}
	return ""
}

// projectEffectiveScope derives the ComponentTags, PathTags and
// WholeRepositoryScope of a ResolutionContext from a WorkItem's effective
// scope and the project's Components. Pure: no I/O, the same inputs always
// give the same (sorted, deduplicated, never nil) result.
func projectEffectiveScope(scopes []workdomain.RepositoryScope, components []project.Component) (componentTags, pathTags []string, wholeRepository bool) {
	type repositoryCoverage struct {
		whole bool
		paths []string
	}
	coverage := make(map[project.RepositoryID]*repositoryCoverage, len(scopes))
	pathSet := make(map[string]struct{})
	for _, scope := range scopes {
		entry := coverage[scope.RepositoryID()]
		if entry == nil {
			entry = &repositoryCoverage{}
			coverage[scope.RepositoryID()] = entry
		}
		paths := scope.PathScopes()
		if len(paths) == 0 {
			entry.whole = true
			wholeRepository = true
			continue
		}
		for _, p := range paths {
			entry.paths = append(entry.paths, p)
			pathSet[p] = struct{}{}
		}
	}

	tagSet := make(map[string]struct{})
	for _, component := range components {
		entry := coverage[component.RepositoryID]
		if entry == nil {
			continue
		}
		touched := entry.whole
		for _, p := range entry.paths {
			if touched {
				break
			}
			touched = contextassembler.PathsOverlap(component.Path, p)
		}
		if touched {
			tagSet[component.Name] = struct{}{}
		}
	}
	return sortedKeys(tagSet), sortedKeys(pathSet), wholeRepository
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// contextResolutionInput is the audit record of the ResolutionContext a
// CONTEXT_RESOLUTION_V1 decision was resolved against (V9-04): the artifact's
// input, so "why was resource X not loaded for this attempt" is answerable
// from the artifact alone — the result lists the NOT_APPLICABLE exclusions and
// the input says what they were not applicable TO. Field order is the JSON key
// order; the arrays are never null.
type contextResolutionInput struct {
	ComponentTags        []string `json:"componentTags"`
	PathTags             []string `json:"pathTags"`
	WholeRepositoryScope bool     `json:"wholeRepositoryScope"`
	BlockKind            string   `json:"blockKind"`
	TaskKind             string   `json:"taskKind"`
	RiskClass            string   `json:"riskClass"`
}

// newContextResolutionInput copies resolutionCtx into its audit shape, sorting
// and deduplicating the two tag sets (they are sets: matching never depended
// on their order) and turning a nil set into an empty one.
func newContextResolutionInput(resolutionCtx contextassembler.ResolutionContext) contextResolutionInput {
	return contextResolutionInput{
		ComponentTags:        sortedUnique(resolutionCtx.ComponentTags),
		PathTags:             sortedUnique(resolutionCtx.PathTags),
		WholeRepositoryScope: resolutionCtx.WholeRepositoryScope,
		BlockKind:            resolutionCtx.BlockKind,
		TaskKind:             resolutionCtx.TaskKind,
		RiskClass:            resolutionCtx.RiskClass,
	}
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedKeys(set)
}
