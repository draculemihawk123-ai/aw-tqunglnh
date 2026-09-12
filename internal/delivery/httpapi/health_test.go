package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestLiveHandler_AlwaysReturns200(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	httpapi.LiveHandler()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestLiveHandler_PassesEvenWhenDependenciesWouldFailReadiness(t *testing.T) {
	// Live must never consult a ReadinessChecker at all — prove it by
	// exercising it alongside a checker whose every check fails, and
	// confirming live is unaffected.
	checker := httpapi.NewReadinessChecker()
	checker.Register("always-fails", func(ctx context.Context) error {
		return errors.New("dependency down")
	})

	liveReq := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	liveRec := httptest.NewRecorder()
	httpapi.LiveHandler()(liveRec, liveReq)
	if liveRec.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200 even while readiness fails", liveRec.Code)
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	readyRec := httptest.NewRecorder()
	checker.ReadyHandler()(readyRec, readyReq)
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503 (sanity: the failing check should actually fail ready)", readyRec.Code)
	}
}

func TestReadyHandler_FailsTypedBeforeStartupThenPassesAfter(t *testing.T) {
	checker := httpapi.NewReadinessChecker()
	startupComplete := false
	checker.Register("startup", func(ctx context.Context) error {
		if !startupComplete {
			return errors.New("startup not complete")
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	checker.ReadyHandler()(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status before startup = %d, want 503", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Fatalf("body status = %v, want not_ready", body["status"])
	}
	checks, ok := body["checks"].([]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("body checks = %v, want exactly one typed failure entry", body["checks"])
	}

	startupComplete = true
	req2 := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec2 := httptest.NewRecorder()
	checker.ReadyHandler()(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status after startup = %d, want 200", rec2.Code)
	}
}

func TestReadyHandler_ReportsEveryFailingCheckByName(t *testing.T) {
	checker := httpapi.NewReadinessChecker()
	checker.Register("database", func(ctx context.Context) error { return errors.New("db down") })
	checker.Register("artifact-root", func(ctx context.Context) error { return nil })
	checker.Register("routes", func(ctx context.Context) error { return errors.New("not finalized") })

	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	rec := httptest.NewRecorder()
	checker.ReadyHandler()(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body struct {
		Checks []struct {
			Check  string `json:"check"`
			Reason string `json:"reason"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Checks) != 2 {
		t.Fatalf("checks = %+v, want exactly 2 (database, routes) — artifact-root passed and must be excluded", body.Checks)
	}
	if body.Checks[0].Check != "database" || body.Checks[1].Check != "routes" {
		t.Fatalf("checks = %+v, want sorted [database, routes]", body.Checks)
	}
}

func TestReadinessChecker_Register_DuplicateNamePanics(t *testing.T) {
	checker := httpapi.NewReadinessChecker()
	checker.Register("db", func(ctx context.Context) error { return nil })
	defer func() {
		if recover() == nil {
			t.Fatal("Register with a duplicate name should panic")
		}
	}()
	checker.Register("db", func(ctx context.Context) error { return nil })
}
