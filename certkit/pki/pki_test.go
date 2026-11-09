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

	t.Run("rejects non-positive validity", func(t *testing.T) {
		t.Parallel()

		for _, validity := range []time.Duration{0, -time.Hour} {
			t.Run(validity.String(), func(t *testing.T) {
				t.Parallel()

				_, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", validity)
				assert.ErrorContains(t, err, "validity must be positive")
			})
		}
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

	t.Run("WithSerial limits the serial to 20 octets when DER-encoded", func(t *testing.T) {
		t.Parallel()

		// DER prefixes a zero octet to a value whose top bit is set, so the
		// largest 20-octet serial is 2^159-1.
		pow2 := func(n uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), n) }
		tests := []struct {
			name    string
			serial  *big.Int
			wantErr bool
		}{
			{name: "accepts 2^159-1, 20 octets", serial: new(big.Int).Sub(pow2(159), big.NewInt(1))},
			{name: "rejects 2^159, 21 octets with the sign octet", serial: pow2(159), wantErr: true},
			{name: "rejects 2^161, 21 octets", serial: pow2(161), wantErr: true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", time.Hour, pki.WithSerial(tt.serial))
				if tt.wantErr {
					assert.ErrorContains(t, err, "20 octets")
					return
				}
				require.NoError(t, err)
				assert.Equal(t, 0, tt.serial.Cmp(parseCert(t, bundle.CertPEM).SerialNumber))
			})
		}
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
			{"ML-DSA-44", pki.MLDSA44()},
			{"ML-DSA-65", pki.MLDSA65()},
			{"ML-DSA-87", pki.MLDSA87()},
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

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithIPAddresses(net.ParseIP("127.0.0.1")))
		require.NoError(t, err)
		assert.NotEmpty(t, leaf.CertPEM)
		assert.NotEmpty(t, leaf.KeyPEM)
	})

	t.Run("generated cert verifies against CA", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithIPAddresses(net.ParseIP("127.0.0.1")))
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
		_, err := pki.GeneratePeerCert(pki.ECDSAP256(), bad, "leaf1", time.Hour, pki.WithDNSNames("localhost"))
		assert.Error(t, err)
	})

	t.Run("error when CA bundle is not a CA certificate", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"))
		require.NoError(t, err)

		_, err = pki.GeneratePeerCert(pki.ECDSAP256(), leaf, "leaf2", time.Hour, pki.WithDNSNames("localhost"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not a CA certificate")
	})

	t.Run("rejects non-positive validity", func(t *testing.T) {
		t.Parallel()

		for _, validity := range []time.Duration{0, -time.Hour} {
			t.Run(validity.String(), func(t *testing.T) {
				t.Parallel()

				_, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "peer", validity, pki.WithDNSNames("localhost"))
				assert.ErrorContains(t, err, "validity must be positive")
			})
		}
	})

	t.Run("error when leaf validity exceeds CA expiry", func(t *testing.T) {
		t.Parallel()

		shortCA, err := pki.GenerateCA(pki.ECDSAP256(), "Short CA", "Test Org", time.Hour)
		require.NoError(t, err)

		_, err = pki.GeneratePeerCert(pki.ECDSAP256(), shortCA, "leaf1", 2*time.Hour, pki.WithDNSNames("localhost"))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "extends beyond CA expiry")
	})

	t.Run("leaf cert has KeyUsageDigitalSignature", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithIPAddresses(net.ParseIP("127.0.0.1")))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.NotZero(t, cert.KeyUsage&x509.KeyUsageDigitalSignature, "expected KeyUsageDigitalSignature")
	})

	t.Run("leaf cert has ExtKeyUsageServerAuth and ExtKeyUsageClientAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithIPAddresses(net.ParseIP("127.0.0.1")))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	})

	t.Run("leaf cert is not a CA", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithIPAddresses(net.ParseIP("127.0.0.1")))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.False(t, cert.IsCA)
	})

	t.Run("leaf cert with different algorithm than CA", func(t *testing.T) {
		t.Parallel()

		rsaCA, err := pki.GenerateCA(pki.RSA2048(), "RSA CA", "Test Org", 24*time.Hour)
		require.NoError(t, err)

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), rsaCA, "ecdsa-leaf", time.Hour, pki.WithDNSNames("localhost"))
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

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("leaf1.local", "localhost"), pki.WithIPAddresses(net.ParseIP("10.0.0.1")))
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

		_, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SAN")
	})

	t.Run("WithSerial sets serial on peer certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(99)
		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})

	t.Run("leaf cert has no URI SANs by default", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Empty(t, cert.URIs)
	})

	t.Run("WithURIs sets a SPIFFE ID that survives the DER round trip", func(t *testing.T) {
		t.Parallel()

		const spiffeID = "spiffe://example.org/service/api"
		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithURIs(spiffeID))
		require.NoError(t, err)

		// Consumers such as mesh read the identity from URIs[0], preferring it
		// over any DNS name or the CommonName.
		cert := parseCert(t, leaf.CertPEM)
		require.Len(t, cert.URIs, 1)
		assert.Equal(t, spiffeID, cert.URIs[0].String())
	})

	t.Run("WithURIs preserves the order of multiple URIs", func(t *testing.T) {
		t.Parallel()

		want := []string{"spiffe://example.org/service/api", "spiffe://example.org/service/backup", "https://example.org/service"}
		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithURIs(want...))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		got := make([]string, 0, len(cert.URIs))
		for _, u := range cert.URIs {
			got = append(got, u.String())
		}
		assert.Equal(t, want, got)
	})

	t.Run("WithURIs alone satisfies the SAN requirement", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithURIs("spiffe://example.org/service/api"))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Empty(t, cert.DNSNames)
		assert.Empty(t, cert.IPAddresses)
		require.Len(t, cert.URIs, 1)
	})

	t.Run("WithURIs rejects URIs that would not be issued verbatim", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			uri  string
			want string
		}{
			{name: "empty", uri: "", want: "not absolute"},
			{name: "relative path", uri: "/service/api", want: "not absolute"},
			{name: "scheme-less authority", uri: "example.org/service/api", want: "not absolute"},
			{name: "non-ASCII", uri: "spiffe://example.org/service/äpi", want: "non-ASCII"},
			{name: "unescaped space", uri: "spiffe://example.org/service/a pi", want: "normalised form"},
			{name: "control character", uri: "spiffe://example.org/service/\x7f", want: "URI SAN"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				_, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "leaf1", time.Hour, pki.WithDNSNames("localhost"), pki.WithURIs(tt.uri))
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.want)
			})
		}
	})
}

