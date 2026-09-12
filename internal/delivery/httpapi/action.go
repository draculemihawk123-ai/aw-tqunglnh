package httpapi

// ValidAction is the advisory client-facing hint a projected read response
// may attach next to an entity — V6-02A's own "Thực hiện" line: "ValidAction
// chứa operationId/scope/targetVersion nhưng chỉ advisory". It names an
// operation the entity's current (possibly stale, possibly already
// out-of-date-by-the-time-the-response-is-read) projected state suggests is
// available, and the exact version the entity was at when that suggestion
// was made — never authority. This whole design doc repeats the same
// principle everywhere a command dispatches (e.g. V6-02's own flow:
// "authenticate/authorize → canonical decode → receipt lookup →
// replay/conflict → nếu absent mới kiểm current version"): the real command
// handler always reloads and revalidates live state before doing anything,
// under its own current-version/optimistic-concurrency check, regardless of
// what a client's stale ValidAction claimed was valid a moment earlier. A
// client uses TargetVersion only to populate its own next request's
// concurrency precondition (e.g. an `If-Match`/ExpectedVersion), not to skip
// the server re-deriving whether the action is still actually valid.
type ValidAction struct {
	OperationID   string    `json:"operationId"`
	ScopeKind     ScopeKind `json:"scopeKind"`
	TargetVersion int64     `json:"targetVersion"`
}
