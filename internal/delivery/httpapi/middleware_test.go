package httpapi_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestCorrelationID_GeneratesWhenAbsent(t *testing.T) {
	var seenInContext string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInContext = httpapi.CorrelationIDFromContext(r.Context())
	})
	handler := httpapi.CorrelationID(idsource.Random{})(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seenInContext == "" {
		t.Fatal("correlation ID was not attached to the request context")
	}
	if got := rec.Header().Get(httpapi.CorrelationIDHeader); got != seenInContext {
		t.Fatalf("response header %s = %q, want %q (same ID echoed back)", httpapi.CorrelationIDHeader, got, seenInContext)
	}
}

func TestCorrelationID_PreservesCallerSuppliedID(t *testing.T) {
	var seenInContext string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenInContext = httpapi.CorrelationIDFromContext(r.Context())
	})
	handler := httpapi.CorrelationID(idsource.Random{})(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(httpapi.CorrelationIDHeader, "caller-supplied-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seenInContext != "caller-supplied-id" {
		t.Fatalf("context correlation ID = %q, want caller-supplied-id (must not overwrite a caller-supplied value)", seenInContext)
	}
	if got := rec.Header().Get(httpapi.CorrelationIDHeader); got != "caller-supplied-id" {
		t.Fatalf("response header = %q, want caller-supplied-id echoed back", got)
	}
}

func TestRecover_CatchesPanicAndReturns500(t *testing.T) {
	var logBuf bytes.Buffer
	logger := logging.New(&logBuf, logging.JSON, redact.NewMatcher())
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := httpapi.Recover(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("panic escaped Recover middleware: %v", recovered)
			}
		}()
		handler.ServeHTTP(rec, req)
	}()

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(logBuf.String(), "http handler panic") {
		t.Fatalf("log output = %q, want it to mention the panic", logBuf.String())
	}
}

func TestRecover_DoesNotInterfereWithNormalRequests(t *testing.T) {
	logger := logging.New(io.Discard, logging.JSON, redact.NewMatcher())
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := httpapi.Recover(logger)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 passed through untouched", rec.Code)
	}
}

func TestRecover_NilLoggerStillRecovers(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	handler := httpapi.Recover(nil)(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req) // must not panic itself
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestMaxBytes_RejectsBodyOverLimit(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Error("expected reading an oversized body to fail")
		}
	})
	handler := httpapi.MaxBytes(4)(inner)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("this is way more than 4 bytes"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

func TestMaxBytes_AllowsBodyUnderLimit(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(body) != "ok" {
			t.Fatalf("body = %q, want ok", body)
		}
	})
	handler := httpapi.MaxBytes(1024)(inner)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("ok"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

func TestChain_AppliesInOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+"-in")
				next.ServeHTTP(w, r)
				order = append(order, name+"-out")
			})
		}
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})
	handler := httpapi.Chain(inner, mw("outer"), mw("inner"))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"outer-in", "inner-in", "handler", "inner-out", "outer-out"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
