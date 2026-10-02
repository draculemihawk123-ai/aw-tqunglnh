// Package contextsnapshot is V5-04's own durable, immutable
// message/resource/RevisionSet manifest for one ExecutionAttempt
// (docs/design/07-v5-execution-evidence.md V5-04; HE-04-M06, GC-INV-08,
// HE-02-M01, HE-03-M02, HE-05-M04): "mỗi Attempt có immutable
// message/resource/RevisionSet manifest".
//
// This is a NEW, separate type from the pre-existing
// internal/domain/runtime.ContextSnapshot (a spike-era shape that inlines
// message content directly and is wired only to crash-recovery/
// checkpoint code, confirmed unused by the real scheduling/dispatch path)
// — this package does not touch that type, its table, or any of its
// existing call sites at all. The two coexist deliberately: legacy
// recovery keeps using runtime.ContextSnapshot; real scheduling/dispatch
// uses this package's own Snapshot from here on. There is no bridge
// between them, and no ID of one kind is ever loaded through the other
// kind's own repository.
//
// This package deliberately does NOT import internal/domain/runtime:
// ExecutionAttempt needs to reference a Snapshot's own ID (via its
// ContextSnapshotID field), so this package cannot import runtime back
// without creating an import cycle. AttemptID is therefore this package's
// own local string-typed identity, not runtime.ExecutionAttemptID — the
// application layer, which imports both packages freely, is what
// cross-checks a Snapshot's AttemptID actually names the ExecutionAttempt
// a caller believes it does.
package contextsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ID is a platform-minted identity for one Snapshot row.
type ID string

// AttemptID mirrors runtime.ExecutionAttemptID's own string identity —
// see this package's own doc comment for why it is declared fresh here
// rather than imported.
type AttemptID string

// MessageRef is one V5-02 Message this Snapshot pins, by reference only —
// content is never inlined here (HE-11-S06: DB metadata only, content
// stays in ArtifactStore).
type MessageRef struct {
	MessageID string
}

// ResourceRef is one resolved resource (a contextassembler.ResolvedCandidate's
// own identity, or any other resource kind a caller pins) this Snapshot
// includes, by reference only.
//
// OwnerVersionID completes ADR-012's own passive-resource identity triple
// (definition.ResourceIdentity: OwnerVersionID+ResourceKey+ContentHash) —
// V5-08B0 adds it because a real request-assembly caller must be able to
// re-load the EXACT SkillVersion/LayerVersion a resource came from (to
// re-verify its content hash before dispatch); ResourceKey+ContentHash
// alone cannot name which published version to re-load. This field is
// tagged `omitempty` deliberately: a Snapshot written before V5-08B0 was
// marshaled with a ResourceRef shape that never had this key at all, and
// computeManifestHash (below) must keep recomputing the IDENTICAL hash for
// those already-stored rows on every load (tamper-check re-verifies by
// recomputing from scratch every time) — omitempty is what makes an
// old row's JSON re-marshal byte-for-byte identical to what was hashed
// when it was first written. A NEW snapshot's own ResourceRef always has
// OwnerVersionID populated (the request assembler fails closed on any ref
// missing it — see internal/app/agentrequest), so this is never
// ambiguous in practice: an empty OwnerVersionID only ever means "a
// snapshot written before this field existed," never "a new ref that
// forgot to set it."
type ResourceRef struct {
	OwnerVersionID string `json:",omitempty"`
	ResourceKey    string
	ContentHash    string
}

// EvidenceRef is one runtime.Evidence row this Snapshot references, by
// reference only — mirrors MessageRef's own bare-ID shape exactly.
// V5-12's own checker input allowlist contract (docs/design/
// 07-v5-execution-evidence.md V5-12, confirmed with the user 2026-09-10):
// a CHECKER-role AGENT node's own Snapshot carries EvidenceRefs instead of
// a maker transcript — see NewSnapshot's own doc comment on ordering for
// why this field, unlike MessageRefs/ResourceRefs, is sorted rather than
// order-preserved.
type EvidenceRef struct {
	EvidenceID string
}

