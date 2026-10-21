package runner

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
	"github.com/atkrad/benchmarks/internal/benchkit/system"
)

func TestAggregateSamples(t *testing.T) {
	tests := []struct {
		name    string
		samples []observation
		want    trialMetrics
		wantErr bool
	}{
		{
			name: "three samples median not last",
			samples: []observation{
				{
					rss: 1000,
					stats: protocol.Stats{
						Goroutines: 5, HeapAlloc: 400, StackInuse: 80,
						HeapInuse: 40, HeapIdle: 10, HeapReleased: 1,
					},
				},
				{
					rss: 2000,
					stats: protocol.Stats{
						Goroutines: 7, HeapAlloc: 100, StackInuse: 100,
						HeapInuse: 20, HeapIdle: 30, HeapReleased: 3,
					},
				},
				{
					rss: 1500,
					stats: protocol.Stats{
						Goroutines: 6, HeapAlloc: 300, StackInuse: 60,
						HeapInuse: 30, HeapIdle: 20, HeapReleased: 2,
					},
				},
			},
			want: trialMetrics{
				RSS: 1500, Goroutines: 6, HeapAlloc: 300, StackInuse: 80,
				HeapInuse: 30, HeapIdle: 20, HeapReleased: 2,
			},
		},
		{
			name: "even count averages the middle pair",
			samples: []observation{
				{rss: 100, stats: protocol.Stats{Goroutines: 1, HeapAlloc: 10, StackInuse: 2, HeapInuse: 4, HeapIdle: 6, HeapReleased: 8}},
				{rss: 300, stats: protocol.Stats{Goroutines: 3, HeapAlloc: 30, StackInuse: 6, HeapInuse: 8, HeapIdle: 10, HeapReleased: 12}},
			},
			want: trialMetrics{
				RSS: 200, Goroutines: 2, HeapAlloc: 20, StackInuse: 4,
				HeapInuse: 6, HeapIdle: 8, HeapReleased: 10,
			},
		},
		{
			name: "single sample",
			samples: []observation{
				{rss: 42, stats: protocol.Stats{Goroutines: 9, HeapAlloc: 11, StackInuse: 13, HeapInuse: 15, HeapIdle: 17, HeapReleased: 19}},
			},
			want: trialMetrics{
				RSS: 42, Goroutines: 9, HeapAlloc: 11, StackInuse: 13,
				HeapInuse: 15, HeapIdle: 17, HeapReleased: 19,
			},
		},
		{
			name:    "empty samples",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := aggregateSamples(tt.samples)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAddResultUsesAggregatedMetrics(t *testing.T) {
	eng := &engine{
		cfg:    Config{GCPolicy: "natural", GOMAXPROCS: 2, GOGC: 100},
		runID:  "test-run",
		hashes: map[string]string{"server": "deadbeef"},
		byCase: map[string]*caseFile{},
		info:   systemInfoForTest(),
	}
	metrics := trialMetrics{
		RSS: 1500, Goroutines: 6.5, HeapAlloc: 300, StackInuse: 80,
		HeapInuse: 30, HeapIdle: 20, HeapReleased: 2,
	}
	eng.addResult(runCase{Name: "go127", Binary: "server", GODEBUG: ""}, 1000, "go1.test", metrics)

	file := eng.byCase["go127"]
	require.NotNil(t, file)
	require.Len(t, file.results, 1)
	require.Equal(t, "HTTP2Idle/connections=1000", file.results[0].Name)

	got := map[string]float64{}
	for _, value := range file.results[0].Values {
		got[value.Unit] = value.Value
	}
	assert.Equal(t, 1500.0, got["rss-B"])
	assert.Equal(t, 80.0, got["stack-inuse-B"])
	assert.Equal(t, 300.0, got["heap-alloc-B"])
	assert.Equal(t, 30.0, got["heap-inuse-B"])
	assert.Equal(t, 20.0, got["heap-idle-B"])
	assert.Equal(t, 2.0, got["heap-released-B"])
	assert.Equal(t, 6.5, got["goroutines"])
}

func TestTrialRecordKeepsFractionalMedian(t *testing.T) {
	metrics, err := aggregateSamples([]observation{
		{rss: 100, stats: protocol.Stats{Goroutines: 1}},
		{rss: 101, stats: protocol.Stats{Goroutines: 2}},
	})
	require.NoError(t, err)

	rec := trialRecord{RSSBytes: metrics.RSS, Goroutines: metrics.Goroutines}
	raw, err := json.Marshal(rec)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.InDelta(t, 100.5, got["rss_bytes"], 0)
	assert.InDelta(t, 1.5, got["goroutines"], 0)
}

func systemInfoForTest() system.Info {
	return system.Info{OS: "linux", Arch: "amd64", CPUModel: "test-cpu"}
}
