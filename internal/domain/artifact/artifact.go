// Package artifact is the durable, database-backed metadata layer over V1's
// content-addressed ports.ArtifactStore (docs/design/03-v1-alpha-foundation.md
// V1-08; docs/design/07-v5-execution-evidence.md V5-01; AK-ARCH-021;
// GC-INV-20). ports.ArtifactStore only ever proves "these exact bytes exist
// and hash-verify" — it has no concept of which Project owns an artifact,
// how long it should be kept, or whether anything has actually vouched for
// it being real evidence rather than an unclaimed write a crash left
// behind. Artifact is that missing durable row: one platform-minted
// identity per attach, wrapping an opaque ports.ArtifactRef locator with
// the ProjectID/retention/sensitivity/hold/attach-state metadata every
// later evidence consumer (Message attachments V5-02, checkpoint diff
// capture V5-08A, gate evidence V5-10, ...) needs without ever touching the
// filesystem store directly.
package artifact

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ID is a platform-minted identity for one Artifact row — never derived
// from content hash. The same underlying bytes MAY back more than one
// Artifact row (ports.ArtifactStore's own Put already dedupes the bytes
// themselves on disk; a fresh ID here lets two independent attaches of
// byte-identical content — even across two different Projects — remain two
// independent, independently-retained/held platform records, the same
// "every row mints its own idsource identity" convention every other
// aggregate in this codebase already follows, rather than the content-
// addressed-ID exception ADR-022 deliberately carved out for
// AdapterBuildVersion's own operational registry).
type ID string

// RetentionClass is the typed lifecycle bucket ADR-017/V5-01 require every
// Artifact to declare — never inferred from caller intent at sweep time.
type RetentionClass string

const (
	// RetentionRawOutputTemp is raw provider/command output and other
	// working evidence payload: 7-day default TTL from CreatedAt unless a
	// Hold is in force (ADR-017 — "TTL 7 ngày mặc định chỉ áp dụng cho raw
	// output/evidence tạm"; this package's own ComputeExpiresAt resolves
	// the exact default).
	RetentionRawOutputTemp RetentionClass = "RAW_OUTPUT_TEMP"
	// RetentionCanonicalContext is canonical Message/context/resource
	// content and durable audit metadata (ADR-017's own literal name for
	// this class): never gets a blanket TTL — ExpiresAt is always nil —
	// and can only ever be removed later by an explicit, audited operation
	// (ADR-017: "Alpha chưa có hard-delete conversation").
	RetentionCanonicalContext RetentionClass = "CANONICAL_CONTEXT"
)

// Valid reports whether c is one of this package's own known classes.
func (c RetentionClass) Valid() bool {
	switch c {
	case RetentionRawOutputTemp, RetentionCanonicalContext:
		return true
	default:
		return false
	}
}

// rawOutputTempTTL is ADR-017's own "TTL 7 ngày mặc định" for
// RetentionRawOutputTemp.
const rawOutputTempTTL = 7 * 24 * time.Hour

// AttachState is whether an Artifact row is trusted, referenceable
// evidence (Attached) or exists only for investigation/eventual cleanup
// (Orphan) — docs/architecture/04-go-core-spec.md §11.2's own "Artifact MAY
// được lưu với nhãn untrusted/orphan để điều tra".
type AttachState string

const (
	// Orphan is content this package has durably recorded but nothing has
	// yet vouched for: freshly Put-and-verified content whose owning
	// operation has not yet confirmed it (a crash before that confirmation
	// leaves it here — exactly the candidate set a future retention
	// sweeper, V5-14, reconciles), or a worker transaction's rejected
	// output kept only "để điều tra" (for investigation).
	Orphan AttachState = "ORPHAN"
	// Attached is confirmed, referenceable evidence — durable AND
	// hash-verified AND vouched for by whatever transaction attached it.
	Attached AttachState = "ATTACHED"
)

// Valid reports whether s is one of this package's own known states.
func (s AttachState) Valid() bool {
	switch s {
	case Orphan, Attached:
		return true
	default:
		return false
	}
}

