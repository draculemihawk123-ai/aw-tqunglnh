package spikeacceptance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

// SPKID identifies one of the fourteen Go core spike acceptance scenarios
// defined in docs/spikes/01-go-core-spike-plan.md §9.
type SPKID string

const (
	SPK01 SPKID = "SPK-01"
	SPK02 SPKID = "SPK-02"
	SPK03 SPKID = "SPK-03"
	SPK04 SPKID = "SPK-04"
	SPK05 SPKID = "SPK-05"
	SPK06 SPKID = "SPK-06"
	SPK07 SPKID = "SPK-07"
	SPK08 SPKID = "SPK-08"
	SPK09 SPKID = "SPK-09"
	SPK10 SPKID = "SPK-10"
	SPK11 SPKID = "SPK-11"
	SPK12 SPKID = "SPK-12"
	SPK13 SPKID = "SPK-13"
	SPK14 SPKID = "SPK-14"
)

// RequiredSPKIDs returns the fixed, ordered set of SPK ids a full-suite
// verdict SPKManifest must contain, each exactly once.
func RequiredSPKIDs() []SPKID {
	return []SPKID{SPK01, SPK02, SPK03, SPK04, SPK05, SPK06, SPK07, SPK08, SPK09, SPK10, SPK11, SPK12, SPK13, SPK14}
}

// Evidence-bundle sections an ArtifactRef.Kind can name, per
// docs/spikes/01-go-core-spike-plan.md §12.
const (
	ArtifactKindWorkflow   = "workflow"
	ArtifactKindRuntime    = "runtime"
	ArtifactKindWorkspace  = "workspace"
	ArtifactKindProviders  = "providers"
	ArtifactKindProcesses  = "processes"
	ArtifactKindAssertions = "assertions"
)

// Assertion is one named pass/fail check evaluated while executing an SPK
// scenario, e.g. "resume call count == 0".
type Assertion struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// CorrelationIDs ties an SPK result back to the platform identities named in
// docs/spikes/01-go-core-spike-plan.md §12. A field that does not apply to a
// given scenario (for example RepositoryID for a publish-only SPK) is left
// empty rather than guessed.
type CorrelationIDs struct {
	ProjectID    string `json:"projectId,omitempty"`
	FamilyID     string `json:"familyId,omitempty"`
	RunID        string `json:"runId,omitempty"`
	NodeRunID    string `json:"nodeRunId,omitempty"`
	AttemptID    string `json:"attemptId,omitempty"`
	RepositoryID string `json:"repositoryId,omitempty"`
	Revision     string `json:"revision,omitempty"`
}

// Platform records the OS/architecture an SPK scenario actually executed on,
// so Windows/Linux evidence (SPK-13) is never conflated.
type Platform struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

