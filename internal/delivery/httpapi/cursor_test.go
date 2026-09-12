package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestCursorCodec_EncodeDecode_RoundTrips(t *testing.T) {
	codec := httpapi.NewCursorCodec([]byte("test-secret"))
	state := httpapi.CursorState{
		ProjectID: "project-1", QueryFingerprint: "fp-1", Generation: 2,
		UpperWatermark: 42, LastKey: "work-item-7",
	}
	token, err := codec.Encode(state)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if token == "" {
		t.Fatal("Encode returned an empty token")
	}
	got, err := codec.Decode(token)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != state {
		t.Fatalf("Decode(Encode(state)) = %+v, want %+v", got, state)
	}
}

func TestCursorCodec_Decode_MalformedTokenRejected(t *testing.T) {
	codec := httpapi.NewCursorCodec([]byte("test-secret"))
	tests := []string{
		"not-valid-base64!!!",
		base64.RawURLEncoding.EncodeToString([]byte("not json at all")),
		"",
	}
	for _, token := range tests {
		t.Run(token, func(t *testing.T) {
			_, err := codec.Decode(token)
			if !errors.Is(err, httpapi.ErrCursorInvalid) {
				t.Fatalf("Decode(%q) error = %v, want ErrCursorInvalid", token, err)
			}
		})
	}
}

// TestCursorCodec_Decode_TamperedPayloadRejected is the "tamper cursor"
// Verify bullet's own real proof: an attacker who can freely edit a
// cursor's decoded payload (here, escalating from one project to another)
// WITHOUT knowing the server's HMAC secret must be rejected — the
// signature was computed over the original bytes and cannot match the
// edited ones.
func TestCursorCodec_Decode_TamperedPayloadRejected(t *testing.T) {
	codec := httpapi.NewCursorCodec([]byte("test-secret"))
	token, err := codec.Encode(httpapi.CursorState{ProjectID: "project-1", LastKey: "k1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	var envelope struct {
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var state map[string]any
	if err := json.Unmarshal(envelope.Payload, &state); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	state["projectId"] = "project-2" // attacker tries to hop to another project's data
	tamperedPayload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal tampered payload: %v", err)
	}
	envelope.Payload = tamperedPayload // signature is now stale relative to this payload

	tamperedRaw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal tampered envelope: %v", err)
	}
	tamperedToken := base64.RawURLEncoding.EncodeToString(tamperedRaw)

	if _, err := codec.Decode(tamperedToken); !errors.Is(err, httpapi.ErrCursorInvalid) {
		t.Fatalf("Decode(tampered token) error = %v, want ErrCursorInvalid", err)
	}
}

func TestCursorCodec_Decode_WrongSecretRejected(t *testing.T) {
	issuer := httpapi.NewCursorCodec([]byte("secret-a"))
	verifier := httpapi.NewCursorCodec([]byte("secret-b"))

	token, err := issuer.Encode(httpapi.CursorState{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := verifier.Decode(token); !errors.Is(err, httpapi.ErrCursorInvalid) {
		t.Fatalf("Decode with wrong secret error = %v, want ErrCursorInvalid", err)
	}
}

func TestNewCursorCodec_EmptySecretPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewCursorCodec(nil) should panic")
		}
	}()
	httpapi.NewCursorCodec(nil)
}

