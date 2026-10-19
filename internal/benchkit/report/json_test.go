package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteJSONFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.json")
	if err := WriteJSONFile(path, map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "summary.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temp files left behind: %v", matches)
	}

	if err := WriteJSONFile(path, map[string]chan int{"nope": nil}); err == nil {
		t.Fatal("expected encoding a channel to fail")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"status": "ok"`) {
		t.Fatalf("original file was lost: %s", body)
	}
}

func TestWriteJSONFileMissingDirectory(t *testing.T) {
	err := WriteJSONFile(filepath.Join(t.TempDir(), "missing", "out.json"), map[string]int{"n": 1})
	if err == nil {
		t.Fatal("expected a missing directory to fail")
	}
}

func TestJSONLinesAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trials.jsonl")
	lines, err := CreateJSONLines(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lines.Append(map[string]any{"trial": 1, "ok": true}); err != nil {
		t.Fatal(err)
	}
	if err := lines.Append(map[string]any{"trial": 2, "ok": false}); err != nil {
		t.Fatal(err)
	}
	if err := lines.Append(map[string]chan int{"bad": nil}); err == nil {
		t.Fatal("expected a failed append")
	}
	if err := lines.Close(); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(records) != 2 {
		t.Fatalf("records = %d\n%s", len(records), body)
	}
	var first struct {
		Trial int  `json:"trial"`
		OK    bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(records[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Trial != 1 || !first.OK {
		t.Fatalf("first = %+v", first)
	}
}
