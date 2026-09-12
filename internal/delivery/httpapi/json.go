package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrBodyTooLarge is DecodeJSON's typed error when the request body
// exceeds the configured limit (V6-01's own "strict JSON/body limit"
// scope line) — a caller maps this to a 413/400 response distinct from a
// generic malformed-body error.
var ErrBodyTooLarge = errors.New("httpapi: request body exceeds size limit")

// ErrMalformedJSON is DecodeJSON's typed error for a body that is not a
// single well-formed JSON value matching dst's shape (invalid syntax, an
// unknown field, a type mismatch, or trailing data after the first
// value).
var ErrMalformedJSON = errors.New("httpapi: request body is not valid JSON")

// DecodeJSON strictly decodes r's body into dst: unknown fields are
// rejected, the body must contain exactly one JSON value (trailing
// non-whitespace data after it is rejected), and reading past limitBytes
// fails with ErrBodyTooLarge instead of allowing an unbounded read. This
// is the one shared decode primitive V6-01's own route-registration
// primitive exists to give every future business-endpoint task, so no
// individual endpoint has to re-derive these bounds itself.
func DecodeJSON(r *http.Request, limitBytes int64, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, limitBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("%w: %v", ErrBodyTooLarge, err)
		}
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	// Reject a second JSON value (or any other trailing non-whitespace) in
	// the same body — e.g. `{"a":1}{"b":2}` or `{"a":1} garbage`.
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values in one request body", ErrMalformedJSON)
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return fmt.Errorf("%w: %v", ErrBodyTooLarge, err)
		}
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return nil
}
