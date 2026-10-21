// Package report writes benchmark results as JSON and as benchfmt.
// It does not compute statistical tests. benchstat does that.
package report

import (
	"fmt"
	"io"
	"slices"

	"golang.org/x/perf/benchfmt"
)

// Value is one measurement on a trial.
type Value struct {
	Value float64
	Unit  string
}

// UnitMeta is benchfmt unit metadata, such as assume=exact.
type UnitMeta struct {
	Unit  string
	Key   string
	Value string
}

// Result is one independent trial.
type Result struct {
	// Name is the benchmark name without the "Benchmark" prefix.
	// The benchfmt writer adds that prefix.
	Name   string
	Values []Value
}

// File is one benchfmt document, usually one experiment case.
type File struct {
	Config  map[string]string
	Units   []UnitMeta
	Results []Result
}

// WriteBenchfmt writes unit metadata, file configuration, and one result
// per trial. Configuration keys are emitted in sorted order.
func WriteBenchfmt(w io.Writer, file File) error {
	writer := benchfmt.NewWriter(w)
	for _, unit := range file.Units {
		if unit.Unit == "" || unit.Key == "" {
			return fmt.Errorf("write benchfmt: unit metadata is incomplete")
		}
		err := writer.Write(&benchfmt.UnitMetadata{
			UnitMetadataKey: benchfmt.UnitMetadataKey{Unit: unit.Unit, Key: unit.Key},
			OrigUnit:        unit.Unit,
			Value:           unit.Value,
		})
		if err != nil {
			return fmt.Errorf("write benchfmt unit metadata: %w", err)
		}
	}

	config := fileConfig(file.Config)
	for i, result := range file.Results {
		if result.Name == "" {
			return fmt.Errorf("write benchfmt: result %d has an empty name", i)
		}
		record := &benchfmt.Result{
			Config: append([]benchfmt.Config(nil), config...),
			Name:   benchfmt.Name(result.Name),
			Iters:  1,
		}
		for _, value := range result.Values {
			if value.Unit == "" {
				return fmt.Errorf("write benchfmt: result %d has an empty unit", i)
			}
			record.Values = append(record.Values, benchfmt.Value{
				Value: value.Value,
				Unit:  value.Unit,
			})
		}
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("write benchfmt result %d: %w", i, err)
		}
	}
	return nil
}

func fileConfig(values map[string]string) []benchfmt.Config {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	config := make([]benchfmt.Config, 0, len(keys))
	for _, key := range keys {
		config = append(config, benchfmt.Config{
			Key:   key,
			Value: []byte(values[key]),
			File:  true,
		})
	}
	return config
}

// EqualInvariants reports an error when files disagree on any of keys.
func EqualInvariants(files []File, keys []string) error {
	if len(files) == 0 {
		return fmt.Errorf("compare benchfmt files: no files")
	}
	base := files[0].Config
	for _, key := range keys {
		want, ok := base[key]
		if !ok || want == "" {
			return fmt.Errorf("compare benchfmt files: missing %s", key)
		}
		for i, file := range files[1:] {
			got, ok := file.Config[key]
			if !ok || got != want {
				return fmt.Errorf("compare benchfmt files: file %d %s = %q, want %q", i+1, key, got, want)
			}
		}
	}
	return nil
}
