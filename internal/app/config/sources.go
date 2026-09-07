package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// rawFileConfig mirrors Overrides field-for-field but keeps durations as
// plain Go-syntax strings ("30s") in the file format, since
// time.Duration's default JSON encoding is an opaque nanosecond integer —
// not what an operator hand-editing a config file should have to write.
type rawFileConfig struct {
	DatabasePath        *string           `json:"database_path"`
	ArtifactRoot        *string           `json:"artifact_root"`
	WorkerID            *string           `json:"worker_id"`
	WorkerConcurrency   *int              `json:"worker_concurrency"`
	LeaseTTL            *string           `json:"lease_ttl"`
	LeaseHeartbeat      *string           `json:"lease_heartbeat"`
	ProcessOutputLimit  *int              `json:"process_output_limit"`
	ProviderExecutables map[string]string `json:"provider_executables"`
	EnvAllowlist        []string          `json:"env_allowlist"`
}

// FromFile parses a JSON config file into Overrides. A missing file is
// not an error — the file layer is optional, and Load simply has nothing
// to apply from it.
func FromFile(path string) (Overrides, error) {
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Overrides{}, nil
	}
	if err != nil {
		return Overrides{}, fmt.Errorf("read config file %s: %w", path, err)
	}
	var raw rawFileConfig
	if err := json.Unmarshal(content, &raw); err != nil {
		return Overrides{}, fmt.Errorf("parse config file %s: %w", path, err)
	}
	overrides := Overrides{
		DatabasePath:        raw.DatabasePath,
		ArtifactRoot:        raw.ArtifactRoot,
		WorkerID:            raw.WorkerID,
		WorkerConcurrency:   raw.WorkerConcurrency,
		ProcessOutputLimit:  raw.ProcessOutputLimit,
		ProviderExecutables: raw.ProviderExecutables,
		EnvAllowlist:        raw.EnvAllowlist,
	}
	if raw.LeaseTTL != nil {
		d, err := time.ParseDuration(*raw.LeaseTTL)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse config file %s: lease_ttl: %w", path, err)
		}
		overrides.LeaseTTL = &d
	}
	if raw.LeaseHeartbeat != nil {
		d, err := time.ParseDuration(*raw.LeaseHeartbeat)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse config file %s: lease_heartbeat: %w", path, err)
		}
		overrides.LeaseHeartbeat = &d
	}
	return overrides, nil
}

const (
	envDatabasePath       = "AGENTKIT_DATABASE_PATH"
	envArtifactRoot       = "AGENTKIT_ARTIFACT_ROOT"
	envWorkerID           = "AGENTKIT_WORKER_ID"
	envWorkerConcurrency  = "AGENTKIT_WORKER_CONCURRENCY"
	envLeaseTTL           = "AGENTKIT_LEASE_TTL"
	envLeaseHeartbeat     = "AGENTKIT_LEASE_HEARTBEAT"
	envProcessOutputLimit = "AGENTKIT_PROCESS_OUTPUT_LIMIT"
)

// FromEnv reads Overrides from environment variables via lookup (pass
// os.LookupEnv in production; a test passes a fake with the same
// signature). ProviderExecutables and EnvAllowlist have no environment
// form — both are list/map-shaped, which does not fit a flat env-var
// scheme; set them via file.
func FromEnv(lookup func(string) (string, bool)) (Overrides, error) {
	var overrides Overrides
	if v, ok := lookup(envDatabasePath); ok {
		overrides.DatabasePath = &v
	}
	if v, ok := lookup(envArtifactRoot); ok {
		overrides.ArtifactRoot = &v
	}
	if v, ok := lookup(envWorkerID); ok {
		overrides.WorkerID = &v
	}
	if v, ok := lookup(envWorkerConcurrency); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse %s: %w", envWorkerConcurrency, err)
		}
		overrides.WorkerConcurrency = &n
	}
	if v, ok := lookup(envLeaseTTL); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse %s: %w", envLeaseTTL, err)
		}
		overrides.LeaseTTL = &d
	}
	if v, ok := lookup(envLeaseHeartbeat); ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse %s: %w", envLeaseHeartbeat, err)
		}
		overrides.LeaseHeartbeat = &d
	}
	if v, ok := lookup(envProcessOutputLimit); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Overrides{}, fmt.Errorf("parse %s: %w", envProcessOutputLimit, err)
		}
		overrides.ProcessOutputLimit = &n
	}
	return overrides, nil
}

// FromFlags parses Overrides from command-line flags. Only a flag the
// caller actually passed populates its Overrides field (via
// FlagSet.Visit, not VisitAll) — an unset flag must never override an
// earlier layer with its zero-value default.
func FromFlags(arguments []string) (Overrides, error) {
	flags := flag.NewFlagSet("config", flag.ContinueOnError)

	databasePath := flags.String("database-path", "", "SQLite database file path")
	artifactRoot := flags.String("artifact-root", "", "filesystem artifact store root")
	workerID := flags.String("worker-id", "", "stable identity for this worker process")
	workerConcurrency := flags.Int("worker-concurrency", 0, "bounded worker pool size")
	leaseTTL := flags.Duration("lease-ttl", 0, "lease time-to-live")
	leaseHeartbeat := flags.Duration("lease-heartbeat", 0, "lease heartbeat interval")
	processOutputLimit := flags.Int("process-output-limit", 0, "bounded process stdout/stderr sink size in bytes")

	if err := flags.Parse(arguments); err != nil {
		return Overrides{}, err
	}

	var overrides Overrides
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "database-path":
			v := *databasePath
			overrides.DatabasePath = &v
		case "artifact-root":
			v := *artifactRoot
			overrides.ArtifactRoot = &v
		case "worker-id":
			v := *workerID
			overrides.WorkerID = &v
		case "worker-concurrency":
			v := *workerConcurrency
			overrides.WorkerConcurrency = &v
		case "lease-ttl":
			v := *leaseTTL
			overrides.LeaseTTL = &v
		case "lease-heartbeat":
			v := *leaseHeartbeat
			overrides.LeaseHeartbeat = &v
		case "process-output-limit":
			v := *processOutputLimit
			overrides.ProcessOutputLimit = &v
		}
	})

	return overrides, nil
}
