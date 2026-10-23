package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
	"github.com/atkrad/benchmarks/internal/benchkit/experiment"
	"github.com/atkrad/benchmarks/internal/benchkit/measurement"
	"github.com/atkrad/benchmarks/internal/benchkit/process"
)

type fakeChild struct {
	pid    int
	events []json.RawMessage
	writes int
}

func (f *fakeChild) PID() int { return f.pid }

func (f *fakeChild) ReadEvent(context.Context) (json.RawMessage, error) {
	if len(f.events) == 0 {
		return nil, os.ErrClosed
	}
	ev := f.events[0]
	f.events = f.events[1:]
	return ev, nil
}

func (f *fakeChild) Write([]byte) (int, error) {
	f.writes++
	return 1, nil
}

func (f *fakeChild) Kill() error { return nil }

func (f *fakeChild) Wait() (process.Result, error) {
	return process.Result{}, nil
}

func readyEvent() json.RawMessage {
	raw, _ := json.Marshal(protocol.Ready{
		Version:     protocol.Version,
		Event:       protocol.EventReady,
		HTTPSAddr:   "127.0.0.1:9",
		ControlAddr: "127.0.0.1:8",
		CertPEM:     "pem",
		GoVersion:   "go1.test",
	})
	return raw
}

func errorEvent() json.RawMessage {
	raw, _ := json.Marshal(protocol.Error{Version: protocol.Version, Event: protocol.EventError, Message: "boom"})
	return raw
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ConnectionCounts: []int{0},
		Rounds:           1,
		DialConcurrency:  1,
		MaxTrialRetries:  1,
		Seed:             1,
		GOMAXPROCS:       2,
		GOGC:             100,
		StabilityPolls:   2,
		PollIntervalMS:   1,
		StabilityTimeout: 30,
		SampleCount:      2,
		SampleIntervalMS: 1,
		RSSDriftPercent:  1,
		RSSDriftBytes:    100,
		GCPolicy:         "natural",
		OutputDir:        t.TempDir(),
		ServerGo127:      "go127",
		ServerGo128:      "go128",
		Client:           "client",
	}
}

func testDeps(clock *time.Time, start Starter) Deps {
	return Deps{
		Start:     start,
		SkipProbe: true,
		Now:       func() time.Time { return *clock },
		Sleep: func(context.Context, time.Duration) error {
			*clock = clock.Add(time.Millisecond)
			return nil
		},
		Hash: func(string) (string, error) { return "abc", nil },
		Memory: func(int) (measurement.Memory, error) {
			return measurement.Memory{
				RSS:  measurement.Quantity{Bytes: 1000, Supported: true},
				Anon: measurement.Quantity{Bytes: 400, Supported: true},
				Peak: measurement.Quantity{Bytes: 2000, Supported: true},
			}, nil
		},
		Stats: func(context.Context, string) (protocol.Stats, error) {
			return protocol.Stats{
				WallTime:     clock.Format(time.RFC3339Nano),
				GoVersion:    "go1.test",
				Goroutines:   10,
				HeapAlloc:    100,
				StackInuse:   50,
				ConnRequests: []int64{},
			}, nil
		},
	}
}