// Artifact is the durable metadata row V5-01 adds on top of
// ports.ArtifactRef (docs/architecture/04-go-core-spec.md §4.6's own
// "ArtifactRef { ID, ProjectID, Kind, URI, ContentHash, Size, MediaType,
// Sensitivity, CreatedAt }" sketch, with V5-01's own retention/orphan/hold
// additions folded in). Kind/URI from that sketch are deliberately not
// reproduced as separate fields here: URI is this codebase's existing
// ports.ArtifactRef.Locator (kept opaque, never renamed/reparsed), and Kind
// has no real caller yet (00-roadmap.md §3: "Không chia chỉ để tạo
// file/field nếu phần đó chưa có contract test hoặc behavior quan sát
// được") — a later V5 task that needs to classify artifacts by purpose adds
// it then, against a real caller.
type Artifact struct {
	ID        ID
	ProjectID project.ProjectID
	// Locator is the opaque ports.ArtifactRef.Locator this row wraps —
	// never parsed here, only ever round-tripped back to
	// ports.ArtifactStore.Open/Verify (see ports.ArtifactRef's own "MUST be
	// treated as opaque" doc comment).
	Locator     string
	ContentHash string
	Size        int64
	MediaType   string
	Sensitivity redact.Sensitivity
	Redacted    bool

	RetentionClass RetentionClass
	AttachState    AttachState
	// Hold is a governance override: true unconditionally blocks a future
	// retention sweeper regardless of RetentionClass/ExpiresAt (ADR-017:
	// "Hold hợp lệ chặn sweeper") — it is not itself a RetentionClass.
	Hold bool
	// ExpiresAt is nil for RetentionCanonicalContext (no blanket TTL,
	// ever) and always non-nil for RetentionRawOutputTemp (ADR-017's own
	// 7-day default) — NewArtifact enforces this pairing structurally so a
	// caller cannot accidentally attach a TTL to canonical content or omit
	// one from temp content.
	ExpiresAt *time.Time
	CreatedAt time.Time
	Version   uint64
}

// NewArtifact validates and constructs an Artifact.
func NewArtifact(
	id ID,
	projectID project.ProjectID,
	locator string,
	contentHash string,
	size int64,
	mediaType string,
	sensitivity redact.Sensitivity,
	redacted bool,
	retentionClass RetentionClass,
	attachState AttachState,
	hold bool,
	expiresAt *time.Time,
	createdAt time.Time,
	version uint64,
) (Artifact, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Artifact{}, errors.New("artifact: ID is required")
	}
	if strings.TrimSpace(string(projectID)) == "" {
		return Artifact{}, errors.New("artifact: ProjectID is required")
	}
	if strings.TrimSpace(locator) == "" {
		return Artifact{}, errors.New("artifact: Locator is required")
	}
	if strings.TrimSpace(contentHash) == "" {
		return Artifact{}, errors.New("artifact: ContentHash is required")
	}
	if size < 0 {
		return Artifact{}, errors.New("artifact: Size must not be negative")
	}
	if strings.TrimSpace(mediaType) == "" {
		return Artifact{}, errors.New("artifact: MediaType is required")
	}
	if sensitivity < redact.Public || sensitivity > redact.Secret {
		return Artifact{}, fmt.Errorf("artifact: unknown Sensitivity %d", sensitivity)
	}
	if !retentionClass.Valid() {
		return Artifact{}, fmt.Errorf("artifact: unknown RetentionClass %q", retentionClass)
	}
	if !attachState.Valid() {
		return Artifact{}, fmt.Errorf("artifact: unknown AttachState %q", attachState)
	}
	if createdAt.IsZero() {
		return Artifact{}, errors.New("artifact: CreatedAt is required")
	}
	if version == 0 {
		return Artifact{}, errors.New("artifact: Version must be positive")
	}
	switch retentionClass {
	case RetentionCanonicalContext:
		if expiresAt != nil {
			return Artifact{}, errors.New("artifact: RetentionCanonicalContext must not carry an ExpiresAt")
		}
	case RetentionRawOutputTemp:
		if expiresAt == nil {
			return Artifact{}, errors.New("artifact: RetentionRawOutputTemp requires an ExpiresAt")
		}
		if expiresAt.Before(createdAt) {
			return Artifact{}, errors.New("artifact: ExpiresAt must not be before CreatedAt")
		}
	}

	var expiresAtCopy *time.Time
	if expiresAt != nil {
		utc := expiresAt.UTC()
		expiresAtCopy = &utc
	}
	return Artifact{
		ID: id, ProjectID: projectID, Locator: locator, ContentHash: contentHash, Size: size,
		MediaType: mediaType, Sensitivity: sensitivity, Redacted: redacted,
		RetentionClass: retentionClass, AttachState: attachState, Hold: hold,
		ExpiresAt: expiresAtCopy, CreatedAt: createdAt.UTC(), Version: version,
	}, nil
}

// ComputeExpiresAt resolves RetentionClass's own default expiry policy
// (ADR-017) as of createdAt: RetentionRawOutputTemp always gets
// createdAt+7d; RetentionCanonicalContext always gets nil (no blanket
// TTL). A future retention sweeper (V5-14) additionally MUST still honor
// Hold regardless of what this returns.
func ComputeExpiresAt(class RetentionClass, createdAt time.Time) *time.Time {
	if class != RetentionRawOutputTemp {
		return nil
	}
	expiresAt := createdAt.UTC().Add(rawOutputTempTTL)
	return &expiresAt
}
