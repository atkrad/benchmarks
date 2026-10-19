// Package runner orchestrates HTTP/2 idle-connection trials on the host.
package runner

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is one experiment run. JSON fields come from the defaults file.
// Binary paths and expected toolchain strings come from flags, usually the
// Nix wrapper.
type Config struct {
	ConnectionCounts []int   `json:"connection_counts"`
	Rounds           int     `json:"rounds"`
	DialConcurrency  int     `json:"dial_concurrency"`
	MaxTrialRetries  int     `json:"max_trial_retries"`
	Seed             uint64  `json:"seed"`
	GOMAXPROCS       int     `json:"gomaxprocs"`
	GOGC             int     `json:"gogc"`
	GODEBUG          string  `json:"godebug"`
	StabilityPolls   int     `json:"stability_polls"`
	PollIntervalMS   int     `json:"poll_interval_ms"`
	StabilityTimeout int     `json:"stability_timeout_sec"`
	SampleCount      int     `json:"sample_count"`
	SampleIntervalMS int     `json:"sample_interval_ms"`
	RSSDriftPercent  float64 `json:"rss_drift_tolerance_pct"`
	RSSDriftBytes    uint64  `json:"rss_drift_tolerance_bytes"`
	GCPolicy         string  `json:"gc_policy"`
	OutputDir        string  `json:"output_dir"`

	Smoke       bool   `json:"-"`
	ServerGo127 string `json:"-"`
	ServerGo128 string `json:"-"`
	Client      string `json:"-"`
	ExpectGo127 string `json:"-"`
	ExpectGo128 string `json:"-"`
	Go128Rev    string `json:"-"`
	NixpkgsRev  string `json:"-"`
	NixpkgsHash string `json:"-"`
	SourceRev   string `json:"-"`
	SourceDirty bool   `json:"-"`
	ConfigPath  string `json:"-"`
}

// LoadFile decodes a defaults file.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.ConfigPath = path
	return cfg, nil
}

