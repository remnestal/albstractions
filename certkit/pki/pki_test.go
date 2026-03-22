package pki_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/remnestal/albstractions/certkit/pki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block, "expected a PEM block in cert")
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

func TestGenerateCA(t *testing.T) {
	t.Parallel()

	t.Run("generates a valid self-signed CA", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour)
		require.NoError(t, err)
		assert.NotEmpty(t, bundle.CertPEM)
		assert.NotEmpty(t, bundle.KeyPEM)
	})

	t.Run("CA cert has IsCA and BasicConstraintsValid set", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.True(t, cert.IsCA)
		assert.True(t, cert.BasicConstraintsValid)
	})

	t.Run("CA cert has KeyUsageCertSign and KeyUsageCRLSign", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.NotZero(t, cert.KeyUsage&x509.KeyUsageCertSign, "expected KeyUsageCertSign")
		assert.NotZero(t, cert.KeyUsage&x509.KeyUsageCRLSign, "expected KeyUsageCRLSign")
	})

	t.Run("CA cert is self-signed", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.Equal(t, cert.Subject.String(), cert.Issuer.String())
	})

	t.Run("CA cert enforces single-level hierarchy by default", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.Equal(t, 0, cert.MaxPathLen)
		assert.True(t, cert.MaxPathLenZero)
	})

	t.Run("WithMaxPathLen overrides path length constraint", func(t *testing.T) {
		t.Parallel()

		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithMaxPathLen(2))
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.Equal(t, 2, cert.MaxPathLen)
	})

	t.Run("WithSerial sets serial on CA certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(42)
		bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, bundle.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})

	t.Run("WithSerial rejects zero serial", func(t *testing.T) {
		t.Parallel()

		_, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithSerial(big.NewInt(0)))
		assert.Error(t, err)
	})

	t.Run("WithSerial rejects negative serial", func(t *testing.T) {
		t.Parallel()

		_, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithSerial(big.NewInt(-1)))
		assert.Error(t, err)
	})

	t.Run("WithSerial rejects serial exceeding 20 octets", func(t *testing.T) {
		t.Parallel()

		// 2^161 is 21 octets.
		tooBig := new(big.Int).Lsh(big.NewInt(1), 161)
		_, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithSerial(tooBig))
		assert.Error(t, err)
	})

	t.Run("supports all key algorithms", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			algorithm pki.KeyAlgorithm
		}{
			{"ECDSA P-256", pki.ECDSAP256()},
			{"ECDSA P-384", pki.ECDSAP384()},
			{"RSA 2048", pki.RSA2048()},
			{"RSA 4096", pki.RSA4096()},
			{"Ed25519", pki.Ed25519()},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				bundle, err := pki.GenerateCA(tt.algorithm, "Test CA", "Test Org", time.Hour)
				require.NoError(t, err)
				assert.NotEmpty(t, bundle.CertPEM)
				assert.NotEmpty(t, bundle.KeyPEM)
			})
		}
	})
}

