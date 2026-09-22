// The optional WorkItem readiness contract CreateRootWorkItem and
// CreateChildWorkItem accept at creation time (V6-04B — a rework of V6-04,
// found by V6-14's black-box acceptance journey).
//
// Why this file exists: work.WorkItem's own contract fields (SchemaVersion,
// Behavior, AcceptanceCriteria, VerificationSpec, RiskLevel, Exclusions,
// WorkflowVersionID) were introduced by V3-03 and given real sqlite columns
// by migration 0007 / V6-04A, but no public command ever populated them —
// commands.go's own original doc comment called that "a later step no V3
// task builds yet". The consequence, proven by V6-14 through the public
// surface alone: every WorkItem any HTTP or CLI caller could create stayed
// BACKLOG with an empty contract, MarkWorkItemReady (V6-04A) therefore always
// answered "not ready", and runtime.StartWorkflowRun (which requires READY)
// could never admit a Run. The UX artifact's own "Create WorkItem" screen
// (docs/design/11-v6-00-ux-artifact.md, Screen 6) always intended the create
// form to collect WHAT/DONE/scope/out-of-scope/workflow version in one go, for
// root AND child — so the fix is to let the two existing create commands carry
// the contract, not to invent a third "edit contract" command.
//
// What this deliberately is NOT:
//
//   - It never calls work.ValidateReadinessGate. A WorkItem may legitimately be
//     created BACKLOG with a partial contract (or none at all — the pre-V6-04B
//     behaviour, byte for byte); readiness stays MarkWorkItemReady's and
//     ExplainWorkItemReadiness's job, evaluated fresh against whatever is
//     stored, exactly as before.
//   - It never accepts a status, family or workspace of any kind: the request
//     type below has no such field, so a caller cannot even attempt to smuggle
//     one in (the HTTP body DTO mirrors this exactly, and internal/archtest's
//     TestWorkItemPackageRequestBodiesNeverAcceptAStatusField keeps guarding
//     the wire side).
//   - It never carries an ApprovalException. Migration 0007 added no column
//     for it, so there is nowhere to persist one; accepting it here would
//     silently drop it. That gap is unchanged and stays a future task's job.
//   - It never resolves an AcceptanceCriterion.VerificationRef against a real
//     CommandDefinition/GateDefinition — that needs the definition registry
//     (see work.ValidateReadinessGate's own doc comment) and is not part of
//     "carry the contract at creation".
//
// The one piece of I/O it does perform is checking that WorkflowVersionID,
// when given, names a real WorkflowVersion: the work_items column is a foreign
// key, so an unknown ID would otherwise surface as the sqlite adapter's
// generic "unexpected error" (an HTTP 500) for what is plainly a caller
// mistake. Existence only — whether that version belongs to a definition
// visible to the WorkItem's project is not decided here (runtime.
// StartWorkflowRun, the only consumer of the pin, compares IDs only, and the
// domain type's own doc comment already draws that same boundary).
package work

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// ErrUnknownWorkflowVersion is returned by CreateRootWorkItem/
// CreateChildWorkItem when WorkItemContractRequest.WorkflowVersionID names no
// WorkflowVersion at all — a request-shape problem the caller can fix, never
// an internal failure.
var ErrUnknownWorkflowVersion = errors.New("work: contract.workflowVersionId does not name a published WorkflowVersion")

// AcceptanceCriterionRequest is one acceptance criterion of a
// WorkItemContractRequest — a plain mirror of work.AcceptanceCriterion. An
// empty VerificationRef is a legitimate, descriptive-only criterion; it simply
// does not count as executable for work.ValidateReadinessGate's own
// "no executable acceptance criterion" rule.
type AcceptanceCriterionRequest struct {
	Description     string
	VerificationRef string
}

// WorkItemContractRequest is the optional readiness contract a caller may
// attach to a WorkItem at creation. Every field is optional individually: it
// is applied verbatim onto the new WorkItem's exported contract fields, and
// whether the result is complete enough for READY is decided later, fresh, by
// MarkWorkItemReady/ExplainWorkItemReadiness (never here). Zero values mean
// "not given": SchemaVersion 0, empty strings, empty slices and an empty
// WorkflowVersionID all leave the corresponding WorkItem field unset.
type WorkItemContractRequest struct {
	SchemaVersion      int
	Behavior           string
	AcceptanceCriteria []AcceptanceCriterionRequest
	VerificationSpec   string
	RiskLevel          string
	Exclusions         []string
	// WorkflowVersionID optionally pins the WorkflowVersion this WorkItem is
	// intended to run under; see this file's own doc comment for the one
	// existence check applied to it.
	WorkflowVersionID string
}

