# certkit

[![Go Reference](https://pkg.go.dev/badge/github.com/remnestal/albstractions/certkit.svg)](https://pkg.go.dev/github.com/remnestal/albstractions/certkit)

Certificate and key management for internal PKI and mutual TLS. Three focused packages in a single module:

- **`keyloader`** loads cryptographic key material from files or environment variables, with optional hex/base64 decoding, permission checks, and secure memory zeroing
- **`pki`** generates self-signed CAs and leaf certificates for mTLS and testing
- **`mtls`** builds a `tls.Config` for mutual TLS from `keyloader.Provider` values

```bash
go get github.com/remnestal/albstractions/certkit
```

## Usage

### Bootstrap a CA and an mTLS peer certificate

`GeneratePeerCert` issues a leaf carrying both server and client extended key usage, so a single certificate works for both sides of an mTLS connection. Use `GenerateServerCert` or `GenerateClientCert` when the roles should be separate.

```go
ca, err := pki.GenerateCA(pki.ECDSAP256(), "My CA", "My Org", 24*time.Hour)

cert, err := pki.GeneratePeerCert(
    pki.ECDSAP256(),
    ca,
    "my-service",
    []string{"localhost", "my-service.internal"},
    []net.IP{net.ParseIP("127.0.0.1")},
    time.Hour,
    pki.WithURIs("spiffe://example.org/ns/default/sa/my-service"),
)
```

Both return a `Bundle` of PEM-encoded `CertPEM` and `KeyPEM` bytes.

### Serve mutual TLS from key material on disk

`NewTLSConfig` produces a config usable for both server and client roles, with TLS 1.3 as the floor and `RequireAndVerifyClientCert` enabled. The CA provider populates both `ClientCAs` and `RootCAs`.

```go
tlsCfg, err := mtls.NewTLSConfig(
    keyloader.FromFile("/etc/myservice/cert.pem"),
    keyloader.FromFile("/etc/myservice/key.pem"),
    keyloader.FromFile("/etc/myservice/ca.pem"),
)

srv := &http.Server{Addr: ":8443", Handler: mux, TLSConfig: tlsCfg}
```

For the client side of the same connection, add `mtls.WithServerName` so verification uses the expected identity rather than the dialled address.

### Load key material from the environment

`FromEnv` reads a variable instead of a file, decoding it on the way out. This suits platforms that inject secrets as base64 or hex blobs.

```go
tlsCfg, err := mtls.NewTLSConfig(
    keyloader.FromEnv("SERVICE_CERT", keyloader.WithEnvBase64()),
    keyloader.FromEnv("SERVICE_KEY", keyloader.WithEnvBase64()),
    keyloader.FromFile("/etc/myservice/ca.pem"),
)
```

Note that the Go string backing an environment variable cannot be zeroed, so a file provider gives key material a shorter lifetime in memory.

## Key material lifetime

A `Provider` returns the key bytes together with a `free` function that zeroes them. `free` is never nil, even when the provider returns an error, and it is idempotent, so call it as soon as the key is no longer needed and defer it as well. The deferred call is the backstop; the explicit one is what keeps the key material short-lived in memory.

```go
key, free, err := keyloader.FromFile("/etc/myservice/key.pem")()
defer free()
if err != nil {
    return err
}

cert, err := tls.X509KeyPair(certPEM, key)
free() // the key is no longer needed; do not wait for the deferred call
```

`FromFile` refuses to read a file whose mode is looser than `0600`. Pass `WithoutPermissionCheck` to opt out where the platform makes that impractical.

## Options

### `keyloader`

| Option | Applies to | Effect |
|--------|-----------|--------|
| `WithEnvHex()` / `WithFileHex()` | env / file | Hex-decode the loaded value |
| `WithEnvBase64()` / `WithFileBase64()` | env / file | Base64-decode the loaded value |
| `WithEnvDecoder(d)` / `WithFileDecoder(d)` | env / file | Decode with a custom `Decoder` |
| `WithEnvTrimWhitespace()` / `WithFileTrimWhitespace()` | env / file | Strip all whitespace before decoding |
| `WithEnvGetter(fn)` | env | Read variables through fn instead of `os.Getenv` |
| `WithFilesystem(fs)` | file | Read through a `FileSystem` instead of the OS |
| `WithoutPermissionCheck()` | file | Skip the `0600`-or-stricter mode check |

### `pki`

| Symbol | Effect |
|--------|--------|
| `ECDSAP256()`, `ECDSAP384()`, `RSA2048()`, `RSA4096()`, `Ed25519()` | Key algorithm for the generated certificate |
| `WithMaxPathLen(n)` | CA path length constraint. Default 0, a single-level hierarchy |
| `WithSerial(n)` | Fixed serial number instead of a random 128-bit one |
| `WithURIs(uris...)` | URI SANs, for example SPIFFE IDs. Validated as absolute ASCII URIs that round-trip verbatim |

### `mtls`

| Option | Effect |
|--------|--------|
| `WithServerName(name)` | Sets `ServerName`, for verifying a peer by identity rather than dial address |

## Generation guarantees

Certificates are back-dated one minute to absorb clock skew between hosts. A leaf is rejected if its validity would outlive the issuing CA, and the signing bundle is checked to be a real CA before use. Server and peer certificates require at least one SAN. Keys are emitted as PKCS#8 PEM.

## Testing

`keyloader/mock` avoids touching real files or environment variables:

```go
tlsCfg, err := mtls.NewTLSConfig(
    mock.StaticProvider(cert.CertPEM),
    mock.StaticProvider(cert.KeyPEM),
    mock.StaticProvider(ca.CertPEM),
)
```

`mock.ErrorProvider(err)` covers the failure path, and `mock.FileSystem` is an in-memory `keyloader.FileSystem` whose `mock.File` entries carry a mode, so the permission-check behaviour of `FromFile` is testable without touching disk:

```go
fsys := &mock.FileSystem{Files: map[string]mock.File{
    "/etc/key.pem": {Data: keyPEM, Mode: 0o644}, // too permissive
}}

_, _, err := keyloader.FromFile("/etc/key.pem", keyloader.WithFilesystem(fsys))()
```
