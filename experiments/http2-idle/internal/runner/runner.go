package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
	"github.com/atkrad/benchmarks/internal/benchkit/experiment"
	"github.com/atkrad/benchmarks/internal/benchkit/measurement"
	"github.com/atkrad/benchmarks/internal/benchkit/process"
	"github.com/atkrad/benchmarks/internal/benchkit/report"
	"github.com/atkrad/benchmarks/internal/benchkit/system"
)

const (
	fdMargin        = 256
	readyTimeout    = 30 * time.Second
	maxSampleSkew   = 5 * time.Second
	benchstatIgnore = "case,go-version,godebug,server-sha256"
)

// benchfmtInvariants must match across case files or benchstat splits the tables.
var benchfmtInvariants = []string{
	"goos",
	"goarch",
	"cpu",
	"gc-policy",
	"gomaxprocs",
	"gogc",
	"run-id",
	"run-kind",
}

// Child is a process the runner can talk to. *process.Proc implements it.
type Child interface {
	PID() int
	ReadEvent(context.Context) (json.RawMessage, error)
	Write([]byte) (int, error)
	Kill() error
	Wait() (process.Result, error)
}

// Starter launches a child. Tests substitute a fake.
type Starter func(context.Context, process.Spec) (Child, error)

// Deps are the host operations a run needs. Zero fields use the real host.
type Deps struct {
	Start     Starter
	Memory    func(pid int) (measurement.Memory, error)
	Stats     func(context.Context, string) (protocol.Stats, error)
	Post      func(context.Context, string) error
	Get       func(context.Context, string) ([]byte, error)
	Now       func() time.Time
	Sleep     func(context.Context, time.Duration) error
	Hash      func(string) (string, error)
	SkipProbe bool
}

