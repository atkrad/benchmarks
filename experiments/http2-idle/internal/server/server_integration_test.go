//go:build integration

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/client"
	"github.com/atkrad/benchmarks/experiments/http2-idle/internal/protocol"
)

func TestServerAndClientHoldIdleHTTP2(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	srv, err := New(Options{Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	if err := requireLoopback(srv.ControlAddr()); err != nil {
		t.Fatal(err)
	}
	health, err := http.Get("http://" + srv.ControlAddr() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, health.Body)
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK || health.ProtoMajor != 1 {
		t.Fatalf("health = %s HTTP/%d", health.Status, health.ProtoMajor)
	}

	session, err := client.Dial(ctx, client.Config{
		URL:             "https://" + srv.HTTPSAddr() + "/work",
		CertPEM:         srv.CertPEM(),
		ServerName:      "localhost",
		Connections:     2,
		DialConcurrency: 2,
		MaxRetries:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	stats := mustStats(t, srv.ControlAddr())
	if stats.Open != 2 || stats.Accepted != 2 || stats.H2Requests != 2 || stats.NonH2Requests != 0 || stats.WriteErrors != 0 {
		t.Fatalf("after dial: %+v", stats)
	}

	if _, err := session.Recheck(ctx, "https://"+srv.HTTPSAddr()+"/work", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	stats = mustStats(t, srv.ControlAddr())
	if stats.Accepted != 2 || stats.Open != 2 || stats.H2Requests != 4 {
		t.Fatalf("after recheck: %+v", stats)
	}
	if len(stats.ConnRequests) != 2 || stats.ConnRequests[0] != 2 || stats.ConnRequests[1] != 2 {
		t.Fatalf("per-conn = %v", stats.ConnRequests)
	}

	// A non-HTTP/2 request on the workload listener must be counted and not
	// treated as another held HTTP/2 connection.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(srv.CertPEM()) {
		t.Fatal("pem")
	}
	h1Protocols := new(http.Protocols)
	h1Protocols.SetHTTP1(true)
	h1 := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: pool, ServerName: "localhost", NextProtos: []string{"http/1.1"}},
		ForceAttemptHTTP2: false,
		Protocols:         h1Protocols,
	}}
	resp, err := h1.Get("https://" + srv.HTTPSAddr() + "/work")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	stats = mustStats(t, srv.ControlAddr())
	if stats.NonH2Requests != 1 || stats.Accepted != 3 {
		t.Fatalf("after http/1: %+v", stats)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

func mustStats(t *testing.T, addr string) protocol.Stats {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats protocol.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	return stats
}
