package measurement

import (
	"errors"
	"testing"
	"time"
)

func rss(at time.Time, bytes uint64) Sample {
	return Sample{
		Time: at,
		RSS:  Quantity{Bytes: bytes, Supported: true},
	}
}

func TestDriftSpan(t *testing.T) {
	start := time.Unix(0, 0)
	delta, err := Drift([]Sample{
		rss(start, 1000),
		rss(start.Add(time.Second), 1200),
		rss(start.Add(2*time.Second), 1100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if delta != 200 {
		t.Fatalf("drift = %d, want 200", delta)
	}
}

func TestDriftZeroSpanIsValid(t *testing.T) {
	start := time.Unix(0, 0)
	delta, err := Drift([]Sample{
		rss(start, 0),
		rss(start.Add(time.Second), 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if delta != 0 {
		t.Fatalf("drift = %d, want 0", delta)
	}
}

func TestDriftUnsupportedIsNotZero(t *testing.T) {
	start := time.Unix(0, 0)
	_, err := Drift([]Sample{
		rss(start, 10),
		{Time: start.Add(time.Second), RSS: Quantity{}},
	})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want unsupported", err)
	}
}

func TestDriftRejectsUnorderedTimes(t *testing.T) {
	start := time.Unix(0, 0)
	_, err := Drift([]Sample{
		rss(start.Add(time.Second), 10),
		rss(start, 12),
	})
	if !errors.Is(err, ErrUnordered) {
		t.Fatalf("err = %v, want unordered", err)
	}
}

func TestDriftRejectsTooFew(t *testing.T) {
	if _, err := Drift([]Sample{rss(time.Unix(0, 0), 1)}); err == nil {
		t.Fatal("expected an error for one sample")
	}
}