// Parse loads defaults from defaultsPath, then applies flags that were set
// on the command line. Unset flags keep the file value. --smoke replaces
// the connection counts and the round count; it is not a publication run.
func Parse(args []string, defaultsPath string) (Config, error) {
	cfg, err := LoadFile(defaultsPath)
	if err != nil {
		return Config{}, err
	}
	fs := flag.NewFlagSet("http2-idle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	counts := fs.String("counts", "", "comma-separated connection counts")
	rounds := fs.Int("rounds", cfg.Rounds, "independent trials per case and count")
	out := fs.String("out", "", "directory for this run's parent output")
	dial := fs.Int("dial-concurrency", cfg.DialConcurrency, "parallel connection dials")
	retries := fs.Int("max-trial-retries", cfg.MaxTrialRetries, "extra attempts after a failed trial")
	seed := fs.Uint64("seed", cfg.Seed, "plan shuffle seed")
	gomaxprocs := fs.Int("gomaxprocs", cfg.GOMAXPROCS, "GOMAXPROCS for the server")
	gogc := fs.Int("gogc", cfg.GOGC, "GOGC for the server")
	godebug := fs.String("godebug", cfg.GODEBUG, "extra GODEBUG for every server")
	polls := fs.Int("stability-polls", cfg.StabilityPolls, "consecutive steady polls required")
	pollMS := fs.Int("poll-interval-ms", cfg.PollIntervalMS, "milliseconds between stability polls")
	timeout := fs.Int("stability-timeout-sec", cfg.StabilityTimeout, "seconds allowed for the series to settle")
	samples := fs.Int("samples", cfg.SampleCount, "samples recorded after the series settles")
	sampleMS := fs.Int("sample-interval-ms", cfg.SampleIntervalMS, "milliseconds between samples")
	driftPct := fs.Float64("rss-drift-pct", cfg.RSSDriftPercent, "allowed RSS span as a percent of the minimum")
	driftBytes := fs.Uint64("rss-drift-bytes", cfg.RSSDriftBytes, "allowed RSS span in bytes")
	gc := fs.String("gc-policy", cfg.GCPolicy, "natural or forced")
	smoke := fs.Bool("smoke", false, "short non-publication run with profiling")
	fs.StringVar(&cfg.ServerGo127, "server-go127", "", "server binary built with Go 1.27.2")
	fs.StringVar(&cfg.ServerGo128, "server-go128", "", "server binary built with the pinned Go 1.28 commit")
	fs.StringVar(&cfg.Client, "client", "", "client binary")
	fs.StringVar(&cfg.ExpectGo127, "expect-go127", "", "runtime.Version required for go127")
	fs.StringVar(&cfg.ExpectGo128, "expect-go128", "", "runtime.Version required for go128 and go128off")
	fs.StringVar(&cfg.Go128Rev, "go128-rev", "", "pinned Go source revision")
	fs.StringVar(&cfg.NixpkgsRev, "nixpkgs-rev", "", "nixpkgs revision")
	fs.StringVar(&cfg.NixpkgsHash, "nixpkgs-hash", "", "nixpkgs nar hash")
	fs.StringVar(&cfg.SourceRev, "source-rev", "", "benchmarks source revision")
	fs.BoolVar(&cfg.SourceDirty, "source-dirty", false, "source tree had uncommitted changes")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	var flagErr error
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "counts":
			parsed, err2 := parseCounts(*counts)
			if err2 != nil {
				flagErr = err2
				return
			}
			cfg.ConnectionCounts = parsed
		case "rounds":
			cfg.Rounds = *rounds
		case "out":
			cfg.OutputDir = *out
		case "dial-concurrency":
			cfg.DialConcurrency = *dial
		case "max-trial-retries":
			cfg.MaxTrialRetries = *retries
		case "seed":
			cfg.Seed = *seed
		case "gomaxprocs":
			cfg.GOMAXPROCS = *gomaxprocs
		case "gogc":
			cfg.GOGC = *gogc
		case "godebug":
			cfg.GODEBUG = *godebug
		case "stability-polls":
			cfg.StabilityPolls = *polls
		case "poll-interval-ms":
			cfg.PollIntervalMS = *pollMS
		case "stability-timeout-sec":
			cfg.StabilityTimeout = *timeout
		case "samples":
			cfg.SampleCount = *samples
		case "sample-interval-ms":
			cfg.SampleIntervalMS = *sampleMS
		case "rss-drift-pct":
			cfg.RSSDriftPercent = *driftPct
		case "rss-drift-bytes":
			cfg.RSSDriftBytes = *driftBytes
		case "gc-policy":
			cfg.GCPolicy = *gc
		case "smoke":
			cfg.Smoke = *smoke
		}
	})
	if flagErr != nil {
		return Config{}, flagErr
	}
	if cfg.Smoke {
		cfg.ConnectionCounts = []int{0, 200}
		cfg.Rounds = 3
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseCounts(text string) ([]int, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("counts is empty")
	}
	parts := strings.Split(text, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("counts: %w", err)
		}
		out = append(out, n)
	}
	return out, nil
}

// Validate rejects combinations that would make the run meaningless.
func (c Config) Validate() error {
	if len(c.ConnectionCounts) == 0 {
		return errors.New("config: no connection counts")
	}
	seen := map[int]struct{}{}
	for _, n := range c.ConnectionCounts {
		if n < 0 {
			return fmt.Errorf("config: connection count %d", n)
		}
		if _, ok := seen[n]; ok {
			return fmt.Errorf("config: duplicate connection count %d", n)
		}
		seen[n] = struct{}{}
	}
	if c.Rounds < 1 {
		return fmt.Errorf("config: rounds %d", c.Rounds)
	}
	if c.DialConcurrency < 1 {
		return fmt.Errorf("config: dial concurrency %d", c.DialConcurrency)
	}
	if c.MaxTrialRetries < 0 {
		return fmt.Errorf("config: retries %d", c.MaxTrialRetries)
	}
	if c.GOMAXPROCS < 1 || c.GOGC < 1 {
		return errors.New("config: GOMAXPROCS and GOGC must be >= 1")
	}
	if c.StabilityPolls < 1 || c.PollIntervalMS < 1 || c.StabilityTimeout < 1 {
		return errors.New("config: stability settings must be positive")
	}
	if c.SampleCount < 1 || c.SampleIntervalMS < 1 {
		return errors.New("config: sample settings must be positive")
	}
	if c.RSSDriftPercent < 0 {
		return errors.New("config: RSS drift percent must be >= 0")
	}
	switch c.GCPolicy {
	case "natural", "forced":
	default:
		return fmt.Errorf("config: gc policy %q", c.GCPolicy)
	}
	return nil
}
