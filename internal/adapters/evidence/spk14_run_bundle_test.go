package evidence

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// spk14RunBundleSecret stands in for a credential a raw provider transcript
// would actually carry (an API key, a session token, ...). It must never
// appear anywhere in the retained bundle once PutRedacted has processed it.
const spk14RunBundleSecret = "sk-fixture-secret-must-not-persist-anywhere"

// populateFullRunBundle writes every section named in
// docs/spikes/01-go-core-spike-plan.md §12's evidence bundle layout —
// workflow, runtime, workspace, providers, processes, assertions — so
// SPK-14's integrity/tamper/redaction guarantees are proven against a
// realistic run-shaped bundle, not the one- or two-file toy bundles the
// adapter-level tests above use. Content is synthetic fixture data: this
// proves the evidence mechanism, not any other SPK's domain logic.
func populateFullRunBundle(t *testing.T, bundle *Bundle) {
	t.Helper()

	mustPutJSON := func(path string, value any) {
		t.Helper()
		if _, err := bundle.PutJSON(path, value); err != nil {
			t.Fatalf("PutJSON(%s) error = %v", path, err)
		}
	}
	mustPut := func(path string, body []byte) {
		t.Helper()
		if _, err := bundle.Put(path, body); err != nil {
			t.Fatalf("Put(%s) error = %v", path, err)
		}
	}

	mustPutJSON("environment.json", map[string]string{
		"goos": "windows", "goarch": "amd64", "dbSchemaVersion": "1",
	})

	mustPutJSON("workflow/source-hash.json", map[string]string{
		"definitionId": "definition-1", "sourceHash": "sha256:fixture-source",
	})
	mustPutJSON("workflow/published-version.json", map[string]string{
		"versionId": "workflow-version-1", "contentHash": "sha256:fixture-content",
	})

	mustPutJSON("runtime/run.json", map[string]string{
		"projectId": "project-1", "familyId": "family-1", "runId": "run-1", "state": "SUCCEEDED",
	})
	mustPut("runtime/transitions.jsonl", []byte(`{"runId":"run-1","from":"RUNNING","to":"SUCCEEDED"}`+"\n"))
	mustPutJSON("runtime/attempts.json", []string{"attempt-1"})
	mustPutJSON("runtime/jobs.json", []string{"job-run-1"})
	mustPutJSON("runtime/leases.json", map[string]uint64{"fenceToken": 1})

	mustPutJSON("workspace/workspace-set.json", map[string]string{
		"workspaceSetId": "workspace-set-1", "familyId": "family-1",
	})
	mustPutJSON("workspace/revisions-before.json", map[string]string{
		"repositoryId": "repo-user", "revision": "user-base",
	})
	mustPutJSON("workspace/revisions-after.json", map[string]string{
		"repositoryId": "repo-user", "revision": "user-head",
	})
	mustPut("workspace/diffs/repo-user.patch", []byte("--- a/service.txt\n+++ b/service.txt\n"))

	if _, err := bundle.PutRedacted(
		"providers/raw-redacted.jsonl",
		[]byte(`{"event":"tool_requested","env":{"ANTHROPIC_API_KEY":"`+spk14RunBundleSecret+`"}}`+"\n"),
		spk14RunBundleSecret,
	); err != nil {
		t.Fatalf("PutRedacted(providers/raw-redacted.jsonl) error = %v", err)
	}
	mustPut("providers/normalized.jsonl", []byte(`{"event":"tool_requested"}`+"\n"))
	mustPutJSON("providers/sessions.json", map[string]string{"provider": "claude", "sessionRef": "fixture-session"})

	mustPut("processes/timeline.jsonl", []byte(`{"pid":1234,"event":"started"}`+"\n"))
	mustPutJSON("processes/exits.json", map[string]int{"exitCode": 0})

	mustPutJSON("assertions/report.json", map[string]bool{"passed": true})
}

// buildSealedFullRunBundle builds, populates and finalizes a full run bundle
// under its own temp root, returning it already verified once (the
// unmodified baseline every tamper case in this file starts from).
func buildSealedFullRunBundle(t *testing.T, bundleID string) *Bundle {
	t.Helper()
	bundle, err := Create(t.TempDir(), bundleID)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	populateFullRunBundle(t, bundle)
	if _, err := bundle.Finalize(map[string]string{"spkId": "SPK-14", "suiteId": bundleID}); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if _, err := Verify(bundle.Directory()); err != nil {
		t.Fatalf("baseline Verify() error = %v, want a clean full run bundle to verify", err)
	}
	return bundle
}

// TestSPK14FullRunBundleVerifiesAndRedactsSecret is SPK-14's positive case,
// elevated from the adapter-level toy bundles above to a full run bundle
// covering every section in the spike plan's evidence layout: sealing
// succeeds, Verify passes, and an exact secret value injected into a raw
// provider artifact does not survive PutRedacted anywhere in the retained
// bundle — not just the one file it was redacted from.
func TestSPK14FullRunBundleVerifiesAndRedactsSecret(t *testing.T) {
	bundle := buildSealedFullRunBundle(t, "spk-14-run-pass")
	assertSecretAbsentFromBundle(t, bundle.Directory(), spk14RunBundleSecret)
}

// TestSPK14FullRunBundleDetectsMutatedArtifact is SPK-14's first negative
// case: an artifact that was part of the sealed manifest is silently
// rewritten after the fact (as if something tried to make a run's recorded
// outcome look different from what actually happened). Verify must fail with
// ErrIntegrity, not accept the mutated content.
func TestSPK14FullRunBundleDetectsMutatedArtifact(t *testing.T) {
	bundle := buildSealedFullRunBundle(t, "spk-14-run-tamper-mutate")
	target := filepath.Join(bundle.Directory(), "workspace", "revisions-after.json")
	if err := os.WriteFile(target, []byte(`{"repositoryId":"repo-user","revision":"attacker-revision"}`), 0o600); err != nil {
		t.Fatalf("mutate declared artifact: %v", err)
	}
	if _, err := Verify(bundle.Directory()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Verify() after mutating a declared artifact error = %v, want ErrIntegrity", err)
	}
}

// TestSPK14FullRunBundleDetectsUnmanifestedFile is SPK-14's second negative
// case: a file appears in the bundle directory after sealing without ever
// having been declared through Bundle.Put. Verify must fail with
// ErrIntegrity even though every declared artifact is still untouched and
// hashes correctly.
func TestSPK14FullRunBundleDetectsUnmanifestedFile(t *testing.T) {
	bundle := buildSealedFullRunBundle(t, "spk-14-run-tamper-unmanifested")
	stray := filepath.Join(bundle.Directory(), "runtime", "shadow-transitions.jsonl")
	if err := os.WriteFile(stray, []byte(`{"runId":"run-1","from":"RUNNING","to":"SUCCEEDED"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write unmanifested file: %v", err)
	}
	if _, err := Verify(bundle.Directory()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Verify() with an unmanifested file error = %v, want ErrIntegrity", err)
	}
}

func assertSecretAbsentFromBundle(t *testing.T, directory string, secret string) {
	t.Helper()
	secretBytes := []byte(secret)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(content, secretBytes) {
			t.Errorf("secret leaked into retained file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk bundle for secret search: %v", err)
	}
}