func TestRunRecordsTrialsAndBenchfmt(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var starts int
	cfg := testConfig(t)
	err := Run(t.Context(), cfg, testDeps(&clock, func(context.Context, process.Spec) (Child, error) {
		starts++
		return &fakeChild{pid: 40 + starts, events: []json.RawMessage{readyEvent()}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if starts != 3 {
		t.Fatalf("starts = %d, want one server per case", starts)
	}
	runDir := filepath.Join(cfg.OutputDir, "http2-idle")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("run dirs = %d", len(entries))
	}
	base := filepath.Join(runDir, entries[0].Name())
	for _, name := range []string{"environment.json", "config.json", "plan.json", "trials.jsonl", "samples.jsonl", "summary.json"} {
		if _, err := os.Stat(filepath.Join(base, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go127.txt", "go128.txt", "go128off.txt"} {
		body, err := os.ReadFile(filepath.Join(base, "benchfmt", name))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	summary, err := os.ReadFile(filepath.Join(base, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(summary) {
		t.Fatal("summary is not json")
	}
	var summaryObj map[string]any
	require.NoError(t, json.Unmarshal(summary, &summaryObj))
	cmd, _ := summaryObj["benchstat"].(string)
	require.Contains(t, cmd, "benchfmt/go127.txt")
	require.Contains(t, cmd, "benchfmt/go128.txt")
	require.Contains(t, cmd, "benchfmt/go128off.txt")
}

func TestRunKeepsFailedAttempts(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var starts int
	cfg := testConfig(t)
	err := Run(t.Context(), cfg, testDeps(&clock, func(context.Context, process.Spec) (Child, error) {
		starts++
		if starts == 1 {
			return &fakeChild{pid: 1, events: []json.RawMessage{errorEvent()}}, nil
		}
		return &fakeChild{pid: starts, events: []json.RawMessage{readyEvent()}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(cfg.OutputDir, "http2-idle")
	entries, err := os.ReadDir(runDir)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(runDir, entries[0].Name(), "trials.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var failed int
	for _, line := range bytesSplit(body) {
		var rec trialRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatal(err)
		}
		if !rec.Success {
			failed++
			if rec.Error == "" {
				t.Fatal("failed trial has no error")
			}
		}
	}
	if failed != 1 {
		t.Fatalf("failed trials = %d", failed)
	}
}

func TestRunRechecksHeldConnections(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var rechecked bool
	cfg := testConfig(t)
	cfg.ConnectionCounts = []int{1}
	deps := testDeps(&clock, func(_ context.Context, spec process.Spec) (Child, error) {
		if spec.Path == cfg.Client {
			return &fakeChild{pid: 7, events: []json.RawMessage{
				readyEvent(),
				mustJSON(protocol.RecheckResult{Version: protocol.Version, Event: protocol.EventRecheckResult, OK: true, Reused: 1}),
				mustJSON(protocol.Closed{Version: protocol.Version, Event: protocol.EventClosed}),
			}}, nil
		}
		return &fakeChild{pid: 9, events: []json.RawMessage{readyEvent()}}, nil
	})
	deps.Stats = func(context.Context, string) (protocol.Stats, error) {
		requests := int64(1)
		per := []int64{1}
		if rechecked {
			requests = 2
			per = []int64{2}
		}
		return protocol.Stats{
			WallTime: clock.Format(time.RFC3339Nano), GoVersion: "go1.test",
			Accepted: 1, Open: 1, H2Requests: requests, ConnRequests: per,
			Goroutines: 10, HeapAlloc: 100, StackInuse: 50,
		}, nil
	}
	prev := deps.Start
	deps.Start = func(ctx context.Context, spec process.Spec) (Child, error) {
		child, err := prev(ctx, spec)
		if spec.Path != cfg.Client {
			rechecked = false
		}
		if err != nil || spec.Path != cfg.Client {
			return child, err
		}
		return &writingChild{fakeChild: child.(*fakeChild), onWrite: func([]byte) { rechecked = true }}, nil
	}
	if err := Run(t.Context(), cfg, deps); err != nil {
		t.Fatal(err)
	}
	if !rechecked {
		t.Fatal("client was not asked to recheck")
	}
}

type writingChild struct {
	*fakeChild
	onWrite func([]byte)
}

func (w *writingChild) Write(p []byte) (int, error) {
	if w.onWrite != nil {
		w.onWrite(p)
	}
	return w.fakeChild.Write(p)
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestTrialKeepsStderrFromWait(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := testConfig(t)
	eng := &engine{
		cfg: cfg,
		deps: testDeps(&clock, func(context.Context, process.Spec) (Child, error) {
			return &stderrChild{fakeChild: &fakeChild{pid: 4}, text: "server blew up\n"}, nil
		}),
		hashes: map[string]string{},
	}
	rec, err := eng.trial(t.Context(), experiment.Cell{Case: "go127", Load: 0}, runCase{Name: "go127", Binary: cfg.ServerGo127}, "run", 0)
	require.Error(t, err)
	require.Contains(t, rec.Stderr, "server blew up")
}

func TestSmokeProfilePrecedesClose(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := testConfig(t)
	cfg.Smoke = true
	cfg.ConnectionCounts = []int{1}
	var closed bool
	var profiled bool
	var profileAfterClose bool
	deps := testDeps(&clock, func(_ context.Context, spec process.Spec) (Child, error) {
		if spec.Path == cfg.Client {
			return &fakeChild{pid: 7, events: []json.RawMessage{
				readyEvent(),
				mustJSON(protocol.RecheckResult{Version: protocol.Version, Event: protocol.EventRecheckResult, OK: true, Reused: 1}),
				mustJSON(protocol.Closed{Version: protocol.Version, Event: protocol.EventClosed}),
			}}, nil
		}
		return &fakeChild{pid: 9, events: []json.RawMessage{readyEvent()}}, nil
	})
	rawProfile := []byte("goroutine profile: total 1\n1 @ 0x1\n#\t0x1\truntime.other+0x1\n")
	deps.Get = func(context.Context, string) ([]byte, error) {
		profiled = true
		if closed {
			profileAfterClose = true
		}
		return rawProfile, nil
	}
	deps.Stats = func(context.Context, string) (protocol.Stats, error) {
		requests := int64(1)
		per := []int64{1}
		if closed {
			requests = 2
			per = []int64{2}
		}
		return protocol.Stats{
			WallTime: clock.Format(time.RFC3339Nano), GoVersion: "go1.test",
			Accepted: 1, Open: 1, H2Requests: requests, ConnRequests: per,
			Goroutines: 10, HeapAlloc: 100, StackInuse: 50,
		}, nil
	}
	prev := deps.Start
	deps.Start = func(ctx context.Context, spec process.Spec) (Child, error) {
		child, err := prev(ctx, spec)
		if spec.Path != cfg.Client {
			closed = false
		}
		if err != nil || spec.Path != cfg.Client {
			return child, err
		}
		return &writingChild{fakeChild: child.(*fakeChild), onWrite: func(p []byte) {
			if bytes.Contains(p, []byte(`"event":"recheck"`)) {
				closed = true
			}
		}}, nil
	}
	require.NoError(t, Run(t.Context(), cfg, deps))
	require.True(t, profiled)
	require.False(t, profileAfterClose)

	entries, err := os.ReadDir(filepath.Join(cfg.OutputDir, "http2-idle"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	diag := filepath.Join(cfg.OutputDir, "http2-idle", entries[0].Name(), "diagnostics", "go127-n1.txt")
	got, err := os.ReadFile(diag)
	require.NoError(t, err)
	require.Equal(t, rawProfile, got)
	summary, err := os.ReadFile(diag + ".summary.json")
	require.NoError(t, err)
	require.JSONEq(t, `{
		"goroutines": 1,
		"http2_serve_frames": 0,
		"read_frames": 0
	}`, string(summary))
}

func TestSmokeProfileKeepsRawWhenParseFails(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "diagnostics"), 0o755))
	cfg := testConfig(t)
	cfg.Smoke = true
	cfg.ConnectionCounts = []int{0}
	rawProfile := []byte("goroutine 1 [running]:\nmain.main()\n")
	deps := testDeps(&clock, func(context.Context, process.Spec) (Child, error) {
		return &fakeChild{pid: 9, events: []json.RawMessage{readyEvent()}}, nil
	})
	deps.Get = func(context.Context, string) ([]byte, error) {
		return rawProfile, nil
	}
	eng := &engine{
		cfg:    cfg,
		deps:   deps,
		dir:    dir,
		hashes: map[string]string{},
		byCase: map[string]*caseFile{},
	}
	_, err := eng.trial(t.Context(), experiment.Cell{Case: "go127", Load: 0}, runCase{Name: "go127"}, "probe", 0)
	require.ErrorContains(t, err, "goroutine profile")
	got, err := os.ReadFile(filepath.Join(dir, "diagnostics", "go127-n0.txt"))
	require.NoError(t, err)
	require.Equal(t, rawProfile, got)
	_, err = os.Stat(filepath.Join(dir, "diagnostics", "go127-n0.txt.summary.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

type stderrChild struct {
	*fakeChild
	text string
}

func (s *stderrChild) ReadEvent(context.Context) (json.RawMessage, error) {
	return nil, os.ErrClosed
}

func (s *stderrChild) Wait() (process.Result, error) {
	return process.Result{ExitCode: 1, Stderr: s.text}, os.ErrClosed
}

func TestRunCancel(t *testing.T) {
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := Run(ctx, testConfig(t), testDeps(&clock, func(context.Context, process.Spec) (Child, error) {
		t.Fatal("starter ran after cancel")
		return nil, nil
	}))
	if err == nil {
		t.Fatal("expected cancellation to fail the run")
	}
}

func bytesSplit(body []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range body {
		if b == '\n' {
			if i > start {
				out = append(out, body[start:i])
			}
			start = i + 1
		}
	}
	return out
}
