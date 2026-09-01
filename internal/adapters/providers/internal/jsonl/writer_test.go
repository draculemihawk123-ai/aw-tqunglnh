package jsonl

import (
	"errors"
	"reflect"
	"testing"
)

func TestWriterHandlesChunkedAndFinalRecords(t *testing.T) {
	t.Parallel()

	var records []string
	writer, err := NewWriter(100, func(line []byte) error {
		records = append(records, string(line))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"{\"a\":", "1}\r\n\n{\"b\":2", "}"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{"{\"a\":1}", "{\"b\":2}"}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records = %#v, want %#v", records, want)
	}
}

func TestWriterFailsClosedOnConsumerErrorAndOversizedLine(t *testing.T) {
	t.Parallel()

	wantError := errors.New("invalid provider event")
	writer, _ := NewWriter(20, func([]byte) error { return wantError })
	if _, err := writer.Write([]byte("{}\n")); !errors.Is(err, wantError) {
		t.Fatalf("consumer error = %v, want %v", err, wantError)
	}

	oversized, _ := NewWriter(2, func([]byte) error { return nil })
	if _, err := oversized.Write([]byte("123")); err == nil {
		t.Fatal("oversized record was accepted")
	}
}

func TestTailBufferIsBounded(t *testing.T) {
	t.Parallel()

	buffer := NewTailBuffer(5)
	_, _ = buffer.Write([]byte("abc"))
	_, _ = buffer.Write([]byte("defg"))
	data, truncated := buffer.Snapshot()
	if string(data) != "cdefg" || !truncated {
		t.Fatalf("tail = %q truncated=%v", data, truncated)
	}
}
