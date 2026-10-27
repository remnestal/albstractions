package mtls

import (
	"crypto/mldsa"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/remnestal/albstractions/certkit/keyloader/mock"
	"github.com/remnestal/albstractions/certkit/pki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCA returns a CA that always has the same subject. That is
// deliberate: in TLS 1.3 a client offers its certificate only when its issuer
// matches a CA the server lists, so a rogue CA with the same name makes the
// client offer a certificate the server then rejects on its signature,
// exercising "unknown certificate authority" rather than "certificate
// required". It also models a forged CA that copies a real CA's name.
func generateTestCA(t *testing.T) pki.Bundle {
	t.Helper()
	bundle, err := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test Org", 48*time.Hour)
	require.NoError(t, err)
	return bundle
}

func generateTestCert(t *testing.T, commonName string, ca pki.Bundle) pki.Bundle {
	t.Helper()
	bundle, err := pki.GeneratePeerCert(pki.ECDSAP256(), ca, commonName, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, 24*time.Hour)
	require.NoError(t, err)
	return bundle
}

func TestNewTLSConfig(t *testing.T) {
	t.Parallel()

	t.Run("creates valid TLS config for both server and client roles", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider(cert.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)

		require.NoError(t, err)
		assert.NotNil(t, tlsConfig)
		assert.Equal(t, uint16(tls.VersionTLS13), tlsConfig.MinVersion)
		assert.Equal(t, tls.RequireAndVerifyClientCert, tlsConfig.ClientAuth)
		assert.NotNil(t, tlsConfig.ClientCAs)
		assert.NotNil(t, tlsConfig.RootCAs)
		assert.Len(t, tlsConfig.Certificates, 1)
	})

	t.Run("returns error when certificate provider fails", func(t *testing.T) {
		t.Parallel()

		tlsConfig, err := NewTLSConfig(
			mock.ErrorProvider(errTest("mock cert error")),
			mock.StaticProvider([]byte("unused")),
			mock.StaticProvider([]byte("unused")),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "load certificate")
	})

	t.Run("returns error when key provider fails", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.ErrorProvider(errTest("mock key error")),
			mock.StaticProvider(ca.CertPEM),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "load private key")
	})

	t.Run("returns error when CA provider fails", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider(cert.KeyPEM),
			mock.ErrorProvider(errTest("mock CA error")),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "load CA certificate")
	})

	t.Run("returns error for invalid certificate PEM", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider([]byte("invalid-cert-pem")),
			mock.StaticProvider(cert.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "parse certificate/key pair")
	})

	t.Run("returns error for invalid key PEM", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider([]byte("invalid-key-pem")),
			mock.StaticProvider(ca.CertPEM),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "parse certificate/key pair")
	})

	t.Run("returns error for mismatched certificate and key", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)
		other := generateTestCert(t, "other-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider(other.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "parse certificate/key pair")
	})

	t.Run("WithServerName sets ServerName on config", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider(cert.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
			WithServerName("example.com"),
		)

		require.NoError(t, err)
		assert.Equal(t, "example.com", tlsConfig.ServerName)
	})

	t.Run("returns error for invalid CA certificate PEM", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		cert := generateTestCert(t, "test-peer", ca)

		tlsConfig, err := NewTLSConfig(
			mock.StaticProvider(cert.CertPEM),
			mock.StaticProvider(cert.KeyPEM),
			mock.StaticProvider([]byte("invalid-ca-pem")),
		)

		assert.Error(t, err)
		assert.Nil(t, tlsConfig)
		assert.Contains(t, err.Error(), "parse CA certificate")
	})
}

