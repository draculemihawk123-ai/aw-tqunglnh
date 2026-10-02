package runtime

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/contextassembler"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-04: the pure part of the scope-to-context projection — which Components a
// scope touches, which path tags and whole-repository flag it yields — and the
// audit shape of the context. The scheduler-level behavior (a real store, a
// real route, the persisted decision) is proved in
// schedule_contextselector_sqlite_test.go.

func scopeEntry(t *testing.T, repositoryID string, access workdomain.RepositoryAccess, paths ...string) workdomain.RepositoryScope {
	t.Helper()
	scope, err := workdomain.NewRepositoryScope("family-1", 1, project.RepositoryID(repositoryID), access, paths, "test", "actor-1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRepositoryScope(%s %v): %v", repositoryID, paths, err)
	}
	return scope
}

func component(repositoryID, name, path string) project.Component {
	return project.Component{
		ID: project.ComponentID("c-" + repositoryID + "-" + name), ProjectID: "project-1",
		RepositoryID: project.RepositoryID(repositoryID), Name: name, Path: path, Kind: "DIRECTORY", Version: 1,
	}
}

func TestProjectEffectiveScope(t *testing.T) {
	probe := []project.Component{
		component("repo-1", "backend", "backend"),
		component("repo-1", "frontend", "frontend"),
		component("repo-1", "docs", "docs"),
		// Same directory name in another repository, and a nested Component.
		component("repo-2", "payments", "backend"),
		component("repo-1", "backend-api", "backend/api"),
		// Longer name sharing a string prefix with "services/api".
		component("repo-1", "api-extras", "services/apix"),
	}
	cases := []struct {
		name          string
		scopes        []workdomain.RepositoryScope
		components    []project.Component
		wantComponent []string
		wantPaths     []string
		wantWhole     bool
	}{
		{
			name:          "no scope, no tags",
			wantComponent: []string{}, wantPaths: []string{},
		},
		{
			name:          "path scope names a directory: the Component and the nested one it contains",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend")},
			components:    probe,
			wantComponent: []string{"backend", "backend-api"}, wantPaths: []string{"backend"},
		},
		{
			name:          "path scope under a Component: the Component that contains it",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend/src/db")},
			components:    probe,
			wantComponent: []string{"backend"}, wantPaths: []string{"backend/src/db"},
		},
		{
			name:          "path scope inside a nested Component touches that Component and its parent",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend/api/handlers")},
			components:    probe,
			wantComponent: []string{"backend", "backend-api"}, wantPaths: []string{"backend/api/handlers"},
		},
		{
			name:          "a shared string prefix is not an overlap",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "services/api")},
			components:    probe,
			wantComponent: []string{}, wantPaths: []string{"services/api"},
		},
		{
			name:          "no path scopes: every Component of that repository only",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-1", workdomain.RepositoryWrite)},
			components:    probe,
			wantComponent: []string{"api-extras", "backend", "backend-api", "docs", "frontend"}, wantPaths: []string{}, wantWhole: true,
		},
		{
			name: "READ and WRITE both count; paths are sorted and deduplicated",
			scopes: []workdomain.RepositoryScope{
				scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "frontend", "backend"),
				scopeEntry(t, "repo-1", workdomain.RepositoryRead, "backend"),
			},
			components:    probe,
			wantComponent: []string{"backend", "backend-api", "frontend"}, wantPaths: []string{"backend", "frontend"},
		},
		{
			name: "a whole-repository entry beside a narrow one: the flag is set, the narrow path is still recorded",
			scopes: []workdomain.RepositoryScope{
				scopeEntry(t, "repo-1", workdomain.RepositoryRead),
				scopeEntry(t, "repo-2", workdomain.RepositoryWrite, "backend"),
			},
			components:    probe,
			wantComponent: []string{"api-extras", "backend", "backend-api", "docs", "frontend", "payments"}, wantPaths: []string{"backend"}, wantWhole: true,
		},
		{
			name:          "only the Components of repositories in the scope",
			scopes:        []workdomain.RepositoryScope{scopeEntry(t, "repo-2", workdomain.RepositoryRead, "backend")},
			components:    probe,
			wantComponent: []string{"payments"}, wantPaths: []string{"backend"},
		},
		{
			name: "Components sharing a name are one tag",
			scopes: []workdomain.RepositoryScope{
				scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend"),
				scopeEntry(t, "repo-2", workdomain.RepositoryWrite, "backend"),
			},
			components: []project.Component{
				component("repo-1", "backend", "backend"),
				component("repo-2", "backend", "backend"),
			},
			wantComponent: []string{"backend"}, wantPaths: []string{"backend"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			componentTags, pathTags, whole := projectEffectiveScope(tc.scopes, tc.components)
			if componentTags == nil || pathTags == nil {
				t.Fatalf("tags must never be nil: components %#v, paths %#v", componentTags, pathTags)
			}
			if !reflect.DeepEqual(componentTags, tc.wantComponent) || !reflect.DeepEqual(pathTags, tc.wantPaths) || whole != tc.wantWhole {
				t.Fatalf("projectEffectiveScope = components %v, paths %v, whole %v; want components %v, paths %v, whole %v",
					componentTags, pathTags, whole, tc.wantComponent, tc.wantPaths, tc.wantWhole)
			}
		})
	}
}

