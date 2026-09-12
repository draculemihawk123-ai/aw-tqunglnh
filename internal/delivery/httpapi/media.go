package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ByteRange is one resolved, inclusive byte range against a resource of a
// known total length.
type ByteRange struct {
	Start int64
	End   int64 // inclusive
}

// Length returns the number of bytes r spans.
func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

// ErrRangeNotSatisfiable is returned by ParseRange for a `Range` header
// this server refuses to honor — outside [0, totalLength), reversed, or a
// multi-range request (Alpha supports exactly one range per request; V6-07B
// is the first real consumer and has no stated need for multi-range).
var ErrRangeNotSatisfiable = errors.New("httpapi: requested range is not satisfiable")

// ParseRange parses header — an HTTP `Range` header value, or "" if the
// caller sent none — against a resource whose total size is totalLength
// bytes. present is false only when header is empty (serve the whole
// resource); every other rejection is a typed ErrRangeNotSatisfiable, per
// V6-02A's own "Thực hiện" line: "reject a Range request outside actual
// content length". Supports the two forms V6-07B's evidence/artifact
// streaming needs: "bytes=start-end" / "bytes=start-" (open-ended) and the
// suffix form "bytes=-N" (last N bytes). A resolved range's End is always
// clamped to totalLength-1, and Start is always < totalLength — a handler
// can trust the returned ByteRange to index directly into the real content
// without any further bounds check of its own.
func ParseRange(header string, totalLength int64) (rng ByteRange, present bool, err error) {
	if header == "" {
		return ByteRange{}, false, nil
	}
	if totalLength <= 0 {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	spec := strings.TrimPrefix(header, prefix)
	if strings.Contains(spec, ",") {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	startStr, endStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	var start, end int64
	switch {
	case startStr == "" && endStr == "":
		return ByteRange{}, false, ErrRangeNotSatisfiable
	case startStr == "":
		// Suffix range: last N bytes.
		n, convErr := strconv.ParseInt(endStr, 10, 64)
		if convErr != nil || n <= 0 {
			return ByteRange{}, false, ErrRangeNotSatisfiable
		}
		if n > totalLength {
			n = totalLength
		}
		start = totalLength - n
		end = totalLength - 1
	default:
		n, convErr := strconv.ParseInt(startStr, 10, 64)
		if convErr != nil || n < 0 {
			return ByteRange{}, false, ErrRangeNotSatisfiable
		}
		start = n
		if endStr == "" {
			end = totalLength - 1
		} else {
			e, convErr := strconv.ParseInt(endStr, 10, 64)
			if convErr != nil || e < start {
				return ByteRange{}, false, ErrRangeNotSatisfiable
			}
			end = e
		}
	}

	if start < 0 || start >= totalLength {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	if end >= totalLength {
		end = totalLength - 1
	}
	if end < start {
		return ByteRange{}, false, ErrRangeNotSatisfiable
	}
	return ByteRange{Start: start, End: end}, true, nil
}

// ApplyPartialContentHeaders sets Accept-Ranges/Content-Range/Content-Length
// for a resolved partial-content (206) response. The caller still writes
// the 206 status itself (via w.WriteHeader) after calling this — this
// package does not assume a handler always wants a partial response typed
// separately from a full one.
func ApplyPartialContentHeaders(w http.ResponseWriter, rng ByteRange, totalLength int64) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rng.Start, rng.End, totalLength))
	w.Header().Set("Content-Length", strconv.FormatInt(rng.Length(), 10))
}

// WriteRangeNotSatisfiable writes the canonical 416 response for a Range
// header this server refuses to honor, including the RFC 7233 `Content-Range:
// bytes */<totalLength>` header that names the actual resource size so a
// well-behaved client can retry with a valid range.
func WriteRangeNotSatisfiable(w http.ResponseWriter, totalLength int64) {
	w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", totalLength))
	WriteError(w, http.StatusRequestedRangeNotSatisfiable, ErrorCodeInvalidRequest, "requested range is not satisfiable", nil)
}

// MediaDisposition says whether a content response is safe to render
// inline in a browser tab or must be forced to download — V6-07B's own
// "no-sniff/download policy" line: an evidence/artifact byte stream whose
// content type is not on a small safe allow-list must never be served
// inline, since a browser could otherwise execute an attacker-controlled
// HTML/SVG payload as if it were this server's own origin.
type MediaDisposition string

const (
	MediaDispositionInline     MediaDisposition = "inline"
	MediaDispositionAttachment MediaDisposition = "attachment"
)

// inlineSafeContentTypes is the closed allow-list of content types safe to
// render inline in a browser tab. Everything else — including text/html
// and image/svg+xml, both script-capable when rendered by a browser — is
// forced to download instead.
var inlineSafeContentTypes = map[string]bool{
	"text/plain":       true,
	"text/csv":         true,
	"application/json": true,
	"image/png":        true,
	"image/jpeg":       true,
	"image/gif":        true,
	"application/pdf":  true,
}

// ResolveMediaDisposition decides inline vs attachment for contentType (a
// full `Content-Type` header value, parameters and all — only the media
// type portion before any `;` is checked).
func ResolveMediaDisposition(contentType string) MediaDisposition {
	mediaType := contentType
	if idx := strings.IndexByte(mediaType, ';'); idx >= 0 {
		mediaType = mediaType[:idx]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if inlineSafeContentTypes[mediaType] {
		return MediaDispositionInline
	}
	return MediaDispositionAttachment
}

// ApplyContentHeaders sets Content-Type, X-Content-Type-Options: nosniff
// (so a browser never second-guesses contentType by sniffing the body) and
// a Content-Disposition resolved via ResolveMediaDisposition — the shared
// header set V6-07B's evidence/artifact-content endpoints reuse instead of
// each composing this policy itself.
func ApplyContentHeaders(w http.ResponseWriter, contentType, filename string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", ResolveMediaDisposition(contentType), filename))
}
