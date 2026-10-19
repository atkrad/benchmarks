// Package server is the measured HTTP/2 process.
package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Options selects optional control routes. The workload listener always
// accepts both HTTP/2 and HTTP/1.1 so a non-HTTP/2 request is visible.
type Options struct {
	AllowForcedGC bool
	EnablePprof   bool
	IdleTimeout   time.Duration
	Now           func() time.Time
}

// Server serves workload traffic and a separate loopback control endpoint.
type Server struct {
	https   *http.Server
	control *http.Server
	counts  *counters
	cert    Certificate
	httpsLn net.Listener
	ctrlLn  net.Listener
}

// New listens on 127.0.0.1 for HTTPS and for the plaintext control endpoint.
func New(opts Options) (*Server, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cert, err := NewCertificate(now())
	if err != nil {
		return nil, err
	}
	counts := newCounters()
	workload, httpsLn, err := newWorkload(cert, counts, opts.IdleTimeout)
	if err != nil {
		return nil, err
	}
	control, ctrlLn, err := newControl(counts, opts, now)
	if err != nil {
		_ = httpsLn.Close()
		return nil, err
	}
	return &Server{
		https:   workload,
		control: control,
		counts:  counts,
		cert:    cert,
		httpsLn: httpsLn,
		ctrlLn:  ctrlLn,
	}, nil
}

// HTTPSAddr is the workload listener address.
func (s *Server) HTTPSAddr() string { return s.httpsLn.Addr().String() }

// ControlAddr is the plaintext loopback listener address.
func (s *Server) ControlAddr() string { return s.ctrlLn.Addr().String() }

// CertPEM is the public certificate clients should trust.
func (s *Server) CertPEM() []byte { return s.cert.PEM }

// Serve accepts until ctx is cancelled or a listener fails.
// Both servers are shut down before Serve returns.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() { errCh <- serveIgnoreClosed(s.https, s.httpsLn) }()
	go func() { errCh <- serveIgnoreClosed(s.control, s.ctrlLn) }()

	var first error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		first = err
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.https.Shutdown(shut)
	_ = s.control.Shutdown(shut)

	pending := 2
	if first != nil {
		pending = 1
	}
	var errs []error
	if first != nil {
		errs = append(errs, first)
	}
	for range pending {
		if err := <-errCh; err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func serveIgnoreClosed(srv *http.Server, ln net.Listener) error {
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func newWorkload(cert Certificate, counts *counters, idle time.Duration) (*http.Server, net.Listener, error) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen https: %w", err)
	}
	if err := requireLoopback(tcp.Addr().String()); err != nil {
		_ = tcp.Close()
		return nil, nil, err
	}
	tracked := &trackedListener{Listener: tcp, counts: counts}
	tlsLn := tls.NewListener(tracked, &tls.Config{
		Certificates: []tls.Certificate{cert.TLS},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)

	srv := &http.Server{
		Handler:           workloadHandler(counts),
		Protocols:         protocols,
		IdleTimeout:       idle,
		ReadHeaderTimeout: 5 * time.Second,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 0,
		},
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			tlsConn, ok := c.(*tls.Conn)
			if !ok {
				return ctx
			}
			tracked, ok := tlsConn.NetConn().(*trackedConn)
			if !ok {
				return ctx
			}
			return context.WithValue(ctx, connIDKey{}, tracked.id)
		},
	}
	return srv, tlsLn, nil
}

func newControl(counts *counters, opts Options, now func() time.Time) (*http.Server, net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen control: %w", err)
	}
	if err := requireLoopback(ln.Addr().String()); err != nil {
		_ = ln.Close()
		return nil, nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := rejectRemote(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		if err := rejectRemote(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		// ReadMemStats is the measurement. It does not force a GC.
		writeJSON(w, counts.snapshot(now()))
	})
	if opts.AllowForcedGC {
		mux.HandleFunc("POST /gc", func(w http.ResponseWriter, r *http.Request) {
			if err := rejectRemote(r); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			runtime.GC()
			debug.FreeOSMemory()
			w.WriteHeader(http.StatusNoContent)
		})
	}
	if opts.EnablePprof {
		mux.HandleFunc("GET /debug/pprof/goroutine", func(w http.ResponseWriter, r *http.Request) {
			if err := rejectRemote(r); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			pprof.Handler("goroutine").ServeHTTP(w, r)
		})
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		Protocols:         http1Only(),
	}
	return srv, ln, nil
}

func http1Only() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	return p
}

func workloadHandler(counts *counters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &countWriter{ResponseWriter: w, writes: &counts.writes}
		if r.ProtoMajor != 2 {
			counts.nonH2.Add(1)
			http.Error(cw, "HTTP/2 required", http.StatusHTTPVersionNotSupported)
			return
		}
		id, _ := r.Context().Value(connIDKey{}).(int64)
		counts.h2.Add(1)
		counts.addRequest(id)
		cw.Header().Set("Content-Type", "text/plain")
		cw.WriteHeader(http.StatusOK)
		_, _ = cw.Write([]byte("ok\n"))
	})
}

type countWriter struct {
	http.ResponseWriter
	writes *atomic.Int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err != nil {
		w.writes.Add(1)
	}
	return n, err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		return
	}
}

type connIDKey struct{}

// requireLoopback fails when addr is not a loopback host.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("parse %s: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listener %s is not loopback", addr)
	}
	return nil
}

func rejectRemote(r *http.Request) error {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return fmt.Errorf("remote address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("control request from %s is not loopback", r.RemoteAddr)
	}
	return nil
}

// trackedListener counts accepted TCP connections. Close of each tracked
// conn is counted once, including when HTTP/2 closes it.
type trackedListener struct {
	net.Listener
	counts *counters
	next   int64
	mu     sync.Mutex
}

func (l *trackedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.next++
	id := l.next
	l.mu.Unlock()
	l.counts.accepted.Add(1)
	l.counts.open.Add(1)
	l.counts.addConn(id)
	return &trackedConn{Conn: conn, id: id, counts: l.counts}, nil
}

type trackedConn struct {
	net.Conn
	id     int64
	counts *counters
	once   sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.counts.open.Add(-1)
		c.counts.closed.Add(1)
	})
	return c.Conn.Close()
}
