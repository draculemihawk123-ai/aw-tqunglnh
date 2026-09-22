package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/clicompose"
)

// oneShotFactory is V6-15O's own composition-root wiring for every `aw
// <resource> <action>` command: it builds the clicompose.Deps a one-shot
// invocation needs from the real adapters (SQLite, the artifact store, the
// Git worktree provider, the process isolation checker, the live provider
// registry) — the only place a CLI leaf ever gets a concrete adapter, since
// internal/delivery/cli/... may import none of them (internal/archtest).
//
// It builds only what the resolved route's Needs asks for, so a command
// that needs no database never opens one, and a missing required option is a
// cli.UsageError raised before anything touches disk. The same --db,
// --artifact-root, --workspace-root, --claude-executable and
// --codex-executable options `aw serve` takes select the installation; the
// principal stays each leaf's own --principal-config (ADR-028).
func oneShotFactory(ctx context.Context, opts clicompose.Options, needs clicompose.Needs) (clicompose.Deps, func(), error) {
	deps := clicompose.Deps{
		// A one-shot process mints no per-process session token, so the
		// known-secrets redactor is the empty one — the same constructor
		// serve.go feeds its session token to (see clicompose.Deps.Matcher).
		Matcher: redact.NewMatcher(),
	}
	var cleanups []func()
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	fail := func(err error) (clicompose.Deps, func(), error) {
		cleanup()
		return clicompose.Deps{}, nil, err
	}

	if needs&(clicompose.NeedUoW|clicompose.NeedStore) != 0 {
		if strings.TrimSpace(opts.DB) == "" {
			return fail(cli.UsageError{Err: errors.New("--db is required (or set AW_DB)")})
		}
		store, uow, err := openDefinitionDB(ctx, opts.DB)
		if err != nil {
			return fail(err)
		}
		cleanups = append(cleanups, func() { store.Close() })
		deps.UoW = uow
		if needs&clicompose.NeedStore != 0 {
			deps.Store = sqlite.NewQueryStore(store)
		}
	}

	if needs&clicompose.NeedArtifactRoot != 0 {
		deps.ArtifactRoot = opts.ArtifactRoot
	}
	if needs&clicompose.NeedArtifactStore != 0 {
		if strings.TrimSpace(opts.ArtifactRoot) == "" {
			return fail(cli.UsageError{Err: errors.New("--artifact-root is required (or set AW_ARTIFACT_ROOT)")})
		}
		if info, statErr := os.Stat(opts.ArtifactRoot); statErr != nil || !info.IsDir() {
			return fail(cli.UsageError{Err: fmt.Errorf("--artifact-root %q is not an existing directory", opts.ArtifactRoot)})
		}
		artifacts, err := artifactstore.New(opts.ArtifactRoot)
		if err != nil {
			return fail(fmt.Errorf("open artifact store: %w", err))
		}
		deps.ArtifactStore = artifacts
	}

	if needs&clicompose.NeedWorkspace != 0 {
		if strings.TrimSpace(opts.WorkspaceRoot) == "" {
			return fail(cli.UsageError{Err: errors.New("--workspace-root is required (or set AW_WORKSPACE_ROOT)")})
		}
		provider, err := gitworktree.New(gitworktree.Config{Root: opts.WorkspaceRoot})
		if err != nil {
			return fail(fmt.Errorf("open git workspace provider: %w", err))
		}
		deps.WorkspaceReader = provider
	}

	if needs&clicompose.NeedIsolation != 0 {
		deps.Isolation = process.NewIsolationChecker()
	}

	if needs&clicompose.NeedAgents != 0 {
		// Zero executors (the default) is the documented-safe empty
		// Registry, exactly like `aw serve`: a provider is registered only
		// when the operator names its executable, because agentregistry.New
		// spawns it once to measure its capabilities.
		var executors []ports.AgentExecutor
		for _, provider := range []struct{ key, path string }{
			{string(ports.ProviderClaude), opts.ClaudeExecutable},
			{string(ports.ProviderCodex), opts.CodexExecutable},
		} {
			if strings.TrimSpace(provider.path) == "" {
				continue
			}
			executor, err := newAgentExecutor(provider.key, provider.path)
			if err != nil {
				return fail(fmt.Errorf("construct %s agent executor: %w", provider.key, err))
			}
			executors = append(executors, executor)
		}
		registry, err := agentregistry.New(ctx, executors...)
		if err != nil {
			return fail(fmt.Errorf("build agent executor registry: %w", err))
		}
		deps.Agents = registry
	}

	if needs&clicompose.NeedConfig != 0 {
		// The same fully-valid config.Config `aw serve` hands GET /doctor,
		// built from this invocation's own options — Doctor diagnoses a
		// missing artifact root, it must not fail before it can.
		appConfig := config.Defaults()
		appConfig.DatabasePath = opts.DB
		appConfig.ArtifactRoot = opts.ArtifactRoot
		appConfig.WorkerID = "aw-cli"
		appConfig.ProviderExecutables = map[string]string{}
		if path := strings.TrimSpace(opts.ClaudeExecutable); path != "" {
			appConfig.ProviderExecutables["claude"] = path
		}
		if path := strings.TrimSpace(opts.CodexExecutable); path != "" {
			appConfig.ProviderExecutables["codex"] = path
		}
		deps.Config = appConfig
	}

	return deps, cleanup, nil
}
