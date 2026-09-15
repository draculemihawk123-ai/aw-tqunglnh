package evidence

import (
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

// listEvidenceResponse/listArtifactsResponse wrap their own DTO collection
// in an object (rather than a bare top-level JSON array) so a future field
// (a page cursor, a total count) can be added beside "items" without a
// breaking wire-shape change — mirrors
// internal/delivery/httpapi/workitem's own workItemListResponse doc comment
// reasoning exactly. Neither list route implements V6-02A's own cursor
// pagination: no citation in this task's own "Phạm vi" line asks for it the
// way V6-07's own ListMessages explicitly does, and every Evidence/Artifact
// list this task returns is bounded by construction (every Evidence row for
// one WorkItem, or every Artifact one specific Evidence row's own
// ArtifactReferences names) rather than an open-ended conversation.
type listEvidenceResponse struct {
	Items []runtimeapp.EvidenceDetail `json:"items"`
}

type listArtifactsResponse struct {
	Items []runtimeapp.ArtifactSummary `json:"items"`
}
