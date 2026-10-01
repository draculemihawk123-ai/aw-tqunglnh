package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// V9-09 / B4: `aw version show|diff` (the definition-VERSION resource
// commands) were shadowed by the process command `aw version` — runStreams
// looked "version" up in the process-command table before the resource
// router ever saw the arguments, so `aw version show <id>` answered "version
// takes no arguments".
//
// Why the pre-existing tests missed it: awInstall.aw prepends the global
// options (--db ...), so the first word the dispatcher saw was "--db", never
// "version". These tests put "version" FIRST, which is what an operator
// types; the installation comes from the environment (the documented
// AW_DB/AW_ARTIFACT_ROOT fallbacks) or from options AFTER the command.

// publishTwoBlockVersions publishes two different versions of one BLOCK
// definition and returns their ids.
func publishTwoBlockVersions(t *testing.T, a *awInstall) (versionA, versionB string) {
	t.Helper()
	a.createDefinition(t, "BLOCK", "blk-1", "version routing", "idem-create")
	fileA := writeFixture(t, "a.json", blockDocumentWithTimeout(60))
	fileB := writeFixture(t, "b.json", blockDocumentWithTimeout(90))
	var vA, vB versionDoc
	if err := json.Unmarshal(decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileA, "--yes", "--idempotency-key", "idem-a", "blk-1")).Result, &vA); err != nil || vA.ID == "" {
		t.Fatalf("publish A: %v %+v", err, vA)
	}
	if err := json.Unmarshal(decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileB, "--yes", "--idempotency-key", "idem-b", "blk-1")).Result, &vB); err != nil || vB.ID == "" {
		t.Fatalf("publish B: %v %+v", err, vB)
	}
	return vA.ID, vB.ID
}

func TestVersionRouting_ShowAndDiffReachTheResourceCommandsWhenVersionIsTheFirstWord(t *testing.T) {
	a := newAWInstall(t)
	versionA, versionB := publishTwoBlockVersions(t, a)
	t.Setenv("AW_DB", a.db)
	t.Setenv("AW_ARTIFACT_ROOT", a.artifactRoot)

	stdout, stderr, code := awRaw("", "version", "show", versionA)
	if code != exitSuccess {
		t.Fatalf("aw version show %s: exit=%d stderr=%q (it was 'version takes no arguments' before V9-09)", versionA, code, stderr)
	}
	var shown versionDoc
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil || shown.ID != versionA {
		t.Fatalf("aw version show = %q (err %v), want version %s", stdout, err, versionA)
	}

	stdout, stderr, code = awRaw("", "version", "diff", versionA, versionB)
	if code != exitSuccess {
		t.Fatalf("aw version diff: exit=%d stderr=%q", code, stderr)
	}
	var diff struct {
		Identical bool `json:"identical"`
	}
	if err := json.Unmarshal([]byte(stdout), &diff); err != nil || diff.Identical {
		t.Fatalf("aw version diff = %q (err %v), want a real, non-identical diff", stdout, err)
	}
}

// The global options may follow the command or sit between `version` and its
// action, exactly as for every other resource command.
func TestVersionRouting_GlobalOptionsAfterOrBetweenStillRoute(t *testing.T) {
	a := newAWInstall(t)
	versionA, _ := publishTwoBlockVersions(t, a)
	t.Setenv("AW_DB", "")
	t.Setenv("AW_ARTIFACT_ROOT", "")

	for _, args := range [][]string{
		{"version", "show", versionA, "--db", a.db, "--artifact-root", a.artifactRoot},
		{"version", "--db", a.db, "--artifact-root", a.artifactRoot, "show", versionA},
	} {
		stdout, stderr, code := awRaw("", args...)
		if code != exitSuccess {
			t.Fatalf("aw %v: exit=%d stderr=%q", args, code, stderr)
		}
		var shown versionDoc
		if err := json.Unmarshal([]byte(stdout), &shown); err != nil || shown.ID != versionA {
			t.Fatalf("aw %v = %q (err %v), want version %s", args, stdout, err, versionA)
		}
	}
}

// A malformed resource invocation is answered by the resource command itself
// (its own usage line), proving the handler was reached.
func TestVersionRouting_ResourceUsageErrorsComeFromTheResourceCommand(t *testing.T) {
	a := newAWInstall(t)
	t.Setenv("AW_DB", a.db)
	t.Setenv("AW_ARTIFACT_ROOT", a.artifactRoot)

	_, stderr, code := awRaw("", "version", "show")
	if code != exitUsage || !strings.Contains(stderr, "usage: aw version show <versionId>") {
		t.Fatalf("aw version show: exit=%d stderr=%q, want the resource command's usage error", code, stderr)
	}
	_, stderr, code = awRaw("", "version", "diff", "only-one")
	if code != exitUsage || !strings.Contains(stderr, "usage: aw version diff") {
		t.Fatalf("aw version diff only-one: exit=%d stderr=%q, want the resource command's usage error", code, stderr)
	}
}

// The process command is untouched: same one-line summary, same manifest,
// same usage error for anything that is not a routed resource action.
func TestVersionRouting_ProcessCommandOutputIsUnchanged(t *testing.T) {
	stdout, stderr, code := awRaw("", "version")
	if code != exitSuccess || stdout != versionLine()+"\n" || stderr != "" {
		t.Fatalf("aw version: exit=%d stdout=%q stderr=%q, want exactly %q", code, stdout, stderr, versionLine()+"\n")
	}

	stdout, stderr, code = awRaw("", "version", "--json")
	if code != exitSuccess || stderr != "" {
		t.Fatalf("aw version --json: exit=%d stderr=%q", code, stderr)
	}
	var manifest releaseManifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil || manifest.SchemaVersion <= 0 || manifest.GoVersion == "" {
		t.Fatalf("aw version --json = %q (err %v), want the release manifest", stdout, err)
	}

	for _, args := range [][]string{{"version", "extra"}, {"version", "shows"}, {"version", "--json", "show"}} {
		_, stderr, code := awRaw("", args...)
		if code != exitUsage || !strings.Contains(stderr, "version takes no arguments") {
			t.Fatalf("aw %v: exit=%d stderr=%q, want the process command's usage error", args, code, stderr)
		}
	}
}

// Both meanings stay discoverable: `aw help` lists the process command and
// the resource commands under the same word, and neither the CLI_LOCAL set
// nor the routed paths changed (the parity ledger is exercised by
// internal/delivery/parity and is unchanged by this fix).
func TestVersionRouting_HelpListsBothMeanings(t *testing.T) {
	stdout, _, code := awRaw("", "help")
	if code != exitSuccess {
		t.Fatalf("aw help exit=%d", code)
	}
	if !strings.Contains(stdout, "version     print the aw build version") {
		t.Errorf("help does not list the process command `aw version`:\n%s", stdout)
	}
	if !strings.Contains(stdout, "version                diff|show") {
		t.Errorf("help does not list the resource commands `aw version diff|show`:\n%s", stdout)
	}
}
