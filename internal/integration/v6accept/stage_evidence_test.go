package v6accept

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

// evidence reads back what the verification Run recorded, through the public
// evidence routes only: the COMMAND execution and the MACHINE_GATE verdict
// each left evidence bound to this Run, every artifact they reference can be
// downloaded, and each download hashes to the digest the API reported for it —
// so a tampered blob could not go unnoticed.
func (j *journey) evidence(t *testing.T) {
	api := j.s.api
	base := "/projects/" + j.projectID + "/work-items/" + j.childWorkItemID

	type evidenceItem struct {
		EvidenceID         string   `json:"evidenceId"`
		RunID              string   `json:"runId"`
		Kind               string   `json:"kind"`
		Verdict            string   `json:"verdict"`
		ArtifactReferences []string `json:"artifactReferences"`
		RevisionSetHash    string   `json:"revisionSetHash"`
	}
	var list struct {
		Items []evidenceItem `json:"items"`
	}
	api.get(t, base+"/evidence").requireStatus(t, http.StatusOK).decode(t, &list)

	byKind := map[string]evidenceItem{}
	for _, item := range list.Items {
		if item.RunID != j.runID {
			t.Errorf("evidence %s belongs to run %s, want %s", item.EvidenceID, item.RunID, j.runID)
		}
		if item.RevisionSetHash == "" {
			t.Errorf("evidence %s has no revision-set hash", item.EvidenceID)
		}
		byKind[item.Kind] = item
	}
	for _, kind := range []string{"COMMAND_EXECUTION", gateEvidenceKey} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("no %s evidence recorded for the run (kinds seen: %v)", kind, keysOf(byKind))
		}
	}
	if verdict := byKind[gateEvidenceKey].Verdict; verdict != "PASS" {
		t.Fatalf("%s evidence verdict = %s, want PASS", gateEvidenceKey, verdict)
	}

	// The single-item route agrees with the list.
	for _, item := range list.Items {
		var one evidenceItem
		api.get(t, base+"/evidence/"+item.EvidenceID).requireStatus(t, http.StatusOK).decode(t, &one)
		if one.EvidenceID != item.EvidenceID || one.Kind != item.Kind || one.Verdict != item.Verdict || one.RevisionSetHash != item.RevisionSetHash {
			t.Errorf("GET evidence %s = %+v, differs from the list entry %+v", item.EvidenceID, one, item)
		}
	}

	downloaded := 0
	for _, item := range list.Items {
		var artifacts struct {
			Items []struct {
				ArtifactID  string `json:"artifactId"`
				ContentHash string `json:"contentHash"`
				Size        int64  `json:"size"`
			} `json:"items"`
		}
		api.get(t, base+"/evidence/"+item.EvidenceID+"/artifacts").requireStatus(t, http.StatusOK).decode(t, &artifacts)
		for _, artifact := range artifacts.Items {
			content := api.get(t, base+"/evidence/"+item.EvidenceID+"/artifacts/"+artifact.ArtifactID+"/content").requireStatus(t, http.StatusOK)
			sum := sha256.Sum256(content.body)
			digest := hex.EncodeToString(sum[:])
			if artifact.ContentHash != digest && artifact.ContentHash != "sha256:"+digest {
				t.Errorf("artifact %s content hashes to %s, the API reported %s", artifact.ArtifactID, digest, artifact.ContentHash)
			}
			if int64(len(content.body)) != artifact.Size {
				t.Errorf("artifact %s is %d bytes, the API reported %d", artifact.ArtifactID, len(content.body), artifact.Size)
			}
			downloaded++
		}
	}
	if downloaded == 0 {
		t.Fatal("the run's evidence references no downloadable artifact: nothing to verify")
	}
	j.extraSnapshotPaths = append(j.extraSnapshotPaths, base+"/evidence")
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
