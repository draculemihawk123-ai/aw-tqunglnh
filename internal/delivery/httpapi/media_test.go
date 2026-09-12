package httpapi_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestParseRange_NoHeaderReturnsNotPresent(t *testing.T) {
	rng, present, err := httpapi.ParseRange("", 1000)
	if err != nil || present {
		t.Fatalf("ParseRange(\"\", 1000) = (%+v, %v, %v), want (_, false, nil)", rng, present, err)
	}
}

func TestParseRange_SimpleRange(t *testing.T) {
	rng, present, err := httpapi.ParseRange("bytes=100-199", 1000)
	if err != nil {
		t.Fatalf("ParseRange: %v", err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if rng.Start != 100 || rng.End != 199 || rng.Length() != 100 {
		t.Fatalf("rng = %+v, want Start=100 End=199 Length=100", rng)
	}
}

func TestParseRange_OpenEndedRange(t *testing.T) {
	rng, present, err := httpapi.ParseRange("bytes=900-", 1000)
	if err != nil || !present {
		t.Fatalf("ParseRange = (%+v, %v, %v)", rng, present, err)
	}
	if rng.Start != 900 || rng.End != 999 {
		t.Fatalf("rng = %+v, want Start=900 End=999", rng)
	}
}

func TestParseRange_SuffixRange(t *testing.T) {
	rng, present, err := httpapi.ParseRange("bytes=-100", 1000)
	if err != nil || !present {
		t.Fatalf("ParseRange = (%+v, %v, %v)", rng, present, err)
	}
	if rng.Start != 900 || rng.End != 999 {
		t.Fatalf("rng = %+v, want Start=900 End=999 (last 100 bytes)", rng)
	}
}

func TestParseRange_SuffixRangeLargerThanContent_ClampsToWholeContent(t *testing.T) {
	rng, present, err := httpapi.ParseRange("bytes=-5000", 1000)
	if err != nil || !present {
		t.Fatalf("ParseRange = (%+v, %v, %v)", rng, present, err)
	}
	if rng.Start != 0 || rng.End != 999 {
		t.Fatalf("rng = %+v, want Start=0 End=999", rng)
	}
}

func TestParseRange_EndBeyondContentLength_ClampsToContentLength(t *testing.T) {
	rng, present, err := httpapi.ParseRange("bytes=500-9999", 1000)
	if err != nil || !present {
		t.Fatalf("ParseRange = (%+v, %v, %v)", rng, present, err)
	}
	if rng.Start != 500 || rng.End != 999 {
		t.Fatalf("rng = %+v, want Start=500 End=999 (clamped)", rng)
	}
}

func TestParseRange_StartBeyondContentLength_NotSatisfiable(t *testing.T) {
	_, _, err := httpapi.ParseRange("bytes=1000-1001", 1000)
	if !errors.Is(err, httpapi.ErrRangeNotSatisfiable) {
		t.Fatalf("err = %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestParseRange_ReversedRange_NotSatisfiable(t *testing.T) {
	_, _, err := httpapi.ParseRange("bytes=500-100", 1000)
	if !errors.Is(err, httpapi.ErrRangeNotSatisfiable) {
		t.Fatalf("err = %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestParseRange_MultipleRanges_NotSatisfiable(t *testing.T) {
	_, _, err := httpapi.ParseRange("bytes=0-99,200-299", 1000)
	if !errors.Is(err, httpapi.ErrRangeNotSatisfiable) {
		t.Fatalf("err = %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestParseRange_MissingBytesPrefix_NotSatisfiable(t *testing.T) {
	_, _, err := httpapi.ParseRange("items=0-99", 1000)
	if !errors.Is(err, httpapi.ErrRangeNotSatisfiable) {
		t.Fatalf("err = %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestParseRange_ZeroContentLength_NotSatisfiable(t *testing.T) {
	_, _, err := httpapi.ParseRange("bytes=0-10", 0)
	if !errors.Is(err, httpapi.ErrRangeNotSatisfiable) {
		t.Fatalf("err = %v, want ErrRangeNotSatisfiable", err)
	}
}

func TestApplyPartialContentHeaders_SetsExpectedHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.ApplyPartialContentHeaders(rec, httpapi.ByteRange{Start: 100, End: 199}, 1000)

	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", got)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 100-199/1000" {
		t.Fatalf("Content-Range = %q, want bytes 100-199/1000", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "100" {
		t.Fatalf("Content-Length = %q, want 100", got)
	}
}

func TestWriteRangeNotSatisfiable_Writes416WithContentRange(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.WriteRangeNotSatisfiable(rec, 1000)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestedRangeNotSatisfiable)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes */1000" {
		t.Fatalf("Content-Range = %q, want bytes */1000", got)
	}
}

func TestResolveMediaDisposition_SafeTypesAreInline(t *testing.T) {
	safe := []string{"text/plain", "text/csv", "application/json", "image/png", "image/jpeg", "image/gif", "application/pdf"}
	for _, ct := range safe {
		if got := httpapi.ResolveMediaDisposition(ct); got != httpapi.MediaDispositionInline {
			t.Errorf("ResolveMediaDisposition(%q) = %s, want inline", ct, got)
		}
	}
}

// TestResolveMediaDisposition_ScriptCapableTypesAreForcedToDownload proves
// V6-07B's own "no-sniff/download policy" line: a content type a browser
// could execute as a page (HTML, SVG) must never be marked inline, no
// matter what the caller's own stored Content-Type claims.
func TestResolveMediaDisposition_ScriptCapableTypesAreForcedToDownload(t *testing.T) {
	dangerous := []string{"text/html", "text/html; charset=utf-8", "image/svg+xml", "application/xhtml+xml"}
	for _, ct := range dangerous {
		if got := httpapi.ResolveMediaDisposition(ct); got != httpapi.MediaDispositionAttachment {
			t.Errorf("ResolveMediaDisposition(%q) = %s, want attachment", ct, got)
		}
	}
}

func TestApplyContentHeaders_SetsNoSniffAndDisposition(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.ApplyContentHeaders(rec, "text/html", "evil.html")

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="evil.html"` {
		t.Fatalf("Content-Disposition = %q, want attachment for text/html", got)
	}
}

func TestApplyContentHeaders_InlineSafeTypeIsInline(t *testing.T) {
	rec := httptest.NewRecorder()
	httpapi.ApplyContentHeaders(rec, "image/png", "screenshot.png")

	if got := rec.Header().Get("Content-Disposition"); got != `inline; filename="screenshot.png"` {
		t.Fatalf("Content-Disposition = %q, want inline for image/png", got)
	}
}
