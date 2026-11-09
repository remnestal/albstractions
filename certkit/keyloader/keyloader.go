// Package keyloader loads cryptographic key material from environment
// variables or files.
//
// Construct a [Provider] with [FromEnv] or [FromFile]; it returns the key
// bytes and a free function that zeros them — always defer free. Wrap it in
// [Hex], [Base64] or [Decode] when the key is stored encoded. The mock
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

// Decoder decodes an encoded key into its raw bytes.
//
// It receives a copy of the encoded key that [Decode] owns, so it may modify
// it, and may return it or a slice of it. Decode copies the result into the
// key and zeroes both, also when the Decoder fails, so a Decoder may return a
// partly filled buffer with its error, as hex.Decode and base64's Decode do.
type Decoder func(encoded []byte) ([]byte, error)

type envConfig struct {
	getter         func(string) string
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

// FromEnv returns a [Provider] that reads a key from an environment variable.
//
// The value is the key, byte for byte. Wrap the provider in [Hex], [Base64] or
// [Decode] for an encoded value.
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

		return processKey([]byte(val), cfg.trimWhitespace)
	}
}

type fileConfig struct {
	filesystem       fs.FS
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

// FromFile returns a [Provider] that reads a key from a file.
//
// The contents are the key, byte for byte. Wrap the provider in [Hex],
// [Base64] or [Decode] for an encoded file.
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

		return processKey(data, cfg.trimWhitespace)
	}
}

// Decode returns a [Provider] that loads an encoded key from p and decodes it
// with d.
//
// The encoded key is copied and p's free function called before d runs, so
// p's buffer is released as early as possible. d works on the copy, and its
// result is copied into the returned key; the copy and d's result are both
// zeroed, also when d fails.
func Decode(p Provider, d Decoder) Provider {
	return func() ([]byte, func(), error) {
		encoded, free, err := p()
		if err != nil {
			free()
			return nil, func() {}, err
		}
		buf := bytes.Clone(encoded)
		free()
		defer clear(buf)

		decoded, err := d(buf)
		defer clear(decoded[:cap(decoded)])
		if err != nil {
			return nil, func() {}, fmt.Errorf("decode key: %w", err)
		}
		if len(decoded) == 0 {
			return nil, func() {}, errors.New("decoded key is empty")
		}

		key := bytes.Clone(decoded)
		return key, func() { clear(key) }, nil
	}
}

// Hex returns a [Provider] that decodes the key from p as hexadecimal.
func Hex(p Provider) Provider {
	return Decode(p, decodeHex)
}

// Base64 returns a [Provider] that decodes the key from p as standard,
// padded base64.
func Base64(p Provider) Provider {
	return Decode(p, decodeBase64)
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

// processKey handles common key processing: trimming whitespace and creating
// a cleanup function.
//
// It takes ownership of data — callers must not use data after this call,
// and must call the returned free function to zero all key material.
func processKey(data []byte, trimWhitespace bool) ([]byte, func(), error) {
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

	return data, func() { clear(data) }, nil
}

// decodeHex and decodeBase64 decode into a buffer they allocate, returning the
// bytes written so far alongside any error, for [Decode] to zero.
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
