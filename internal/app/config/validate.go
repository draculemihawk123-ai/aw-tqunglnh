package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
)

// Validate checks every field's own constraint plus cross-field
// combinations (e.g. LeaseHeartbeat must be shorter than LeaseTTL, or a
// lease would already look expired the instant it's granted), collecting
// every problem found rather than stopping at the first — an operator
// fixing config from scratch needs the whole picture at once, not one
// error per restart. The returned *apperror.Error's Details always
// include a fresh correlationId, since a startup failure happens before
// any other correlation source exists yet.
func Validate(cfg Config) error {
	var problems []string

	if strings.TrimSpace(cfg.DatabasePath) == "" {
		problems = append(problems, "database_path: WHAT is empty; WHY every durable write needs a SQLite file location; FIX set database_path (file, env AGENTKIT_DATABASE_PATH, or --database-path)")
	}
	if strings.TrimSpace(cfg.ArtifactRoot) == "" {
		problems = append(problems, "artifact_root: WHAT is empty; WHY evidence/artifacts need a filesystem root to write under; FIX set artifact_root (file, env AGENTKIT_ARTIFACT_ROOT, or --artifact-root)")
	}
	if strings.TrimSpace(cfg.WorkerID) == "" {
		problems = append(problems, "worker_id: WHAT is empty; WHY lease/recovery identity has no safe default; FIX set worker_id (file, env AGENTKIT_WORKER_ID, or --worker-id)")
	}
	if cfg.WorkerConcurrency <= 0 {
		problems = append(problems, fmt.Sprintf("worker_concurrency: WHAT is %d; WHY a worker pool needs at least one slot; FIX set worker_concurrency to a positive integer", cfg.WorkerConcurrency))
	}
	if cfg.LeaseTTL <= 0 {
		problems = append(problems, fmt.Sprintf("lease_ttl: WHAT is %s; WHY a non-positive lease never grants real exclusivity; FIX set lease_ttl to a positive duration", cfg.LeaseTTL))
	}
	if cfg.LeaseHeartbeat <= 0 {
		problems = append(problems, fmt.Sprintf("lease_heartbeat: WHAT is %s; WHY a non-positive heartbeat can never renew a lease; FIX set lease_heartbeat to a positive duration", cfg.LeaseHeartbeat))
	}
	if cfg.LeaseTTL > 0 && cfg.LeaseHeartbeat > 0 && cfg.LeaseHeartbeat >= cfg.LeaseTTL {
		problems = append(problems, fmt.Sprintf("lease_heartbeat/lease_ttl: WHAT heartbeat %s is not shorter than TTL %s; WHY a lease would already look expired the instant it is granted; FIX set lease_heartbeat to less than lease_ttl (e.g. TTL/3)", cfg.LeaseHeartbeat, cfg.LeaseTTL))
	}
	if cfg.ProcessOutputLimit <= 0 {
		problems = append(problems, fmt.Sprintf("process_output_limit: WHAT is %d; WHY an unbounded or zero sink cannot bound a spawned process's output; FIX set process_output_limit to a positive byte count", cfg.ProcessOutputLimit))
	}

	providerNames := make([]string, 0, len(cfg.ProviderExecutables))
	for name := range cfg.ProviderExecutables {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		if strings.TrimSpace(cfg.ProviderExecutables[name]) == "" {
			problems = append(problems, fmt.Sprintf("provider_executables[%s]: WHAT executable path is empty; WHY a configured provider with no executable can never be dispatched; FIX set a real path or remove the %s entry", name, name))
		}
	}

	for i, name := range cfg.EnvAllowlist {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, fmt.Sprintf("env_allowlist[%d]: WHAT entry is blank; WHY a blank name cannot match any real environment variable; FIX remove the blank entry", i))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return apperror.New(apperror.CodeInvalidArgument, fmt.Sprintf("%d config problem(s) found", len(problems)), false).
		WithDetails(map[string]string{
			"correlationId": idsource.Random{}.NewID(),
			"problems":      strings.Join(problems, " | "),
		})
}
