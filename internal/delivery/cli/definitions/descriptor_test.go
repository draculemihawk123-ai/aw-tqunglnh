package definitions_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

// TestDescriptorsRegisterAllSixteenCommandsWithConsistentMetadata is this
// task's own descriptor coverage proof — mirrors
// internal/delivery/cli/catalog's own
// TestDescriptorsRegisterAllTenCommandsWithConsistentMetadata: every
// command this task's brief lists under "Command surface to build" must
// be registered into cli.Default (via this package's own init()) at BOTH
// scopes (installation and project — V6-15E's own "for exact global/
// project scopes" line), each with the HTTPOperationID
// internal/delivery/httpapi/definitions/routes.go's own matching scoped
// route actually uses, except `definition list` (cli.CLILocalOperation at
// both scopes — no HTTP route exists to mirror, see doc.go's own top
// comment).
func TestDescriptorsRegisterAllSixteenCommandsWithConsistentMetadata(t *testing.T) {
	type key struct {
		path  string
		scope string
	}
	want := map[key]string{
		{"definition list", "INSTALLATION"}: cli.CLILocalOperation,
		{"definition list", "PROJECT"}:      cli.CLILocalOperation,

		{"definition create", "INSTALLATION"}: "createDefinition",
		{"definition create", "PROJECT"}:      "createProjectDefinition",

		{"definition show", "INSTALLATION"}: "getDefinition",
		{"definition show", "PROJECT"}:      "getProjectDefinition",

		{"definition versions", "INSTALLATION"}: "listDefinitionVersions",
		{"definition versions", "PROJECT"}:      "listProjectDefinitionVersions",

		{"definition validate", "INSTALLATION"}: "validateDefinitionDraft",
		{"definition validate", "PROJECT"}:      "validateProjectDefinitionDraft",

		{"definition publish", "INSTALLATION"}: "publishDefinitionVersion",
		{"definition publish", "PROJECT"}:      "publishProjectDefinitionVersion",

		{"version show", "INSTALLATION"}: "getDefinitionVersion",
		{"version show", "PROJECT"}:      "getProjectDefinitionVersion",

		{"version diff", "INSTALLATION"}: "diffDefinitionVersions",
		{"version diff", "PROJECT"}:      "diffProjectDefinitionVersions",
	}

	all := cli.All()
	got := 0
	for _, d := range all {
		path := strings.Join(d.Path, " ")
		k := key{path, string(d.Scope)}
		expectedOp, ok := want[k]
		if !ok {
			continue // a descriptor registered by some other already-loaded package (e.g. catalog, settings) in the same process/test binary.
		}
		got++
		if d.HTTPOperationID != expectedOp {
			t.Errorf("descriptor %q scope %q HTTPOperationID = %q, want %q", path, d.Scope, d.HTTPOperationID, expectedOp)
		}
		if strings.TrimSpace(d.AppOperation) == "" {
			t.Errorf("descriptor %q scope %q has empty AppOperation", path, d.Scope)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Fatalf("descriptors never registered: %+v (found %d of %d expected)", want, got, got+len(want))
	}
}

// TestDescriptorsHaveNoDuplicateRegistration proves this package's own
// init() never registers the same (Path, Scope) pair twice — Registry.
// Register's own "never silently overwriting" contract would otherwise
// have already panicked at package init time (MustRegister), so this test
// mostly documents the invariant; it also guards against a future edit
// accidentally re-registering a descriptor under a subtly different Scope
// string that Register's own key() would treat as distinct.
func TestDescriptorsHaveNoDuplicateRegistration(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range cli.All() {
		path := strings.Join(d.Path, " ")
		if path != "definition list" && path != "definition create" && path != "definition show" &&
			path != "definition versions" && path != "definition validate" && path != "definition publish" &&
			path != "version show" && path != "version diff" {
			continue
		}
		k := path + "|" + string(d.Scope)
		if seen[k] {
			t.Fatalf("duplicate descriptor registration for %s", k)
		}
		seen[k] = true
	}
	if len(seen) != 16 {
		t.Fatalf("got %d definitions-package descriptors, want exactly 16: %v", len(seen), seen)
	}
}
