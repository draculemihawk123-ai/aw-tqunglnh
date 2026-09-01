package config

import (
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// Dump renders cfg as a map safe to log or display: every value is routed
// through matcher (V1-02A's shared redactor) before being copied into the
// result — the exact same contract every other future sink is required to
// use; a config field gets no special exemption.
func Dump(cfg Config, matcher redact.Matcher) (map[string]any, error) {
	raw := map[string]any{
		"database_path":        cfg.DatabasePath,
		"artifact_root":        cfg.ArtifactRoot,
		"worker_id":            cfg.WorkerID,
		"worker_concurrency":   cfg.WorkerConcurrency,
		"lease_ttl":            cfg.LeaseTTL.String(),
		"lease_heartbeat":      cfg.LeaseHeartbeat.String(),
		"process_output_limit": cfg.ProcessOutputLimit,
		"provider_executables": cfg.ProviderExecutables,
	}
	redacted, err := matcher.Value(raw)
	if err != nil {
		return nil, err
	}
	result, ok := redacted.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("redact config dump: unexpected type %T", redacted)
	}
	return result, nil
}
