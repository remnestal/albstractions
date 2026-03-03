# mtls

Build a `tls.Config` for mutual TLS from `keyloader.Provider` values.

```bash
go get github.com/remnestal/albstractions/mtls
```

## Overview

`NewTLSConfig` takes three providers — certificate, private key, and CA — and returns a `*tls.Config` that:

- Enforces TLS 1.3 minimum
- Requires and verifies client certificates (`tls.RequireAndVerifyClientCert`)
- Uses the same CA pool for both `ClientCAs` and `RootCAs`, so the config works in both server and client roles

```go
cfg, err := mtls.NewTLSConfig(
    keyloader.FromFile("/etc/service/cert.pem"),
    keyloader.FromFile("/etc/service/key.pem"),
    keyloader.FromFile("/etc/service/ca.pem"),
)
```

## gRPC and raw TLS

The stdlib HTTP client sets `ServerName` automatically from the request URL. For gRPC or raw `tls.Dial` calls, set it explicitly:

```go
cfg, err := mtls.NewTLSConfig(cert, key, ca,
    mtls.WithServerName("my-service.internal"),
)
```

## Testing

Use the `pki` package to generate certificates and `keyloader/mock` to inject them without touching the filesystem:

```go
ca, _ := pki.GenerateCA(pki.ECDSAP256(), "Test CA", "Test", time.Hour)
peer, _ := pki.GeneratePeerCert(pki.ECDSAP256(), ca, "peer", nil, nil, time.Hour)

cfg, err := mtls.NewTLSConfig(
    mock.StaticProvider(peer.CertPEM),
    mock.StaticProvider(peer.KeyPEM),
    mock.StaticProvider(ca.CertPEM),
)
```