// Timing is the wall-clock span of one SPK scenario execution.
type Timing struct {
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`
}

// ArtifactRef points at one evidence-bundle artifact that backs an SPK
// result, tagged with the bundle section it belongs to (see the
// ArtifactKind* constants).
type ArtifactRef struct {
	Kind     string            `json:"kind"`
	Artifact evidence.Artifact `json:"artifact"`
}

// SPKResult is the typed, machine-checkable outcome of one SPK-01..SPK-14
// scenario. It is not itself a gate verdict; only a validated SPKManifest
// covering all fourteen ids is.
type SPKResult struct {
	SPKID       SPKID          `json:"spkId"`
	Passed      bool           `json:"passed"`
	Assertions  []Assertion    `json:"assertions"`
	Correlation CorrelationIDs `json:"correlation"`
	Platform    Platform       `json:"platform"`
	Timing      Timing         `json:"timing"`
	Artifacts   []ArtifactRef  `json:"artifacts"`
}

// SPKManifest is a validated full-suite SPK-01..SPK-14 verdict record: each
// required id exactly once, nothing missing and nothing extra. It can only
// be constructed through NewSPKManifest, which is what "locks" the schema.
type SPKManifest struct {
	SuiteID     string      `json:"suiteId"`
	GeneratedAt time.Time   `json:"generatedAt"`
	Results     []SPKResult `json:"results"`
}

var (
	// ErrSPKIDMissing means a required SPK id has no result in the manifest.
	ErrSPKIDMissing = errors.New("full-suite manifest is missing a required SPK id")
	// ErrSPKIDDuplicate means a required SPK id has more than one result.
	ErrSPKIDDuplicate = errors.New("full-suite manifest has a duplicate SPK id")
	// ErrSPKIDUnknown means a result's SPKID is not one of the fourteen
	// required ids.
	ErrSPKIDUnknown = errors.New("full-suite manifest has an SPK id outside SPK-01..SPK-14")
	// ErrCorrelationMissing means a result carries an artifact of a kind that
	// requires certain CorrelationIDs fields (see requiredCorrelationFieldsFor)
	// and at least one of those fields is empty.
	ErrCorrelationMissing = errors.New("full-suite manifest result is missing a correlation field required by one of its artifact kinds")
)

// requiredCorrelationFieldsFor names the CorrelationIDs fields a result must
// populate once it carries at least one artifact of the given kind, per
// docs/spikes/01-go-core-spike-plan.md §12: "record liên quan execution phải
// correlate được ít nhất project_id, family_id, run_id, node_run_id,
// attempt_id; record code-related phải có repository_id và exact revision."
//
// Runtime/providers/processes artifacts are execution-related (they only
// exist once a NodeRun/ExecutionAttempt is dispatched), so they take the
// project/family/run/node/attempt baseline. Workspace artifacts are the
// code-related record: they need project/family plus repository/revision,
// but deliberately not run/node/attempt, because SPK-05/06 (WorkspaceSet
// provisioning and root-family isolation) produce workspace evidence with no
// workflow run involved at all. Workflow artifacts (publish/hash — they can
// predate any run, and WorkflowDefinition.ProjectID is itself optional in
// the domain model) and assertions artifacts (a pass/fail summary wrapper,
// not itself an execution or code record) carry no kind-specific
// requirement here.
func requiredCorrelationFieldsFor(kind string) []string {
	switch kind {
	case ArtifactKindRuntime, ArtifactKindProviders, ArtifactKindProcesses:
		return []string{"projectId", "familyId", "runId", "nodeRunId", "attemptId"}
	case ArtifactKindWorkspace:
		return []string{"projectId", "familyId", "repositoryId", "revision"}
	default:
		return nil
	}
}

func correlationFieldValue(correlation CorrelationIDs, field string) string {
	switch field {
	case "projectId":
		return correlation.ProjectID
	case "familyId":
		return correlation.FamilyID
	case "runId":
		return correlation.RunID
	case "nodeRunId":
		return correlation.NodeRunID
	case "attemptId":
		return correlation.AttemptID
	case "repositoryId":
		return correlation.RepositoryID
	case "revision":
		return correlation.Revision
	default:
		return ""
	}
}

// missingCorrelationFields reports, in stable sorted order, every
// CorrelationIDs field result's own artifact kinds require but left empty.
func missingCorrelationFields(result SPKResult) []string {
	required := make(map[string]struct{})
	for _, artifact := range result.Artifacts {
		for _, field := range requiredCorrelationFieldsFor(artifact.Kind) {
			required[field] = struct{}{}
		}
	}
	missing := make([]string, 0, len(required))
	for field := range required {
		if correlationFieldValue(result.Correlation, field) == "" {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

// NewSPKManifest validates results against RequiredSPKIDs and, only if the
// set is exactly the fourteen required ids with no duplicates and nothing
// unknown, returns a sealed SPKManifest. Any other combination is rejected
// so a full SPK gate verdict can never be assembled from a partial or
// malformed result set; RunOfflineBaseline's RunResult is a separate,
// non-verdict type and is never accepted here.
func NewSPKManifest(suiteID string, generatedAt time.Time, results []SPKResult) (SPKManifest, error) {
	if strings.TrimSpace(suiteID) == "" {
		return SPKManifest{}, errors.New("spk manifest suite id is required")
	}
	if generatedAt.IsZero() {
		return SPKManifest{}, errors.New("spk manifest generation time is required")
	}

	required := make(map[SPKID]struct{}, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		required[id] = struct{}{}
	}

	counts := make(map[SPKID]int, len(results))
	for _, result := range results {
		counts[result.SPKID]++
	}

	var problems []error
	for _, id := range RequiredSPKIDs() {
		if counts[id] == 0 {
			problems = append(problems, fmt.Errorf("%w: %s", ErrSPKIDMissing, id))
		}
	}
	seenIDs := make([]SPKID, 0, len(counts))
	for id := range counts {
		seenIDs = append(seenIDs, id)
	}
	sort.Slice(seenIDs, func(i, j int) bool { return seenIDs[i] < seenIDs[j] })
	for _, id := range seenIDs {
		if _, ok := required[id]; !ok {
			problems = append(problems, fmt.Errorf("%w: %s", ErrSPKIDUnknown, id))
			continue
		}
		if counts[id] > 1 {
			problems = append(problems, fmt.Errorf("%w: %s", ErrSPKIDDuplicate, id))
		}
	}
	for _, result := range results {
		if missing := missingCorrelationFields(result); len(missing) > 0 {
			problems = append(problems, fmt.Errorf("%w: %s missing %s",
				ErrCorrelationMissing, result.SPKID, strings.Join(missing, ", ")))
		}
	}
	if len(problems) > 0 {
		return SPKManifest{}, errors.Join(problems...)
	}

	return SPKManifest{
		SuiteID:     suiteID,
		GeneratedAt: generatedAt.UTC(),
		Results:     append([]SPKResult(nil), results...),
	}, nil
}
