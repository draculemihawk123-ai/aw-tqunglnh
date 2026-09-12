package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
)

func TestLoadLocalPrincipalFile_EmptyPathUsesDefault(t *testing.T) {
	lp, err := config.LoadLocalPrincipalFile("")
	if err != nil {
		t.Fatalf("LoadLocalPrincipalFile(\"\"): %v", err)
	}
	want := config.DefaultLocalPrincipal()
	if lp.Actor != want.Actor || len(lp.Roles) != len(want.Roles) || lp.Roles[0] != want.Roles[0] {
		t.Fatalf("lp = %+v, want default %+v", lp, want)
	}
}

func TestLoadLocalPrincipalFile_MissingFileUsesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	lp, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		t.Fatalf("LoadLocalPrincipalFile(missing): %v", err)
	}
	want := config.DefaultLocalPrincipal()
	if lp.Actor != want.Actor {
		t.Fatalf("lp.Actor = %q, want default %q", lp.Actor, want.Actor)
	}
}

func TestLoadLocalPrincipalFile_FileWithoutLocalPrincipalKeyUsesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"some_other_key": "value"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lp, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		t.Fatalf("LoadLocalPrincipalFile: %v", err)
	}
	want := config.DefaultLocalPrincipal()
	if lp.Actor != want.Actor {
		t.Fatalf("lp.Actor = %q, want default %q", lp.Actor, want.Actor)
	}
}

func TestLoadLocalPrincipalFile_ReadsCustomActorAndRoles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"localPrincipal": {"actor": "alice", "roles": ["operator", "auditor"]}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lp, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		t.Fatalf("LoadLocalPrincipalFile: %v", err)
	}
	if lp.Actor != "alice" {
		t.Fatalf("lp.Actor = %q, want alice", lp.Actor)
	}
	if len(lp.Roles) != 2 || lp.Roles[0] != "operator" || lp.Roles[1] != "auditor" {
		t.Fatalf("lp.Roles = %v, want [operator auditor]", lp.Roles)
	}
}

func TestLoadLocalPrincipalFile_MalformedJSONFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{not valid json`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := config.LoadLocalPrincipalFile(path); err == nil {
		t.Fatal("LoadLocalPrincipalFile with malformed JSON should fail")
	}
}

func TestValidateLocalPrincipal_DefaultIsValid(t *testing.T) {
	if err := config.ValidateLocalPrincipal(config.DefaultLocalPrincipal()); err != nil {
		t.Fatalf("ValidateLocalPrincipal(default) = %v, want nil", err)
	}
}

func TestValidateLocalPrincipal_RejectsEmptyActor(t *testing.T) {
	lp := config.LocalPrincipal{Actor: "", Roles: []string{"operator"}}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal with empty Actor should fail")
	}
}

func TestValidateLocalPrincipal_RejectsBlankActor(t *testing.T) {
	lp := config.LocalPrincipal{Actor: "   ", Roles: []string{"operator"}}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal with whitespace-only Actor should fail")
	}
}

func TestValidateLocalPrincipal_RejectsEmptyRoles(t *testing.T) {
	lp := config.LocalPrincipal{Actor: "local-operator", Roles: nil}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal with no Roles should fail")
	}
}

func TestValidateLocalPrincipal_RejectsBlankRole(t *testing.T) {
	lp := config.LocalPrincipal{Actor: "local-operator", Roles: []string{"operator", "  "}}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal with a blank role should fail")
	}
}

func TestValidateLocalPrincipal_RejectsCaseSensitiveDuplicateRole(t *testing.T) {
	lp := config.LocalPrincipal{Actor: "local-operator", Roles: []string{"operator", "operator"}}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal with a duplicate role should fail")
	}
}

func TestValidateLocalPrincipal_AllowsCaseVariantsAsDistinctRoles(t *testing.T) {
	// ADR-028: role matching is case-sensitive, so "Operator" and "operator"
	// are two distinct roles, not a duplicate — this is what "case-sensitive"
	// means in practice, not just a comparison detail.
	lp := config.LocalPrincipal{Actor: "local-operator", Roles: []string{"operator", "Operator"}}
	if err := config.ValidateLocalPrincipal(lp); err != nil {
		t.Fatalf("ValidateLocalPrincipal with case-distinct roles = %v, want nil", err)
	}
}

func TestLoadLocalPrincipalFile_PartialDeclarationIsNotSilentlyDefaulted(t *testing.T) {
	// A file that DOES declare localPrincipal but only an actor (no roles)
	// must not quietly fall back to the [operator] default — that would mask
	// what is far more likely an operator's config mistake. Load returns the
	// partial value as-is; Validate is what turns it into a startup failure.
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"localPrincipal": {"actor": "alice"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lp, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		t.Fatalf("LoadLocalPrincipalFile: %v", err)
	}
	if lp.Actor != "alice" {
		t.Fatalf("lp.Actor = %q, want alice", lp.Actor)
	}
	if len(lp.Roles) != 0 {
		t.Fatalf("lp.Roles = %v, want empty (not silently defaulted to [operator])", lp.Roles)
	}
	if err := config.ValidateLocalPrincipal(lp); err == nil {
		t.Fatal("ValidateLocalPrincipal on a partial declaration (actor without roles) should fail")
	}
}
