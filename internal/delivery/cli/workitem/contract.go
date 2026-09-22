package workitem

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
)

// acceptanceCriterionBody is the wire shape of one acceptance criterion inside
// workItemContractBody — mirrors
// internal/delivery/httpapi/workitem/dto.go's own acceptanceCriterionBody
// exactly (field order and tags included: this struct is json.Marshal'd into
// the command's NormalizedPayload, and an HTTP call and a CLI call for the
// same request must hash identically — see helpers.go's own commandType
// constants comment).
type acceptanceCriterionBody struct {
	Description     string `json:"description"`
	VerificationRef string `json:"verificationRef,omitempty"`
}

// workItemContractBody is the optional "contract" object `work-item create`
// and `work-item create-child` accept inside their own --file/stdin JSON body
// (V6-04B) — the identical document shape POST /projects/{projectId}/work-items
// and .../children accept over HTTP, mirroring
// internal/delivery/httpapi/workitem/dto.go's own workItemContractBody. It
// rides in the same bounded body those two leaves already read via
// cli.ReadBoundedInput rather than behind a separate flag: the body is
// already one JSON document with the HTTP request's own shape, and a second
// bounded read (a --contract-file) could not share stdin with it.
type workItemContractBody struct {
	SchemaVersion      int                       `json:"schemaVersion,omitempty"`
	Behavior           string                    `json:"behavior,omitempty"`
	AcceptanceCriteria []acceptanceCriterionBody `json:"acceptanceCriteria,omitempty"`
	VerificationSpec   string                    `json:"verificationSpec,omitempty"`
	RiskLevel          string                    `json:"riskLevel,omitempty"`
	Exclusions         []string                  `json:"exclusions,omitempty"`
	WorkflowVersionID  string                    `json:"workflowVersionId,omitempty"`
}

// UnmarshalJSON decodes the contract object strictly: an unknown key —
// anywhere inside it, acceptance-criterion entries included — is an error.
// HTTP already rejects such a body (DecodeJSON's DisallowUnknownFields); this
// leaf's own body decode is deliberately lenient about unknown TOP-LEVEL keys
// (unchanged since V6-15G), but a contract is the one place a silently dropped
// typo (say "acceptanceCriterias") would leave a WorkItem quietly half-specified
// and unable to ever reach READY, so this object alone is held to the HTTP
// standard. The plain alias has no methods, so decoding it cannot recurse into
// this method.
func (c *workItemContractBody) UnmarshalJSON(data []byte) error {
	type plain workItemContractBody
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*plain)(c))
}

// toRequest converts c into the application-layer request; a nil c (no
// "contract" key in the body) stays nil, which is exactly "create the WorkItem
// with an empty contract, as before".
func (c *workItemContractBody) toRequest() *workapp.WorkItemContractRequest {
	if c == nil {
		return nil
	}
	req := &workapp.WorkItemContractRequest{
		SchemaVersion: c.SchemaVersion, Behavior: c.Behavior, VerificationSpec: c.VerificationSpec,
		RiskLevel: c.RiskLevel, Exclusions: c.Exclusions, WorkflowVersionID: c.WorkflowVersionID,
	}
	if len(c.AcceptanceCriteria) > 0 {
		req.AcceptanceCriteria = make([]workapp.AcceptanceCriterionRequest, 0, len(c.AcceptanceCriteria))
		for _, criterion := range c.AcceptanceCriteria {
			req.AcceptanceCriteria = append(req.AcceptanceCriteria, workapp.AcceptanceCriterionRequest{
				Description: criterion.Description, VerificationRef: criterion.VerificationRef,
			})
		}
	}
	return req
}

// validateContractBody runs the application layer's own
// WorkItemContractRequest.Validate — the one rule set the HTTP handler and the
// commands themselves also use, never a second copy — before dispatching, so a
// malformed contract gets a precise cli.UsageError naming every offending field
// instead of falling through to the command's own generic error.
func validateContractBody(c *workItemContractBody) error {
	err := c.toRequest().Validate()
	if err == nil {
		return nil
	}
	var invalid *workapp.InvalidWorkItemContractError
	if errors.As(err, &invalid) {
		parts := make([]string, 0, len(invalid.Problems))
		for _, problem := range invalid.Problems {
			parts = append(parts, fmt.Sprintf("contract.%s %s", problem.Field, problem.Message))
		}
		return usageErrorf("%s", strings.Join(parts, "; "))
	}
	return err
}