func TestFingerprint_DeterministicForEquivalentQuery(t *testing.T) {
	type query struct {
		Status string
		Sort   string
	}
	a, err := httpapi.Fingerprint(query{Status: "OPEN", Sort: "key_asc"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	b, err := httpapi.Fingerprint(query{Status: "OPEN", Sort: "key_asc"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if a != b {
		t.Fatalf("Fingerprint of two equivalent queries differ: %q vs %q", a, b)
	}
}

func TestFingerprint_DifferentForDifferentQuery(t *testing.T) {
	type query struct {
		Status string
		Sort   string
	}
	a, err := httpapi.Fingerprint(query{Status: "OPEN", Sort: "key_asc"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	b, err := httpapi.Fingerprint(query{Status: "BLOCKED", Sort: "key_asc"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if a == b {
		t.Fatal("Fingerprint of two different queries collided")
	}
}

func TestBind_MatchingStateReturnsNil(t *testing.T) {
	state := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 1, LastKey: "k1"}
	want := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 1}
	if err := httpapi.Bind(state, want); err != nil {
		t.Fatalf("Bind = %v, want nil", err)
	}
}

func TestBind_ProjectMismatch_ReturnsResyncError(t *testing.T) {
	state := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 1}
	want := httpapi.CursorState{ProjectID: "p2", QueryFingerprint: "fp1", Generation: 1}

	err := httpapi.Bind(state, want)
	var resyncErr *httpapi.ResyncError
	if !errors.As(err, &resyncErr) {
		t.Fatalf("Bind error = %v, want *ResyncError", err)
	}
	if resyncErr.Reason != httpapi.ResyncReasonProjectMismatch {
		t.Fatalf("Reason = %s, want %s", resyncErr.Reason, httpapi.ResyncReasonProjectMismatch)
	}
}

func TestBind_QueryFingerprintMismatch_ReturnsResyncError(t *testing.T) {
	state := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 1}
	want := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp2", Generation: 1}

	err := httpapi.Bind(state, want)
	var resyncErr *httpapi.ResyncError
	if !errors.As(err, &resyncErr) {
		t.Fatalf("Bind error = %v, want *ResyncError", err)
	}
	if resyncErr.Reason != httpapi.ResyncReasonQueryChanged {
		t.Fatalf("Reason = %s, want %s", resyncErr.Reason, httpapi.ResyncReasonQueryChanged)
	}
}

// TestBind_GenerationMismatch_ReturnsResyncError is the "generation swap
// resync" Verify bullet's own dedicated proof: V6-09A's fenced cutover
// atomically swaps the active projection generation; a cursor still
// naming the old generation must resync rather than silently mix rows
// from two generations.
func TestBind_GenerationMismatch_ReturnsResyncError(t *testing.T) {
	state := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 1}
	want := httpapi.CursorState{ProjectID: "p1", QueryFingerprint: "fp1", Generation: 2}

	err := httpapi.Bind(state, want)
	var resyncErr *httpapi.ResyncError
	if !errors.As(err, &resyncErr) {
		t.Fatalf("Bind error = %v, want *ResyncError", err)
	}
	if resyncErr.Reason != httpapi.ResyncReasonGenerationChanged {
		t.Fatalf("Reason = %s, want %s", resyncErr.Reason, httpapi.ResyncReasonGenerationChanged)
	}
}

// fakeRow is a minimal keyset-paginated row: Key is the stable sort key a
// real query would order by, Position is a monotonic write-order marker
// (like a global JournalPosition) a real UpperWatermark bounds against.
type fakeRow struct {
	Key      string
	Position int64
}

// fetchPage returns, in Key order, up to limit rows with Key > after and
// Position <= asOf — the same {keyset cursor, upper-bound watermark} shape
// a real projection-backed query (V6-10 etc.) would use; this test's own
// stand-in for that not-yet-built query executor, existing only to prove
// CursorState's fields are sufficient to make such a query stable.
func fetchPage(rows []fakeRow, after string, asOf int64, limit int) []fakeRow {
	var page []fakeRow
	for _, row := range rows {
		if row.Position > asOf || row.Key <= after {
			continue
		}
		page = append(page, row)
		if len(page) == limit {
			break
		}
	}
	return page
}

// TestCursorPaging_ConcurrentWriteBetweenPages_StaysStable is the "stable
// paging across a concurrent write" Verify bullet's own proof. Page 1 is
// fetched, its cursor is minted (binding UpperWatermark=4, the greatest
// Position visible at that moment). Before page 2 is fetched, a
// concurrent write inserts a new row ("bb") that Position-wise happened
// AFTER the walk began, but whose Key would sort INSIDE the still-unread
// tail of the walk ("bb" between "b" and "c"). Page 2 must not include
// "bb" — round-tripping CursorState.UpperWatermark through the real
// CursorCodec is what keeps page 2 bounded to the walk's own original
// snapshot — and page 1 + page 2 together must equal exactly the four
// pre-write rows, each exactly once (no duplicate, nothing missing).
func TestCursorPaging_ConcurrentWriteBetweenPages_StaysStable(t *testing.T) {
	codec := httpapi.NewCursorCodec([]byte("test-secret"))
	fingerprint, err := httpapi.Fingerprint(struct{ Sort string }{Sort: "key_asc"})
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	rows := []fakeRow{
		{Key: "a", Position: 1},
		{Key: "b", Position: 2},
		{Key: "c", Position: 3},
		{Key: "d", Position: 4},
	}
	watermark := int64(4) // greatest Position visible when this walk begins

	page1 := fetchPage(rows, "", watermark, 2)
	if len(page1) != 2 || page1[0].Key != "a" || page1[1].Key != "b" {
		t.Fatalf("page1 = %+v, want [a b]", page1)
	}

	cursorToken, err := codec.Encode(httpapi.CursorState{
		ProjectID: "p1", QueryFingerprint: fingerprint, Generation: 1,
		UpperWatermark: watermark, LastKey: page1[len(page1)-1].Key,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	// Concurrent write: a new row lands between "b" and "c" lexically,
	// but at a Position AFTER this walk's own watermark.
	rows = append(rows, fakeRow{Key: "bb", Position: 5})

	state, err := codec.Decode(cursorToken)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if state.UpperWatermark != watermark {
		t.Fatalf("decoded UpperWatermark = %d, want %d (round-trip must be exact)", state.UpperWatermark, watermark)
	}
	if err := httpapi.Bind(state, httpapi.CursorState{ProjectID: "p1", QueryFingerprint: fingerprint, Generation: 1}); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	page2 := fetchPage(rows, state.LastKey, state.UpperWatermark, 10)
	for _, row := range page2 {
		if row.Key == "bb" {
			t.Fatalf("page2 = %+v leaked a row (%q) written after the walk's own UpperWatermark", page2, row.Key)
		}
	}
	if len(page2) != 2 || page2[0].Key != "c" || page2[1].Key != "d" {
		t.Fatalf("page2 = %+v, want [c d]", page2)
	}

	total := append(append([]fakeRow{}, page1...), page2...)
	if len(total) != 4 {
		t.Fatalf("page1+page2 = %+v (len %d), want exactly the 4 pre-watermark rows with no duplicate/missing", total, len(total))
	}
}
