package jsonl

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
)

const DefaultMaxLineBytes = 4 << 20

type Consumer func([]byte) error

// Writer converts arbitrary stdout chunks into complete JSONL records. It is
// deliberately independent from any provider schema.
type Writer struct {
	mu      sync.Mutex
	buffer  []byte
	maxLine int
	consume Consumer
	err     error
	closed  bool
}

func NewWriter(maxLine int, consume Consumer) (*Writer, error) {
	if consume == nil {
		return nil, errors.New("JSONL consumer is required")
	}
	if maxLine <= 0 {
		maxLine = DefaultMaxLineBytes
	}
	return &Writer{maxLine: maxLine, consume: consume}, nil
}

func (w *Writer) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("write to closed JSONL writer")
	}
	if w.err != nil {
		return 0, w.err
	}

	w.buffer = append(w.buffer, data...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := bytes.TrimSuffix(w.buffer[:index], []byte{'\r'})
		w.buffer = w.buffer[index+1:]
		if len(line) == 0 {
			continue
		}
		if len(line) > w.maxLine {
			w.err = fmt.Errorf("JSONL record exceeds %d bytes", w.maxLine)
			return 0, w.err
		}
		if err := w.consume(append([]byte(nil), line...)); err != nil {
			w.err = err
			return 0, err
		}
	}
	if len(w.buffer) > w.maxLine {
		w.err = fmt.Errorf("JSONL record exceeds %d bytes", w.maxLine)
		return 0, w.err
	}
	return len(data), nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.err
	}
	w.closed = true
	if w.err != nil {
		return w.err
	}
	line := bytes.TrimSuffix(w.buffer, []byte{'\r'})
	w.buffer = nil
	if len(line) == 0 {
		return nil
	}
	if len(line) > w.maxLine {
		w.err = fmt.Errorf("JSONL record exceeds %d bytes", w.maxLine)
		return w.err
	}
	w.err = w.consume(append([]byte(nil), line...))
	return w.err
}

func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// TailBuffer retains only the last max bytes of stderr so a noisy child
// process cannot grow memory without bound.
type TailBuffer struct {
	mu        sync.Mutex
	data      []byte
	max       int
	truncated bool
}

func NewTailBuffer(max int) *TailBuffer {
	if max <= 0 {
		max = 64 << 10
	}
	return &TailBuffer{max: max}
}

func (b *TailBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(data)
	if len(data) >= b.max {
		b.data = append(b.data[:0], data[len(data)-b.max:]...)
		b.truncated = true
		return originalLength, nil
	}
	if excess := len(b.data) + len(data) - b.max; excess > 0 {
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
		b.truncated = true
	}
	b.data = append(b.data, data...)
	return originalLength, nil
}

func (b *TailBuffer) Snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), b.truncated
}
