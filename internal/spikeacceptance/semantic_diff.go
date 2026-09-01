package spikeacceptance

import (
	"fmt"
	"sort"
)

// PlatformDifference is one field where two SPKResults for the same SPKID,
// captured on different platforms, differ in a way SemanticDiff's fixed
// allowlist does not explain.
type PlatformDifference struct {
	Field string
	A     string
	B     string
}

func (d PlatformDifference) String() string {
	return fmt.Sprintf("%s: %q != %q", d.Field, d.A, d.B)
}

type artifactKey struct {
	kind string
	path string
}

// SemanticDiff compares two SPKResults for the same SPKID captured on
// different platforms (see each result's own Platform.GOOS) and reports
// every difference not explained by the fixed allowlist from
// docs/design/02-v0-spike-verdict.md V0-10:
//
//   - Platform and Timing are platform/wall-clock metadata by definition and
//     are never compared.
//   - ArtifactRef entries of kind ArtifactKindProcesses record real PIDs and
//     process timing (docs/spikes/01-go-core-spike-plan.md §12's
//     processes/timeline.jsonl, processes/exits.json): their Kind and Path
//     must still match on both platforms (the same process artifacts must
//     exist), but their SHA256/Size are not compared.
//
// Every other field is compared exactly: SPKID, Passed, all seven
// Correlation identity fields, every Assertion (name/passed/detail) and
// every other artifact kind's Kind/Path/SHA256/Size. No separate
// locator/path-separator substitution is applied at this level: bundle
// artifact paths are already forward-slash-normalized by the evidence
// adapter (normalizeRelativePath in internal/adapters/evidence), and every
// identity compared here is application-assigned rather than derived from
// an OS path (ports.WorkspaceHandle is deliberately "opaque, path-free").
// If one of these ever legitimately differed across platforms, that is
// exactly the unknown difference this function must report, not an
// artifact to normalize away — SemanticDiff never deletes or skips a field
// it does not explicitly name above.
func SemanticDiff(a, b SPKResult) []PlatformDifference {
	var diffs []PlatformDifference
	add := func(field, left, right string) {
		if left != right {
			diffs = append(diffs, PlatformDifference{Field: field, A: left, B: right})
		}
	}

	add("spkId", string(a.SPKID), string(b.SPKID))
	add("passed", fmt.Sprintf("%t", a.Passed), fmt.Sprintf("%t", b.Passed))
	add("correlation.projectId", a.Correlation.ProjectID, b.Correlation.ProjectID)
	add("correlation.familyId", a.Correlation.FamilyID, b.Correlation.FamilyID)
	add("correlation.runId", a.Correlation.RunID, b.Correlation.RunID)
	add("correlation.nodeRunId", a.Correlation.NodeRunID, b.Correlation.NodeRunID)
	add("correlation.attemptId", a.Correlation.AttemptID, b.Correlation.AttemptID)
	add("correlation.repositoryId", a.Correlation.RepositoryID, b.Correlation.RepositoryID)
	add("correlation.revision", a.Correlation.Revision, b.Correlation.Revision)

	if len(a.Assertions) != len(b.Assertions) {
		diffs = append(diffs, PlatformDifference{
			Field: "assertions.count",
			A:     fmt.Sprintf("%d", len(a.Assertions)),
			B:     fmt.Sprintf("%d", len(b.Assertions)),
		})
	} else {
		for i := range a.Assertions {
			field := fmt.Sprintf("assertions[%d]", i)
			add(field+".name", a.Assertions[i].Name, b.Assertions[i].Name)
			add(field+".passed", fmt.Sprintf("%t", a.Assertions[i].Passed), fmt.Sprintf("%t", b.Assertions[i].Passed))
			add(field+".detail", a.Assertions[i].Detail, b.Assertions[i].Detail)
		}
	}

	diffs = append(diffs, diffArtifacts(a.Artifacts, b.Artifacts)...)
	return diffs
}

func diffArtifacts(a, b []ArtifactRef) []PlatformDifference {
	left := indexArtifacts(a)
	right := indexArtifacts(b)
	keys := make(map[artifactKey]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	sortedKeys := make([]artifactKey, 0, len(keys))
	for key := range keys {
		sortedKeys = append(sortedKeys, key)
	}
	sort.Slice(sortedKeys, func(i, j int) bool {
		if sortedKeys[i].kind != sortedKeys[j].kind {
			return sortedKeys[i].kind < sortedKeys[j].kind
		}
		return sortedKeys[i].path < sortedKeys[j].path
	})

	var diffs []PlatformDifference
	for _, key := range sortedKeys {
		field := fmt.Sprintf("artifacts[%s:%s]", key.kind, key.path)
		leftArtifact, leftOK := left[key]
		rightArtifact, rightOK := right[key]
		if !leftOK || !rightOK {
			diffs = append(diffs, PlatformDifference{Field: field, A: presence(leftOK), B: presence(rightOK)})
			continue
		}
		if key.kind == ArtifactKindProcesses {
			continue // real PIDs/process timing: allowlisted, per docs/design/02-v0-spike-verdict.md V0-10
		}
		if leftArtifact.Artifact.SHA256 != rightArtifact.Artifact.SHA256 {
			diffs = append(diffs, PlatformDifference{Field: field + ".sha256", A: leftArtifact.Artifact.SHA256, B: rightArtifact.Artifact.SHA256})
		}
		if leftArtifact.Artifact.Size != rightArtifact.Artifact.Size {
			diffs = append(diffs, PlatformDifference{
				Field: field + ".size",
				A:     fmt.Sprintf("%d", leftArtifact.Artifact.Size),
				B:     fmt.Sprintf("%d", rightArtifact.Artifact.Size),
			})
		}
	}
	return diffs
}

func indexArtifacts(refs []ArtifactRef) map[artifactKey]ArtifactRef {
	index := make(map[artifactKey]ArtifactRef, len(refs))
	for _, ref := range refs {
		index[artifactKey{kind: ref.Kind, path: ref.Artifact.Path}] = ref
	}
	return index
}

func presence(ok bool) string {
	if ok {
		return "present"
	}
	return "absent"
}
