package runtime

import "strings"

// ExplainedFailure (V11-11, ADR-042) turns a failure code into what an operator needs: what it means in plain words, and what to do
// next. The codes are the closed set of go-core-spec §18; Detail carries the specific facts the run recorded (the violating paths, the
// provider's own words, the revision of a drifted HEAD) when there are any. Action names the web/CLI action that usually fixes it, so a
// client can offer the button; it is advice, never authority.
type ExplainedFailure struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Explanation string `json:"explanation"`
	Suggestion  string `json:"suggestion"`
	Detail      string `json:"detail,omitempty"`
	// Action is one of RETRY_WORK_ITEM, RECREATE_WORK_ITEM, RECONCILE_WORKSPACE, ADOPT_HEAD, CHECK_PROVIDER, "" (nothing to press).
	Action string `json:"action,omitempty"`
}

// ExplainFailure explains code. detail is the recorded specifics for the attempt (may be empty). An unknown code still gets a generic
// explanation that names the code, never an empty one.
func ExplainFailure(code, detail string) ExplainedFailure {
	out := ExplainedFailure{Code: code, Detail: strings.TrimSpace(detail)}
	switch code {
	case "SCOPE_VIOLATION":
		out.Title = "The agent changed files outside its scope"
		out.Explanation = "The work item may only change the paths in its scope, and the attempt (or a file left by an earlier run) touched others."
		out.Suggestion = "Run Again and choose to park the files outside the scope, or widen the scope with a scope expansion if the change is really needed."
		out.Action = "RETRY_WORK_ITEM"
	case "VALIDATION_FAILED":
		if strings.Contains(out.Detail, "WORKSPACE_HEAD_DRIFT") || strings.Contains(strings.ToLower(out.Detail), "head") {
			out.Title = "A commit was made outside aw"
			out.Explanation = "Git's HEAD in the worktree is not the revision aw recorded, so the gate refused to vouch for unvetted history."
			out.Suggestion = "Keep those commits with adopt-head, or put the worktree back on the recorded revision, then run again."
			out.Action = "ADOPT_HEAD"
			break
		}
		out.Title = "The result did not meet the node's contract"
		out.Explanation = "The agent finished, but its final message did not end with a valid outcome marker (or named an outcome the node does not allow)."
		out.Suggestion = "Run Again; the skill tells the agent to end its final message with the marker."
		out.Action = "RETRY_WORK_ITEM"
	case "EXECUTION_FAILED":
		out.Title = "The attempt ended with an error"
		out.Explanation = "The agent or command finished unsuccessfully, or the provider reported a failure."
		out.Suggestion = "Read the output below for the cause. If it was the environment, fix it and Run Again; if the definition is wrong, fix it, publish, and Redo with Latest Version."
		out.Action = "RETRY_WORK_ITEM"
	case "PROVIDER_UNAVAILABLE":
		out.Title = "The provider could not be reached or crashed"
		out.Explanation = "The agent's provider CLI did not start or ended abnormally before producing a result."
		out.Suggestion = "Check that the provider CLI is installed, signed in and allowed by the worker (aw doctor), then Run Again."
		out.Action = "CHECK_PROVIDER"
	case "PROVIDER_QUOTA_EXCEEDED":
		out.Title = "The provider's usage limit was reached"
		out.Explanation = "The provider refused the work because the account hit a session, usage or rate limit. A limit resets in hours, so aw does not retry on its own."
		out.Suggestion = "Wait until the time the provider's message names, then Run Again."
		out.Action = "RETRY_WORK_ITEM"
	case "TIMEOUT":
		out.Title = "The attempt ran out of time"
		out.Explanation = "The node's attempt policy sets a time limit and the attempt did not finish inside it."
		out.Suggestion = "Look at what it was doing last; raise the timeout in the attempt policy (publish, then Redo with Latest Version) or give the agent a smaller task."
		out.Action = "RETRY_WORK_ITEM"
	case "CANCELLED":
		out.Title = "Cancelled"
		out.Explanation = "The run or the work item was cancelled while this node was working."
		out.Suggestion = "Run Again when you want it to continue."
		out.Action = "RETRY_WORK_ITEM"
	case "WORKSPACE_QUARANTINED":
		out.Title = "The workspace is quarantined"
		out.Explanation = "A repository workspace of this task family failed an integrity check and was taken out of use."
		out.Suggestion = "Reconcile the workspace in the Workspace tab, then Run Again."
		out.Action = "RECONCILE_WORKSPACE"
	case "ADAPTER_BUILD_DRIFT":
		out.Title = "The provider build changed"
		out.Explanation = "The provider executable no longer matches the adapter build the workflow was pinned to."
		out.Suggestion = "Register the new build (or restore the old one), then retry the blocked node from the Graph tab."
	case "ISOLATION_ENFORCEMENT_UNAVAILABLE":
		out.Title = "The required isolation is not available"
		out.Explanation = "The node asks for an isolation level this machine cannot enforce right now."
		out.Suggestion = "Run aw doctor to see what is missing, or pick a weaker isolation tier in the definition."
	case "LEASE_LOST", "FENCE_REJECTED", "INDETERMINATE":
		out.Title = "The outcome of the attempt is uncertain"
		out.Explanation = "The worker lost its lease while the attempt was running, so aw cannot tell whether it finished."
		out.Suggestion = "Wait for crash recovery to settle it, then Run Again if the node did not complete."
		out.Action = "RETRY_WORK_ITEM"
	case "RETRY_EXHAUSTED":
		out.Title = "Retries ran out"
		out.Explanation = "The attempt policy allows a fixed number of tries and every one failed."
		out.Suggestion = "Read the failure of the last attempt, fix the cause, then Run Again."
		out.Action = "RETRY_WORK_ITEM"
	case "POLICY_DENIED":
		out.Title = "A policy refused the action"
		out.Explanation = "The permission or approval policy of this node does not allow what was attempted."
		out.Suggestion = "Check the node's policies, or ask someone with the authorized role."
	case "CONFLICT":
		out.Title = "Another attempt holds the worktree"
		out.Explanation = "Another attempt was writing to the same repository workspace, so this one could not start."
		out.Suggestion = "It is retried by the attempt policy; if it keeps failing, cancel the other run."
	default:
		out.Title = "Failed (" + code + ")"
		out.Explanation = "The attempt ended with the code " + code + "."
		out.Suggestion = "Read the output and the recorded detail below; Run Again once the cause is fixed."
		out.Action = "RETRY_WORK_ITEM"
	}
	return out
}
