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
    time.Hour,
    pki.WithDNSNames("localhost", "my-service.internal"),
    pki.WithIPAddresses(net.ParseIP("127.0.0.1")),
    pki.WithURIs("spiffe://example.org/ns/default/sa/my-service"),
)
```

Both return a `Bundle` of PEM-encoded `CertPEM` and `KeyPEM` bytes.

### Serve mutual TLS from key material on disk

`NewTLSConfig` produces a config usable for both server and client roles, with TLS 1.3 as the floor and `RequireAndVerifyClientCert` enabled. The CA provider populates both `ClientCAs` and `RootCAs`.

`FromFile` applies its `0600` check to every file it reads, so the certificate and CA providers opt out with `WithoutPermissionCheck`: those files are public and usually `0644`.

```go
tlsCfg, err := mtls.NewTLSConfig(
    keyloader.FromFile("/etc/myservice/cert.pem", keyloader.WithoutPermissionCheck()),
    keyloader.FromFile("/etc/myservice/key.pem"),
    keyloader.FromFile("/etc/myservice/ca.pem", keyloader.WithoutPermissionCheck()),
)

srv := &http.Server{Addr: ":8443", Handler: mux, TLSConfig: tlsCfg}
```

For the client side of the same connection, add `mtls.WithServerName` so verification uses the expected identity rather than the dialled address.

The client matches the server's DNS name or IP address, never a URI SAN, so a server whose leaf carries only URI SANs (for example a SPIFFE ID from `pki.WithURIs`) fails the handshake. Issue server leaves a DNS name or IP address as well. Servers check client certificates against the CA alone, so URI-only clients are unaffected.

### Load key material from the environment

`FromEnv` reads a variable instead of a file, and wrapping it in `Base64` decodes it on the way out. This suits platforms that inject secrets as base64 or hex blobs. The decoding wrappers take any `Provider`, so they work the same on a file or a test double.

```go
tlsCfg, err := mtls.NewTLSConfig(
    keyloader.Base64(keyloader.FromEnv("SERVICE_CERT")),
    keyloader.Base64(keyloader.FromEnv("SERVICE_KEY")),
    keyloader.FromFile("/etc/myservice/ca.pem", keyloader.WithoutPermissionCheck()),
)
```

`FromEnv` zeroes its copy of the value, but not the variable itself: the runtime holds it as an immutable string for the life of the process, and child processes inherit it. A file provider keeps key material shorter-lived.

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

`Hex`, `Base64` and `Decode` zero every buffer they decode through, also when decoding fails.

`FromFile` refuses to read a file whose mode is looser than `0600`. Pass `WithoutPermissionCheck` to opt out where the platform makes that impractical, such as Windows, which reports every file as `0666`, or `0444` when read-only. The file is opened once and both the check and the read use that handle, so a file swapped in between cannot slip past the check.

## Options

### `keyloader`

| Symbol | Applies to | Effect |
|--------|-----------|--------|
| `Hex(p)` / `Base64(p)` | any provider | Hex- or base64-decode the key from `p`, skipping ASCII space, tab, CR and LF |
| `Decode(p, d)` | any provider | Decode the key from `p` with a custom `Decoder`, which receives the value unchanged |
| `WithEnvGetter(fn)` | env | Read variables through fn instead of `os.Getenv` |
| `WithFilesystem(fsys)` | file | Open the file through an `fs.FS` instead of the OS |
| `WithoutPermissionCheck()` | file | Skip the `0600`-or-stricter mode check |

### `pki`

`GenerateCA` takes `CAOption`s and the leaf generators take `LeafOption`s, so an option that does not apply is a compile error rather than silently ignored.

| Symbol | Effect |
|--------|--------|
| `ECDSAP256()`, `ECDSAP384()`, `RSA2048()`, `RSA4096()`, `Ed25519()` | Key algorithm for the generated certificate |
| `MLDSA44()`, `MLDSA65()`, `MLDSA87()` | Post-quantum ML-DSA (FIPS 204) key. Peers must accept ML-DSA signatures in TLS 1.3, as Go does from 1.27 |
| `WithMaxPathLen(n)` | CA only: path length constraint. Default 0, a single-level hierarchy |
| `WithSerial(n)` | CA or leaf: fixed serial number instead of a random 128-bit one |
| `WithDNSNames(names...)` | Leaf only: DNS name SANs |
| `WithIPAddresses(ips...)` | Leaf only: IP address SANs |
| `WithURIs(uris...)` | Leaf only: URI SANs, for example SPIFFE IDs. Validated as absolute ASCII URIs that round-trip verbatim |

### `mtls`

| Option | Effect |
|--------|--------|
| `WithServerName(name)` | Sets `ServerName`, for verifying a peer by identity rather than dial address |

## Raw keys

A raw key is used byte for byte. A key file written with `echo` ends in a newline, and that newline becomes part of the key; write it with `printf '%s'`, or store the key hex- or base64-encoded, whose decoders skip whitespace.

## Generation guarantees

Certificates are back-dated one minute to absorb clock skew between hosts. A zero or negative validity is rejected. A leaf is rejected if its validity would outlive the issuing CA, and the signing bundle is checked to be a real CA before use. Server and peer certificates require at least one SAN. Keys are emitted as PKCS#8 PEM.

## Testing

`keyloader/mock` avoids touching real files or environment variables:

```go
tlsCfg, err := mtls.NewTLSConfig(
    mock.StaticProvider(cert.CertPEM),
    mock.StaticProvider(cert.KeyPEM),
    mock.StaticProvider(ca.CertPEM),
)
```

`mock.ErrorProvider(err)` covers the failure path. `FromFile` reads through any `fs.FS`, so the standard library's `fstest.MapFS`, whose entries carry a mode, makes the permission check testable without touching disk. Its paths are unrooted:

```go
fsys := fstest.MapFS{
    "key.pem": {Data: keyPEM, Mode: 0o644}, // too permissive
}

_, _, err := keyloader.FromFile("key.pem", keyloader.WithFilesystem(fsys))()
```
