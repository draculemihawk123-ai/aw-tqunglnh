package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// V11-07: the author identity of a commit made on behalf of an approver is the git configuration of
// the machine running aw, per repository. These tests isolate git's global/system configuration so
// they do not depend on the developer's machine. They cannot run in parallel (they set the
// environment git inherits).

// withoutLocalIdentity removes the identity the test repository fixture sets locally, so what is
// left is only what the test configures.
func (f *inspectionFixture) withoutLocalIdentity(t *testing.T) {
	t.Helper()
	f.gitOutput(t, "config", "--unset", "user.name")
	f.gitOutput(t, "config", "--unset", "user.email")
}

func isolateGitConfig(t *testing.T, globalContent string) {
	t.Helper()
	home := t.TempDir()
	global := filepath.Join(home, "gitconfig-global")
	if err := os.WriteFile(global, []byte(globalContent), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func TestReadGitIdentity_ReportsTheGlobalIdentityAndWhereItCameFrom(t *testing.T) {
	isolateGitConfig(t, "[user]\n\tname = Global Reviewer\n\temail = global@example.test\n")
	f := newInspectionFixture(t)
	f.withoutLocalIdentity(t)
	identity, err := f.provider.ReadGitIdentity(context.Background(), f.handle)
	if err != nil {
		t.Fatalf("ReadGitIdentity: %v", err)
	}
	if identity.Name != "Global Reviewer" || identity.Email != "global@example.test" || identity.NameSource != "global" || identity.EmailSource != "global" || !identity.Complete() {
		t.Fatalf("identity = %+v, want the global one", identity)
	}
}

func TestReadGitIdentity_ARepositoryLocalSettingOverridesTheGlobalOne(t *testing.T) {
	isolateGitConfig(t, "[user]\n\tname = Global Reviewer\n\temail = global@example.test\n")
	f := newInspectionFixture(t)
	f.withoutLocalIdentity(t)
	f.gitOutput(t, "config", "user.name", "Repo Reviewer")
	identity, err := f.provider.ReadGitIdentity(context.Background(), f.handle)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Name != "Repo Reviewer" || identity.NameSource != "local" || identity.Email != "global@example.test" || identity.EmailSource != "global" {
		t.Fatalf("identity = %+v, want the local name over the global one and the global email", identity)
	}
}

func TestReadGitIdentity_MissingSettingsAreReportedNotGuessed(t *testing.T) {
	isolateGitConfig(t, "")
	f := newInspectionFixture(t)
	f.withoutLocalIdentity(t)
	identity, err := f.provider.ReadGitIdentity(context.Background(), f.handle)
	if err != nil {
		t.Fatalf("a missing identity is a fact, not an error: %v", err)
	}
	if identity.Name != "" || identity.Email != "" || identity.NameSource != "" || identity.Complete() {
		t.Fatalf("identity = %+v, want nothing (and not a guess from the OS user or the host name)", identity)
	}

	f.gitOutput(t, "config", "user.name", "Only A Name")
	identity, err = f.provider.ReadGitIdentity(context.Background(), f.handle)
	if err != nil || identity.Name != "Only A Name" || identity.Email != "" || identity.Complete() {
		t.Fatalf("identity = %+v, %v; want a name and no email, incomplete", identity, err)
	}
}

func TestReadGitIdentity_NeverWritesGitConfiguration(t *testing.T) {
	isolateGitConfig(t, "")
	f := newInspectionFixture(t)
	before := f.gitOutput(t, "config", "--list", "--show-origin")
	if _, err := f.provider.ReadGitIdentity(context.Background(), f.handle); err != nil {
		t.Fatal(err)
	}
	if after := f.gitOutput(t, "config", "--list", "--show-origin"); after != before {
		t.Fatalf("reading the identity changed the git configuration:\n%s\n--- vs ---\n%s", before, after)
	}
}
