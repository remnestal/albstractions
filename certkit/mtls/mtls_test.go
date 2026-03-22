package mtls

import (
	"crypto/tls"
	"crypto/x509"
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

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Tests for NewTLSConfig
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Integration tests
// ---------------------------------------------------------------------------

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
			Timeout:   2 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.Error(t, err)
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
			Timeout:   2 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.Error(t, err)
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
			Timeout:   2 * time.Second,
		}
		_, err = c.Get(srv.URL)
		assert.Error(t, err)
	})
}

// errTest is a simple error type for test providers.
type errTest string

func (e errTest) Error() string { return string(e) }
