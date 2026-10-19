package report

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/perf/benchfmt"
)

func sampleFiles() []File {
	base := map[string]string{
		"goos":       "linux",
		"goarch":     "amd64",
		"cpu":        "test-cpu",
		"gc-policy":  "natural",
		"gomaxprocs": "12",
		"gogc":       "100",
		"run-id":     "golden",
	}
	cases := []struct {
		name    string
		version string
		godebug string
		hash    string
		rss     []float64
		goro    float64
	}{
		{name: "go127", version: "go1.27.2", hash: "aa", rss: []float64{1000, 1010, 1005}, goro: 10},
		{name: "go128", version: "devel go1.28-5c51011e82ed", godebug: "http2serveparking=1", hash: "bb", rss: []float64{900, 910, 905}, goro: 8},
		{name: "go128off", version: "devel go1.28-5c51011e82ed", godebug: "http2serveparking=0", hash: "bb", rss: []float64{1000, 1020, 1010}, goro: 10},
	}
	files := make([]File, 0, len(cases))
	for _, item := range cases {
		config := map[string]string{}
		for key, value := range base {
			config[key] = value
		}
		config["case"] = item.name
		config["go-version"] = item.version
		config["godebug"] = item.godebug
		config["server-sha256"] = item.hash
		file := File{
			Config: config,
			Units:  []UnitMeta{{Unit: "goroutines", Key: "assume", Value: "exact"}},
		}
		for _, connections := range []int{0, 1000} {
			for trial := range 3 {
				file.Results = append(file.Results, Result{
					Name: ConnectionsName(connections),
					Values: []Value{
						{Value: item.rss[trial] + float64(connections), Unit: "rss-B"},
						{Value: item.goro, Unit: "goroutines"},
					},
				})
			}
		}
		files = append(files, file)
	}
	return files
}

func TestBenchfmtRoundTrip(t *testing.T) {
	files := sampleFiles()
	if err := EqualInvariants(files, InvariantKeys); err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		var buf bytes.Buffer
		if err := WriteBenchfmt(&buf, file); err != nil {
			t.Fatal(err)
		}
		reader := benchfmt.NewReader(&buf, file.Config["case"])
		var results []*benchfmt.Result
		var sawExact bool
		for reader.Scan() {
			switch rec := reader.Result().(type) {
			case *benchfmt.UnitMetadata:
				if rec.Unit == "goroutines" && rec.Key == "assume" && rec.Value == "exact" {
					sawExact = true
				}
			case *benchfmt.Result:
				results = append(results, rec.Clone())
			case *benchfmt.SyntaxError:
				t.Fatal(rec)
			default:
				t.Fatalf("unexpected record %T", rec)
			}
		}
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		if !sawExact {
			t.Fatal("missing assume=exact for goroutines")
		}
		if len(results) != len(file.Results) {
			t.Fatalf("read %d results, wrote %d", len(results), len(file.Results))
		}
		for i, got := range results {
			if got.Iters != 1 {
				t.Fatalf("result %d iters = %d", i, got.Iters)
			}
			if got.Name.String() != file.Results[i].Name {
				t.Fatalf("name = %s, want %s", got.Name, file.Results[i].Name)
			}
			if _, ok := got.Value("rss-B"); !ok {
				t.Fatalf("result %d missing rss-B", i)
			}
			if _, ok := got.Value("goroutines"); !ok {
				t.Fatalf("result %d missing goroutines", i)
			}
			if got.GetConfig("gc-policy") != "natural" || got.GetConfig("case") == "" {
				t.Fatalf("config = %q %q", got.GetConfig("gc-policy"), got.GetConfig("case"))
			}
		}
	}
}

func TestEqualInvariantsRejectsDrift(t *testing.T) {
	files := sampleFiles()
	files[1].Config["goos"] = "windows"
	if err := EqualInvariants(files, InvariantKeys); err == nil {
		t.Fatal("expected mismatched goos to fail")
	}
}

func TestBenchfmtGolden(t *testing.T) {
	files := sampleFiles()
	names := []string{"go127.txt", "go128.txt", "go128off.txt"}
	for i, file := range files {
		var buf bytes.Buffer
		if err := WriteBenchfmt(&buf, file); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join("testdata", "benchfmt", names[i])
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, buf.Bytes()) {
			t.Fatalf("%s drifted from the writer; set UPDATE_GOLDEN=1 to refresh\n%s", path, buf.String())
		}
	}
}