// The projection and the selector must agree: a context built by the
// projection from a scope selects, through MatchesContext, the resources a
// reader of the scope would expect.
func TestProjectEffectiveScope_FeedsMatchesContext(t *testing.T) {
	components := []project.Component{component("repo-1", "backend", "backend"), component("repo-1", "frontend", "frontend")}
	for _, tc := range []struct {
		name       string
		scope      workdomain.RepositoryScope
		selector   contextassembler.Selector
		wantSelect bool
	}{
		{"scope backend, pathTags backend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend"), contextassembler.Selector{PathTags: []string{"backend"}}, true},
		{"scope backend, pathTags frontend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend"), contextassembler.Selector{PathTags: []string{"frontend"}}, false},
		{"scope backend/src, pathTags backend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend/src"), contextassembler.Selector{PathTags: []string{"backend"}}, true},
		{"whole repository, pathTags frontend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite), contextassembler.Selector{PathTags: []string{"frontend"}}, true},
		{"scope backend, componentTags backend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend"), contextassembler.Selector{ComponentTags: []string{"backend"}}, true},
		{"scope backend, componentTags frontend", scopeEntry(t, "repo-1", workdomain.RepositoryWrite, "backend"), contextassembler.Selector{ComponentTags: []string{"frontend"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			componentTags, pathTags, whole := projectEffectiveScope([]workdomain.RepositoryScope{tc.scope}, components)
			ctx := contextassembler.ResolutionContext{ComponentTags: componentTags, PathTags: pathTags, WholeRepositoryScope: whole}
			if got := tc.selector.MatchesContext(ctx); got != tc.wantSelect {
				t.Fatalf("MatchesContext = %v, want %v (context %+v)", got, tc.wantSelect, ctx)
			}
		})
	}
}

func TestContextBlockKind(t *testing.T) {
	agent := func(role workflow.AgentRole) workflow.Node {
		return workflow.Node{Key: "n", Type: workflow.NodeAgent, Agent: &workflow.AgentNodeConfig{Role: role}}
	}
	for _, tc := range []struct {
		name string
		node workflow.Node
		want string
	}{
		{"explicit maker", agent(workflow.AgentRoleMaker), "MAKER"},
		{"explicit checker", agent(workflow.AgentRoleChecker), "CHECKER"},
		{"unset role defaults to maker, like every other reader of the role", agent(""), "MAKER"},
		{"agent node without config", workflow.Node{Key: "n", Type: workflow.NodeAgent}, ""},
		{"command node never resolves a route", workflow.Node{Key: "n", Type: workflow.NodeCommand}, ""},
		{"machine gate never resolves a route", workflow.Node{Key: "n", Type: workflow.NodeMachineGate}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextBlockKind(tc.node); got != tc.want {
				t.Fatalf("contextBlockKind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewContextResolutionInput_KeyOrderSortedAndNeverNull(t *testing.T) {
	encode := func(c contextassembler.ResolutionContext) string {
		out, err := json.Marshal(newContextResolutionInput(c))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(out)
	}

	if got, want := encode(contextassembler.ResolutionContext{}),
		`{"componentTags":[],"pathTags":[],"wholeRepositoryScope":false,"blockKind":"","taskKind":"","riskClass":""}`; got != want {
		t.Fatalf("empty context =\n  %s\nwant\n  %s", got, want)
	}
	// Sets are written sorted and deduplicated whatever order they arrive in.
	got := encode(contextassembler.ResolutionContext{
		ComponentTags: []string{"web", "api", "web"}, PathTags: []string{"b", "a"}, WholeRepositoryScope: true,
		BlockKind: "CHECKER", TaskKind: "ROOT", RiskClass: "HIGH",
	})
	want := `{"componentTags":["api","web"],"pathTags":["a","b"],"wholeRepositoryScope":true,"blockKind":"CHECKER","taskKind":"ROOT","riskClass":"HIGH"}`
	if got != want {
		t.Fatalf("populated context =\n  %s\nwant\n  %s", got, want)
	}
}
