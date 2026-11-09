// Package keyloader loads cryptographic key material from environment
// variables or files.
//
// Construct a [Provider] with [FromEnv] or [FromFile]; it returns the key
// bytes and a free function that zeros them — always defer free. The mock
// sub-package provides test doubles.
package keyloader

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// Provider returns a cryptographic key and a cleanup function.
//
// The cleanup function zeros the key bytes and should be called when the key
// is no longer needed. It is always non-nil (even on error) so callers can
// unconditionally defer it, and idempotent so it is safe to call multiple
// times.
type Provider func() (key []byte, free func(), err error)

// Decoder is a function that decodes a string into bytes.
//
// The provider takes ownership of the returned slice and zeros it, on error as
// well as on free, so a Decoder may return a partly filled buffer with its
// error, as the encoding/hex and encoding/base64 DecodeString functions do.
//
// A Decoder necessarily receives the encoded key as a string copy, which
// cannot be zeroed. The built-in hex and base64 options avoid that copy by
// decoding bytes to bytes.
type Decoder func(string) ([]byte, error)

// decodeFunc is the byte-wise form of [Decoder] that processKey runs.
type decodeFunc func([]byte) ([]byte, error)

type envConfig struct {
	getter         func(string) string
	decode         decodeFunc
	trimWhitespace bool
}

// EnvOption configures an environment variable key provider.
type EnvOption func(*envConfig)

// WithEnvGetter overrides the function used to get environment variables
// (for testing).
func WithEnvGetter(getter func(string) string) EnvOption {
	return func(c *envConfig) {
		c.getter = getter
	}
}

// WithEnvDecoder sets a decoder for the environment variable value.
//
// decoder sees an unzeroable string copy of the value; see [Decoder].
func WithEnvDecoder(decoder Decoder) EnvOption {
	return func(c *envConfig) {
		c.decode = fromDecoder(decoder)
	}
}

// WithEnvTrimWhitespace removes all whitespace characters from the value before decoding.
//
// This removes all whitespace (spaces, tabs, newlines) — not just leading/trailing.
// This is intentional for handling multiline encoded formats, but means
// a value like "my key" will silently become "mykey".
func WithEnvTrimWhitespace() EnvOption {
	return func(c *envConfig) {
		c.trimWhitespace = true
	}
}

// WithEnvHex sets hex decoding for the environment variable value.
func WithEnvHex() EnvOption {
	return func(c *envConfig) {
		c.decode = decodeHex
	}
}

// WithEnvBase64 sets base64 decoding (standard encoding) for the
// environment variable value.
func WithEnvBase64() EnvOption {
	return func(c *envConfig) {
		c.decode = decodeBase64
	}
}

