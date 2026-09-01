package spikeacceptance

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

// EvidenceWriter is the narrow evidence-bundle write surface a ScenarioFunc
// uses to persist its own workflow/runtime/workspace/provider/process
// artifacts as it runs (docs/spikes/01-go-core-spike-plan.md §12). It is
// satisfied by *evidence.Bundle; RunAll owns creating, finalizing and
// verifying the bundle instance a scenario receives, so a ScenarioFunc never
// decides evidence-bundle lifecycle for itself.
type EvidenceWriter interface {
	Put(path string, body []byte) (evidence.Artifact, error)
	PutJSON(path string, value any) (evidence.Artifact, error)
	PutRedacted(path string, body []byte, secrets ...string) (evidence.Artifact, error)
}

// ScenarioFunc executes one SPK-01..SPK-14 acceptance scenario and reports
// its genuine outcome. A ScenarioFunc must always return a well-formed
// SPKResult; a non-nil error signals that the scenario harness itself could
// not run (fixture setup failure, environment problem, ...), not that the
// SPK's contract failed to hold — that case is Passed: false in the result.
// The supplied EvidenceWriter is that scenario's own sealed evidence bundle
// (one bundle per SPK, per docs/design/02-v0-spike-verdict.md V0-08): any
// artifact the scenario writes should show up, kind-tagged, in the
// SPKResult.Artifacts it returns.
type ScenarioFunc func(ctx context.Context, evidence EvidenceWriter) (SPKResult, error)

// ScenarioEntry binds one SPKID to its ScenarioFunc. Registrations are kept
// as a slice, not a map literal, so NewRegistry can detect a caller
// registering the same id twice.
type ScenarioEntry struct {
	SPKID   SPKID
	Handler ScenarioFunc
}

// Registry is a validated, one-to-one mapping from every required SPKID to a
// real, callable ScenarioFunc. It can only be built through NewRegistry.
type Registry struct {
	entries map[SPKID]ScenarioFunc
}

var (
	// ErrScenarioMissing means a required SPK id has no registry entry.
	ErrScenarioMissing = errors.New("full-suite registry is missing a required SPK scenario")
	// ErrScenarioDuplicate means a required SPK id was registered more than once.
	ErrScenarioDuplicate = errors.New("full-suite registry has a duplicate SPK scenario")
	// ErrScenarioNoOp means an SPK id was registered with a nil handler.
	ErrScenarioNoOp = errors.New("full-suite registry has a no-op (nil) SPK scenario handler")
	// ErrScenarioUnknown means a registered SPK id is not one of the
	// fourteen required ids.
	ErrScenarioUnknown = errors.New("full-suite registry has a scenario for an SPK id outside SPK-01..SPK-14")
)

// NewRegistry validates entries the same way NewSPKManifest validates
// results: the fourteen required ids, each exactly once, each bound to a
// non-nil handler, nothing unknown. Any other combination is rejected so a
// full-suite dispatch can never silently skip, duplicate or fabricate an SPK
// scenario.
func NewRegistry(entries []ScenarioEntry) (Registry, error) {
	required := make(map[SPKID]struct{}, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		required[id] = struct{}{}
	}

	counts := make(map[SPKID]int, len(entries))
	handlers := make(map[SPKID]ScenarioFunc, len(entries))
	for _, entry := range entries {
		counts[entry.SPKID]++
		if _, exists := handlers[entry.SPKID]; !exists {
			handlers[entry.SPKID] = entry.Handler
		}
	}

	var problems []error
	for _, id := range RequiredSPKIDs() {
		if counts[id] == 0 {
			problems = append(problems, fmt.Errorf("%w: %s", ErrScenarioMissing, id))
		}
	}
	seenIDs := make([]SPKID, 0, len(counts))
	for id := range counts {
		seenIDs = append(seenIDs, id)
	}
	sort.Slice(seenIDs, func(i, j int) bool { return seenIDs[i] < seenIDs[j] })
	for _, id := range seenIDs {
		if _, ok := required[id]; !ok {
			problems = append(problems, fmt.Errorf("%w: %s", ErrScenarioUnknown, id))
			continue
		}
		if counts[id] > 1 {
			problems = append(problems, fmt.Errorf("%w: %s", ErrScenarioDuplicate, id))
		}
		if handlers[id] == nil {
			problems = append(problems, fmt.Errorf("%w: %s", ErrScenarioNoOp, id))
		}
	}
	if len(problems) > 0 {
		return Registry{}, errors.Join(problems...)
	}

	return Registry{entries: handlers}, nil
}

// RunAll invokes every registered scenario exactly once, in RequiredSPKIDs
// order, and assembles the results into a validated SPKManifest via
// NewSPKManifest. A non-nil error means the dispatch harness itself failed
// for one SPK (see ScenarioFunc); it does not mean an SPK's contract failed
// to hold — that is Passed: false inside the returned manifest.
//
// Each SPK gets its own sealed evidence bundle under evidenceRoot, named
// "<suiteID>-<spkId>" — one bundle per SPK, not one shared bundle, because
// artifact paths inside a bundle (e.g. "runtime/run.json") are not scoped
// per SPK and would collide if fourteen scenarios wrote into the same
// bundle. RunAll finalizes and verifies each bundle itself immediately
// after its scenario returns, before moving to the next SPK: a scenario
// never controls its own bundle's lifecycle. A scenario that errors leaves
// its bundle unfinalized rather than sealed with a partial result;
// evidence.PruneExpired never removes an unfinalized bundle, so it stays
// available for investigation.
func (r Registry) RunAll(ctx context.Context, evidenceRoot, suiteID string, now time.Time) (SPKManifest, error) {
	results := make([]SPKResult, 0, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		handler := r.entries[id]
		if handler == nil {
			return SPKManifest{}, fmt.Errorf("%w: %s", ErrScenarioMissing, id)
		}
		bundle, err := evidence.CreateAt(evidenceRoot, suiteID+"-"+string(id), now)
		if err != nil {
			return SPKManifest{}, fmt.Errorf("create evidence bundle for %s: %w", id, err)
		}
		if _, err := bundle.PutJSON("environment.json", map[string]string{
			"goos": runtime.GOOS, "goarch": runtime.GOARCH, "suiteId": suiteID, "spkId": string(id),
		}); err != nil {
			return SPKManifest{}, fmt.Errorf("write environment for %s: %w", id, err)
		}
		result, err := handler(ctx, bundle)
		if err != nil {
			return SPKManifest{}, fmt.Errorf("run scenario %s: %w", id, err)
		}
		result.SPKID = id
		if _, err := bundle.Finalize(map[string]string{"suiteId": suiteID, "spkId": string(id)}); err != nil {
			return SPKManifest{}, fmt.Errorf("finalize evidence bundle for %s: %w", id, err)
		}
		if _, err := evidence.Verify(bundle.Directory()); err != nil {
			return SPKManifest{}, fmt.Errorf("verify evidence bundle for %s: %w", id, err)
		}
		results = append(results, result)
	}
	return NewSPKManifest(suiteID, now, results)
}
