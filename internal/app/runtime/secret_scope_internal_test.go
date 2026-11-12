package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
)

type scopedRecorder struct{ asked [][]string }

func (s *scopedRecorder) Resolve(context.Context, string) (string, error) { return "from-env", nil }
func (s *scopedRecorder) ResolveScoped(_ context.Context, repos []string, name string) (string, error) {
	s.asked = append(s.asked, repos)
	return "from-store:" + name, nil
}

// V12-29 (ADR-061): a COMMAND's secret is looked up in its cwd repository's store first, then the other repositories in scope.
func TestResolveArgvAndSecrets_AsksTheRepositoryStoreInOrder(t *testing.T) {
	mounts := map[string]ports.AgentWorkspaceMount{"web": {RepositoryID: "web"}, "api": {RepositoryID: "api"}, "docs": {RepositoryID: "docs"}}
	doc := command.CommandDocument{SecretRefs: []string{"DB_PASSWORD"}}
	rec := &scopedRecorder{}
	_, env, err := resolveArgvAndSecrets(context.Background(), doc, mounts, rec, "web")
	if err != nil || env["DB_PASSWORD"] != "from-store:DB_PASSWORD" {
		t.Fatalf("env = %v, %v", env, err)
	}
	if !reflect.DeepEqual(rec.asked[0], []string{"web", "api", "docs"}) {
		t.Fatalf("order = %v, want the cwd repository first, then by id", rec.asked[0])
	}
	if got := secretRepositoryOrder(mounts, ""); !reflect.DeepEqual(got, []string{"api", "docs", "web"}) {
		t.Fatalf("gate order = %v", got)
	}
}