// Snapshot is the immutable manifest go-core-spec §4.6 names:
// "ContextSnapshot { ID, ProjectID, WorkItemID, AttemptID, MessageRefs[],
// ResourceRefs[], RevisionSet, ManifestHash, CreatedAt }". MessageRefs and
// ResourceRefs preserve the EXACT order they were given in — unlike
// RevisionSet (a set, canonically sorted since two different orderings of
// the same revisions are semantically identical), HE-04-M06 requires this
// manifest to record "thứ tự" (the order) resources were actually
// rendered in, so two Snapshots with the identical refs in a different
// order are semantically DIFFERENT manifests and must hash differently.
type Snapshot struct {
	ID           ID
	ProjectID    project.ProjectID
	WorkItemID   work.WorkItemID
	AttemptID    AttemptID
	MessageRefs  []MessageRef
	ResourceRefs []ResourceRef
	// EvidenceRefs is V5-12's own checker input allowlist addition
	// (2026-09-10) — empty for every COMMAND/MACHINE_GATE Attempt (unchanged
	// behavior), populated for a CHECKER-role AGENT Attempt, whose caller
	// (internal/app/runtime/schedule.go) gathers it instead of MessageRefs,
	// and, since V9-02 (ADR-031 decision 6), for a MAKER AGENT Attempt that a
	// check's failureOutcome edge sent back (the failing check's own
	// evidence, next to the MessageRefs it always had). Unlike
	// MessageRefs/ResourceRefs, order carries no meaning here (this is not a
	// rendered transcript), so NewSnapshot sorts it for canonicalization.
	EvidenceRefs []EvidenceRef
	Revisions    workspace.RevisionSet
	// InstructionSchemaVersion (V9-03, ADR-032 decision 2) names the
	// instruction-artifact schema this Snapshot is rendered with when its
	// Attempt's request is assembled
	// (internal/app/runtime/assemble_execution_request.go). Zero means the
	// Snapshot recorded none — every Snapshot written before V9-03 — and such
	// a Snapshot is rendered with schema v1 EXACTLY as before, byte for byte.
	// A Snapshot created by the scheduler after V9-03 records
	// InstructionSchemaV2. Because the same manifest renders to a different
	// instruction under a different schema, the version is part of the
	// manifest ManifestHash is computed over (canonicalManifest), omitted
	// while zero so a v1 Snapshot's hash is unchanged.
	InstructionSchemaVersion int
	ManifestHash             string
	CreatedAt                time.Time
}

const (
	// InstructionSchemaV1 is the schema every Snapshot without a recorded
	// version is rendered with. It is never stored: a Snapshot that records
	// nothing IS a v1 Snapshot (InstructionSchemaVersion == 0).
	InstructionSchemaV1 = 1
	// InstructionSchemaV2 is the schema of ADR-032: hard constraints first,
	// a task contract with risk level and allowed outcomes, priority-ordered
	// resources, and a closing checklist.
	InstructionSchemaV2 = 2
)

// InstructionSchema returns the instruction-artifact schema version s is
// rendered with: InstructionSchemaV1 while none is recorded.
func (s Snapshot) InstructionSchema() int {
	if s.InstructionSchemaVersion == 0 {
		return InstructionSchemaV1
	}
	return s.InstructionSchemaVersion
}

// Option adjusts a Snapshot NewSnapshot is building. Options exist so that
// the many callers that build a plain (v1) Snapshot stay as they are.
type Option func(*Snapshot)

// WithInstructionSchemaVersion records version as the Snapshot's
// InstructionSchemaVersion. 0 records nothing (a v1 Snapshot) — the value a
// clone of a Snapshot that recorded none passes, and the value a row without
// the column loads as. NewSnapshot rejects any version other than 0 and
// InstructionSchemaV2.
func WithInstructionSchemaVersion(version int) Option {
	return func(s *Snapshot) { s.InstructionSchemaVersion = version }
}

// CloneForAttempt returns a Snapshot with s's message, resource and evidence
// refs, revision set and instruction schema version, but a new identity: id,
// bound to attemptID and created at createdAt. A technical retry
// (decideRetryOrExhaustion) and the recovery paths (recovery_reaper.go) give
// the NEW Attempt its own Snapshot this way, never a shared one (each
// Snapshot row has exactly one owning attempt). Carrying the instruction
// schema version is the point of doing it here rather than at each call site:
// an old v1 Attempt's retry stays v1, a v2 Attempt's retry stays v2, and a
// future field added to Snapshot is copied in this one place.
func (s Snapshot) CloneForAttempt(id ID, attemptID AttemptID, createdAt time.Time) (Snapshot, error) {
	return NewSnapshot(
		id, s.ProjectID, s.WorkItemID, attemptID,
		s.MessageRefs, s.ResourceRefs, s.EvidenceRefs, s.Revisions, createdAt,
		WithInstructionSchemaVersion(s.InstructionSchemaVersion),
	)
}

