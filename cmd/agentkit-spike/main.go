package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
	"github.com/taQuangLing/agent-workflow/internal/spikeacceptance"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "agentkit-spike:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("expected 'acceptance', 'evidence verify' or 'semantic-diff'")
	}
	switch arguments[0] {
	case "acceptance":
		return runAcceptance(arguments[1:])
	case "evidence":
		return runEvidence(arguments[1:])
	case "semantic-diff":
		return runSemanticDiff(arguments[1:])
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func runAcceptance(arguments []string) error {
	flags := flag.NewFlagSet("acceptance", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	offline := flags.Bool("offline", false, "run offline baseline only")
	full := flags.Bool("full", false, "dispatch the SPK-01..SPK-14 registry, one sealed+verified evidence bundle per SPK")
	assessment := flags.Bool("assessment", false, "with --full: exit 0 if the harness/evidence is complete even when some SPKs report false — never claims GO")
	requireAllPass := flags.Bool("require-all-pass", false, "with --full: exit non-zero if any SPK reports false; use for a final gate, not routine CI")
	evidenceDir := flags.String("evidence-dir", "docs/spikes/evidence", "evidence root")
	goExecutable := flags.String("go", "go", "Go executable")
	fakeClaude := flags.String("fake-claude", "", "path to the fake-claude binary (required by SPK-11/SPK-12)")
	fakeCodex := flags.String("fake-codex", "", "path to the fake-codex binary (required by SPK-11/SPK-12)")
	spikeHelper := flags.String("spike-helper", "", "path to the spike-helper binary (required by SPK-06/SPK-07)")
	spikeWorker := flags.String("spike-worker", "", "path to the spike-worker binary (required by SPK-04)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	switch {
	case *offline && *full:
		return fmt.Errorf("--offline and --full are mutually exclusive")
	case *full:
		if *assessment == *requireAllPass {
			return fmt.Errorf("--full requires exactly one of --assessment or --require-all-pass")
		}
		mode := fullSuiteAssessment
		if *requireAllPass {
			mode = fullSuiteRequireAllPass
		}
		return runFullSuite(*evidenceDir, mode, spikeacceptance.ScenarioBinaries{
			FakeClaude: *fakeClaude, FakeCodex: *fakeCodex, SpikeHelper: *spikeHelper, SpikeWorker: *spikeWorker,
		})
	case *offline:
		return runOfflineBaseline(*evidenceDir, *goExecutable)
	default:
		return fmt.Errorf("one of --offline or --full is required")
	}
}

func runOfflineBaseline(evidenceDir, goExecutable string) error {
	workingDir, err := os.Getwd()
	if err != nil {
		return err
	}
	result, err := spikeacceptance.RunOfflineBaseline(context.Background(), spikeacceptance.OSCommandRunner{}, spikeacceptance.RunRequest{
		EvidenceRoot: evidenceDir,
		WorkingDir:   workingDir,
		GoExecutable: goExecutable,
		Now:          time.Now().UTC(),
	})
	fmt.Printf("suite=%s evidence=%s passed=%t\n", result.SuiteID, result.EvidenceDir, result.Passed)
	return err
}

type fullSuiteMode int

const (
	// fullSuiteAssessment exits 0 once the harness/evidence itself is
	// complete — every SPK dispatched, every bundle sealed and verified,
	// the manifest valid — regardless of individual SPK Passed values. This
	// is what CI's "matrix job is green" gate uses (docs/design/02-v0-spike-verdict.md
	// V0-11): it proves the acceptance mechanism works, not that the SPK
	// gate is GO. Only V0-14 declares GO.
	fullSuiteAssessment fullSuiteMode = iota
	// fullSuiteRequireAllPass additionally exits non-zero if any SPK
	// reports Passed: false. Intended for a final closing gate once every
	// SPK is expected to genuinely pass, not for routine CI.
	fullSuiteRequireAllPass
)

// runFullSuite dispatches every registered SPK-01..SPK-14 scenario, each
// into its own sealed and verified evidence bundle under evidenceDir (see
// Registry.RunAll), writes the resulting SPKManifest as its own JSON
// artifact for a later semantic-diff run to consume, and prints each SPK's
// real, current status. A harness failure (err != nil from RunAll — a
// missing handler, an unusable evidence bundle, a malformed manifest, a
// scenario's own setup failing) is always a non-zero exit in both modes;
// see fullSuiteMode for how individual SPK Passed values are handled.
func runFullSuite(evidenceDir string, mode fullSuiteMode, binaries spikeacceptance.ScenarioBinaries) error {
	registry, err := spikeacceptance.NewRegistry(spikeacceptance.DefaultScenarios())
	if err != nil {
		return fmt.Errorf("build SPK registry: %w", err)
	}
	now := time.Now().UTC()
	suiteID := "full-" + now.Format("20060102t150405.000000000z")
	manifest, err := registry.RunAll(context.Background(), evidenceDir, suiteID, now, binaries)
	if err != nil {
		return fmt.Errorf("run SPK registry: %w", err)
	}

	passed := 0
	for _, result := range manifest.Results {
		status := "FAIL"
		if result.Passed {
			status = "PASS"
			passed++
		}
		fmt.Printf("%s %s\n", result.SPKID, status)
	}

	evidenceRootAbs, err := filepath.Abs(evidenceDir)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(evidenceRootAbs, suiteID+"-manifest.json")
	encodedManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode platform manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, encodedManifest, 0o600); err != nil {
		return fmt.Errorf("write platform manifest: %w", err)
	}

	fmt.Printf("suite=%s spk=%d/%d passed=%t evidence-root=%s manifest=%s (one sealed bundle per SPK, named <suite>-<spkId>)\n",
		manifest.SuiteID, passed, len(manifest.Results), passed == len(manifest.Results), evidenceRootAbs, manifestPath)

	if mode == fullSuiteRequireAllPass && passed != len(manifest.Results) {
		return fmt.Errorf("%d/%d SPK(s) failed and --require-all-pass was set", len(manifest.Results)-passed, len(manifest.Results))
	}
	return nil
}

func runEvidence(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "verify" {
		return fmt.Errorf("expected 'evidence verify'")
	}
	flags := flag.NewFlagSet("evidence verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	suiteID := flags.String("suite", "", "suite id")
	evidenceDir := flags.String("evidence-dir", "docs/spikes/evidence", "evidence root")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if err := spikeacceptance.VerifySuite(*evidenceDir, *suiteID); err != nil {
		return err
	}
	abs, err := filepath.Abs(filepath.Join(*evidenceDir, *suiteID))
	if err != nil {
		return err
	}
	fmt.Printf("verified=%s\n", abs)
	return nil
}

// runSemanticDiff reads two platform manifests (see runFullSuite's
// "<suite>-manifest.json" output — normally one from a Windows CI job, one
// from Ubuntu) and, for every SPK except SPK-13 itself, runs
// spikeacceptance.SemanticDiff between the two platforms' results. SPK-13
// cannot conclude anything from a single platform (see notYetProven's
// PENDING_PEER_PLATFORM result in DefaultScenarios); this command is the
// only place that produces its authoritative result, once both platform
// manifests actually exist (docs/design/02-v0-spike-verdict.md V0-11).
func runSemanticDiff(arguments []string) error {
	flags := flag.NewFlagSet("semantic-diff", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	left := flags.String("left", "", "path to the left platform's <suite>-manifest.json (e.g. Windows)")
	right := flags.String("right", "", "path to the right platform's <suite>-manifest.json (e.g. Ubuntu)")
	out := flags.String("out", "", "optional path to write the SPK-13 authoritative SPKResult as plain JSON")
	evidenceDir := flags.String("evidence-dir", "", "optional root to seal the SPK-13 authoritative result as a real, checksum-verified evidence bundle (same treatment as every other SPK's result)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *left == "" || *right == "" {
		return fmt.Errorf("--left and --right are required")
	}
	started := time.Now().UTC()

	leftManifest, err := loadManifest(*left)
	if err != nil {
		return fmt.Errorf("load left manifest: %w", err)
	}
	rightManifest, err := loadManifest(*right)
	if err != nil {
		return fmt.Errorf("load right manifest: %w", err)
	}
	// A manifest is only as trustworthy as its own schema: re-validate both
	// through the same gate RunAll used to produce them (14 required ids,
	// each exactly once, correlation present where an artifact kind
	// requires it). A manifest that merely round-trips through JSON without
	// this check could be truncated or hand-edited and still "load".
	if _, err := spikeacceptance.NewSPKManifest(leftManifest.SuiteID, leftManifest.GeneratedAt, leftManifest.Results); err != nil {
		return fmt.Errorf("left manifest failed schema validation: %w", err)
	}
	if _, err := spikeacceptance.NewSPKManifest(rightManifest.SuiteID, rightManifest.GeneratedAt, rightManifest.Results); err != nil {
		return fmt.Errorf("right manifest failed schema validation: %w", err)
	}

	leftGOOS, err := singlePlatformGOOS(leftManifest.Results)
	if err != nil {
		return fmt.Errorf("left manifest: %w", err)
	}
	rightGOOS, err := singlePlatformGOOS(rightManifest.Results)
	if err != nil {
		return fmt.Errorf("right manifest: %w", err)
	}
	if leftGOOS == rightGOOS {
		return fmt.Errorf("left and right manifests both report GOOS=%s: SPK-13 requires two genuinely different platforms, not the same platform compared against itself", leftGOOS)
	}

	// Bundles are re-verified relative to the directory each manifest itself
	// lives in: RunAll always writes "<evidenceRoot>/<suiteId>-manifest.json"
	// alongside every "<evidenceRoot>/<suiteId>-<spkId>" bundle it seals, so
	// the manifest's own directory is exactly the evidence root to look in.
	leftEvidenceRoot := filepath.Dir(*left)
	rightEvidenceRoot := filepath.Dir(*right)

	leftByID := indexResultsByID(leftManifest.Results)
	rightByID := indexResultsByID(rightManifest.Results)

	var allDiffs []spikeacceptance.PlatformDifference
	comparedSPKs := 0
	for _, id := range spikeacceptance.RequiredSPKIDs() {
		if id == spikeacceptance.SPK13 {
			continue
		}
		leftResult, leftOK := leftByID[id]
		rightResult, rightOK := rightByID[id]
		if !leftOK || !rightOK {
			return fmt.Errorf("SPK %s is missing from one platform manifest (left=%t right=%t)", id, leftOK, rightOK)
		}
		// A manifest's Results are only a claim about what each sealed bundle
		// contains; re-verifying the bundle itself (checksums against its own
		// manifest.json) is what makes that claim trustworthy after the
		// bundle has travelled through an untrusted channel (CI artifact
		// upload/download, a copy, ...). A bundle that fails this is a real
		// harness/integrity failure, not something to diff around.
		if err := spikeacceptance.VerifySuite(leftEvidenceRoot, leftManifest.SuiteID+"-"+string(id)); err != nil {
			return fmt.Errorf("re-verify left evidence bundle for %s: %w", id, err)
		}
		if err := spikeacceptance.VerifySuite(rightEvidenceRoot, rightManifest.SuiteID+"-"+string(id)); err != nil {
			return fmt.Errorf("re-verify right evidence bundle for %s: %w", id, err)
		}
		comparedSPKs++
		diffs := spikeacceptance.SemanticDiff(leftResult, rightResult)
		fmt.Printf("%s diffs=%d\n", id, len(diffs))
		allDiffs = append(allDiffs, diffs...)
	}

	spk13Passed := len(allDiffs) == 0
	fmt.Printf("SPK-13 authoritative passed=%t (%d unexplained difference(s) across %d SPK(s), left=%s/%s right=%s/%s)\n",
		spk13Passed, len(allDiffs), comparedSPKs, leftManifest.SuiteID, leftGOOS, rightManifest.SuiteID, rightGOOS)
	for _, diff := range allDiffs {
		fmt.Printf("  %s\n", diff)
	}

	result := spikeacceptance.SPKResult{
		SPKID:  spikeacceptance.SPK13,
		Passed: spk13Passed,
		Assertions: []spikeacceptance.Assertion{
			{
				Name:   "left and right manifests each pass full SPKManifest schema validation",
				Passed: true,
				Detail: fmt.Sprintf("left suite=%s (%d results) right suite=%s (%d results)", leftManifest.SuiteID, len(leftManifest.Results), rightManifest.SuiteID, len(rightManifest.Results)),
			},
			{
				Name:   "left and right manifests are from two genuinely different platforms",
				Passed: leftGOOS != rightGOOS,
				Detail: fmt.Sprintf("left=%s right=%s", leftGOOS, rightGOOS),
			},
			{
				Name:   "every compared bundle re-verifies (checksums intact after transfer)",
				Passed: true,
				Detail: fmt.Sprintf("%d SPK bundle(s) re-verified on each side", comparedSPKs),
			},
			{
				Name:   "cross-platform semantic diff is empty for every other SPK",
				Passed: spk13Passed,
				Detail: fmt.Sprintf("%d unexplained difference(s) across %d SPK(s)", len(allDiffs), comparedSPKs),
			},
		},
		Platform: spikeacceptance.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   spikeacceptance.Timing{StartedAt: started, EndedAt: time.Now().UTC()},
	}

	if *out != "" {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("encode SPK-13 authoritative result: %w", err)
		}
		if err := os.WriteFile(*out, encoded, 0o600); err != nil {
			return fmt.Errorf("write SPK-13 authoritative result: %w", err)
		}
	}

	if *evidenceDir != "" {
		// Sealed the same way every other SPK's result is sealed by
		// Registry.RunAll (create -> PutJSON -> Finalize -> Verify): SPK-13's
		// authoritative result deserves the identical tamper-evident,
		// checksum-verified evidence bundle treatment, not a loose JSON file.
		diffSuiteID := "diff-" + started.Format("20060102t150405.000000000z")
		bundle, err := evidence.CreateAt(*evidenceDir, diffSuiteID+"-"+string(spikeacceptance.SPK13), started)
		if err != nil {
			return fmt.Errorf("create SPK-13 evidence bundle: %w", err)
		}
		if _, err := bundle.PutJSON("assertions/report.json", result); err != nil {
			return fmt.Errorf("write SPK-13 evidence report: %w", err)
		}
		if _, err := bundle.Finalize(map[string]string{
			"suiteId": diffSuiteID, "spkId": string(spikeacceptance.SPK13),
			"leftSuiteId": leftManifest.SuiteID, "rightSuiteId": rightManifest.SuiteID,
			"leftGoos": leftGOOS, "rightGoos": rightGOOS,
		}); err != nil {
			return fmt.Errorf("finalize SPK-13 evidence bundle: %w", err)
		}
		if _, err := evidence.Verify(bundle.Directory()); err != nil {
			return fmt.Errorf("verify SPK-13 evidence bundle: %w", err)
		}
		fmt.Printf("SPK-13 evidence bundle sealed and verified at %s\n", bundle.Directory())
	}

	if !spk13Passed {
		return fmt.Errorf("SPK-13 semantic diff found %d unexplained difference(s) across %d SPK(s)", len(allDiffs), comparedSPKs)
	}
	return nil
}

func loadManifest(path string) (spikeacceptance.SPKManifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return spikeacceptance.SPKManifest{}, err
	}
	var manifest spikeacceptance.SPKManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return spikeacceptance.SPKManifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}
	return manifest, nil
}

func indexResultsByID(results []spikeacceptance.SPKResult) map[spikeacceptance.SPKID]spikeacceptance.SPKResult {
	index := make(map[spikeacceptance.SPKID]spikeacceptance.SPKResult, len(results))
	for _, result := range results {
		index[result.SPKID] = result
	}
	return index
}

// singlePlatformGOOS returns the one GOOS every result in a manifest was
// recorded under. A manifest whose own results disagree on GOOS cannot have
// come from a single, genuine full-suite run on one platform, so that is
// treated as a harness error rather than silently picking one value.
func singlePlatformGOOS(results []spikeacceptance.SPKResult) (string, error) {
	goos := ""
	for _, result := range results {
		if result.Platform.GOOS == "" {
			continue
		}
		if goos == "" {
			goos = result.Platform.GOOS
			continue
		}
		if goos != result.Platform.GOOS {
			return "", fmt.Errorf("manifest results disagree on platform: %s and %s both present", goos, result.Platform.GOOS)
		}
	}
	if goos == "" {
		return "", fmt.Errorf("no result in this manifest records a platform GOOS")
	}
	return goos, nil
}
