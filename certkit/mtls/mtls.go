// Package mtls provides utilities for configuring mutual TLS (mTLS).
//
// [NewTLSConfig] builds a [crypto/tls.Config] suitable for both server and
// client roles from three [keyloader.Provider] values — certificate, private
// key, and CA certificate. The resulting config enforces TLS 1.3 and mutual
// authentication. Load key material with the keyloader package and generate
// test certificates with the pki package.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"

	"github.com/remnestal/albstractions/certkit/keyloader"
)

// Option configures a TLS config.
type Option func(*tls.Config)

// WithServerName sets the ServerName field on the returned tls.Config.
// This is not required when using stdlib http.Client — the Transport sets
// ServerName automatically from the request URL. It is useful for raw
// tls.Dial calls or gRPC where no URL parsing occurs.
func WithServerName(name string) Option {
	return func(c *tls.Config) {
		c.ServerName = name
	}
}

// NewTLSConfig creates a tls.Config suitable for use as both a server and client
// in an mTLS setup. ClientCAs and RootCAs are set to the same pool since all
// peers share a single CA. The Go TLS stack uses the relevant fields depending
// on which role the connection takes.
//
// Note: the free functions from cert, key, and ca zero the raw PEM bytes after
// parsing, but X509KeyPair copies key material internally — the parsed private
// key inside tls.Certificate is not zeroed. This is a known limitation of the
// standard library.
func NewTLSConfig(cert, key, ca keyloader.Provider, opts ...Option) (*tls.Config, error) {
	certPEM, freeCert, err := cert()
	if err != nil {
		return nil, fmt.Errorf("load certificate: %w", err)
	}
	defer freeCert()

	keyPEM, freeKey, err := key()
	if err != nil {
		return nil, fmt.Errorf("load private key: %w", err)
	}
	defer freeKey()

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse certificate/key pair: %w", err)
	}

	caPEM, freeCA, err := ca()
	if err != nil {
		return nil, fmt.Errorf("load CA certificate: %w", err)
	}
	defer freeCA()

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse CA certificate: invalid PEM")
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		RootCAs:      caPool,
		MinVersion:   tls.VersionTLS13,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg, nil
}
