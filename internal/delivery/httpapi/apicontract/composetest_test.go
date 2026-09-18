package apicontract

// This file — and every other _test.go in this directory — deliberately
// uses `package apicontract` (an INTERNAL test package), not
// `package apicontract_test`: uxgap_test.go's own
// TestKnownRenamedProposalsAreActuallyRegistered and
// TestEveryOwnerTaskIDIsInTheKnownMergedSet need direct access to the
// unexported knownRenamedProposals/knownOpenOwnerTasks/knownUnimplementedGaps
// maps (contract.go/breaking.go/uxgap.go's own doc comments reference
// these test names directly), and every OTHER test file in this directory
// shares this same buildRealRegistry helper — splitting into a black-box
// `_test` package for some files and an internal one for others would only
// buy a purity distinction this package's own small, cohesive test suite
// does not need, at the cost of duplicating this ~90-line real-
// infrastructure composition helper.

import (
	"context"
	"net/http"
	"path/filepath"
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
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpcompose"
)

// buildRealRegistry composes the exact production route set — every field
// of httpcompose.Dependencies built from REAL infrastructure (a real
// temporary SQLite database, real temporary artifact/workspace root
// directories, a real zero-executor agentregistry.Registry), the same way
// cmd/aw/serve_test.go already builds them for ITS OWN end-to-end tests,
// and the same way cmd/aw/serve.go itself builds them at real startup. No
// field here is a mock or a stub value standing in for something a real
// process would have — the whole point of this helper (and of extracting
// httpcompose.ComposeRoutes out of cmd/aw/serve.go in the first place) is
// that every test in this package exercises the IDENTICAL composition
// logic production uses, so "the generated contract equals what actually
// ships" is true by construction, not by a second, hand-maintained
// assertion that could quietly drift.
//
// LiveHandler/ReadyHandler/BootstrapHandler are simple non-nil
// http.HandlerFunc placeholders (httpapi.RouteRegistry.Register only
// requires Handler to be non-nil — see internal/delivery/httpcompose's own
// Dependencies.LiveHandler doc comment for why a caller that only needs
// the route LIST, never a live connection, is allowed to do this): this
// package's own tests never issue a live HTTP round trip against
// health/live, health/ready or bootstrap, they only inspect the
// registered RouteDescriptor list and (in routeinventory_test.go) the
// real net/http.ServeMux's own pattern-matching behavior built from that
// list.
func buildRealRegistry(t *testing.T) *httpapi.RouteRegistry {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "apicontract.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	artifactRoot := t.TempDir()
	artifactStoreImpl, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	workspaceProvider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	workspaceInspectionQueries := appworkspaceinspection.New(uow, workspaceProvider)

	matcher := redact.NewMatcher("apicontract-test-known-secret")
	cursorCodec := httpapi.NewCursorCodec([]byte("apicontract-test-cursor-secret-value"))

	// Zero registered executors — the documented-safe default (see
	// cmd/aw/serve.go's own --claude-executable/--codex-executable doc
	// comment): this package's tests never need a live provider probe.
	agentRegistry, err := agentregistry.New(ctx)
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	isolationChecker := process.NewIsolationChecker()

	appConfig := config.Defaults()
	appConfig.DatabasePath = filepath.Join(t.TempDir(), "apicontract.db")
	appConfig.ArtifactRoot = artifactRoot
	appConfig.WorkerID = "apicontract-test"
	appConfig.ProviderExecutables = map[string]string{}

	safeSettingsAtBoot, _ := safesettingsapp.GetSafeSettings(ctx, uow)
	safeSettingsEffective := safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, safeSettingsAtBoot.Desired,
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{},
	)

	routes := httpapi.NewRouteRegistry()
	httpcompose.ComposeRoutes(routes, httpcompose.Dependencies{
		UnitOfWork:                 uow,
		ArtifactStore:              artifactStoreImpl,
		WorkspaceInspectionQueries: workspaceInspectionQueries,
		Matcher:                    matcher,
		Cursor:                     cursorCodec,
		Isolation:                  isolationChecker,
		Agents:                     agentRegistry,
		AppConfig:                  appConfig,
		Store:                      sqlite.NewQueryStore(store),
		SafeSettingsEffective:      safeSettingsEffective,
		Shutdown:                   ctx,
		LiveHandler:                placeholderHandler,
		ReadyHandler:               placeholderHandler,
		BootstrapHandler:           placeholderHandler,
	})
	return routes
}

func placeholderHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
