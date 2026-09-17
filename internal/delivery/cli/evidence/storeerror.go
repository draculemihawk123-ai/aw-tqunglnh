package evidence

import (
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
)

// sanitizeStoreError converts an error from ports.ArtifactStore.Verify/Open
// into a fixed, locator/filesystem-path-free message — mirroring
// internal/delivery/httpapi's own WriteAppError exactly (that function's
// own doc comment: "any other error ... writes a generic message, never
// the raw error's own text, which has made no promise of being safe to
// expose"). This matters here specifically because
// internal/adapters/artifactstore's own real ArtifactStore implementation
// is confirmed (filesystem.go's own Verify/Open) to embed ref.Locator
// and/or a real on-disk path directly into a PLAIN wrapped error's own
// .Error() text (Verify: `fmt.Errorf("%w: locator=%s", ErrIntegrity,
// ref.Locator)`; Open: an *os.PathError wrapped straight through, whose
// own .Error() names the real path) — neither is an *apperror.Error, so
// neither has ever made apperror's own "Message is the safe, public-facing
// half" promise. This package must never forward either verbatim into any
// field its own JSON output carries, or into any error text a future
// composition root eventually prints to stderr, per this task's own "no
// raw locator ever printed" line — a not-found *apperror.Error (the one
// case ArtifactStore's own errors ARE already locator-free by construction)
// still surfaces its own real, safe Message; anything else collapses to one
// fixed, generic classification.
func sanitizeStoreError(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr.Message
	}
	return "artifact content verification failed (stored bytes do not match their recorded hash)"
}
