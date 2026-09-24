package parity

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	appworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/clicompose"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpcompose"
)

// uxDocPath locates docs/design/11-v6-00-ux-artifact.md from this package's
// own directory (internal/delivery/parity) — three levels up is the
// repository root.
const uxDocPath = "../../../docs/design/11-v6-00-ux-artifact.md"

func readUXDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(uxDocPath))
	if err != nil {
		t.Fatalf("read %s: %v", uxDocPath, err)
	}
	return string(data)
}

// realRoutes composes the exact production HTTP route set against real
// temporary infrastructure — the same construction
// internal/delivery/httpapi/apicontract's own tests use (their helper is
// unexported test code, so this is the second copy that V6-12's doc comment
// anticipates). Every field is a real adapter built the way cmd/aw/serve.go
// builds it; nothing is a mock.
func realRoutes(t *testing.T) *httpapi.RouteRegistry {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	artifactRoot := t.TempDir()
	artifacts, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	agents, err := agentregistry.New(ctx)
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	appConfig := config.Defaults()
	appConfig.DatabasePath = filepath.Join(t.TempDir(), "parity.db")
	appConfig.ArtifactRoot = artifactRoot
	appConfig.WorkerID = "parity-test"
	appConfig.ProviderExecutables = map[string]string{}

	atBoot, _ := safesettingsapp.GetSafeSettings(ctx, uow)
	effective := safesettingsapp.ResolveEffective(safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, atBoot.Desired,
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{})

	placeholder := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	routes := httpapi.NewRouteRegistry()
	httpcompose.ComposeRoutes(routes, httpcompose.Dependencies{
		UnitOfWork: uow, ArtifactStore: artifacts,
		WorkspaceInspectionQueries: appworkspaceinspection.New(uow, provider),
		Matcher:                    redact.NewMatcher("parity-test-secret"),
		Cursor:                     httpapi.NewCursorCodec([]byte("parity-test-cursor-secret-value")),
		Isolation:                  process.NewIsolationChecker(), Agents: agents, AppConfig: appConfig,
		Store: sqlite.NewQueryStore(store), SafeSettingsEffective: effective, Shutdown: ctx,
		LiveHandler: placeholder, ReadyHandler: placeholder, BootstrapHandler: placeholder, StaticAssetHandler: placeholder,
	})
	return routes
}

// realInputs assembles the four real parties: the parsed UX document, the
// contract of the real composed HTTP routes, the public operation registry
// and every CLI descriptor the composed leaves registered, plus the paths the
// composed router can dispatch.
var (
	baseOnce   sync.Once
	baseInputs Inputs
)

// realInputs returns a private copy of the assembled real inputs. The
// expensive part (composing the real HTTP routes against real temporary
// infrastructure) runs once per test binary: the resulting Contract is plain
// data, so the infrastructure may be torn down with the first caller's t.
func realInputs(t *testing.T) Inputs {
	t.Helper()
	baseOnce.Do(func() { baseInputs = assembleRealInputs(t) })
	if baseInputs.HTTP.Operations == nil {
		t.Fatal("the real inputs failed to assemble in an earlier test")
	}
	return clone(baseInputs)
}

func assembleRealInputs(t *testing.T) Inputs {
	t.Helper()
	rows := apicontract.ParseUXDoc(readUXDoc(t))
	if len(rows) == 0 {
		t.Fatal("ParseUXDoc returned no rows — the parser or the path is broken")
	}
	var routes []string
	for _, r := range clicompose.Routes() {
		routes = append(routes, r.Name())
	}
	descriptors := cli.All()
	if len(descriptors) == 0 {
		t.Fatal("cli.All() is empty — clicompose must import every leaf")
	}
	return Inputs{
		UX:       rows,
		HTTP:     apicontract.Build(realRoutes(t).Descriptors()),
		Registry: PublicOperations(),
		CLI:      descriptors,
		Routes:   routes,
	}
}

// clone copies the parts of in a fixture mutates, so planting a violation in
// one test can never leak into another.
func clone(in Inputs) Inputs {
	out := in
	out.UX = append([]apicontract.UXRow(nil), in.UX...)
	out.HTTP = apicontract.Contract{Version: in.HTTP.Version, Operations: append([]apicontract.Operation(nil), in.HTTP.Operations...)}
	out.CLI = append([]cli.Descriptor(nil), in.CLI...)
	out.Registry = make([]PublicOperation, len(in.Registry))
	for i, op := range in.Registry {
		op.HTTP = append([]HTTPBinding(nil), op.HTTP...)
		out.Registry[i] = op
	}
	out.Routes = append([]string(nil), in.Routes...)
	return out
}