// ContractProblem is one field-level reason a WorkItemContractRequest was
// rejected. Field is relative to the contract object and uses the same
// vocabulary as its JSON keys (e.g. "acceptanceCriteria[0].description"), so
// a delivery layer can prefix it with "contract." without translating.
type ContractProblem struct {
	Field   string
	Message string
}

// InvalidWorkItemContractError lists every reason a WorkItemContractRequest
// can never be a valid contract — reported all at once, the same "collect
// every problem" shape work.ReadinessError already uses.
type InvalidWorkItemContractError struct {
	Problems []ContractProblem
}

func (e *InvalidWorkItemContractError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, problem.Field+" "+problem.Message)
	}
	return "work: invalid WorkItem contract: " + strings.Join(parts, "; ")
}

// Validate rejects only shapes that can never become a valid contract — a
// negative schema version, an acceptance criterion with no statement, a blank
// exclusion entry or a blank workflow version ID. Absence is never a problem
// here (a partial contract is legitimate at creation): "is the contract
// complete" is work.ValidateReadinessGate's question, asked later. The
// per-field rules deliberately mirror that validator's own (blank exclusion
// entries and a blank pinned workflow version are exactly what it would
// report), so a contract Validate accepts can only ever fail readiness for a
// reason a later, complete contract could cure. A nil receiver is valid (no
// contract given). It returns nil or an *InvalidWorkItemContractError.
func (c *WorkItemContractRequest) Validate() error {
	if c == nil {
		return nil
	}
	var problems []ContractProblem
	if c.SchemaVersion < 0 {
		problems = append(problems, ContractProblem{Field: "schemaVersion", Message: "must not be negative"})
	}
	for i, criterion := range c.AcceptanceCriteria {
		if strings.TrimSpace(criterion.Description) == "" {
			problems = append(problems, ContractProblem{Field: fmt.Sprintf("acceptanceCriteria[%d].description", i), Message: "is required"})
		}
	}
	for i, exclusion := range c.Exclusions {
		if strings.TrimSpace(exclusion) == "" {
			problems = append(problems, ContractProblem{Field: fmt.Sprintf("exclusions[%d]", i), Message: "must not be blank"})
		}
	}
	if c.WorkflowVersionID != "" && strings.TrimSpace(c.WorkflowVersionID) == "" {
		problems = append(problems, ContractProblem{Field: "workflowVersionId", Message: "must not be blank"})
	}
	if len(problems) > 0 {
		return &InvalidWorkItemContractError{Problems: problems}
	}
	return nil
}

// applyWorkItemContract copies c onto item's exported contract fields — called
// right after the domain constructor and before persistence, inside the
// command's own write transaction, so a contract that fails to apply (an
// unknown WorkflowVersionID) rolls back everything else the command wrote. A
// nil c is a no-op: item stays exactly what the constructor built.
func applyWorkItemContract(ctx context.Context, tx ports.Tx, item *workdomain.WorkItem, c *WorkItemContractRequest) error {
	if c == nil {
		return nil
	}
	item.SchemaVersion = c.SchemaVersion
	item.Behavior = c.Behavior
	item.VerificationSpec = c.VerificationSpec
	item.RiskLevel = workdomain.RiskLevel(c.RiskLevel)
	if len(c.AcceptanceCriteria) > 0 {
		item.AcceptanceCriteria = make([]workdomain.AcceptanceCriterion, 0, len(c.AcceptanceCriteria))
		for _, criterion := range c.AcceptanceCriteria {
			item.AcceptanceCriteria = append(item.AcceptanceCriteria, workdomain.AcceptanceCriterion{
				Description: criterion.Description, VerificationRef: criterion.VerificationRef,
			})
		}
	}
	if len(c.Exclusions) > 0 {
		item.Exclusions = append([]string(nil), c.Exclusions...)
	}
	if c.WorkflowVersionID != "" {
		if _, err := tx.Definitions().GetWorkflowVersion(ctx, c.WorkflowVersionID); err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				return fmt.Errorf("%w: %s", ErrUnknownWorkflowVersion, c.WorkflowVersionID)
			}
			return err
		}
		versionID := workflow.WorkflowVersionID(c.WorkflowVersionID)
		item.WorkflowVersionID = &versionID
	}
	return nil
}
