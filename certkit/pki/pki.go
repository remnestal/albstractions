// Package pki generates CA and leaf certificates for internal PKI and testing.
//
// Use [GenerateCA] for a self-signed CA, then [GenerateServerCert],
// [GenerateClientCert], or [GeneratePeerCert] for leaf certificates. Each
// returns a [Bundle] of PEM-encoded certificate and key bytes.
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
	"net/url"
	"time"
	"unicode"
)

// Bundle holds a PEM-encoded certificate and its matching private key.
type Bundle struct {
	CertPEM []byte
	KeyPEM  []byte
}

// KeyAlgorithm generates a private key.
//
// Use one of the provided constructors ([ECDSAP256], [ECDSAP384], [RSA2048],
// [RSA4096], [Ed25519]) or supply a custom one.
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
	uris           []string
}

// WithMaxPathLen sets the maximum CA chain depth.
//
// A value of 0 enforces a single-level hierarchy (no intermediate CAs); this
// is the default.
//
// WithMaxPathLen is ignored when passed to leaf certificate functions.
func WithMaxPathLen(n int) Option {
	return func(c *certConfig) {
		c.maxPathLen = n
		c.maxPathLenZero = n == 0
	}
}

// WithSerial sets a fixed serial number for the generated certificate.
//
// serial must be a positive integer whose absolute value fits within 20
// octets (RFC 5280 §4.1.2.2). If not set, a cryptographically random 128-bit
// serial is used.
func WithSerial(serial *big.Int) Option {
	return func(c *certConfig) {
		c.serial = serial
	}
}

// WithURIs sets the URI SANs of the generated certificate, preserving the
// given order.
//
// Typical use is a SPIFFE ID such as "spiffe://example.org/service/name",
// which peers can read back from [crypto/x509.Certificate.URIs] as the
// certificate's identity.
//
// Each uri must be an absolute, ASCII-only URI that is already in normalised
// form; anything the issued certificate would not reproduce verbatim is
// rejected, so an identity is never silently rewritten.
//
// A URI SAN satisfies the requirement that a server or peer certificate carry
// at least one SAN, so a leaf may be issued with no DNS name or IP address.
//
// WithURIs is ignored when passed to [GenerateCA].
func WithURIs(uris ...string) Option {
	return func(c *certConfig) {
		c.uris = uris
	}
}

// GenerateCA creates a self-signed CA certificate using the given algorithm.
//
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
// signed by the given CA.
//
// At least one SAN must be provided, either as a DNS name, an IP address, or a
// URI via [WithURIs].
func GenerateServerCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, opts...)
}

// GenerateClientCert creates a leaf certificate for client authentication,
// signed by the given CA.
func GenerateClientCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, opts...)
}

// GeneratePeerCert creates a leaf certificate for mutual TLS, valid for both
// server and client authentication, signed by the given CA.
//
// At least one SAN must be provided, either as a DNS name, an IP address, or a
// URI via [WithURIs].
func GeneratePeerCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, opts ...Option) (Bundle, error) {
	return generateCert(algorithm, ca, commonName, dnsNames, ips, validity, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, opts...)
}

func generateCert(algorithm KeyAlgorithm, ca Bundle, commonName string, dnsNames []string, ips []net.IP, validity time.Duration, extKeyUsage []x509.ExtKeyUsage, opts ...Option) (Bundle, error) {
	cfg := &certConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	uris, err := resolveURIs(cfg)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve leaf URI SANs: %w", err)
	}

	for _, u := range extKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			if len(dnsNames) == 0 && len(ips) == 0 && len(uris) == 0 {
				return Bundle{}, fmt.Errorf("server certificate requires at least one SAN (DNS name, IP address, or URI)")
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
		URIs:        uris,
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

func resolveURIs(cfg *certConfig) ([]*url.URL, error) {
	if len(cfg.uris) == 0 {
		return nil, nil
	}
	uris := make([]*url.URL, 0, len(cfg.uris))
	for _, raw := range cfg.uris {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse URI SAN %q: %w", raw, err)
		}
		if !u.IsAbs() {
			return nil, fmt.Errorf("URI SAN %q is not absolute (no scheme)", raw)
		}
		// URI SANs are encoded as IA5String; a non-ASCII byte would produce a
		// certificate that other implementations reject or read back differently.
		if !isASCII(raw) {
			return nil, fmt.Errorf("URI SAN %q contains non-ASCII characters", raw)
		}
		// x509 serialises u.String(), which url.Parse is free to normalise away
		// from the input. Reject anything that does not survive the round trip
		// so the issued identity is exactly the one the caller asked for.
		if u.String() != raw {
			return nil, fmt.Errorf("URI SAN %q is not in normalised form (would be issued as %q)", raw, u.String())
		}
		uris = append(uris, u)
	}
	return uris, nil
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > unicode.MaxASCII {
			return false
		}
	}
	return true
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
