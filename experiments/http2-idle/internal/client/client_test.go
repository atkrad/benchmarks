package client

import (
	"context"
	"crypto/tls"
	"testing"
	"time"
)

func TestNewTransportForcesHTTP2(t *testing.T) {
	transport := newTransport(nil, "localhost")
	if !transport.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 is false")
	}
	if transport.TLSClientConfig == nil || len(transport.TLSClientConfig.NextProtos) != 1 || transport.TLSClientConfig.NextProtos[0] != "h2" {
		t.Fatalf("NextProtos = %v", transport.TLSClientConfig.NextProtos)
	}
	if transport.Protocols == nil || !transport.Protocols.HTTP2() || transport.Protocols.HTTP1() {
		t.Fatal("protocols must be HTTP/2 only")
	}
	if transport.HTTP2 == nil || transport.HTTP2.SendPingTimeout != 0 {
		t.Fatal("SendPingTimeout must be 0 so the connection stays idle")
	}
	if transport.MaxConnsPerHost != 1 || transport.IdleConnTimeout != 0 || transport.DisableKeepAlives {
		t.Fatalf("keep-alive settings = max %d idle %s disable %v", transport.MaxConnsPerHost, transport.IdleConnTimeout, transport.DisableKeepAlives)
	}
	if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("min TLS version")
	}
}

func TestRequireFresh(t *testing.T) {
	if err := requireFresh([]string{"127.0.0.1:1", "127.0.0.1:2"}); err != nil {
		t.Fatal(err)
	}
	if err := requireFresh([]string{"127.0.0.1:1", "127.0.0.1:1"}); err == nil {
		t.Fatal("expected a duplicate local address to fail")
	}
	if err := requireFresh([]string{""}); err == nil {
		t.Fatal("expected an empty local address to fail")
	}
	if err := requireFresh(nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateConfig(t *testing.T) {
	ok := Config{URL: "https://127.0.0.1", ServerName: "localhost", Connections: 0, DialConcurrency: 1}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	ok.Connections = -1
	if err := validate(ok); err == nil {
		t.Fatal("expected a negative count to fail")
	}
	ok.Connections = 1
	ok.DialConcurrency = 0
	if err := validate(ok); err == nil {
		t.Fatal("expected zero concurrency to fail")
	}
}

func TestDialRejectsBadCert(t *testing.T) {
	_, err := Dial(context.Background(), Config{
		URL:             "https://127.0.0.1",
		CertPEM:         []byte("nope"),
		ServerName:      "localhost",
		Connections:     1,
		DialConcurrency: 1,
	})
	if err == nil {
		t.Fatal("expected a bad certificate to fail")
	}
}

func TestAcquireHonorsCancel(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := acquire(ctx, sem); err == nil {
		t.Fatal("expected acquire to time out while the semaphore is full")
	}
}
