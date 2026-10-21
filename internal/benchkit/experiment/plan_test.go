package experiment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanCoversEveryCellOnce(t *testing.T) {
	cases := []string{"go127", "go128", "go128off"}
	counts := []int{0, 1000, 5000}
	const rounds = 4
	cells, err := Plan(cases, counts, rounds, 42)
	if err != nil {
		t.Fatal(err)
	}
	pairs := len(cases) * len(counts)
	want := pairs * rounds
	if len(cells) != want {
		t.Fatalf("len = %d, want %d", len(cells), want)
	}
	seen := make(map[Cell]int, want)
	perRound := make([]map[Cell]int, rounds)
	for i := range perRound {
		perRound[i] = map[Cell]int{}
	}
	for _, cell := range cells {
		seen[cell]++
		perRound[cell.Round][Cell{Case: cell.Case, Load: cell.Load}]++
	}
	for cell, n := range seen {
		if n != 1 {
			t.Fatalf("cell %+v appeared %d times", cell, n)
		}
	}
	for round, got := range perRound {
		if len(got) != pairs {
			t.Fatalf("round %d has %d pairs, want %d", round, len(got), pairs)
		}
		for _, n := range got {
			if n != 1 {
				t.Fatalf("round %d repeated a pair", round)
			}
		}
	}
}

func TestPlanSeedIsReproducible(t *testing.T) {
	a, err := Plan([]string{"a", "b"}, []int{0, 1}, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Plan([]string{"a", "b"}, []int{0, 1}, 3, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatal("length mismatch")
	}
	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if !same {
		t.Fatalf("same seed produced different plans: %#v vs %#v", a, b)
	}
	other, err := Plan([]string{"a", "b"}, []int{0, 1}, 3, 8)
	if err != nil {
		t.Fatal(err)
	}
	differ := false
	for i := range a {
		if a[i] != other[i] {
			differ = true
			break
		}
	}
	if !differ {
		t.Fatal("different seeds produced the same order")
	}
}

func TestPlanRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		cases  []string
		loads  []int
		rounds int
	}{
		{name: "no cases", loads: []int{0}, rounds: 1},
		{name: "no loads", cases: []string{"a"}, rounds: 1},
		{name: "zero rounds", cases: []string{"a"}, loads: []int{0}},
		{name: "negative load", cases: []string{"a"}, loads: []int{-1}, rounds: 1},
		{name: "duplicate load", cases: []string{"a"}, loads: []int{1, 1}, rounds: 1},
		{name: "duplicate case", cases: []string{"a", "a"}, loads: []int{0}, rounds: 1},
		{name: "empty case", cases: []string{""}, loads: []int{0}, rounds: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Plan(tt.cases, tt.loads, tt.rounds, 42)
			require.ErrorIs(t, err, ErrInvalidPlan)
		})
	}
}

func TestPlanAllowsZeroConnections(t *testing.T) {
	cells, err := Plan([]string{"base"}, []int{0}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].Load != 0 {
		t.Fatalf("cells = %+v", cells)
	}
}