func TestMTLSIntegration(t *testing.T) {
	t.Parallel()

	t.Run("peers communicate successfully with valid mTLS", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		server := generateTestCert(t, "localhost", ca)
		client := generateTestCert(t, "test-client", ca)

		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("mTLS success"))
			require.NoError(t, err)
		}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(client.CertPEM),
			mock.StaticProvider(client.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		c := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLSConfig}}
		resp, err := c.Get(srv.URL)
		require.NoError(t, err)
		defer func() { require.NoError(t, resp.Body.Close()) }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "mTLS success", string(body))
	})

	t.Run("peers communicate successfully with ML-DSA certificates", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			algorithm pki.KeyAlgorithm
			params    mldsa.Parameters
			signature x509.SignatureAlgorithm
		}{
			{"ML-DSA-44", pki.MLDSA44(), mldsa.MLDSA44(), x509.MLDSA44},
			{"ML-DSA-65", pki.MLDSA65(), mldsa.MLDSA65(), x509.MLDSA65},
			{"ML-DSA-87", pki.MLDSA87(), mldsa.MLDSA87(), x509.MLDSA87},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// CA and both leaves share the algorithm, so every signature in
				// the chain and in the handshake is ML-DSA at this level.
				ca, err := pki.GenerateCA(tt.algorithm, "Test CA", "Test Org", 48*time.Hour)
				require.NoError(t, err)
				server, err := pki.GeneratePeerCert(tt.algorithm, ca, "localhost", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, 24*time.Hour)
				require.NoError(t, err)
				client, err := pki.GeneratePeerCert(tt.algorithm, ca, "test-client", []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, 24*time.Hour)
				require.NoError(t, err)

				serverTLSConfig, err := NewTLSConfig(
					mock.StaticProvider(server.CertPEM),
					mock.StaticProvider(server.KeyPEM),
					mock.StaticProvider(ca.CertPEM),
				)
				require.NoError(t, err)

				// Echo the verified client key's parameter set so the server's
				// view is checked on the test goroutine.
				srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var level string
					if pub, ok := r.TLS.VerifiedChains[0][0].PublicKey.(*mldsa.PublicKey); ok {
						level = pub.Parameters().String()
					}
					_, err := w.Write([]byte(level))
					assert.NoError(t, err)
				}))
				srv.TLS = serverTLSConfig
				srv.StartTLS()
				defer srv.Close()

				clientTLSConfig, err := NewTLSConfig(
					mock.StaticProvider(client.CertPEM),
					mock.StaticProvider(client.KeyPEM),
					mock.StaticProvider(ca.CertPEM),
				)
				require.NoError(t, err)

				c := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLSConfig}}
				resp, err := c.Get(srv.URL)
				require.NoError(t, err)
				defer func() { require.NoError(t, resp.Body.Close()) }()

				assert.Equal(t, uint16(tls.VersionTLS13), resp.TLS.Version)
				peer := resp.TLS.PeerCertificates[0]
				assert.Equal(t, x509.MLDSA, peer.PublicKeyAlgorithm)
				assert.Equal(t, tt.signature, peer.SignatureAlgorithm)
				if pub, ok := peer.PublicKey.(*mldsa.PublicKey); assert.True(t, ok, "server key is %T", peer.PublicKey) {
					assert.Equal(t, tt.params, pub.Parameters())
				}

				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, tt.params.String(), string(body), "client key as verified by the server")
			})
		}
	})

	t.Run("connection fails without client certificate", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		server := generateTestCert(t, "localhost", ca)

		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig := &tls.Config{
			RootCAs:    x509.NewCertPool(),
			MinVersion: tls.VersionTLS13,
		}
		clientTLSConfig.RootCAs.AppendCertsFromPEM(ca.CertPEM)

		c := &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLSConfig},
			Timeout:   10 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.ErrorContains(t, err, "remote error: tls: certificate required")
	})

	t.Run("connection fails with certificate from untrusted CA", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		server := generateTestCert(t, "localhost", ca)

		untrustedCA := generateTestCA(t)
		untrustedClient := generateTestCert(t, "untrusted-client", untrustedCA)

		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(untrustedClient.CertPEM),
			mock.StaticProvider(untrustedClient.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		c := &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLSConfig},
			Timeout:   10 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.ErrorContains(t, err, "remote error: tls: unknown certificate authority")
	})

	t.Run("connection fails when server uses wrong CA to verify client", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		server := generateTestCert(t, "localhost", ca)
		client := generateTestCert(t, "test-client", ca)
		wrongCA := generateTestCA(t)

		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(wrongCA.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(client.CertPEM),
			mock.StaticProvider(client.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		c := &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLSConfig},
			Timeout:   10 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.ErrorContains(t, err, "remote error: tls: unknown certificate authority")
	})

	t.Run("client rejects server certificate from untrusted CA", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		rogueCA := generateTestCA(t)
		server := generateTestCert(t, "localhost", rogueCA)
		client := generateTestCert(t, "test-client", ca)

		// The server would accept the client; only the client's own
		// verification of the server can make this connection fail.
		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(client.CertPEM),
			mock.StaticProvider(client.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		c := &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLSConfig},
			Timeout:   10 * time.Second,
		}
		_, err = c.Get(srv.URL)
		require.Error(t, err)
		_, ok := errors.AsType[*tls.CertificateVerificationError](err)
		assert.True(t, ok, "expected a client-side certificate verification error, got: %v", err)
		_, ok = errors.AsType[x509.UnknownAuthorityError](err)
		assert.True(t, ok, "expected the chain check to fail, got: %v", err)
	})

	t.Run("client rejects server whose certificate does not match ServerName", func(t *testing.T) {
		t.Parallel()

		ca := generateTestCA(t)
		server := generateTestCert(t, "localhost", ca)
		client := generateTestCert(t, "test-client", ca)

		serverTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(server.CertPEM),
			mock.StaticProvider(server.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
		)
		require.NoError(t, err)

		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.TLS = serverTLSConfig
		srv.StartTLS()
		defer srv.Close()

		clientTLSConfig, err := NewTLSConfig(
			mock.StaticProvider(client.CertPEM),
			mock.StaticProvider(client.KeyPEM),
			mock.StaticProvider(ca.CertPEM),
			WithServerName("other.example"),
		)
		require.NoError(t, err)

		c := &http.Client{
			Transport: &http.Transport{TLSClientConfig: clientTLSConfig},
			Timeout:   10 * time.Second,
		}
		_, err = c.Get(srv.URL)
		require.Error(t, err)
		_, ok := errors.AsType[*tls.CertificateVerificationError](err)
		assert.True(t, ok, "expected a client-side certificate verification error, got: %v", err)
		_, ok = errors.AsType[x509.HostnameError](err)
		assert.True(t, ok, "expected the hostname check to fail, got: %v", err)
	})
}

// errTest is a simple error type for test providers.
type errTest string

func (e errTest) Error() string { return string(e) }
