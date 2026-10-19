package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// Certificate is an ephemeral self-signed certificate for 127.0.0.1.
// PEM is the public certificate, so a client can trust this server only.
type Certificate struct {
	TLS tls.Certificate
	PEM []byte
}

// NewCertificate builds a certificate that expires in a day.
// It is not a trust anchor outside this process.
func NewCertificate(now time.Time) (Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return Certificate{}, fmt.Errorf("generate serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return Certificate{
		TLS: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		PEM: encoded,
	}, nil
}
