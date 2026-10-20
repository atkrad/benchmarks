// Package experiment plans trials and polls until a series looks steady.
// It has no knowledge of a particular workload.
package experiment

import (
	"errors"
	"fmt"

	"math/rand/v2"
)

// ErrInvalidPlan means the requested cells cannot be built.
var ErrInvalidPlan = errors.New("invalid plan")

// Cell is one trial slot. Round numbers start at 0.
// Load is the numeric setting for that trial, such as a size or a count.
type Cell struct {
	Case  string
	Load  int
	Round int
}

// Plan returns rounds shuffled copies of every (case, load) pair.
// The shuffle for a seed is fixed. Every cell appears once. Loads may be
// zero (a baseline) but not negative, and neither cases nor loads may repeat.
func Plan(cases []string, loads []int, rounds int, seed uint64) ([]Cell, error) {
	if len(cases) == 0 {
		return nil, fmt.Errorf("%w: no cases", ErrInvalidPlan)
	}
	if len(loads) == 0 {
		return nil, fmt.Errorf("%w: no loads", ErrInvalidPlan)
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
	seenLoad := make(map[int]struct{}, len(loads))
	for _, load := range loads {
		if load < 0 {
			return nil, fmt.Errorf("%w: load %d", ErrInvalidPlan, load)
		}
		if _, ok := seenLoad[load]; ok {
			return nil, fmt.Errorf("%w: duplicate load %d", ErrInvalidPlan, load)
		}
		seenLoad[load] = struct{}{}
	}

	rng := rand.New(rand.NewPCG(seed, 0))
	out := make([]Cell, 0, len(cases)*len(loads)*rounds)
	for round := range rounds {
		cells := make([]Cell, 0, len(cases)*len(loads))
		for _, name := range cases {
			for _, load := range loads {
				cells = append(cells, Cell{Case: name, Load: load, Round: round})
			}
		}
		rng.Shuffle(len(cells), func(i, j int) {
			cells[i], cells[j] = cells[j], cells[i]
		})
		out = append(out, cells...)
	}
	return out, nil
}
