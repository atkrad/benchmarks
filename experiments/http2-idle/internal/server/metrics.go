package server

import (
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
)

// counters are the workload totals. The control listener does not touch them.
type counters struct {
	accepted atomic.Int64
	open     atomic.Int64
	closed   atomic.Int64
	h2       atomic.Int64
	nonH2    atomic.Int64
	writes   atomic.Int64

	mu       sync.Mutex
	requests map[int64]*atomic.Int64
}

func newCounters() *counters {
	return &counters{requests: make(map[int64]*atomic.Int64)}
}

func (c *counters) addConn(id int64) {
	c.mu.Lock()
	c.requests[id] = &atomic.Int64{}
	c.mu.Unlock()
}

func (c *counters) addRequest(id int64) {
	c.mu.Lock()
	slot := c.requests[id]
	c.mu.Unlock()
	if slot != nil {
		slot.Add(1)
	}
}

func (c *counters) snapshot(now time.Time) protocol.Stats {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	c.mu.Lock()
	perConn := make([]int64, 0, len(c.requests))
	for _, slot := range c.requests {
		perConn = append(perConn, slot.Load())
	}
	c.mu.Unlock()

	return protocol.Stats{
		WallTime:      now.UTC().Format(time.RFC3339Nano),
		Accepted:      c.accepted.Load(),
		Open:          c.open.Load(),
		Closed:        c.closed.Load(),
		H2Requests:    c.h2.Load(),
		NonH2Requests: c.nonH2.Load(),
		WriteErrors:   c.writes.Load(),
		ConnRequests:  perConn,
		Goroutines:    runtime.NumGoroutine(),
		HeapAlloc:     mem.HeapAlloc,
		StackInuse:    mem.StackInuse,
		HeapInuse:     mem.HeapInuse,
		HeapIdle:      mem.HeapIdle,
		HeapReleased:  mem.HeapReleased,
		NumGC:         mem.NumGC,
		PauseTotalNs:  mem.PauseTotalNs,
		GoVersion:     runtime.Version(),
	}
}
