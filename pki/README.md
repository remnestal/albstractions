# pki

Generate self-signed CA and leaf certificates for mTLS and testing.

```bash
go get github.com/remnestal/albstractions/pki
```

## Overview

All functions return a `Bundle` — PEM-encoded certificate and private key bytes:

```go
type Bundle struct {
    CertPEM []byte
    KeyPEM  []byte
}
```

## Generating a CA and leaf certificate

```go
ca, err := pki.GenerateCA(pki.ECDSAP256(), "My CA", "My Org", 24*time.Hour)

// Peer cert (valid for both server and client auth)
peer, err := pki.GeneratePeerCert(
    pki.ECDSAP256(), ca, "my-service",
    []string{"my-service.internal"}, nil,
    time.Hour,
)

// Server-only or client-only
server, err := pki.GenerateServerCert(pki.ECDSAP256(), ca, "server", ...)
client, err := pki.GenerateClientCert(pki.ECDSAP256(), ca, "client", ...)
```

## Key algorithms

| Constructor | Algorithm |
|------------|-----------|
| `pki.ECDSAP256()` | ECDSA P-256 (recommended) |
| `pki.ECDSAP384()` | ECDSA P-384 |
| `pki.Ed25519()` | Ed25519 |
| `pki.RSA2048()` | RSA 2048-bit |
| `pki.RSA4096()` | RSA 4096-bit |

## Notes

- CA certificates default to `MaxPathLen=0` (no intermediate CAs). Use `pki.WithMaxPathLen(n)` to allow deeper chains.
- Leaf certificate validity is validated against CA expiry — generating a cert that outlives its CA returns an error.
- `NotBefore` is back-dated by one minute to tolerate minor clock skew between peers.
