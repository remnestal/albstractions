// Package pki provides CA and leaf certificate generation for internal PKI
// and testing.
//
// Use [GenerateCA] to create a self-signed CA, then [GeneratePeerCert],
// [GenerateServerCert], or [GenerateClientCert] to issue leaf certificates
// signed by that CA. All functions return a [Bundle] containing PEM-encoded
// certificate and private key bytes ready for use with the standard library
// or the mtls package.
package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"
)

// Bundle holds a PEM-encoded certificate and its matching private key.
type Bundle struct {
	CertPEM []byte
	KeyPEM  []byte
}

// KeyAlgorithm generates a private key. Use one of the provided constructors
// (ECDSAP256, ECDSAP384, RSA2048, RSA4096, Ed25519) or supply a custom one.
type KeyAlgorithm func() (crypto.Signer, error)

// ECDSAP256 generates an ECDSA P-256 key.
func ECDSAP256() KeyAlgorithm {
	return func() (crypto.Signer, error) {
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
}

// ECDSAP384 generates an ECDSA P-384 key.
func ECDSAP384() KeyAlgorithm {
	return func() (crypto.Signer, error) {
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	}
}

// RSA2048 generates a 2048-bit RSA key.
func RSA2048() KeyAlgorithm {
	return func() (crypto.Signer, error) {
		return rsa.GenerateKey(rand.Reader, 2048)
	}
}

// RSA4096 generates a 4096-bit RSA key.
func RSA4096() KeyAlgorithm {
	return func() (crypto.Signer, error) {
		return rsa.GenerateKey(rand.Reader, 4096)
	}
}

// Ed25519 generates an Ed25519 key.
func Ed25519() KeyAlgorithm {
	return func() (crypto.Signer, error) {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		return priv, err
	}
}

// Option configures certificate generation.
type Option func(*certConfig)

type certConfig struct {
	maxPathLen     int
	maxPathLenZero bool
	serial         *big.Int
}

// WithMaxPathLen sets the maximum CA chain depth. A value of 0 with
// WithMaxPathLen(0) enforces a single-level hierarchy (no intermediate CAs).
// By default, path length is set to 0 (single-level).
//
// WithMaxPathLen is ignored when passed to leaf certificate functions.
func WithMaxPathLen(n int) Option {
	return func(c *certConfig) {
		c.maxPathLen = n
		c.maxPathLenZero = n == 0
	}
}

// WithSerial sets a fixed serial number for the generated certificate.
// serial must be a positive integer whose absolute value fits within 20 octets
// (RFC 5280 §4.1.2.2). If not set, a cryptographically random 128-bit serial
// is used.
func WithSerial(serial *big.Int) Option {
	return func(c *certConfig) {
		c.serial = serial
	}
}

// GenerateCA creates a self-signed CA certificate using the given algorithm.
// By default the CA enforces a single-level hierarchy (MaxPathLen=0).
func GenerateCA(algorithm KeyAlgorithm, commonName, org string, validity time.Duration, opts ...Option) (Bundle, error) {
	cfg := &certConfig{
		maxPathLen:     0,
		maxPathLenZero: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	privateKey, err := algorithm()
	if err != nil {
		return Bundle{}, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := resolveSerial(cfg)
	if err != nil {
		return Bundle{}, fmt.Errorf("generate CA serial: %w", err)
	}

	// Back-date by one minute to tolerate minor clock skew between peers.
	notBefore := time.Now().Add(-time.Minute)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{org},
		},
		NotBefore:             notBefore,
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            cfg.maxPathLen,
		MaxPathLenZero:        cfg.maxPathLenZero,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("create CA certificate: %w", err)
	}

	keyPEM, err := marshalPrivateKey(privateKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("marshal CA private key: %w", err)
	}

	return Bundle{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		KeyPEM:  keyPEM,
	}, nil
}

// GenerateServerCert creates a leaf certificate for server authentication,
// signed by the given CA. At least one DNS name or IP address must be provided.
func GenerateServerCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, opts...)
}

// GenerateClientCert creates a leaf certificate for client authentication,
// signed by the given CA.
func GenerateClientCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, opts...)
}

// GeneratePeerCert creates a leaf certificate for mutual TLS, valid for both
// server and client authentication, signed by the given CA. At least one DNS
// name or IP address must be provided.
func GeneratePeerCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, opts...)
}

func generateCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, extKeyUsage []x509.ExtKeyUsage, opts ...Option) (Bundle, error) {
	cfg := &certConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	for _, u := range extKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			if len(dnsNames) == 0 && len(ips) == 0 {
				return Bundle{}, fmt.Errorf("server certificate requires at least one SAN (DNS name or IP address)")
			}
			break
		}
	}

	caCert, caKey, err := parseCA(ca)
	if err != nil {
		return Bundle{}, fmt.Errorf("parse CA bundle: %w", err)
	}

	if !caCert.IsCA {
		return Bundle{}, fmt.Errorf("provided bundle is not a CA certificate")
	}

	leafNotAfter := time.Now().Add(validity)
	if leafNotAfter.After(caCert.NotAfter) {
		return Bundle{}, fmt.Errorf("leaf certificate validity (%s) extends beyond CA expiry (%s)", leafNotAfter.Format(time.RFC3339), caCert.NotAfter.Format(time.RFC3339))
	}

	privateKey, err := algorithm()
	if err != nil {
		return Bundle{}, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := resolveSerial(cfg)
	if err != nil {
		return Bundle{}, fmt.Errorf("generate leaf serial: %w", err)
	}

	// Back-date by one minute to tolerate minor clock skew between peers.
	notBefore := time.Now().Add(-time.Minute)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: caCert.Subject.Organization,
		},
		DNSNames:    dnsNames,
		IPAddresses: ips,
		NotBefore:   notBefore,
		NotAfter:    leafNotAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: extKeyUsage,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, privateKey.Public(), caKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("create leaf certificate: %w", err)
	}

	keyPEM, err := marshalPrivateKey(privateKey)
	if err != nil {
		return Bundle{}, fmt.Errorf("marshal leaf private key: %w", err)
	}

	return Bundle{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		KeyPEM:  keyPEM,
	}, nil
}

func parseCA(ca Bundle) (*x509.Certificate, crypto.Signer, error) {
	certBlock, _ := pem.Decode(ca.CertPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("decode CA certificate PEM: no PEM block found")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA certificate: %w", err)
	}

	keyBlock, _ := pem.Decode(ca.KeyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("decode CA key PEM: no PEM block found")
	}
	key, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA private key: %w", err)
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, fmt.Errorf("CA private key does not implement crypto.Signer")
	}

	return cert, signer, nil
}

func resolveSerial(cfg *certConfig) (*big.Int, error) {
	if cfg.serial == nil {
		return randomSerial()
	}
	if cfg.serial.Sign() <= 0 {
		return nil, fmt.Errorf("serial must be a positive integer")
	}
	if len(cfg.serial.Bytes()) > 20 {
		return nil, fmt.Errorf("serial exceeds maximum length of 20 octets (RFC 5280)")
	}
	return cfg.serial, nil
}

func randomSerial() (*big.Int, error) {
	// Generate in [1, 2^128] — RFC 5280 requires serial numbers to be positive integers.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	return serial.Add(serial, big.NewInt(1)), nil
}

func marshalPrivateKey(key crypto.Signer) ([]byte, error) {
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}