func TestGeneratePeerCert(t *testing.T) {
	t.Parallel()

	ca, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 24*time.Hour)
	require.NoError(t, err)

	t.Run("generates a leaf cert signed by CA", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		require.NoError(t, err)
		assert.NotEmpty(t, leaf.CertPEM)
		assert.NotEmpty(t, leaf.KeyPEM)
	})

	t.Run("generated cert verifies against CA", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		require.NoError(t, err)

		caPool := x509.NewCertPool()
		ok := caPool.AppendCertsFromPEM(ca.CertPEM)
		require.True(t, ok)

		_, err = tls.X509KeyPair(leaf.CertPEM, leaf.KeyPEM)
		require.NoError(t, err)

		leafCert := parseCert(t, leaf.CertPEM)
		_, err = leafCert.Verify(x509.VerifyOptions{
			Roots:     caPool,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		require.NoError(t, err)
	})

	t.Run("error on invalid CA bundle", func(t *testing.T) {
		t.Parallel()

		bad := pki.Bundle{CertPEM: []byte("not-pem"), KeyPEM: []byte("not-pem")}
		_, err := pki.GeneratePeerCert(pki.ECDSAP256(), bad, "leaf1", []string{"localhost"}, nil, time.Hour)
		assert.Error(t, err)
	})

	t.Run("error when CA bundle is not a CA certificate", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, nil, time.Hour)
		require.NoError(t, err)

		_, err = pki.GeneratePeerCert(pki.ECDSAP256(), leaf, "leaf2", []string{"localhost"}, nil, time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a CA certificate")
	})

	t.Run("error when leaf validity exceeds CA expiry", func(t *testing.T) {
		t.Parallel()

		shortCA, err := pki.GenerateCA(pki.ECDSAP256(), "Short CA", "Test Org", time.Hour)
		require.NoError(t, err)

		_, err = pki.GeneratePeerCert(pki.ECDSAP256(), shortCA, "leaf1", []string{"localhost"}, nil, 2*time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "extends beyond CA expiry")
	})

	t.Run("leaf cert has KeyUsageDigitalSignature", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.NotZero(t, cert.KeyUsage&x509.KeyUsageDigitalSignature, "expected KeyUsageDigitalSignature")
	})

	t.Run("leaf cert has ExtKeyUsageServerAuth and ExtKeyUsageClientAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	})

	t.Run("leaf cert is not a CA", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.False(t, cert.IsCA)
	})

	t.Run("leaf cert with different algorithm than CA", func(t *testing.T) {
		t.Parallel()

		rsaCA, err := pki.GenerateCA(pki.RSA2048(), "RSA CA", "Test Org", 24*time.Hour)
		require.NoError(t, err)

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), rsaCA, "ecdsa-leaf", []string{"localhost"}, nil, time.Hour)
		require.NoError(t, err)

		caPool := x509.NewCertPool()
		require.True(t, caPool.AppendCertsFromPEM(rsaCA.CertPEM))

		leafCert := parseCert(t, leaf.CertPEM)
		_, err = leafCert.Verify(x509.VerifyOptions{
			Roots:     caPool,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		require.NoError(t, err)
	})

	t.Run("leaf cert SANs contain supplied DNS names and IPs", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"leaf1.local", "localhost"}, []net.IP{net.ParseIP("10.0.0.1")}, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.DNSNames, "leaf1.local")
		assert.Contains(t, cert.DNSNames, "localhost")
		assert.True(t, func() bool {
			for _, ip := range cert.IPAddresses {
				if ip.Equal(net.ParseIP("10.0.0.1")) {
					return true
				}
			}
			return false
		}(), "expected IP SAN 10.0.0.1")
	})

	t.Run("returns error when no SANs are provided", func(t *testing.T) {
		t.Parallel()

		_, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", nil, nil, time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SAN")
	})

	t.Run("WithSerial sets serial on peer certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(99)
		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", []string{"localhost"}, nil, time.Hour, pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})
}

func TestGenerateServerCert(t *testing.T) {
	t.Parallel()

	ca, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 24*time.Hour)
	require.NoError(t, err)

	t.Run("has only ExtKeyUsageServerAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", []string{"localhost"}, nil, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.NotContains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	})

	t.Run("cert with SANs passes hostname verification", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", []string{"localhost"}, nil, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.NoError(t, cert.VerifyHostname("localhost"))
	})

	t.Run("returns error when no SANs are provided", func(t *testing.T) {
		t.Parallel()

		_, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", nil, nil, time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SAN")
	})

	t.Run("WithSerial sets serial on server certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(7)
		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", []string{"localhost"}, nil, time.Hour, pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})
}

func TestGenerateClientCert(t *testing.T) {
	t.Parallel()

	ca, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 24*time.Hour)
	require.NoError(t, err)

	t.Run("has only ExtKeyUsageClientAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", nil, nil, time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
		assert.NotContains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	})

	t.Run("WithSerial sets serial on client certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(1000)
		leaf, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", nil, nil, time.Hour, pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})
}
