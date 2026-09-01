// Package config is Alpha's startup configuration loader
// (docs/design/03-v1-alpha-foundation.md V1-03): config is immutable once
// loaded, resolved through a fixed precedence — safe defaults < file <
// environment < flags — and validated fail-fast before anything else
// starts.
package config

import "time"

// Config is Alpha's fully resolved, validated startup configuration.
type Config struct {
	DatabasePath        string
	ArtifactRoot         string
	WorkerID             string
	WorkerConcurrency    int
	LeaseTTL             time.Duration
	LeaseHeartbeat       time.Duration
	ProcessOutputLimit   int
	ProviderExecutables  map[string]string
}

// Defaults returns the safe, always-valid-shape starting point every Load
// begins from. WorkerID is deliberately left empty: there is no safe
// default identity for a worker process, so leaving it unset is what lets
// Validate catch a missing one instead of silently running under a
// meaningless placeholder.
func Defaults() Config {
	return Config{
		DatabasePath:        "agentkit.db",
		ArtifactRoot:        "artifacts",
		WorkerID:            "",
		WorkerConcurrency:   4,
		LeaseTTL:            30 * time.Second,
		LeaseHeartbeat:      10 * time.Second,
		ProcessOutputLimit:  1 << 20, // 1 MiB
		ProviderExecutables: map[string]string{},
	}
}

// Overrides is a partial Config: a nil field means "this source did not
// set it", so Apply only overwrites fields the source actually provided —
// it never overwrites with a zero value that would just mean "absent".
// ProviderExecutables is the one field merged key-by-key rather than
// wholesale-replaced, so one source can add a provider without having to
// repeat every provider an earlier source already set.
type Overrides struct {
	DatabasePath        *string
	ArtifactRoot        *string
	WorkerID            *string
	WorkerConcurrency   *int
	LeaseTTL            *time.Duration
	LeaseHeartbeat      *time.Duration
	ProcessOutputLimit  *int
	ProviderExecutables map[string]string
}

// Apply layers o onto base, field by field, returning the merged Config.
// base is never mutated.
func (o Overrides) Apply(base Config) Config {
	result := base
	if o.DatabasePath != nil {
		result.DatabasePath = *o.DatabasePath
	}
	if o.ArtifactRoot != nil {
		result.ArtifactRoot = *o.ArtifactRoot
	}
	if o.WorkerID != nil {
		result.WorkerID = *o.WorkerID
	}
	if o.WorkerConcurrency != nil {
		result.WorkerConcurrency = *o.WorkerConcurrency
	}
	if o.LeaseTTL != nil {
		result.LeaseTTL = *o.LeaseTTL
	}
	if o.LeaseHeartbeat != nil {
		result.LeaseHeartbeat = *o.LeaseHeartbeat
	}
	if o.ProcessOutputLimit != nil {
		result.ProcessOutputLimit = *o.ProcessOutputLimit
	}
	if len(o.ProviderExecutables) > 0 {
		merged := make(map[string]string, len(result.ProviderExecutables)+len(o.ProviderExecutables))
		for k, v := range result.ProviderExecutables {
			merged[k] = v
		}
		for k, v := range o.ProviderExecutables {
			merged[k] = v
		}
		result.ProviderExecutables = merged
	}
	return result
}

// Load resolves Config from safe defaults, then layers file, then
// environment, then flags on top in that fixed order — a later source
// always wins over an earlier one for the same field. Load either returns
// a fully validated Config or a *apperror.Error describing exactly what
// to fix; it never returns a partially valid Config alongside an error.
func Load(file, env, flags Overrides) (Config, error) {
	cfg := Defaults()
	cfg = file.Apply(cfg)
	cfg = env.Apply(cfg)
	cfg = flags.Apply(cfg)
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
