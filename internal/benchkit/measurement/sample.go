package measurement

import (
	"errors"
	"fmt"
	"time"
)

// ErrUnsupported means the kernel did not provide a metric.
var ErrUnsupported = errors.New("metric unsupported")

// ErrUnordered means sample timestamps do not increase.
var ErrUnordered = errors.New("sample times not increasing")

// Sample is one timestamped memory observation.
type Sample struct {
	Time time.Time
	RSS  Quantity
	Anon Quantity
	Peak Quantity
}

// Drift returns max(RSS) - min(RSS) across samples.
// It fails if any RSS is unsupported, if fewer than two samples are given,
// or if timestamps do not strictly increase. A zero span is a real result.
func Drift(samples []Sample) (uint64, error) {
	if len(samples) < 2 {
		return 0, fmt.Errorf("drift: need at least 2 samples")
	}
	var min, max uint64
	for i, sample := range samples {
		value, err := sample.RSS.Value()
		if err != nil {
			return 0, fmt.Errorf("drift: sample %d: %w", i, err)
		}
		if i > 0 && !sample.Time.After(samples[i-1].Time) {
			return 0, fmt.Errorf("drift: sample %d: %w", i, ErrUnordered)
		}
		if i == 0 || value < min {
			min = value
		}
		if i == 0 || value > max {
			max = value
		}
	}
	return max - min, nil
}