// Run executes the plan and writes the result tree under cfg.OutputDir.
// A failed cell is recorded and makes Run return an error. It is not dropped.
func Run(ctx context.Context, cfg Config, deps Deps) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.OutputDir == "" {
		return errors.New("output directory is required (--out)")
	}
	deps = deps.defaults()
	cases, err := cfg.cases()
	if err != nil {
		return err
	}
	if err := checkFDs(maxCount(cfg.ConnectionCounts)); err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, path := range []string{cfg.ServerGo127, cfg.ServerGo128, cfg.Client} {
		sum, err := deps.Hash(path)
		if err != nil {
			return err
		}
		hashes[path] = sum
	}

	parent := filepath.Join(cfg.OutputDir, "http2-idle")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	prefix := deps.Now().UTC().Format("20060102T150405Z") + "-"
	dir, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		return fmt.Errorf("create run directory: %w", err)
	}
	runID := filepath.Base(dir)
	if err := os.MkdirAll(filepath.Join(dir, "benchfmt"), 0o755); err != nil {
		return err
	}
	if cfg.Smoke {
		if err := os.MkdirAll(filepath.Join(dir, "diagnostics"), 0o755); err != nil {
			return err
		}
	}
	info, err := system.Collect("", system.Identity{
		NixpkgsRev:  cfg.NixpkgsRev,
		NixpkgsHash: cfg.NixpkgsHash,
		SourceRev:   cfg.SourceRev,
		Dirty:       cfg.SourceDirty,
	})
	if err != nil {
		return err
	}
	if err := report.WriteJSONFile(filepath.Join(dir, "environment.json"), map[string]any{
		"host": info, "binaries": hashes, "go128_rev": cfg.Go128Rev,
	}); err != nil {
		return err
	}
	if err := report.WriteJSONFile(filepath.Join(dir, "config.json"), cfg); err != nil {
		return err
	}

	eng := &engine{cfg: cfg, deps: deps, dir: dir, runID: runID, info: info, hashes: hashes, byCase: map[string]*caseFile{}}
	trials, err := report.CreateJSONLines(filepath.Join(dir, "trials.jsonl"))
	if err != nil {
		return err
	}
	defer func() { _ = trials.Close() }()
	samples, err := report.CreateJSONLines(filepath.Join(dir, "samples.jsonl"))
	if err != nil {
		return err
	}
	defer func() { _ = samples.Close() }()
	eng.trials = trials
	eng.samples = samples

	if !deps.SkipProbe {
		if err := eng.probe(ctx); err != nil {
			_ = eng.finish(true)
			return err
		}
	}

	cells, err := experiment.Plan(caseNames(cases), cfg.ConnectionCounts, cfg.Rounds, cfg.Seed)
	if err != nil {
		return err
	}
	if err := report.WriteJSONFile(filepath.Join(dir, "plan.json"), cells); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	incomplete := false
	for _, cell := range cells {
		if err := ctx.Err(); err != nil {
			incomplete = true
			break
		}
		cas := cases[indexCase(cases, cell.Case)]
		ok := false
		for attempt := 0; attempt <= cfg.MaxTrialRetries; attempt++ {
			rec, runErr := eng.trial(ctx, cell, cas, "run", attempt)
			if err := trials.Append(rec); err != nil {
				return err
			}
			if runErr == nil {
				ok = true
				break
			}
		}
		if !ok {
			incomplete = true
		}
	}
	if err := eng.finish(incomplete); err != nil {
		return err
	}
	if incomplete {
		return errors.New("run incomplete: a cell had no successful trial")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

type caseFile struct {
	config  map[string]string
	results []report.Result
}

type engine struct {
	cfg      Config
	deps     Deps
	dir      string
	runID    string
	info     system.Info
	hashes   map[string]string
	trials   *report.JSONLines
	samples  *report.JSONLines
	byCase   map[string]*caseFile
	probe128 int
	probeOff int
}

type trialRecord struct {
	Phase       string `json:"phase"`
	Case        string `json:"case"`
	Connections int    `json:"connections"`
	Round       int    `json:"round"`
	Attempt     int    `json:"attempt"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	Started     string `json:"started"`
	Finished    string `json:"finished"`
	PID         int    `json:"pid,omitempty"`
	GoVersion   string `json:"go_version,omitempty"`
	GODEBUG     string `json:"godebug,omitempty"`
	SHA256      string `json:"server_sha256,omitempty"`
	Retries     int    `json:"client_retries,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	// Goroutines and RSSBytes are medians of the trial sample window.
	// Both stay float64 so an even-sized window can keep a half unit.
	Goroutines float64 `json:"goroutines,omitempty"`
	RSSBytes   float64 `json:"rss_bytes,omitempty"`
}

func (e *engine) probe(ctx context.Context) error {
	for _, name := range []string{"go128", "go128off"} {
		cas := e.mustCase(name)
		base, err := e.goroutines(ctx, cas, 0)
		if err != nil {
			return fmt.Errorf("parking probe %s baseline: %w", name, err)
		}
		loaded, err := e.goroutines(ctx, cas, 16)
		if err != nil {
			return fmt.Errorf("parking probe %s: %w", name, err)
		}
		if name == "go128" {
			e.probe128 = loaded - base
		} else {
			e.probeOff = loaded - base
		}
	}
	if e.probe128 >= e.probeOff {
		return fmt.Errorf("parking probe: go128 goroutine delta %d is not below go128off %d; the http2serveparking behavior was not observed", e.probe128, e.probeOff)
	}
	return nil
}

func (e *engine) goroutines(ctx context.Context, cas runCase, n int) (int, error) {
	rec, err := e.trial(ctx, experiment.Cell{Case: cas.Name, Load: n, Round: -1}, cas, "probe", 0)
	if appendErr := e.trials.Append(rec); appendErr != nil {
		return 0, appendErr
	}
	if err != nil {
		return 0, err
	}
	return int(math.Round(rec.Goroutines)), nil
}

func (e *engine) trial(ctx context.Context, cell experiment.Cell, cas runCase, phase string, attempt int) (rec trialRecord, trialErr error) {
	started := e.deps.Now()
	rec = trialRecord{
		Phase: phase, Case: cell.Case, Connections: cell.Load, Round: cell.Round,
		Attempt: attempt, Started: started.Format(time.RFC3339Nano),
		GODEBUG: cas.GODEBUG, SHA256: e.hashes[cas.Binary],
	}
	var srv, cli Child
	defer func() {
		text := stopChild(cli) + stopChild(srv)
		rec.Stderr = clip(text, 4096)
		if rec.Finished == "" {
			rec.Finished = e.deps.Now().Format(time.RFC3339Nano)
		}
	}()
	fail := func(err error) (trialRecord, error) {
		rec.Success = false
		rec.Error = err.Error()
		rec.Finished = e.deps.Now().Format(time.RFC3339Nano)
		trialErr = err
		return rec, err
	}

	trialCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var err error
	srv, err = e.deps.Start(trialCtx, serverSpec(cas, e.cfg))
	if err != nil {
		return fail(err)
	}
	rec.PID = srv.PID()

	readyCtx, readyCancel := context.WithTimeout(trialCtx, readyTimeout)
	msg, err := readEvent(readyCtx, srv)
	readyCancel()
	if err != nil {
		return fail(err)
	}
	ready, ok := msg.(*protocol.Ready)
	if !ok {
		return fail(fmt.Errorf("server sent %s, want ready", protocol.EventOf(msg)))
	}
	if ready.GoVersion == "" {
		return fail(errors.New("server ready message has no go version"))
	}
	if cas.Expect != "" && ready.GoVersion != cas.Expect {
		return fail(fmt.Errorf("server go version %q, want %q", ready.GoVersion, cas.Expect))
	}
	rec.GoVersion = ready.GoVersion
	base := "http://" + ready.ControlAddr

	if cell.Load > 0 {
		certPath, err := writeCert(ready.CertPEM)
		if err != nil {
			return fail(err)
		}
		defer func() { _ = os.RemoveAll(filepath.Dir(certPath)) }()
		cli, err = e.deps.Start(trialCtx, clientSpec(e.cfg, ready.HTTPSAddr, certPath, cell.Load))
		if err != nil {
			return fail(err)
		}
		clientReady, err := e.readClientReady(trialCtx, cli)
		if err != nil {
			return fail(err)
		}
		rec.Retries = clientReady.Retries
	}

	if e.cfg.GCPolicy == "forced" {
		if err := e.deps.Post(trialCtx, base+"/gc"); err != nil {
			return fail(fmt.Errorf("forced gc: %w", err))
		}
	}

	outcome, err := experiment.Stabilize(trialCtx, func(ctx context.Context) (experiment.Observation, error) {
		stats, err := e.deps.Stats(ctx, base+"/stats")
		if err != nil {
			return experiment.Observation{}, err
		}
		if err := checkHeld(cell.Load, stats); err != nil {
			// A mismatch resets the window instead of failing the poll,
			// unless the server is reporting the wrong protocol.
			if stats.NonH2Requests != 0 || stats.WriteErrors != 0 {
				return experiment.Observation{}, err
			}
		}
		mem, err := e.deps.Memory(srv.PID())
		if err != nil {
			return experiment.Observation{}, err
		}
		rss, err := mem.RSS.Value()
		if err != nil {
			return experiment.Observation{}, err
		}
		return experiment.Observation{ActiveUnits: int(stats.Open), Goroutines: stats.Goroutines, RSSBytes: rss}, nil
	}, e.criteria(cell.Load), e.deps.Now, e.deps.Sleep)
	if err != nil {
		return fail(err)
	}
	if !outcome.Stable {
		return fail(errors.New("stability window was not reached"))
	}

	samples := make([]observation, 0, e.cfg.SampleCount)
	for i := range e.cfg.SampleCount {
		if i > 0 {
			if err := e.deps.Sleep(trialCtx, time.Duration(e.cfg.SampleIntervalMS)*time.Millisecond); err != nil {
				return fail(err)
			}
		}
		sample, err := e.observe(trialCtx, base, srv.PID())
		if err != nil {
			return fail(err)
		}
		if err := checkHeld(cell.Load, sample.stats); err != nil {
			return fail(err)
		}
		samples = append(samples, sample)
		if phase == "run" {
			if err := e.samples.Append(sample.record(cell, attempt, i)); err != nil {
				return fail(err)
			}
		}
	}
	metrics, err := aggregateSamples(samples)
	if err != nil {
		return fail(err)
	}

	// Profile while the held connections are still idle, before recheck or close.
	if cfgSmokeProfile(e.cfg, cell) {
		body, err := e.deps.Get(trialCtx, base+"/debug/pprof/goroutine?debug=1")
		if err != nil {
			return fail(err)
		}
		name := fmt.Sprintf("%s-n%d.txt", cell.Case, cell.Load)
		path := filepath.Join(e.dir, "diagnostics", name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return fail(err)
		}
		// The raw dump is already on disk. A parse error must not invent a summary.
		sum, err := SummarizeProfile(string(body))
		if err != nil {
			return fail(fmt.Errorf("goroutine profile: %w", err))
		}
		if err := report.WriteJSONFile(filepath.Join(e.dir, "diagnostics", name+".summary.json"), sum); err != nil {
			return fail(err)
		}
	}

	if cli != nil {
		if err := writeMsg(cli, protocol.Recheck{}); err != nil {
			return fail(err)
		}
		msg, err := readEvent(trialCtx, cli)
		if err != nil {
			return fail(err)
		}
		result, ok := msg.(*protocol.RecheckResult)
		if !ok || !result.OK {
			return fail(fmt.Errorf("recheck: %#v", msg))
		}
	}

	// Count connections before the client exits. Closing it drops the sockets.
	final, err := e.deps.Stats(trialCtx, base+"/stats")
	if err != nil {
		return fail(err)
	}
	if err := checkStats(cell.Load, final, cas.Expect, e.deps.Now()); err != nil {
		return fail(err)
	}
	if cli != nil {
		if err := writeMsg(cli, protocol.Close{}); err != nil {
			return fail(err)
		}
		if _, err := readEvent(trialCtx, cli); err != nil {
			return fail(err)
		}
	}

	rec.Success = true
	rec.Finished = e.deps.Now().Format(time.RFC3339Nano)
	rec.Goroutines = metrics.Goroutines
	rec.RSSBytes = metrics.RSS
	if phase == "run" {
		e.addResult(cas, cell.Load, ready.GoVersion, metrics)
	}
	return rec, nil
}

// trialMetrics is the per-trial aggregate written to benchfmt and trials.jsonl.
// Every field is the median of that metric over the sample window.
type trialMetrics struct {
	RSS          float64
	Goroutines   float64
	HeapAlloc    float64
	StackInuse   float64
	HeapInuse    float64
	HeapIdle     float64
	HeapReleased float64
}

func aggregateSamples(samples []observation) (trialMetrics, error) {
	if len(samples) == 0 {
		return trialMetrics{}, errors.New("aggregate samples: empty sample set")
	}
	rss := make([]float64, 0, len(samples))
	goroutines := make([]float64, 0, len(samples))
	heapAlloc := make([]float64, 0, len(samples))
	stackInuse := make([]float64, 0, len(samples))
	heapInuse := make([]float64, 0, len(samples))
	heapIdle := make([]float64, 0, len(samples))
	heapReleased := make([]float64, 0, len(samples))
	for _, sample := range samples {
		rss = append(rss, float64(sample.rss))
		goroutines = append(goroutines, float64(sample.stats.Goroutines))
		heapAlloc = append(heapAlloc, float64(sample.stats.HeapAlloc))
		stackInuse = append(stackInuse, float64(sample.stats.StackInuse))
		heapInuse = append(heapInuse, float64(sample.stats.HeapInuse))
		heapIdle = append(heapIdle, float64(sample.stats.HeapIdle))
		heapReleased = append(heapReleased, float64(sample.stats.HeapReleased))
	}
	return trialMetrics{
		RSS:          median(rss),
		Goroutines:   median(goroutines),
		HeapAlloc:    median(heapAlloc),
		StackInuse:   median(stackInuse),
		HeapInuse:    median(heapInuse),
		HeapIdle:     median(heapIdle),
		HeapReleased: median(heapReleased),
	}, nil
}

type observation struct {
	stats   protocol.Stats
	mem     measurement.Memory
	rss     uint64
	statsAt time.Time
	memAt   time.Time
	skew    time.Duration
}

func (o observation) record(cell experiment.Cell, attempt, index int) map[string]any {
	return map[string]any{
		"case": cell.Case, "connections": cell.Load, "round": cell.Round,
		"attempt": attempt, "index": index,
		"stats_at":      o.stats.WallTime,
		"host_stats_at": o.statsAt.Format(time.RFC3339Nano),
		"memory_at":     o.memAt.Format(time.RFC3339Nano),
		"skew_ns":       o.skew.Nanoseconds(),
		"rss_bytes":     o.rss, "rss_supported": o.mem.RSS.Supported,
		"anon_bytes": o.mem.Anon.Bytes, "anon_supported": o.mem.Anon.Supported,
		"peak_bytes": o.mem.Peak.Bytes, "peak_supported": o.mem.Peak.Supported,
		"heap_alloc": o.stats.HeapAlloc, "stack_inuse": o.stats.StackInuse,
		"heap_inuse": o.stats.HeapInuse, "heap_idle": o.stats.HeapIdle,
		"heap_released": o.stats.HeapReleased, "goroutines": o.stats.Goroutines,
		"num_gc": o.stats.NumGC, "pause_total_ns": o.stats.PauseTotalNs,
		"open": o.stats.Open,
	}
}

func (e *engine) observe(ctx context.Context, base string, pid int) (observation, error) {
	statsAt := e.deps.Now()
	stats, err := e.deps.Stats(ctx, base+"/stats")
	if err != nil {
		return observation{}, err
	}
	memAt := e.deps.Now()
	mem, err := e.deps.Memory(pid)
	if err != nil {
		return observation{}, err
	}
	rss, err := mem.RSS.Value()
	if err != nil {
		return observation{}, fmt.Errorf("rss: %w", err)
	}
	skew := memAt.Sub(statsAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSampleSkew {
		return observation{}, fmt.Errorf("memory sample is %s after the runtime sample", skew)
	}
	wall, err := time.Parse(time.RFC3339Nano, stats.WallTime)
	if err != nil {
		return observation{}, fmt.Errorf("stats wall time: %w", err)
	}
	wallSkew := statsAt.Sub(wall)
	if wallSkew < 0 {
		wallSkew = -wallSkew
	}
	if wallSkew > maxSampleSkew {
		return observation{}, fmt.Errorf("runtime sample is %s away from the host clock", wallSkew)
	}
	return observation{stats: stats, mem: mem, rss: rss, statsAt: statsAt, memAt: memAt, skew: skew}, nil
}

func (e *engine) addResult(cas runCase, connections int, goVersion string, metrics trialMetrics) {
	file := e.byCase[cas.Name]
	if file == nil {
		file = &caseFile{config: map[string]string{
			"goos": infoOr(e.info.OS, "linux"), "goarch": e.info.Arch, "cpu": e.info.CPUModel,
			"gc-policy": e.cfg.GCPolicy, "gomaxprocs": strconv.Itoa(e.cfg.GOMAXPROCS),
			"gogc": strconv.Itoa(e.cfg.GOGC), "run-id": e.runID,
			"run-kind": runKind(e.cfg),
			"case":     cas.Name, "go-version": goVersion, "godebug": cas.GODEBUG,
			"server-sha256": e.hashes[cas.Binary],
		}}
		e.byCase[cas.Name] = file
	}
	file.results = append(file.results, report.Result{
		Name: connectionsName(connections),
		Values: []report.Value{
			{Value: metrics.RSS, Unit: "rss-B"},
			{Value: metrics.StackInuse, Unit: "stack-inuse-B"},
			{Value: metrics.HeapAlloc, Unit: "heap-alloc-B"},
			{Value: metrics.HeapInuse, Unit: "heap-inuse-B"},
			{Value: metrics.HeapIdle, Unit: "heap-idle-B"},
			{Value: metrics.HeapReleased, Unit: "heap-released-B"},
			{Value: metrics.Goroutines, Unit: "goroutines"},
		},
	})
}

func (e *engine) finish(incomplete bool) error {
	var files []report.File
	order := []string{"go127", "go128", "go128off"}
	for _, name := range order {
		file := e.byCase[name]
		if file == nil || len(file.results) == 0 {
			continue
		}
		files = append(files, report.File{
			Config:  file.config,
			Units:   []report.UnitMeta{{Unit: "goroutines", Key: "assume", Value: "exact"}},
			Results: file.results,
		})
	}
	if len(files) > 0 {
		keys := benchfmtInvariants
		if err := report.EqualInvariants(files, keys); err != nil {
			return err
		}
		for _, file := range files {
			path := filepath.Join(e.dir, "benchfmt", file.Config["case"]+".txt")
			buf := &bytes.Buffer{}
			if err := report.WriteBenchfmt(buf, file); err != nil {
				return err
			}
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				return err
			}
		}
	}
	cmd := "benchstat -ignore " + benchstatIgnore + " benchfmt/go127.txt benchfmt/go128.txt benchfmt/go128off.txt"
	summary := map[string]any{
		"publication": !e.cfg.Smoke && !incomplete,
		"incomplete":  incomplete,
		"smoke":       e.cfg.Smoke,
		"benchstat":   cmd,
		"note":        "Each benchfmt value is the median of that trial's sample window. benchstat compares independent trials. It does not remove host noise. Baseline-adjusted RSS subtracts the same case at 0 connections from the raw median.",
		"cases":       e.summaries(),
	}
	return report.WriteJSONFile(filepath.Join(e.dir, "summary.json"), summary)
}

func (e *engine) summaries() []map[string]any {
	var out []map[string]any
	for name, file := range e.byCase {
		byCount := map[int][]float64{}
		for _, result := range file.results {
			n := connectionsFromName(result.Name)
			byCount[n] = append(byCount[n], valueOf(result, "rss-B"))
		}
		var baseline float64
		haveBase := false
		if values, ok := byCount[0]; ok {
			baseline = median(values)
			haveBase = true
		}
		for n, values := range byCount {
			item := map[string]any{
				"case": name, "connections": n, "trials": len(values),
				"rss_median": median(values), "rss_min": slices.Min(values), "rss_max": slices.Max(values),
			}
			if haveBase {
				item["baseline_adjusted_rss_median"] = median(values) - baseline
			}
			out = append(out, item)
		}
	}
	return out
}

func (e *engine) criteria(load int) experiment.Criteria {
	return experiment.Criteria{
		ExpectedActiveUnits: load,
		MinPolls:            e.cfg.StabilityPolls,
		Interval:            time.Duration(e.cfg.PollIntervalMS) * time.Millisecond,
		Timeout:             time.Duration(e.cfg.StabilityTimeout) * time.Second,
		DriftPercent:        e.cfg.RSSDriftPercent,
		DriftBytes:          e.cfg.RSSDriftBytes,
	}
}

func (e *engine) readClientReady(ctx context.Context, cli Child) (*protocol.Ready, error) {
	state := ""
	for {
		msg, err := readEvent(ctx, cli)
		if err != nil {
			return nil, err
		}
		next, err := protocol.AdvanceClient(state, protocol.EventOf(msg))
		if err != nil {
			return nil, err
		}
		state = next
		switch m := msg.(type) {
		case *protocol.Ready:
			return m, nil
		case *protocol.Error:
			return nil, errors.New(m.Message)
		}
	}
}

func (e *engine) mustCase(name string) runCase {
	cases, _ := e.cfg.cases()
	return cases[indexCase(cases, name)]
}

type runCase struct {
	Name    string
	Binary  string
	Expect  string
	GODEBUG string
}

func (c Config) cases() ([]runCase, error) {
	if c.ServerGo127 == "" || c.ServerGo128 == "" || c.Client == "" {
		return nil, errors.New("config: --server-go127, --server-go128, and --client are required")
	}
	return []runCase{
		{Name: "go127", Binary: c.ServerGo127, Expect: c.ExpectGo127, GODEBUG: c.GODEBUG},
		{Name: "go128", Binary: c.ServerGo128, Expect: c.ExpectGo128, GODEBUG: c.GODEBUG},
		{Name: "go128off", Binary: c.ServerGo128, Expect: c.ExpectGo128, GODEBUG: mergeDebug(c.GODEBUG, "http2serveparking=0")},
	}, nil
}

func (d Deps) defaults() Deps {
	if d.Start == nil {
		d.Start = func(ctx context.Context, spec process.Spec) (Child, error) {
			return process.Start(ctx, spec)
		}
	}
	if d.Memory == nil {
		d.Memory = func(pid int) (measurement.Memory, error) {
			return measurement.ReadMemory("", pid)
		}
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Sleep == nil {
		d.Sleep = sleepCtx
	}
	if d.Hash == nil {
		d.Hash = fileSHA256
	}
	client := &http.Client{Timeout: 5 * time.Second}
	if d.Stats == nil {
		d.Stats = func(ctx context.Context, url string) (protocol.Stats, error) {
			body, err := httpDo(ctx, client, http.MethodGet, url)
			if err != nil {
				return protocol.Stats{}, err
			}
			var stats protocol.Stats
			if err := json.Unmarshal(body, &stats); err != nil {
				return protocol.Stats{}, fmt.Errorf("decode stats: %w", err)
			}
			return stats, nil
		}
	}
	if d.Post == nil {
		d.Post = func(ctx context.Context, url string) error {
			_, err := httpDo(ctx, client, http.MethodPost, url)
			return err
		}
	}
	if d.Get == nil {
		d.Get = func(ctx context.Context, url string) ([]byte, error) {
			return httpDo(ctx, client, http.MethodGet, url)
		}
	}
	return d
}

func httpDo(ctx context.Context, client *http.Client, method, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s", method, url, resp.Status)
	}
	return body, nil
}

func serverSpec(cas runCase, cfg Config) process.Spec {
	args := []string{}
	if cfg.GCPolicy == "forced" {
		args = append(args, "-allow-forced-gc")
	}
	if cfg.Smoke {
		args = append(args, "-enable-pprof")
	}
	return process.Spec{
		Path: cas.Binary, Args: args, Env: serverEnv(cfg, cas.GODEBUG),
		StdoutLimit: 1 << 20, StderrLimit: 64 << 10,
	}
}

func clientSpec(cfg Config, httpsAddr, certPath string, n int) process.Spec {
	return process.Spec{
		Path: cfg.Client,
		Args: []string{
			"-url", "https://" + httpsAddr + "/work",
			"-cert", certPath,
			"-n", strconv.Itoa(n),
			"-dial-concurrency", strconv.Itoa(cfg.DialConcurrency),
			"-max-retries", strconv.Itoa(cfg.DialRetries),
			"-server-name", "localhost",
		},
		Env:         []string{"GOTOOLCHAIN=local"},
		StdoutLimit: 1 << 20,
		StderrLimit: 64 << 10,
	}
}

func serverEnv(cfg Config, godebug string) []string {
	env := []string{
		"GOMAXPROCS=" + strconv.Itoa(cfg.GOMAXPROCS),
		"GOGC=" + strconv.Itoa(cfg.GOGC),
		"GOTOOLCHAIN=local",
	}
	if godebug != "" {
		env = append(env, "GODEBUG="+godebug)
	}
	return env
}

func writeCert(pemText string) (string, error) {
	dir, err := os.MkdirTemp("", "http2-idle-cert-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(path, []byte(pemText), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func readEvent(ctx context.Context, child Child) (protocol.Message, error) {
	raw, err := child.ReadEvent(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.Decode(raw)
}

func writeMsg(child Child, msg protocol.Message) error {
	var buf bytes.Buffer
	if err := protocol.Encode(&buf, msg); err != nil {
		return err
	}
	_, err := child.Write(buf.Bytes())
	return err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func checkFDs(maxN int) error {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return fmt.Errorf("file descriptors: %w", err)
	}
	need := uint64(maxN + fdMargin)
	if lim.Cur < need {
		return fmt.Errorf("file descriptors: this run needs about %d open files and ulimit -n is %d. Raise it, for example `ulimit -n %d`. The runner does not change the limit", need, lim.Cur, need)
	}
	return nil
}

func mergeDebug(base, extra string) string {
	parts := []string{}
	for _, part := range strings.Split(base, ",") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "http2serveparking=") {
			continue
		}
		parts = append(parts, part)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, ",")
}

func maxCount(counts []int) int {
	m := 0
	for _, n := range counts {
		if n > m {
			m = n
		}
	}
	return m
}

func caseNames(cases []runCase) []string {
	names := make([]string, len(cases))
	for i, cas := range cases {
		names[i] = cas.Name
	}
	return names
}

func indexCase(cases []runCase, name string) int {
	for i, cas := range cases {
		if cas.Name == name {
			return i
		}
	}
	return 0
}

func connectionsName(n int) string {
	return "HTTP2Idle/connections=" + strconv.Itoa(n)
}

func stopChild(child Child) string {
	if child == nil {
		return ""
	}
	_ = child.Kill()
	res, _ := child.Wait()
	return res.Stderr
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

func valueOf(result report.Result, unit string) float64 {
	for _, value := range result.Values {
		if value.Unit == unit {
			return value.Value
		}
	}
	return 0
}

func connectionsFromName(name string) int {
	_, tail, ok := strings.Cut(name, "connections=")
	if !ok {
		return -1
	}
	n, _ := strconv.Atoi(tail)
	return n
}

func runKind(cfg Config) string {
	if cfg.Smoke {
		return "smoke"
	}
	return "measurement"
}

func cfgSmokeProfile(cfg Config, cell experiment.Cell) bool {
	if !cfg.Smoke {
		return false
	}
	return cell.Load == maxCount(cfg.ConnectionCounts) && cell.Round == 0
}

func infoOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func clip(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n] + "\n[truncated]\n"
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
