package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

func TestWriteError_WritesCanonicalEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteError(rec, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "bad input",
		[]httpapi.ErrorDetail{{Field: "name", Message: "is required"}})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	want := httpapi.ErrorResponse{Error: httpapi.ErrorBody{
		Code: httpapi.ErrorCodeInvalidRequest, Message: "bad input",
		Details: []httpapi.ErrorDetail{{Field: "name", Message: "is required"}},
	}}
	if got.Error.Code != want.Error.Code || got.Error.Message != want.Error.Message ||
		len(got.Error.Details) != len(want.Error.Details) || got.Error.Details[0] != want.Error.Details[0] {
		t.Fatalf("body = %+v, want %+v", got, want)
	}
}

// TestWriteResourceHidden_NotFoundAndUnauthorizedProduceIdenticalResponse
// is V6-02A's own central leakage-normalization proof: a handler branch
// that decided "this resource genuinely does not exist" and a handler
// branch that decided "this resource exists but the caller's scope cannot
// see it" must write byte-for-byte identical responses — otherwise an
// attacker could enumerate real IDs by noticing which of the two a guessed
// ID produces.
func TestWriteResourceHidden_NotFoundAndUnauthorizedProduceIdenticalResponse(t *testing.T) {
	notFoundRec := httptest.NewRecorder()
	httpapi.WriteResourceHidden(notFoundRec) // simulates: lookup found nothing

	unauthorizedRec := httptest.NewRecorder()
	httpapi.WriteResourceHidden(unauthorizedRec) // simulates: lookup found it, but caller's scope can't see it

	if notFoundRec.Code != unauthorizedRec.Code {
		t.Fatalf("status codes differ: not-found=%d, unauthorized=%d", notFoundRec.Code, unauthorizedRec.Code)
	}
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", notFoundRec.Code, http.StatusNotFound)
	}
	if notFoundRec.Body.String() != unauthorizedRec.Body.String() {
		t.Fatalf("bodies differ: not-found=%q, unauthorized=%q", notFoundRec.Body.String(), unauthorizedRec.Body.String())
	}
	if notFoundRec.Header().Get("Content-Type") != unauthorizedRec.Header().Get("Content-Type") {
		t.Fatal("Content-Type headers differ between not-found and unauthorized responses")
	}
}

func TestWriteDecodeError_BodyTooLarge(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteDecodeError(rec, httpapi.ErrBodyTooLarge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("code = %s, want %s", got.Error.Code, httpapi.ErrorCodeInvalidRequest)
	}
}

func TestWriteDecodeError_MalformedJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteDecodeError(rec, httpapi.ErrMalformedJSON)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("code = %s, want %s", got.Error.Code, httpapi.ErrorCodeInvalidRequest)
	}
}

func TestWriteResyncRequired_WritesTypedResyncResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteResyncRequired(rec, httpapi.ResyncReasonGenerationChanged)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeResyncRequired {
		t.Fatalf("code = %s, want %s", got.Error.Code, httpapi.ErrorCodeResyncRequired)
	}
	if len(got.Error.Details) != 1 || got.Error.Details[0].Message != string(httpapi.ResyncReasonGenerationChanged) {
		t.Fatalf("details = %+v, want reason GENERATION_CHANGED", got.Error.Details)
	}
}

func TestWriteCursorInvalid_Writes400InvalidRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteCursorInvalid(rec)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("code = %s, want %s", got.Error.Code, httpapi.ErrorCodeInvalidRequest)
	}
}

