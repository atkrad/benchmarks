package server

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCertificateTrustsLocalhost(t *testing.T) {
	cert, err := NewCertificate(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert.PEM) {
		t.Fatal("certificate PEM was rejected")
	}
	leaf, err := x509.ParseCertificate(cert.TLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost"}); err != nil {
		t.Fatal(err)
	}
}

func TestRequireLoopback(t *testing.T) {
	if err := requireLoopback("127.0.0.1:80"); err != nil {
		t.Fatal(err)
	}
	if err := requireLoopback("10.0.0.1:80"); err == nil {
		t.Fatal("expected a non-loopback address to fail")
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/stats", nil)
	req.RemoteAddr = "10.1.1.1:9"
	if err := rejectRemote(req); err == nil {
		t.Fatal("expected a non-loopback remote to fail")
	}
	req.RemoteAddr = "127.0.0.1:9"
	if err := rejectRemote(req); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerCountsHTTP2AndRejectsHTTP1(t *testing.T) {
	counts := newCounters()
	counts.addConn(7)
	handler := workloadHandler(counts)

	h2 := httptest.NewRequest(http.MethodGet, "https://127.0.0.1/work", nil)
	h2.Proto = "HTTP/2.0"
	h2.ProtoMajor = 2
	h2 = h2.WithContext(context.WithValue(h2.Context(), connIDKey{}, int64(7)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, h2)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("h2 response = %d %q", rec.Code, rec.Body.String())
	}

	h1 := httptest.NewRequest(http.MethodGet, "https://127.0.0.1/work", nil)
	h1.ProtoMajor = 1
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, h1)
	if rec.Code != http.StatusHTTPVersionNotSupported {
		t.Fatalf("h1 status = %d", rec.Code)
	}
	if counts.h2.Load() != 1 || counts.nonH2.Load() != 1 {
		t.Fatalf("h2=%d non-h2=%d", counts.h2.Load(), counts.nonH2.Load())
	}
	snap := counts.snapshot(time.Unix(0, 0).UTC())
	if len(snap.ConnRequests) != 1 || snap.ConnRequests[0] != 1 {
		t.Fatalf("per-conn requests = %v", snap.ConnRequests)
	}
	if snap.GoVersion == "" || snap.HeapAlloc == 0 && snap.StackInuse == 0 {
		// HeapAlloc can be non-zero in any real process. An empty version cannot.
		if snap.GoVersion == "" {
			t.Fatal("missing go version")
		}
	}
}

func TestTrackedConnCloseOnce(t *testing.T) {
	left, right := net.Pipe()
	t.Cleanup(func() { _ = right.Close() })
	counts := newCounters()
	conn := &trackedConn{Conn: left, id: 1, counts: counts}
	counts.open.Add(1)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if counts.open.Load() != 0 || counts.closed.Load() != 1 {
		t.Fatalf("open=%d closed=%d", counts.open.Load(), counts.closed.Load())
	}
}

func TestSnapshotReadsRuntimeWithoutInventingCounts(t *testing.T) {
	counts := newCounters()
	snap := counts.snapshot(time.Now())
	if snap.GoVersion == "" {
		t.Fatal("snapshot missing go version")
	}
	if snap.Accepted != 0 || snap.H2Requests != 0 || snap.NonH2Requests != 0 {
		t.Fatalf("fresh counters = %+v", snap)
	}
}
