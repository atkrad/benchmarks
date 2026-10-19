package process

import (
	"bytes"
	"sync"
	"testing"
)

func TestBufferKeepsBytesUnderLimit(t *testing.T) {
	b := NewBuffer(8)
	n, err := b.Write([]byte("abcd"))
	if err != nil || n != 4 {
		t.Fatalf("Write returned %d, %v", n, err)
	}
	if b.String() != "abcd" || b.Truncated() {
		t.Fatalf("buffer = %q truncated=%v", b.String(), b.Truncated())
	}
}

func TestBufferDiscardsPastLimit(t *testing.T) {
	b := NewBuffer(5)
	n, err := b.Write([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("Write n = %d, want 6 (discarded bytes still count as accepted)", n)
	}
	if !b.Truncated() {
		t.Fatal("expected truncation")
	}
	if b.Len() != 5 {
		t.Fatalf("retained %d bytes, want 5", b.Len())
	}
	if got := b.String(); got != "abcde\n"+TruncationMarker+"\n" {
		t.Fatalf("String = %q", got)
	}

	n, err = b.Write([]byte("zz"))
	if err != nil || n != 2 {
		t.Fatalf("second Write = %d, %v", n, err)
	}
	if b.Len() != 5 {
		t.Fatalf("retained %d bytes after a full buffer", b.Len())
	}
}

func TestBufferConcurrentWrites(t *testing.T) {
	const (
		writers = 8
		each    = 1000
	)
	b := NewBuffer(writers * each)
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := bytes.Repeat([]byte("x"), each)
			n, err := b.Write(payload)
			if err != nil || n != len(payload) {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if b.Len() != writers*each {
		t.Fatalf("retained %d, want %d", b.Len(), writers*each)
	}
	if b.Truncated() {
		t.Fatal("buffer truncated inside its limit")
	}
}