func TestStatusForAppErrorCode_MapsKnownCodes(t *testing.T) {
	tests := []struct {
		code       apperror.Code
		wantStatus int
		wantCode   httpapi.ErrorCode
	}{
		{errorcode.CodeInvalidArgument, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest},
		{errorcode.CodeValidationFailed, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest},
		{errorcode.CodeNotFound, http.StatusNotFound, httpapi.ErrorCodeNotFound},
		{errorcode.CodeAlreadyExists, http.StatusConflict, httpapi.ErrorCodeConflict},
		{errorcode.CodeConflict, http.StatusConflict, httpapi.ErrorCodeConflict},
		{errorcode.CodeIdempotencyConflict, http.StatusConflict, httpapi.ErrorCodeConflict},
		{errorcode.CodePreconditionFailed, http.StatusPreconditionFailed, httpapi.ErrorCodeConflict},
		{errorcode.CodeWorkspaceQuarantined, http.StatusLocked, httpapi.ErrorCodeConflict},
		{errorcode.CodePolicyDenied, http.StatusForbidden, httpapi.ErrorCodeForbidden},
		{errorcode.CodeScopeViolation, http.StatusForbidden, httpapi.ErrorCodeForbidden},
		{errorcode.CodeUnavailable, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable},
		{errorcode.CodeProviderUnavailable, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable},
		{errorcode.CodeIsolationEnforcementUnavailable, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable},
		{errorcode.CodeTimeout, http.StatusGatewayTimeout, httpapi.ErrorCodeUnavailable},
		{errorcode.CodeInternal, http.StatusInternalServerError, httpapi.ErrorCodeInternal},
		{errorcode.CodeIndeterminate, http.StatusInternalServerError, httpapi.ErrorCodeInternal},
		{errorcode.Code("SOME_FUTURE_UNKNOWN_CODE"), http.StatusInternalServerError, httpapi.ErrorCodeInternal},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			gotStatus, gotCode := httpapi.StatusForAppErrorCode(tt.code)
			if gotStatus != tt.wantStatus || gotCode != tt.wantCode {
				t.Fatalf("StatusForAppErrorCode(%s) = (%d, %s), want (%d, %s)", tt.code, gotStatus, gotCode, tt.wantStatus, tt.wantCode)
			}
		})
	}
}

func TestWriteAppError_UsesMappedStatusAndMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteAppError(rec, apperror.New(errorcode.CodeConflict, "version already advanced", false))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeConflict || got.Error.Message != "version already advanced" {
		t.Fatalf("body = %+v, want code=%s message=%q", got.Error, httpapi.ErrorCodeConflict, "version already advanced")
	}
}

func TestWriteAppError_NonAppErrorWritesGenericInternal(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteAppError(rec, os.ErrNotExist) // a plain, non-*apperror.Error error

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got.Error.Code != httpapi.ErrorCodeInternal {
		t.Fatalf("code = %s, want %s", got.Error.Code, httpapi.ErrorCodeInternal)
	}
	if got.Error.Message == os.ErrNotExist.Error() {
		t.Fatal("WriteAppError must never expose a non-apperror error's own raw message text")
	}
}

// TestErrorResponse_GoldenFixtureDecodes proves ErrorResponse's wire shape
// is frozen, mirroring internal/app/catalog/event_schema_test.go's own
// golden-fixture-decode pattern.
func TestErrorResponse_GoldenFixtureDecodes(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "error_response_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal golden fixture: %v", err)
	}
	want := httpapi.ErrorResponse{Error: httpapi.ErrorBody{
		Code:    httpapi.ErrorCodeNotFound,
		Message: "the requested resource was not found",
		Details: []httpapi.ErrorDetail{{Field: "workItemId", Message: "no work item matches this id in the caller's visible scope"}},
	}}
	if got.Error.Code != want.Error.Code || got.Error.Message != want.Error.Message ||
		len(got.Error.Details) != len(want.Error.Details) || got.Error.Details[0] != want.Error.Details[0] {
		t.Fatalf("ErrorResponse golden decode = %+v, want %+v", got, want)
	}
}

// TestErrorResponse_RoundTripsThroughMarshal mirrors
// TestErrorResponse_GoldenFixtureDecodes above with a produced value.
func TestErrorResponse_RoundTripsThroughMarshal(t *testing.T) {
	produced := httpapi.ErrorResponse{Error: httpapi.ErrorBody{Code: httpapi.ErrorCodeConflict, Message: "stale version"}}
	raw, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced ErrorResponse: %v", err)
	}
	var got httpapi.ErrorResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal marshaled ErrorResponse: %v", err)
	}
	if got.Error.Code != produced.Error.Code || got.Error.Message != produced.Error.Message {
		t.Fatalf("round-trip = %+v, want %+v", got, produced)
	}
}
