package certutil

import (
	"crypto/x509"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewLoopbackTrustsLoopbackNames(t *testing.T) {
	now := time.Now()
	cert, err := NewLoopback(now)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(cert.PEM))

	leaf, err := x509.ParseCertificate(cert.TLS.Certificate[0])
	require.NoError(t, err)
	require.True(t, leaf.NotAfter.Before(now.Add(25*time.Hour)))
	require.Contains(t, leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost"})
	require.NoError(t, err)
	require.Equal(t, []string{"localhost"}, leaf.DNSNames)
	require.True(t, leaf.IPAddresses[0].Equal(net.ParseIP("127.0.0.1")))
	require.True(t, leaf.IPAddresses[1].Equal(net.IPv6loopback))
	require.NotNil(t, cert.TLS.PrivateKey)
}
