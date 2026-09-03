package ports

import "context"

// ReleaseEligibilityAuthority is the abstract "caller supplies a valid
// ReleaseSet sealed|abandoned authorization" port RequestWorkspaceSetRelease
// depends on (V3-11, docs/design/05-v3-project-workspace.md's own Mục tiêu
// line: "caller đưa authorization ReleaseSet sealed|abandoned hợp lệ";
// GC-INV-26: "Alpha không có remote Git mutation; ReleaseSet local phải
// được seal hoặc abandon trước cleanup"). No real ReleaseSet or
// CompletionPolicy exists anywhere in this codebase yet — V3-11's own
// citations never name which later task builds one for real, and V3-12
// (this design doc's own next entry) explicitly defers "completion/
// evidence/ReleaseSet authority" to V5. RequestWorkspaceSetRelease
// therefore depends on this interface alone, never a concrete
// implementation: V3's own test coverage supplies a fake (see
// internal/app/workspacerelease's own commands_test.go); a real
// implementation, backed by a real sealed/abandoned ReleaseSet, is left
// entirely to that later task.
type ReleaseEligibilityAuthority interface {
	// IsReleaseAuthorized reports whether familyID's own ReleaseSet is
	// currently sealed or abandoned — the one authorization state
	// GC-INV-26 requires before any cleanup of a local ReleaseSet may even
	// be requested. reason is a short, human-readable explanation for a
	// caller-visible rejection when authorized is false (e.g. "ReleaseSet
	// is still open"); it carries no meaning when authorized is true. err
	// is reserved for a genuine failure to resolve the authorization at
	// all (the authority itself unreachable, a malformed familyID, ...),
	// never used to signal "not authorized" — that outcome is always
	// authorized=false, err=nil.
	IsReleaseAuthorized(ctx context.Context, familyID string) (authorized bool, reason string, err error)
}
