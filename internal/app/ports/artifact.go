package ports

import (
	"context"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// ArtifactMetadata is what a caller supplies to Put — everything the
// stored bytes can't tell the store on their own.
type ArtifactMetadata struct {
	ContentType string
	Sensitivity redact.Sensitivity
	// Redacted records whether the caller already ran body through
	// V1-02A's redactor before calling Put. ArtifactStore never redacts
	// content itself — that stays the caller's responsibility (HE-11-S06:
	// "DB chỉ giữ metadata, preview, hash và access rules") — this flag
	// only records whether that step happened, for callers deciding
	// whether an artifact is safe to preview/display.
	Redacted bool
}

// ArtifactRef is the opaque handle Put returns and Open/Verify accept.
// Locator MUST be treated as opaque by every caller — never parsed,
// guessed, or hand-constructed; only ever a value Put itself returned.
// SHA256/Size/ContentType/Sensitivity are informational fields Put fills
// in for convenience so a caller doesn't need a second round trip just to
// learn what it stored — a different ArtifactStore implementation is
// free to use a different Locator scheme internally as long as it keeps
// filling in the same informational fields.
type ArtifactRef struct {
	Locator     string
	SHA256      string
	Size        int64
	ContentType string
	Sensitivity redact.Sensitivity
	Redacted    bool
}

// ArtifactStore is a content-addressed, immutable artifact store
// (docs/design/03-v1-alpha-foundation.md V1-08, HE-11-S06, AK-ARCH-021):
// Put streams body exactly once and never overwrites existing content;
// Open returns content only after verifying it against its own recorded
// hash; Verify re-checks stored content against that hash without a
// caller needing to stream/discard the body just to prove it is intact.
type ArtifactStore interface {
	Put(ctx context.Context, meta ArtifactMetadata, body io.Reader) (ArtifactRef, error)
	Open(ctx context.Context, ref ArtifactRef) (io.ReadCloser, error)
	Verify(ctx context.Context, ref ArtifactRef) error
	// Delete removes ref's own underlying content (V5-14, the system's
	// first real destructive filesystem operation) — never a database
	// row, never metadata, only the content-addressed bytes a prior Put
	// wrote. It is the CALLER's own responsibility to have already
	// confirmed, atomically and against real persisted state, that no
	// other Artifact row anywhere still needs this exact Locator's content
	// (docs/architecture/04-go-core-spec.md §19: "Sweeper phải check
	// reference/hold atomically trước xóa") — this method has no
	// visibility into that decision and never second-guesses it. It MUST
	// be idempotent (a no-op, not an error, if the content is already
	// gone — the same crash-safe "already resolved" discipline every
	// other real-I/O operation in this codebase follows) and MUST refuse
	// (return an error, delete nothing) if the content actually stored at
	// ref.Locator no longer hashes/sizes to ref.SHA256/ref.Size — a
	// mismatch here means the caller's own bookkeeping about what this
	// Locator holds has drifted from reality, and deleting blindly would
	// risk destroying content a DIFFERENT, still-live Artifact row
	// legitimately depends on.
	Delete(ctx context.Context, ref ArtifactRef) error
}