// NewSnapshot validates fields and computes ManifestHash itself — the same
// "constructor self-computes its own canonical hash" discipline
// workspace.NewRevisionSet and workflow.Compile's own ContentHash already
// use (docs/design/07-v5-execution-evidence.md V5-04 research: "Pattern
// B" — sort-where-order-is-not-meaningful, marshal, sha256 — is the
// established precedent for a runtime fact-record's own hash, as opposed
// to authoring.Canonicalize, which exists specifically for ambiguous
// user-authored YAML/JSON).
func NewSnapshot(
	id ID,
	projectID project.ProjectID,
	workItemID work.WorkItemID,
	attemptID AttemptID,
	messageRefs []MessageRef,
	resourceRefs []ResourceRef,
	evidenceRefs []EvidenceRef,
	revisions workspace.RevisionSet,
	createdAt time.Time,
	options ...Option,
) (Snapshot, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: ID is required")
	}
	if strings.TrimSpace(string(projectID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: ProjectID is required")
	}
	if strings.TrimSpace(string(workItemID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: WorkItemID is required")
	}
	if strings.TrimSpace(string(attemptID)) == "" {
		return Snapshot{}, errors.New("contextsnapshot: AttemptID is required")
	}
	if createdAt.IsZero() {
		return Snapshot{}, errors.New("contextsnapshot: CreatedAt is required")
	}

	messageRefsCopy := append([]MessageRef(nil), messageRefs...)
	for _, ref := range messageRefsCopy {
		if strings.TrimSpace(ref.MessageID) == "" {
			return Snapshot{}, errors.New("contextsnapshot: MessageRef.MessageID must not be blank")
		}
	}
	resourceRefsCopy := append([]ResourceRef(nil), resourceRefs...)
	for _, ref := range resourceRefsCopy {
		if strings.TrimSpace(ref.ResourceKey) == "" || strings.TrimSpace(ref.ContentHash) == "" {
			return Snapshot{}, errors.New("contextsnapshot: ResourceRef.ResourceKey and ContentHash must not be blank")
		}
	}
	evidenceRefsCopy := append([]EvidenceRef(nil), evidenceRefs...)
	for _, ref := range evidenceRefsCopy {
		if strings.TrimSpace(ref.EvidenceID) == "" {
			return Snapshot{}, errors.New("contextsnapshot: EvidenceRef.EvidenceID must not be blank")
		}
	}
	sort.Slice(evidenceRefsCopy, func(i, j int) bool { return evidenceRefsCopy[i].EvidenceID < evidenceRefsCopy[j].EvidenceID })
	revisionsCopy, err := workspace.NewRevisionSet(revisions.Entries())
	if err != nil {
		return Snapshot{}, err
	}

	var recorded Snapshot
	for _, option := range options {
		option(&recorded)
	}
	switch recorded.InstructionSchemaVersion {
	case 0, InstructionSchemaV2:
	default:
		return Snapshot{}, fmt.Errorf("contextsnapshot: unsupported InstructionSchemaVersion %d (want 0 for none, or %d)", recorded.InstructionSchemaVersion, InstructionSchemaV2)
	}

	manifestHash, err := computeManifestHash(messageRefsCopy, resourceRefsCopy, evidenceRefsCopy, revisionsCopy, recorded.InstructionSchemaVersion)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, AttemptID: attemptID,
		MessageRefs: messageRefsCopy, ResourceRefs: resourceRefsCopy, EvidenceRefs: evidenceRefsCopy, Revisions: revisionsCopy,
		InstructionSchemaVersion: recorded.InstructionSchemaVersion,
		ManifestHash:             manifestHash, CreatedAt: createdAt.UTC(),
	}, nil
}

// canonicalManifest is exactly what ManifestHash is computed over — never
// ID/AttemptID/CreatedAt (identity/timestamp, not manifest content), so
// re-verifying an already-stored Snapshot's own hash (tamper detection)
// never depends on anything but its own message/resource/revision
// content. RevisionSet contributes its own already-canonical ContentHash
// rather than raw entries, reusing that type's own established
// canonicalization instead of re-deriving it.
type canonicalManifest struct {
	MessageRefs  []MessageRef  `json:"messageRefs"`
	ResourceRefs []ResourceRef `json:"resourceRefs"`
	// EvidenceRefs is tagged omitempty deliberately — the same reasoning
	// ResourceRef.OwnerVersionID's own doc comment already gives: a
	// Snapshot written before V5-12 was marshaled with no EvidenceRefs key
	// at all, and computeManifestHash must keep recomputing the IDENTICAL
	// hash for those already-stored rows on every load (loadSnapshotTx's
	// own tamper check). A NEW MAKER/COMMAND/MACHINE_GATE Snapshot always
	// has this nil too (unchanged behavior), so omitempty never hides a
	// real CHECKER Snapshot's own non-empty EvidenceRefs from the hash —
	// only the "no Evidence at all" case, which is exactly when omitting
	// the key changes nothing observable.
	EvidenceRefs    []EvidenceRef `json:"evidenceRefs,omitempty"`
	RevisionSetHash string        `json:"revisionSetHash"`
	// InstructionSchemaVersion (V9-03, ADR-032) is tagged omitempty for the
	// same reason: a Snapshot written before it existed has no such key in
	// what was hashed, and stays hashing exactly as before while the version
	// is zero. A v2 Snapshot carries it, so flipping a stored row's version
	// (which would silently change the instruction the same refs render to)
	// is caught by loadSnapshotTx's tamper check instead of going unnoticed.
	// It is the LAST key so a zero value changes no byte of the earlier ones.
	InstructionSchemaVersion int `json:"instructionSchemaVersion,omitempty"`
}

func computeManifestHash(messageRefs []MessageRef, resourceRefs []ResourceRef, evidenceRefs []EvidenceRef, revisions workspace.RevisionSet, instructionSchemaVersion int) (string, error) {
	data, err := json.Marshal(canonicalManifest{
		MessageRefs: messageRefs, ResourceRefs: resourceRefs, EvidenceRefs: evidenceRefs, RevisionSetHash: revisions.ContentHash(),
		InstructionSchemaVersion: instructionSchemaVersion,
	})
	if err != nil {
		return "", fmt.Errorf("contextsnapshot: marshal canonical manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
