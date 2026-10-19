package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.json")
	body := []byte(`{
	  "connection_counts": [0, 1000],
	  "rounds": 10,
	  "dial_concurrency": 4,
	  "max_trial_retries": 1,
	  "seed": 3,
	  "gomaxprocs": 12,
	  "gogc": 100,
	  "godebug": "",
	  "stability_polls": 5,
	  "poll_interval_ms": 1000,
	  "stability_timeout_sec": 60,
	  "sample_count": 3,
	  "sample_interval_ms": 1000,
	  "rss_drift_tolerance_pct": 2,
	  "rss_drift_tolerance_bytes": 10,
	  "gc_policy": "natural",
	  "output_dir": ""
	}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse([]string{"--rounds", "4", "--server-go127", "a", "--server-go128", "b", "--client", "c"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rounds != 4 || cfg.Seed != 3 || cfg.DialConcurrency != 4 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.ConnectionCounts) != 2 || cfg.ConnectionCounts[1] != 1000 {
		t.Fatalf("counts = %v", cfg.ConnectionCounts)
	}

	smoke, err := Parse([]string{"--smoke", "--counts", "1,2", "--server-go127", "a", "--server-go128", "b", "--client", "c"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if !smoke.Smoke || smoke.Rounds != 3 || len(smoke.ConnectionCounts) != 2 || smoke.ConnectionCounts[1] != 200 {
		t.Fatalf("smoke = %+v", smoke)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.json")
	if err := os.WriteFile(path, []byte(`{
	  "connection_counts": [0], "rounds": 1, "dial_concurrency": 1,
	  "max_trial_retries": 0, "seed": 1, "gomaxprocs": 1, "gogc": 1,
	  "godebug": "", "stability_polls": 1, "poll_interval_ms": 1,
	  "stability_timeout_sec": 1, "sample_count": 1, "sample_interval_ms": 1,
	  "rss_drift_tolerance_pct": 0, "rss_drift_tolerance_bytes": 0,
	  "gc_policy": "natural", "output_dir": ""
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse([]string{"--gc-policy", "sometimes"}, path); err == nil {
		t.Fatal("expected an invalid gc policy to fail")
	}
	if _, err := Parse([]string{"--counts", "-1"}, path); err == nil {
		t.Fatal("expected a negative count to fail")
	}
	if _, err := Parse([]string{"--rounds", "0"}, path); err == nil {
		t.Fatal("expected zero rounds to fail")
	}
}

func TestLoadRepoDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join("..", "..", "config", "default.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Rounds != 10 || cfg.GOMAXPROCS != 12 || cfg.GCPolicy != "natural" {
		t.Fatalf("defaults = %+v", cfg)
	}
}