func TestGenerateServerCert(t *testing.T) {
	t.Parallel()

	ca, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 24*time.Hour)
	require.NoError(t, err)

	t.Run("rejects non-positive validity", func(t *testing.T) {
		t.Parallel()

		for _, validity := range []time.Duration{0, -time.Hour} {
			t.Run(validity.String(), func(t *testing.T) {
				t.Parallel()

				_, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", validity, pki.WithDNSNames("localhost"))
				assert.ErrorContains(t, err, "validity must be positive")
			})
		}
	})

	t.Run("has only ExtKeyUsageServerAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, pki.WithDNSNames("localhost"))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
		assert.NotContains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	})

	t.Run("cert with SANs passes hostname verification", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, pki.WithDNSNames("localhost"))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.NoError(t, cert.VerifyHostname("localhost"))
	})

	t.Run("returns error when no SANs are provided", func(t *testing.T) {
		t.Parallel()

		_, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SAN")
	})

	t.Run("WithURIs alone satisfies the SAN requirement", func(t *testing.T) {
		t.Parallel()

		const spiffeID = "spiffe://example.org/service/api"
		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, pki.WithURIs(spiffeID))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		require.Len(t, cert.URIs, 1)
		assert.Equal(t, spiffeID, cert.URIs[0].String())
	})

	t.Run("WithSerial sets serial on server certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(7)
		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, pki.WithDNSNames("localhost"), pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})

	t.Run("SAN options keep their own copy of the names given", func(t *testing.T) {
		t.Parallel()

		names := []string{"localhost"}
		opt := pki.WithDNSNames(names...)
		names[0] = "changed.example"

		leaf, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, opt)
		require.NoError(t, err)

		assert.Equal(t, []string{"localhost"}, parseCert(t, leaf.CertPEM).DNSNames)
	})

	t.Run("rejects SANs that would make the certificate unparseable or empty", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			dnsNames []string
			ips      []net.IP
			uri      string
			want     string
		}{
			{name: "nil IP from a failed net.ParseIP", ips: []net.IP{net.ParseIP("10.0.0.300")}, want: "IP address SAN at index 0"},
			{name: "empty IP", ips: []net.IP{{}}, want: "IP address SAN at index 0"},
			{name: "empty DNS name", dnsNames: []string{""}, want: "DNS name SAN at index 0 is empty"},
			{name: "percent-encoded non-ASCII URI host", dnsNames: []string{"localhost"}, uri: "spiffe://a%C3%A4b/x", want: "parse created leaf certificate"},
			{name: "URI host with trailing dot", dnsNames: []string{"localhost"}, uri: "spiffe://example.org./x", want: "parse created leaf certificate"},
			{name: "URI host with empty label", dnsNames: []string{"localhost"}, uri: "spiffe://a..b/x", want: "parse created leaf certificate"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				opts := []pki.LeafOption{pki.WithDNSNames(tt.dnsNames...), pki.WithIPAddresses(tt.ips...)}
				if tt.uri != "" {
					opts = append(opts, pki.WithURIs(tt.uri))
				}
				_, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", time.Hour, opts...)
				require.Error(t, err, "created a certificate that cannot be used")
				assert.Contains(t, err.Error(), tt.want)
			})
		}
	})
}

func TestGenerateClientCert(t *testing.T) {
	t.Parallel()

	ca, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 24*time.Hour)
	require.NoError(t, err)

	t.Run("rejects non-positive validity", func(t *testing.T) {
		t.Parallel()

		for _, validity := range []time.Duration{0, -time.Hour} {
			t.Run(validity.String(), func(t *testing.T) {
				t.Parallel()

				_, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", validity)
				assert.ErrorContains(t, err, "validity must be positive")
			})
		}
	})

	t.Run("has only ExtKeyUsageClientAuth", func(t *testing.T) {
		t.Parallel()

		leaf, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", time.Hour)
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
		assert.NotContains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
	})

	t.Run("WithURIs sets URI SAN on client certificate", func(t *testing.T) {
		t.Parallel()

		const spiffeID = "spiffe://example.org/service/cli"
		leaf, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", time.Hour, pki.WithURIs(spiffeID))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		require.Len(t, cert.URIs, 1)
		assert.Equal(t, spiffeID, cert.URIs[0].String())
	})

	t.Run("WithSerial sets serial on client certificate", func(t *testing.T) {
		t.Parallel()

		want := big.NewInt(1000)
		leaf, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", time.Hour, pki.WithSerial(want))
		require.NoError(t, err)

		cert := parseCert(t, leaf.CertPEM)
		assert.Equal(t, 0, want.Cmp(cert.SerialNumber))
	})
}
