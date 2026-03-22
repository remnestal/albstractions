# certkit

Certificate and key management for internal PKI and mutual TLS. Three focused packages in a single module:

- **`keyloader`** — load cryptographic key material from files or environment variables, with optional hex/base64 decoding and secure memory zeroing
- **`pki`** — generate self-signed CAs and leaf certificates for mTLS and testing
- **`mtls`** — build a `tls.Config` for mutual TLS from `keyloader.Provider` values

## Install

```bash
go get github.com/remnestal/albstractions/certkit
```

## Usage

```go
import (
    "github.com/remnestal/albstractions/certkit/keyloader"
    "github.com/remnestal/albstractions/certkit/mtls"
    "github.com/remnestal/albstractions/certkit/pki"
)

// Generate a CA and peer certificate (e.g. in tests or bootstrap code)
ca, _ := pki.GenerateCA(pki.ECDSAP256(), "My CA", "My Org", 24*time.Hour)
cert, _ := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "my-service", []string{"localhost"}, nil, time.Hour)

// Load key material from files at runtime
tlsCfg, err := mtls.NewTLSConfig(
    keyloader.FromFile("/etc/myservice/cert.pem"),
    keyloader.FromFile("/etc/myservice/key.pem"),
    keyloader.FromFile("/etc/myservice/ca.pem"),
)
```

Always defer the `free` function returned by a `Provider` to limit the lifetime of key material in memory:

```go
key, free, err := keyloader.FromFile("/etc/myservice/key.pem")()
if err != nil {
    return err
}
defer free()
```

## Testing

Use `keyloader/mock` to avoid touching real files or environment variables in tests:

```go
import "github.com/remnestal/albstractions/certkit/keyloader/mock"

tlsCfg, err := mtls.NewTLSConfig(
    mock.StaticProvider(cert.CertPEM),
    mock.StaticProvider(cert.KeyPEM),
    mock.StaticProvider(ca.CertPEM),
)
```
