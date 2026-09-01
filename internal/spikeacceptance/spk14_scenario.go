package spikeacceptance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

const spk14ScenarioSecret = "sk-fixture-secret-must-not-persist-anywhere"

// runSPK14Scenario closes SPK-14 as a real, registry-dispatched scenario:
// it builds three independently sealed sub-bundles (see
// ScenarioContext.NewSubBundle, since RunAll already owns finalizing and
// verifying this scenario's own bundle and corrupting that one would break
// RunAll itself, not demonstrate anything) covering every section from
// docs/spikes/01-go-core-spike-plan.md §12: a positive bundle that verifies
// and never leaks a fixture secret, and two tamper cases (mutated artifact,
// unmanifested file) that must fail verification.
func runSPK14Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	positive, err := sc.NewSubBundle("positive")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk14: create positive sub-bundle: %w", err)
	}
	if err := populateSPK14RunBundle(positive); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: populate positive sub-bundle: %w", err)
	}
	if _, err := positive.Finalize(map[string]string{"spkId": "SPK-14", "case": "positive"}); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: finalize positive sub-bundle: %w", err)
	}
	if _, err := evidence.Verify(positive.Directory()); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: unexpected Verify failure on a clean bundle: %w", err)
	}
	record("a clean full-run bundle verifies", true, "")

	secretLeaked, err := secretPresentInDirectory(positive.Directory(), spk14ScenarioSecret)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk14: search positive bundle for secret: %w", err)
	}
	record("the injected secret is absent from every retained file", !secretLeaked, "")

	mutateCase, err := sc.NewSubBundle("tamper-mutate")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk14: create tamper-mutate sub-bundle: %w", err)
	}
	if err := populateSPK14RunBundle(mutateCase); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: populate tamper-mutate sub-bundle: %w", err)
	}
	if _, err := mutateCase.Finalize(map[string]string{"spkId": "SPK-14", "case": "tamper-mutate"}); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: finalize tamper-mutate sub-bundle: %w", err)
	}
	mutatedTarget := filepath.Join(mutateCase.Directory(), "workspace", "revisions-after.json")
	if err := os.WriteFile(mutatedTarget, []byte(`{"repositoryId":"repo-user","revision":"attacker-revision"}`), 0o600); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: mutate declared artifact: %w", err)
	}
	_, mutateVerifyErr := evidence.Verify(mutateCase.Directory())
	record("mutating a declared artifact after sealing fails verification",
		errors.Is(mutateVerifyErr, evidence.ErrIntegrity), fmt.Sprintf("error=%v", mutateVerifyErr))

	unmanifestedCase, err := sc.NewSubBundle("tamper-unmanifested")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk14: create tamper-unmanifested sub-bundle: %w", err)
	}
	if err := populateSPK14RunBundle(unmanifestedCase); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: populate tamper-unmanifested sub-bundle: %w", err)
	}
	if _, err := unmanifestedCase.Finalize(map[string]string{"spkId": "SPK-14", "case": "tamper-unmanifested"}); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: finalize tamper-unmanifested sub-bundle: %w", err)
	}
	strayPath := filepath.Join(unmanifestedCase.Directory(), "runtime", "shadow-transitions.jsonl")
	if err := os.WriteFile(strayPath, []byte(`{"runId":"run-1"}`+"\n"), 0o600); err != nil {
		return SPKResult{}, fmt.Errorf("spk14: write unmanifested file: %w", err)
	}
	_, unmanifestedVerifyErr := evidence.Verify(unmanifestedCase.Directory())
	record("an unmanifested file after sealing fails verification",
		errors.Is(unmanifestedVerifyErr, evidence.ErrIntegrity), fmt.Sprintf("error=%v", unmanifestedVerifyErr))

	summaryArtifact, err := sc.Bundle.PutJSON("assertions/report.json", map[string]any{"assertions": assertions})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk14: write summary evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Platform:   Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:     Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts:  []ArtifactRef{{Kind: ArtifactKindAssertions, Artifact: summaryArtifact}},
	}, nil
}

// populateSPK14RunBundle writes every section named in
// docs/spikes/01-go-core-spike-plan.md §12's evidence bundle layout into a
// fresh bundle, injecting spk14ScenarioSecret into a raw provider artifact
// via PutRedacted so the positive case's secret-absence check is real.
func populateSPK14RunBundle(bundle *evidence.Bundle) error {
	steps := []func() error{
		func() error {
			_, err := bundle.PutJSON("environment.json", map[string]string{"goos": "windows", "goarch": "amd64"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("workflow/source-hash.json", map[string]string{"definitionId": "definition-1", "sourceHash": "sha256:fixture-source"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("workflow/published-version.json", map[string]string{"versionId": "workflow-version-1", "contentHash": "sha256:fixture-content"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("runtime/run.json", map[string]string{"runId": "run-1", "state": "SUCCEEDED"})
			return err
		},
		func() error {
			_, err := bundle.Put("runtime/transitions.jsonl", []byte(`{"runId":"run-1","from":"RUNNING","to":"SUCCEEDED"}`+"\n"))
			return err
		},
		func() error {
			_, err := bundle.PutJSON("runtime/attempts.json", []string{"attempt-1"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("runtime/jobs.json", []string{"job-run-1"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("runtime/leases.json", map[string]uint64{"fenceToken": 1})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("workspace/workspace-set.json", map[string]string{"workspaceSetId": "workspace-set-1"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("workspace/revisions-before.json", map[string]string{"repositoryId": "repo-user", "revision": "user-base"})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("workspace/revisions-after.json", map[string]string{"repositoryId": "repo-user", "revision": "user-head"})
			return err
		},
		func() error {
			_, err := bundle.Put("workspace/diffs/repo-user.patch", []byte("--- a/service.txt\n+++ b/service.txt\n"))
			return err
		},
		func() error {
			_, err := bundle.PutRedacted(
				"providers/raw-redacted.jsonl",
				[]byte(`{"event":"tool_requested","env":{"ANTHROPIC_API_KEY":"`+spk14ScenarioSecret+`"}}`+"\n"),
				spk14ScenarioSecret,
			)
			return err
		},
		func() error {
			_, err := bundle.Put("providers/normalized.jsonl", []byte(`{"event":"tool_requested"}`+"\n"))
			return err
		},
		func() error {
			_, err := bundle.PutJSON("providers/sessions.json", map[string]string{"provider": "claude"})
			return err
		},
		func() error {
			_, err := bundle.Put("processes/timeline.jsonl", []byte(`{"pid":1234,"event":"started"}`+"\n"))
			return err
		},
		func() error {
			_, err := bundle.PutJSON("processes/exits.json", map[string]int{"exitCode": 0})
			return err
		},
		func() error {
			_, err := bundle.PutJSON("assertions/report.json", map[string]bool{"passed": true})
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func secretPresentInDirectory(directory, secret string) (bool, error) {
	found := false
	secretBytes := []byte(secret)
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
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
			found = true
		}
		return nil
	})
	return found, err
}
