package doctor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// CheckLiveness always reports HEALTHY: the mere fact this function ran
// and returned is itself the liveness signal — there is nothing further
// to check for "is the process alive and able to respond."
func CheckLiveness() CheckResult {
	return CheckResult{
		Name: "process_liveness", Category: CategoryLiveness, Status: StatusHealthy,
		Detail: "process is running and able to respond",
	}
}

// CheckAppConfig runs cfg through config.Validate. On failure it reuses
// Validate's own WHAT/WHY/FIX problem text as the remediation directly —
// that text was already written to tell an operator exactly what to fix.
func CheckAppConfig(cfg config.Config) CheckResult {
	err := config.Validate(cfg)
	if err == nil {
		return CheckResult{
			Name: "app_config", Category: CategoryReadiness, Status: StatusHealthy,
			Detail: "startup configuration is valid",
		}
	}
	remediation := err.Error()
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		if problems, ok := appErr.Details["problems"]; ok {
			remediation = problems
		}
	}
	return CheckResult{
		Name: "app_config", Category: CategoryReadiness, Status: StatusBlocked,
		Detail: "startup configuration failed validation", Remediation: remediation,
	}
}

// CheckDatabase pings an already-opened ports.QueryStore. Doctor takes
// the store as a dependency rather than opening one itself — opening (and
// on first run, migrating) the real database is a startup concern for
// whatever adapter-aware code constructs the store in the first place
// (e.g. sqlite.Open); internal/app/doctor, like every other internal/app
// package, must never import internal/adapters/... directly
// (internal/archtest.TestDomainAppNeverImportAdapters enforces this for
// the whole module). By the time a caller has a QueryStore to hand
// Doctor, migrations already succeeded or that caller would have failed
// to start at all — this check's job is only to confirm the connection
// is still alive right now, which is what "readiness" actually asks.
func CheckDatabase(ctx context.Context, store ports.QueryStore) CheckResult {
	if err := store.Ping(ctx); err != nil {
		return CheckResult{
			Name: "database", Category: CategoryReadiness, Status: StatusBlocked,
			Detail:      "database is not reachable",
			Remediation: "check that the database file is accessible and not locked by another process",
		}
	}
	return CheckResult{
		Name: "database", Category: CategoryReadiness, Status: StatusHealthy,
		Detail: "database is reachable",
	}
}

// CheckRoot verifies path exists, is a directory, and is writable — the
// writability probe is the one mutation this check performs, a small
// temp-owned fixture file it creates and immediately removes itself,
// never anything a caller has to clean up.
func CheckRoot(name, path string) CheckResult {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return CheckResult{
			Name: name, Category: CategoryReadiness, Status: StatusDegraded,
			Detail:      fmt.Sprintf("%s does not exist yet", path),
			Remediation: fmt.Sprintf("create %s, or let it be created automatically on first write", path),
		}
	}
	if err != nil {
		return CheckResult{
			Name: name, Category: CategoryReadiness, Status: StatusBlocked,
			Detail: fmt.Sprintf("cannot inspect %s", path), Remediation: fmt.Sprintf("check permissions on %s", path),
		}
	}
	if !info.IsDir() {
		return CheckResult{
			Name: name, Category: CategoryReadiness, Status: StatusBlocked,
			Detail:      fmt.Sprintf("%s exists but is not a directory", path),
			Remediation: "remove the file at that path or point the config at a real directory",
		}
	}

	probe := filepath.Join(path, ".agentkit-doctor-probe")
	writeErr := os.WriteFile(probe, []byte("doctor probe"), 0o600)
	if writeErr == nil {
		_ = os.Remove(probe)
	}
	if writeErr != nil {
		return CheckResult{
			Name: name, Category: CategoryReadiness, Status: StatusBlocked,
			Detail: fmt.Sprintf("%s is not writable", path), Remediation: fmt.Sprintf("check write permissions on %s", path),
		}
	}
	return CheckResult{
		Name: name, Category: CategoryReadiness, Status: StatusHealthy,
		Detail: fmt.Sprintf("%s exists and is writable", path),
	}
}

