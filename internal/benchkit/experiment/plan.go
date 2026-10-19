// Package experiment plans trials and polls until a series looks steady.
// It has no HTTP/2-specific rules.
package experiment

import (
	"errors"
	"fmt"

	"math/rand/v2"
)

// ErrInvalidPlan means the requested cells cannot be built.
var ErrInvalidPlan = errors.New("invalid plan")

// Cell is one trial slot. Round numbers start at 0.
type Cell struct {
	Case        string
	Connections int
	Round       int
}

// Plan returns rounds shuffled copies of every (case, connection count) pair.
// The shuffle for a seed is fixed. Every cell appears once. Counts may be
// zero (a baseline) but not negative, and neither cases nor counts may repeat.
func Plan(cases []string, counts []int, rounds int, seed uint64) ([]Cell, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("%w: no cases", ErrInvalidPlan)
	}
	if len(counts) == 0 {
		return nil, fmt.Errorf("%w: no connection counts", ErrInvalidPlan)
	}
	if rounds < 1 {
		return nil, fmt.Errorf("%w: rounds must be >= 1", ErrInvalidPlan)
	}
	seenCase := make(map[string]struct{}, len(cases))
	for _, name := range cases {
		if name == "" {
			return nil, fmt.Errorf("%w: empty case name", ErrInvalidPlan)
		}
		if _, ok := seenCase[name]; ok {
			return nil, fmt.Errorf("%w: duplicate case %q", ErrInvalidPlan, name)
		}
		seenCase[name] = struct{}{}
	}
	seenCount := make(map[int]struct{}, len(counts))
	for _, count := range counts {
		if count < 0 {
			return nil, fmt.Errorf("%w: connection count %d", ErrInvalidPlan, count)
		}
		if _, ok := seenCount[count]; ok {
			return nil, fmt.Errorf("%w: duplicate connection count %d", ErrInvalidPlan, count)
		}
		seenCount[count] = struct{}{}
	}

	rng := rand.New(rand.NewPCG(seed, 0))
	out := make([]Cell, 0, len(cases)*len(counts)*rounds)
	for round := range rounds {
		cells := make([]Cell, 0, len(cases)*len(counts))
		for _, name := range cases {
			for _, count := range counts {
				cells = append(cells, Cell{Case: name, Connections: count, Round: round})
			}
		}
		rng.Shuffle(len(cells), func(i, j int) {
			cells[i], cells[j] = cells[j], cells[i]
		})
		out = append(out, cells...)
	}
	return out, nil
}
