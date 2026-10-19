// Package client holds N idle HTTP/2 connections, one transport each.
package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// Config is the dial plan for one trial.
type Config struct {
	URL             string
	CertPEM         []byte
	ServerName      string
	Connections     int
	DialConcurrency int
	MaxRetries      int
	RequestTimeout  time.Duration
}

// Session is the set of held transports. Close drops them.
type Session struct {
	transports []*http.Transport
	locals     []string
	Retries    int
}

// Dial opens one connection per slot. The first response on each transport
// must be a fresh HTTP/2 connection, and the local addresses must all differ.
func Dial(ctx context.Context, cfg Config) (*Session, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.CertPEM) {
		return nil, errors.New("dial: certificate PEM is empty or invalid")
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	session := &Session{
		transports: make([]*http.Transport, cfg.Connections),
		locals:     make([]string, cfg.Connections),
	}
	sem := make(chan struct{}, cfg.DialConcurrency)
	var wg sync.WaitGroup
	errCh := make(chan error, cfg.Connections)
	var mu sync.Mutex
	for i := range cfg.Connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			transport := newTransport(pool, cfg.ServerName)
			local, used, err := dialOne(ctx, sem, transport, cfg.URL, timeout, cfg.MaxRetries)
			mu.Lock()
			session.Retries += used
			if err == nil {
				session.transports[i] = transport
				session.locals[i] = local
			}
			mu.Unlock()
			if err != nil {
				transport.CloseIdleConnections()
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		session.Close()
		return nil, fmt.Errorf("dial: %w", errors.Join(errs...))
	}
	if err := requireFresh(session.locals); err != nil {
		session.Close()
		return nil, err
	}
	return session, nil
}

// Recheck sends one more request on each held transport.
// Every response must reuse the same local address.
func (s *Session) Recheck(ctx context.Context, url string, timeout time.Duration) (Result, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	result := Result{Locals: make([]string, len(s.transports))}
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, transport := range s.transports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local, reused, err := do(ctx, transport, url, timeout)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.Errors++
				result.Detail = err.Error()
				return
			}
			result.Locals[i] = local
			switch {
			case reused && local == s.locals[i]:
				result.Reused++
			default:
				result.Replaced++
			}
		}()
	}
	wg.Wait()
	result.OK = result.Errors == 0 && result.Replaced == 0 && result.Reused == len(s.transports)
	if !result.OK {
		return result, fmt.Errorf("recheck: reused %d, replaced %d, errors %d", result.Reused, result.Replaced, result.Errors)
	}
	return result, nil
}

// Result is the outcome of Recheck.
type Result struct {
	OK       bool
	Reused   int
	Replaced int
	Errors   int
	Locals   []string
	Detail   string
}

// Locals returns the local address of each held connection, in slot order.
func (s *Session) Locals() []string {
	out := make([]string, len(s.locals))
	copy(out, s.locals)
	return out
}

// Close drops every idle connection.
func (s *Session) Close() {
	for _, transport := range s.transports {
		if transport != nil {
			transport.CloseIdleConnections()
		}
	}
}

func validate(cfg Config) error {
	if cfg.URL == "" {
		return errors.New("dial: url is empty")
	}
	if cfg.Connections < 0 {
		return fmt.Errorf("dial: connections %d", cfg.Connections)
	}
	if cfg.DialConcurrency < 1 {
		return fmt.Errorf("dial: concurrency %d", cfg.DialConcurrency)
	}
	if cfg.MaxRetries < 0 {
		return fmt.Errorf("dial: retries %d", cfg.MaxRetries)
	}
	if cfg.ServerName == "" {
		return errors.New("dial: server name is empty")
	}
	return nil
}

func newTransport(pool *x509.CertPool, serverName string) *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	return &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			RootCAs:    pool,
			ServerName: serverName,
			NextProtos: []string{"h2"},
			MinVersion: tls.VersionTLS12,
		},
		// A custom TLS config disables automatic HTTP/2 unless this is set.
		ForceAttemptHTTP2: true,
		Protocols:         protocols,
		MaxConnsPerHost:   1,
		IdleConnTimeout:   0,
		// Zero means no HTTP/2 health check, so the connection stays idle.
		HTTP2:             &http.HTTP2Config{SendPingTimeout: 0},
		DisableKeepAlives: false,
	}
}

func dialOne(ctx context.Context, sem chan struct{}, transport *http.Transport, url string, timeout time.Duration, maxRetries int) (string, int, error) {
	var last error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := acquire(ctx, sem); err != nil {
			return "", attempt, err
		}
		local, reused, err := do(ctx, transport, url, timeout)
		release(sem)
		if err != nil {
			last = err
			transport.CloseIdleConnections()
			continue
		}
		if reused {
			return "", attempt, errors.New("first request reused a connection")
		}
		if local == "" {
			return "", attempt, errors.New("first request has no local address")
		}
		return local, attempt, nil
	}
	if last == nil {
		last = errors.New("dial failed")
	}
	return "", maxRetries, last
}

func do(ctx context.Context, transport *http.Transport, url string, timeout time.Duration) (local string, reused bool, err error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Conn != nil && info.Conn.LocalAddr() != nil {
				local = info.Conn.LocalAddr().String()
			}
			reused = info.Reused
		},
	}))
	resp, err := transport.RoundTrip(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("status %s", resp.Status)
	}
	if resp.ProtoMajor != 2 {
		return "", false, fmt.Errorf("protocol HTTP/%d", resp.ProtoMajor)
	}
	if resp.TLS == nil || resp.TLS.NegotiatedProtocol != "h2" {
		got := ""
		if resp.TLS != nil {
			got = resp.TLS.NegotiatedProtocol
		}
		return "", false, fmt.Errorf("ALPN %q, want h2", got)
	}
	return local, reused, nil
}

func requireFresh(locals []string) error {
	seen := make(map[string]struct{}, len(locals))
	for i, local := range locals {
		if local == "" {
			return fmt.Errorf("connection %d has no local address", i)
		}
		if _, ok := seen[local]; ok {
			return fmt.Errorf("local address %s is used by more than one transport", local)
		}
		seen[local] = struct{}{}
	}
	return nil
}

func acquire(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func release(sem chan struct{}) {
	<-sem
}
