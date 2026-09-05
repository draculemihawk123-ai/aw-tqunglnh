package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// NetworkAccess mirrors workflow/command.NetworkAccess's own closed choice
// — duplicated here rather than imported, since this type describes the
// EFFECTIVE, composition-root-resolved network posture an execution
// actually ran under, not a node's own authored declaration (the two can
// legitimately differ: a composition-root policy may be stricter than
// what a COMMAND node declares, never looser).
type NetworkAccess string

const (
	NetworkAccessNone    NetworkAccess = "NONE"
	NetworkAccessAllowed NetworkAccess = "ALLOWED"
)

var validNetworkAccess = map[NetworkAccess]bool{
	NetworkAccessNone: true, NetworkAccessAllowed: true,
}

// RuntimeExecutionConfigSnapshotV1 is the resolved, composition-root
// Configuration (go-core-spec §19: "process timeout, output limit,
// environment/network/secret policy") an execution actually ran under —
// ADR-027's own locked contract. It intentionally excludes provider
// executable/argv/protocol/capability (already covered by
// ResolvedAdapterBuildRef) and AttemptPolicy's own timeout (already
// covered by ResolvedExecutionProfileV1.TimeoutSeconds, resolved from a
// node's own ATTEMPT-category Policy pin, not composition-root config) —
// this snapshot only carries the pieces that come from the OPERATOR's own
// installation configuration, never from a workflow author's declaration.
//
// This type is deliberately a PURE, I/O-free domain value, exactly like
// ResolvedExecutionProfileV1: it never itself reads
// internal/app/config.Config or any other live source. A
// ports.RuntimeExecutionConfigProvider (ADR-027) resolves one and hands it
// to NewRuntimeExecutionConfigSnapshotV1, which is the ONLY place
// RuntimeExecutionConfigHash is ever computed — never accepted as a
// caller-supplied string, since nothing could then verify it was actually
// derived from real config (ADR-027's own "không nhận hash trần từ
// caller").
type RuntimeExecutionConfigSnapshotV1 struct {
	SchemaVersion uint32 `json:"schemaVersion"`
	// ProcessOutputLimitBytes mirrors internal/app/config.Config.ProcessOutputLimit
	// (V1-03) — the resolved, validated ceiling on captured stdout/stderr.
	ProcessOutputLimitBytes int `json:"processOutputLimitBytes"`
	// EnvAllowlist is the composition-root's own effective environment
	// variable allowlist — required to be a closed list even when empty
	// (an empty, non-nil list means "no environment variables pass
	// through", the same "allowlist, not blocklist" discipline
	// command.CommandDocument.EnvAllowlist already documents).
	EnvAllowlist []string `json:"envAllowlist"`
	// NetworkAccess is the composition-root's own effective network
	// posture for this execution.
	NetworkAccess NetworkAccess `json:"networkAccess"`
	// SecretRefs names which secrets this execution may resolve — names
	// only, never values (go-core-spec's own "secret chỉ resolve ở worker
	// ngay trước spawn và không persist").
	SecretRefs []string `json:"secretRefs,omitempty"`
}

// NewRuntimeExecutionConfigSnapshotV1 validates snapshot, normalizes its
// set-like fields (sorted, deduplicated — same canonicalization discipline
// as NewResolvedExecutionProfileV1), and returns both the normalized value
// and its RuntimeExecutionConfigHash ("sha256:" + hex digest of the
// normalized value's own canonical JSON). This is the ONLY function in
// this codebase that may ever produce a RuntimeExecutionConfigHash value —
// see this type's own doc comment for why a caller-supplied hash string is
// never accepted anywhere else.
func NewRuntimeExecutionConfigSnapshotV1(snapshot RuntimeExecutionConfigSnapshotV1) (RuntimeExecutionConfigSnapshotV1, string, error) {
	if snapshot.SchemaVersion != 1 {
		return RuntimeExecutionConfigSnapshotV1{}, "", fmt.Errorf("runtime execution config snapshot schema version must be 1, got %d", snapshot.SchemaVersion)
	}
	if snapshot.ProcessOutputLimitBytes <= 0 {
		return RuntimeExecutionConfigSnapshotV1{}, "", errors.New("runtime execution config snapshot process output limit must be greater than zero")
	}
	if !validNetworkAccess[snapshot.NetworkAccess] {
		return RuntimeExecutionConfigSnapshotV1{}, "", fmt.Errorf("runtime execution config snapshot has unsupported network access %q", snapshot.NetworkAccess)
	}
	for i, name := range snapshot.EnvAllowlist {
		if strings.TrimSpace(name) == "" {
			return RuntimeExecutionConfigSnapshotV1{}, "", fmt.Errorf("runtime execution config snapshot envAllowlist[%d] is blank", i)
		}
	}

	normalized := snapshot
	normalized.EnvAllowlist = sortedUniqueStrings(snapshot.EnvAllowlist)
	if normalized.EnvAllowlist == nil {
		normalized.EnvAllowlist = []string{}
	}
	normalized.SecretRefs = sortedUniqueStrings(snapshot.SecretRefs)

	canonical, err := json.Marshal(normalized)
	if err != nil {
		return RuntimeExecutionConfigSnapshotV1{}, "", fmt.Errorf("canonicalize runtime execution config snapshot: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return normalized, "sha256:" + hex.EncodeToString(digest[:]), nil
}
