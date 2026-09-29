package maintenance

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
)

// ArtifactRestoreStatus is one manifest entry's own real outcome once
// checked against the restored artifact-store's real files.
type ArtifactRestoreStatus string

const (
	// ArtifactRestoreOK: the real file exists and its real content still
	// hashes/sizes to exactly what the manifest recorded.
	ArtifactRestoreOK ArtifactRestoreStatus = "OK"
	// ArtifactRestoreMissing: the manifest names this artifact, but no real
	// file exists at its Locator in the artifact-store root being checked —
	// V8-06's own "nhận biết missing evidence" bar (a restored system must
	// RECOGNIZE missing evidence, not silently proceed as if it were
	// intact).
	ArtifactRestoreMissing ArtifactRestoreStatus = "MISSING"
	// ArtifactRestoreCorrupt: a real file exists at this Locator, but its
	// content no longer verifies against the manifest's own recorded
	// hash/size (or some other real I/O error prevented verifying it) —
	// V8-06's own "corrupt ... artifact" case, made concrete.
	ArtifactRestoreCorrupt ArtifactRestoreStatus = "CORRUPT"
)

// ArtifactRestoreFinding is one manifest entry's own real, non-OK outcome.
type ArtifactRestoreFinding struct {
	ArtifactID string                `json:"artifactId"`
	Locator    string                `json:"locator"`
	Status     ArtifactRestoreStatus `json:"status"`
	Detail     string                `json:"detail"`
}

// RestoreVerificationReport is VerifyRestoredArtifacts' own real,
// before/after-style audit of the restored artifact store against the
// manifest a prior Backup wrote.
type RestoreVerificationReport struct {
	TotalArtifacts int                      `json:"totalArtifacts"`
	OKCount        int                      `json:"okCount"`
	MissingCount   int                      `json:"missingCount"`
	CorruptCount   int                      `json:"corruptCount"`
	Findings       []ArtifactRestoreFinding `json:"findings"`
}

// Clean reports whether every manifest entry verified OK — the restored
// system's own "no missing/corrupt evidence" bar, made a single boolean a
// CLI/doctor-style caller can branch on.
func (r RestoreVerificationReport) Clean() bool {
	return r.MissingCount == 0 && r.CorruptCount == 0
}

// VerifyRestoredArtifacts checks every entry in manifest against the REAL
// content-addressed store rooted at wherever the operator restored (or
// already keeps) the artifact-store directory — a Purged manifest entry is
// deliberately skipped (its own real content was legitimately, durably
// deleted by a real retention sweep before this backup was even taken;
// V8-05's own sweeper never removes the row, only the bytes, so a Purged
// row correctly having no real content is expected, not evidence of
// corruption).
func VerifyRestoredArtifacts(ctx context.Context, store ports.ArtifactStore, manifest Manifest) RestoreVerificationReport {
	report := RestoreVerificationReport{}
	for _, entry := range manifest.Artifacts {
		if entry.AttachState == string(artifact.Purged) {
			continue
		}
		report.TotalArtifacts++
		ref := ports.ArtifactRef{Locator: entry.Locator, SHA256: entry.ContentHash, Size: entry.Size, ContentType: entry.MediaType}
		err := store.Verify(ctx, ref)
		if err == nil {
			report.OKCount++
			continue
		}
		if apperror.CodeOf(err) == apperror.CodeNotFound {
			report.MissingCount++
			report.Findings = append(report.Findings, ArtifactRestoreFinding{
				ArtifactID: entry.ID, Locator: entry.Locator, Status: ArtifactRestoreMissing,
				Detail: "no file found at this Locator in the restored artifact store",
			})
			continue
		}
		report.CorruptCount++
		report.Findings = append(report.Findings, ArtifactRestoreFinding{
			ArtifactID: entry.ID, Locator: entry.Locator, Status: ArtifactRestoreCorrupt,
			Detail: "content at this Locator no longer verifies against its recorded hash/size: " + err.Error(),
		})
	}
	return report
}