// FromEnv returns a [Provider] that reads a key from an environment variable.
//
// By default the key is read as raw bytes. Use options to customise behaviour.
//
// Note: the Go runtime represents environment variable values as immutable
// strings. The Provider converts the value to a []byte copy and zeros that
// copy when free is called, but the original string backing the environment
// variable cannot be zeroed from Go without CGo.
func FromEnv(envVar string, opts ...EnvOption) Provider {
	cfg := &envConfig{
		getter: os.Getenv,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func() ([]byte, func(), error) {
		val := cfg.getter(envVar)
		if val == "" {
			return nil, func() {}, fmt.Errorf("environment variable %s is not set or empty", envVar)
		}

		return processKey([]byte(val), cfg.trimWhitespace, cfg.decode)
	}
}

type fileConfig struct {
	filesystem       fs.FS
	decode           decodeFunc
	trimWhitespace   bool
	checkPermissions bool
}

// FileOption configures a file key provider.
type FileOption func(*fileConfig)

// WithFilesystem makes [FromFile] open the file through filesystem instead of
// the OS.
//
// The path given to FromFile is passed to filesystem's Open unchanged, so it
// must be valid there: an [io/fs] path is unrooted and slash-separated, such
// as "keys/tls.key". [testing/fstest.MapFS] serves files with a mode from
// memory, for testing the permission check.
func WithFilesystem(filesystem fs.FS) FileOption {
	return func(c *fileConfig) {
		c.filesystem = filesystem
	}
}

// WithFileDecoder sets a decoder for the file contents.
//
// decoder sees an unzeroable string copy of the contents; see [Decoder].
func WithFileDecoder(decoder Decoder) FileOption {
	return func(c *fileConfig) {
		c.decode = fromDecoder(decoder)
	}
}

// WithFileTrimWhitespace removes all whitespace characters from the file contents before decoding.
//
// This removes all whitespace (spaces, tabs, newlines) — not just leading/trailing.
// This is intentional for handling multiline encoded formats, but means
// content like "my key" will silently become "mykey".
func WithFileTrimWhitespace() FileOption {
	return func(c *fileConfig) {
		c.trimWhitespace = true
	}
}

// WithoutPermissionCheck disables the 0600 permission check (use with caution).
func WithoutPermissionCheck() FileOption {
	return func(c *fileConfig) {
		c.checkPermissions = false
	}
}

// WithFileHex sets hex decoding for the file contents.
func WithFileHex() FileOption {
	return func(c *fileConfig) {
		c.decode = decodeHex
	}
}

// WithFileBase64 sets base64 decoding (standard encoding) for the file
// contents.
func WithFileBase64() FileOption {
	return func(c *fileConfig) {
		c.decode = decodeBase64
	}
}

// FromFile returns a [Provider] that reads a key from a file.
//
// By default it uses the OS filesystem and checks that file permissions are
// 0600 or stricter.
//
// The file is opened once, and the permission check and the read both use
// that handle, so a file swapped in between cannot pass the check for
// another.
func FromFile(path string, opts ...FileOption) Provider {
	cfg := &fileConfig{
		checkPermissions: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func() ([]byte, func(), error) {
		f, err := cfg.open(path)
		if err != nil {
			return nil, func() {}, fmt.Errorf("open key file: %w", err)
		}
		// A read-only file has nothing to flush, so a Close error is moot.
		defer func() { _ = f.Close() }()

		info, err := f.Stat()
		if err != nil {
			return nil, func() {}, fmt.Errorf("stat key file: %w", err)
		}
		if cfg.checkPermissions && info.Mode()&0o077 != 0 {
			return nil, func() {}, fmt.Errorf("key file %s has insecure permissions %o (must be 0600 or stricter)", path, info.Mode().Perm())
		}

		data, err := readKey(f, info.Size())
		if err != nil {
			return nil, func() {}, fmt.Errorf("read key file: %w", err)
		}

		if len(data) == 0 {
			return nil, func() {}, fmt.Errorf("key file %s is empty", path)
		}

		return processKey(data, cfg.trimWhitespace, cfg.decode)
	}
}

func (c *fileConfig) open(path string) (fs.File, error) {
	if c.filesystem != nil {
		return c.filesystem.Open(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// readKey reads r to the end into a buffer sized from size, the file's length
// as reported by Stat, so that reading a regular file never grows the buffer.
// Where it must grow, the outgrown buffer is zeroed before it is dropped, so no
// copy of the key escapes zeroing; io.ReadAll gives no such guarantee.
func readKey(r io.Reader, size int64) ([]byte, error) {
	// One spare byte lets the read that returns io.EOF land without growing.
	n := 512
	if size > 0 && size < 1<<20 {
		n = int(size) + 1
	}
	buf := make([]byte, 0, n)
	for {
		if len(buf) == cap(buf) {
			grown := make([]byte, len(buf), 2*cap(buf))
			copy(grown, buf)
			clear(buf)
			buf = grown
		}
		m, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+m]
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			clear(buf)
			return nil, err
		}
	}
}

// processKey handles common key processing: trimming whitespace, decoding,
// and creating a cleanup function.
//
// It takes ownership of data — callers must not use data after this call,
// and must call the returned free function to zero all key material.
func processKey(data []byte, trimWhitespace bool, decode decodeFunc) ([]byte, func(), error) {
	if trimWhitespace {
		original := data
		defer clear(original)
		data = bytes.Map(func(r rune) rune {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, original)
		if len(data) == 0 {
			return nil, func() {}, fmt.Errorf("key is empty after trimming whitespace")
		}
	}

	if decode != nil {
		defer clear(data)
		decoded, err := decode(data)
		if err != nil {
			clear(decoded)
			return nil, func() {}, fmt.Errorf("decode key: %w", err)
		}
		if len(decoded) == 0 {
			return nil, func() {}, fmt.Errorf("decoded key is empty")
		}
		data = decoded
	}

	return data, func() { clear(data) }, nil
}

// fromDecoder adapts a [Decoder] to a decodeFunc. A nil decoder stays nil, so
// it still means "no decoding".
func fromDecoder(decoder Decoder) decodeFunc {
	if decoder == nil {
		return nil
	}
	return func(data []byte) ([]byte, error) {
		return decoder(string(data))
	}
}

// decodeHex and decodeBase64 decode into a buffer they allocate, without the
// string copies of the DecodeString functions. Like those, they return the
// bytes written so far alongside any error, for processKey to zero.
func decodeHex(data []byte) ([]byte, error) {
	dst := make([]byte, hex.DecodedLen(len(data)))
	n, err := hex.Decode(dst, data)
	return dst[:n], err
}

func decodeBase64(data []byte) ([]byte, error) {
	dst := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(dst, data)
	return dst[:n], err
}
