// Package process runs child processes with deadlines, process-group
// termination, and bounded output capture. It has no knowledge of HTTP/2.
package process

import "sync"

// defaultCapture is the output kept when a caller does not set a limit.
const defaultCapture = 256 << 10

// TruncationMarker is appended to Buffer.String when bytes were dropped.
const TruncationMarker = "[truncated]"

// Buffer is a concurrency-safe writer that keeps at most limit bytes.
// Writes past the limit are discarded on purpose: Write still reports
// len(p) and a nil error, and Truncated becomes true.
type Buffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

// NewBuffer returns a buffer that keeps at most limit bytes.
// A non-positive limit uses defaultCapture.
func NewBuffer(limit int) *Buffer {
	if limit <= 0 {
		limit = defaultCapture
	}
	return &Buffer{limit: limit}
}

// Write implements io.Writer. It always reports len(p) when the buffer
// accepts or intentionally discards bytes.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	remain := b.limit - len(b.buf)
	if remain <= 0 {
		if len(p) > 0 {
			b.truncated = true
		}
		return len(p), nil
	}
	if len(p) > remain {
		b.buf = append(b.buf, p[:remain]...)
		b.truncated = true
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// String returns the captured bytes. A truncated buffer ends with TruncationMarker.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + "\n" + TruncationMarker + "\n"
}

// Truncated reports whether any byte was discarded.
func (b *Buffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// Len reports how many bytes are retained, not counting the marker.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buf)
}