// CheckGit confirms a git executable is reachable on PATH — the harness
// needs it for every RepositoryWorkspace operation.
func CheckGit(ctx context.Context) CheckResult {
	output, err := exec.CommandContext(ctx, "git", "--version").Output()
	if err != nil {
		return CheckResult{
			Name: "git", Category: CategoryReadiness, Status: StatusBlocked,
			Detail: "git executable not found or failed to run", Remediation: "install git and ensure it is on PATH",
		}
	}
	return CheckResult{
		Name: "git", Category: CategoryReadiness, Status: StatusHealthy,
		Detail: strings.TrimSpace(string(output)),
	}
}

// CheckWorkerConfig runs cfg through workerpool.Config.Validate.
func CheckWorkerConfig(cfg workerpool.Config) CheckResult {
	if _, err := cfg.Validate(); err != nil {
		return CheckResult{
			Name: "worker_config", Category: CategoryReadiness, Status: StatusBlocked,
			Detail: "lease/reaper worker configuration is invalid", Remediation: err.Error(),
		}
	}
	return CheckResult{
		Name: "worker_config", Category: CategoryReadiness, Status: StatusHealthy,
		Detail: "lease/reaper worker configuration is valid",
	}
}

// CheckProviderExecutable reports what was OBSERVED about a configured
// provider executable — never whether it is admitted into a registry.
// ADR-022 requires this distinction explicitly: the AdapterBuildVersion
// registry does not exist until V2-07A, so at V1 this is the only claim
// Doctor is allowed to make about a provider executable.
func CheckProviderExecutable(name, path string) CheckResult {
	checkName := "provider:" + name
	if strings.TrimSpace(path) == "" {
		return CheckResult{
			Name: checkName, Category: CategoryCapability, Status: StatusDegraded,
			Detail:      "no executable path configured",
			Remediation: fmt.Sprintf("set provider_executables[%s] if this provider is needed", name),
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return CheckResult{
			Name: checkName, Category: CategoryCapability, Status: StatusDegraded,
			Detail:      "configured executable was not found",
			Remediation: fmt.Sprintf("verify the configured executable path for %s is correct and the file exists", name),
		}
	}
	digest, hashErr := sha256File(path)
	if hashErr != nil {
		return CheckResult{
			Name: checkName, Category: CategoryCapability, Status: StatusDegraded,
			Detail:      "configured executable exists but could not be read for fingerprinting",
			Remediation: fmt.Sprintf("check read permissions on the executable configured for %s", name),
		}
	}
	return CheckResult{
		Name: checkName, Category: CategoryCapability, Status: StatusHealthy,
		Detail: fmt.Sprintf(
			"observed executable fingerprint sha256:%s, size=%d bytes (observed only — not a registered AdapterBuildVersion; that registry does not exist until V2-07A)",
			digest, info.Size(),
		),
	}
}

// CheckSafeSettings reads the persisted safe-settings desired document
// (V6-10G, docs/design/08-v6-api-projections.md) through uow and reports
// BLOCKED with a typed Detail/Remediation — never a panic, never a silent
// fallback to the zero/default document — when it fails to decode
// (ports.ErrSafeSettingsCorrupt), this task's own "corrupt persisted
// settings fail ... Doctor typed" Verify line. This only ever reads: no
// check in this package ever mutates safe settings or any other aggregate.
func CheckSafeSettings(ctx context.Context, uow ports.UnitOfWork) CheckResult {
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, err := tx.SafeSettings().Get(ctx)
		return err
	})
	if err != nil {
		if errors.Is(err, ports.ErrSafeSettingsCorrupt) {
			return CheckResult{
				Name: "safe_settings", Category: CategoryReadiness, Status: StatusBlocked,
				Detail:      "persisted safe settings desired document failed to decode",
				Remediation: "the safe_settings row is corrupt (a bit-rotted or hand-edited value); restore it from backup or reset it via a fresh UpdateSafeSettings call",
			}
		}
		return CheckResult{
			Name: "safe_settings", Category: CategoryReadiness, Status: StatusBlocked,
			Detail: "safe settings could not be read", Remediation: "check that the database is reachable and migrated",
		}
	}
	return CheckResult{
		Name: "safe_settings", Category: CategoryReadiness, Status: StatusHealthy,
		Detail: "persisted safe settings desired document decodes cleanly",
	}
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
