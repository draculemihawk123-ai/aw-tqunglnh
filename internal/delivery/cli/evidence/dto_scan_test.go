package evidence_test

import (
	"reflect"
	"strings"
	"testing"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	clievidence "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

// TestEvidenceCLI_NeverExposesLocatorPathOrSecretShapedFields is this
// task's own "secret redaction" Verify bullet, read carefully per the
// task's own brief: no runtime content-redaction happens on read (content
// is already redacted before Put, at write time), so this is really a
// proof that this package's own response DTOs never accidentally expose a
// filesystem path/locator/PID-shaped field — mirroring
// internal/delivery/cli/run/diagnostics_test.go's own
// TestRunDiagnostics_NeverExposesProcessOrSecretShapedFields pattern
// exactly: a reflect-based field-name scan across every exported type this
// package's own leaves either define (evidenceListResult is unexported,
// scanned via its own runtimeapp.EvidenceDetail element type instead) or
// reuse verbatim from internal/app/runtime (EvidenceDetail/ArtifactSummary/
// ContextSnapshotDetail and their own nested view types) — proving both
// "own new DTOs" (VerifyResult/ArtifactVerification) and "reused DTOs"
// never carry a field name that WOULD leak a locator/filesystem path/
// secret/process-identity if one existed.
func TestEvidenceCLI_NeverExposesLocatorPathOrSecretShapedFields(t *testing.T) {
	forbidden := []string{"locator", "path", "secret", "pid", "argv", "cwd", "workingdirectory", "executablepath"}

	types := []reflect.Type{
		// This package's own genuinely new DTOs (verify.go).
		reflect.TypeOf(clievidence.VerifyResult{}),
		reflect.TypeOf(clievidence.ArtifactVerification{}),
		// Reused verbatim from internal/app/runtime (list.go, contextsnapshot.go,
		// artifact.go) — scanned here too, so a future field added upstream
		// that this package then starts surfacing is caught the moment this
		// package's own test suite runs, not only by that package's own tests.
		reflect.TypeOf(runtimeapp.EvidenceDetail{}),
		reflect.TypeOf(runtimeapp.RevisionView{}),
		reflect.TypeOf(runtimeapp.ArtifactSummary{}),
		reflect.TypeOf(runtimeapp.ContextSnapshotDetail{}),
		reflect.TypeOf(runtimeapp.MessageRefView{}),
		reflect.TypeOf(runtimeapp.ResourceRefView{}),
		reflect.TypeOf(runtimeapp.EvidenceRefView{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			lower := strings.ToLower(field.Name)
			for _, bad := range forbidden {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s: field name contains forbidden fragment %q — a locator/filesystem-path/secret/process-identity-shaped field must never appear in this package's own output", typ.Name(), field.Name, bad)
				}
			}
		}
	}
}
