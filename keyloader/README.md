# keyloader

Secure loading of key material (e.g. cryptographic, tokens, etc.) from environment variables or files.

```bash
go get github.com/remnestal/albstractions/keyloader
```

## Overview

The core type is `Provider` — a function that returns key bytes and a `free` function that zeros them:

```go
type Provider func() (key []byte, free func(), err error)
```

Always defer `free` to limit how long sensitive material lives in memory:

```go
key, free, err := provider()
if err != nil { ... }
defer free()
```

## Loading from a file

```go
// Raw PEM file with strict 0600 permission check
provider := keyloader.FromFile("/etc/service/key.pem")

// Base64-encoded, with whitespace trimming (e.g. multi-line env-injected secrets)
provider := keyloader.FromFile("/etc/service/key.b64",
    keyloader.WithFileBase64(),
    keyloader.WithFileTrimWhitespace(),
)
```

## Loading from an environment variable

```go
// Hex-encoded key in an env var
provider := keyloader.FromEnv("SERVICE_KEY", keyloader.WithEnvHex())
```

## Testing

Use `keyloader/mock` in tests to avoid touching real files or environment variables:

```go
import "github.com/remnestal/albstractions/keyloader/mock"

provider := mock.StaticProvider([]byte("test-key-material"))

mfs := &mock.FileSystem{
    Files: map[string]mock.File{
        "/key.pem": {Data: pemBytes, Mode: 0600},
    },
}
provider := keyloader.FromFile("/key.pem", keyloader.WithFilesystem(mfs))
```
