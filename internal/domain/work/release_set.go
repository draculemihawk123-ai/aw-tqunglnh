// ReleaseSet (V5-10A, docs/design/07-v5-execution-evidence.md; AK-ARCH-015C,
// GC-DS-04) is a TaskFamily's own sealed, provenance-bearing record of
// exactly what a completion decision considered: one entry per repository
// in the family's own scope, naming the base revision it started from, the
// result revision it ended at, and that repository's own verdict — never a
// remote-authority record (this package's own doc comment on
// NewReleaseSet explains why "sealed" never implies a real Git push/PR/
// merge happened; V5-10A's own locked scope is "không push/PR/merge/
// force-push").
//
// Like WorkItemBlocker (blocker.go), this is a per-family runtime aggregate,
// not a DefinitionKind schema — it belongs here, not in a Skill/Command/Gate
// -shaped package, and reuses gate.Verdict directly (no cycle: neither gate
// nor command imports this package) rather than declaring a second,
// identical enum.
package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ReleaseSetID identifies one durable ReleaseSet row.
type ReleaseSetID string

// ReleaseSetState is a ReleaseSet's own three-state lifecycle — the
// "create-seal-abandon" commands V5-10A's own Thực hiện line names.
// CREATED is the only state either terminal state (SEALED, ABANDONED) may
// ever transition from; once terminal, a ReleaseSet is immutable —
// mirrors WorkItemBlocker's own OPEN->{RESOLVED,WAIVED} discipline
// exactly (blocker.go's own BlockerState).
type ReleaseSetState string

const (
	ReleaseSetCreated   ReleaseSetState = "CREATED"
	ReleaseSetSealed    ReleaseSetState = "SEALED"
	ReleaseSetAbandoned ReleaseSetState = "ABANDONED"
)

// RepositoryRelease is one repository's own exact contribution to a
// ReleaseSet — "exact base/result/verdict từng repository" (V5-10A's own
// Thực hiện line). BaseVCSObjectID/ResultVCSObjectID are plain VCS object
// ID strings (the same shape workspace.Revision.VCSObjectID already uses
// throughout this codebase) rather than a full workspace.Revision, since a
// ReleaseSet's own WorkspaceGeneration is not itself part of what a
// completion decision's own provenance needs to distinguish (unlike a
// live mount, which must pin the exact generation it resolved against).
type RepositoryRelease struct {
	RepositoryID      project.RepositoryID `json:"repositoryId"`
	BaseVCSObjectID   string               `json:"baseVcsObjectId"`
	ResultVCSObjectID string               `json:"resultVcsObjectId"`
	Verdict           gate.Verdict         `json:"verdict"`
}

// ReleaseSet is one TaskFamily's own sealed-or-abandoned release record.
// Entries is kept unexported and always accessed through Entries()/
// ReleaseFor() — mirroring workspace.RevisionSet's own "sorted, validated,
// immutable once constructed" discipline exactly, including the identical
// sha256 ContentHash convention (canonical JSON of the sorted entries).
type ReleaseSet struct {
	ID        ReleaseSetID
	ProjectID project.ProjectID
	FamilyID  TaskFamilyID
	State     ReleaseSetState
	entries   []RepositoryRelease
	hash      string
	CreatedAt time.Time
	SealedAt  *time.Time
	// AbandonedAt is populated only once State == ABANDONED.
	AbandonedAt *time.Time
	Version     uint64
}

// NewReleaseSet validates and builds a new, CREATED ReleaseSet — never
// SEALED or ABANDONED directly; SealReleaseSet/AbandonReleaseSet
// (internal/app/work, this task's own application layer) are the only
// commands with authority to transition it further.
func NewReleaseSet(
	id ReleaseSetID, projectID project.ProjectID, familyID TaskFamilyID, repositories []RepositoryRelease, createdAt time.Time,
) (ReleaseSet, error) {
	if id == "" || projectID == "" || familyID == "" {
		return ReleaseSet{}, errors.New("release set identities are required")
	}
	if createdAt.IsZero() {
		return ReleaseSet{}, errors.New("release set created timestamp is required")
	}
	if len(repositories) == 0 {
		return ReleaseSet{}, errors.New("release set must name at least one repository")
	}

	normalized := append([]RepositoryRelease(nil), repositories...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].RepositoryID < normalized[j].RepositoryID })
	for index := range normalized {
		normalized[index].BaseVCSObjectID = strings.TrimSpace(normalized[index].BaseVCSObjectID)
		normalized[index].ResultVCSObjectID = strings.TrimSpace(normalized[index].ResultVCSObjectID)
		if normalized[index].RepositoryID == "" || normalized[index].BaseVCSObjectID == "" || normalized[index].ResultVCSObjectID == "" {
			return ReleaseSet{}, fmt.Errorf("release for repository %q is missing a required identity/revision field", normalized[index].RepositoryID)
		}
		if !normalized[index].Verdict.IsValid() {
			return ReleaseSet{}, fmt.Errorf("release for repository %q has invalid verdict %q", normalized[index].RepositoryID, normalized[index].Verdict)
		}
		if index > 0 && normalized[index-1].RepositoryID == normalized[index].RepositoryID {
			return ReleaseSet{}, fmt.Errorf("duplicate release for repository %q", normalized[index].RepositoryID)
		}
	}

	canonical, err := json.Marshal(normalized)
	if err != nil {
		return ReleaseSet{}, fmt.Errorf("marshal release set entries: %w", err)
	}
	digest := sha256.Sum256(canonical)

	return ReleaseSet{
		ID: id, ProjectID: projectID, FamilyID: familyID, State: ReleaseSetCreated,
		entries: normalized, hash: "sha256:" + hex.EncodeToString(digest[:]),
		CreatedAt: createdAt.UTC(), Version: 1,
	}, nil
}

// Entries returns a defensive copy of this ReleaseSet's own sorted
// per-repository releases.
func (s ReleaseSet) Entries() []RepositoryRelease {
	return append([]RepositoryRelease(nil), s.entries...)
}

// ContentHash is this ReleaseSet's own canonical sha256 digest — the
// identical "same input always produces the same hash" guarantee
// workspace.RevisionSet.ContentHash() already provides.
func (s ReleaseSet) ContentHash() string {
	return s.hash
}

// ReleaseFor returns repositoryID's own RepositoryRelease, if this
// ReleaseSet names one.
func (s ReleaseSet) ReleaseFor(repositoryID project.RepositoryID) (RepositoryRelease, bool) {
	index := sort.Search(len(s.entries), func(i int) bool { return s.entries[i].RepositoryID >= repositoryID })
	if index >= len(s.entries) || s.entries[index].RepositoryID != repositoryID {
		return RepositoryRelease{}, false
	}
	return s.entries[index], true
}
