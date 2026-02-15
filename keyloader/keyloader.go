// Package keyloader provides secure methods for loading cryptographic key
// material from environment variables or files.
//
// The central type is [Provider]: a function that returns the raw key bytes
// and a free function that zeros them when called. Callers should always
// defer the free function to limit the lifetime of sensitive material in
// memory. Providers are constructed with [FromEnv] or [FromFile] and support
// optional hex/base64 decoding and whitespace trimming.
//
// The keyloader/mock sub-package provides [mock.FileSystem] and helpers for
// use in tests of code that accepts a Provider.
package keyloader

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
)

// Provider is a function that returns a cryptographic key and a cleanup function.
// The cleanup function zeros the key bytes and should be called when the key
// is no longer needed. It is always non-nil (even on error) so callers can
// unconditionally defer it, and idempotent so it is safe to call multiple times.
type Provider func() (key []byte, free func(), err error)

// Decoder is a function that decodes a string into bytes.
type Decoder func(string) ([]byte, error)

// ---------------------------------------------------------------------------
// Environment variable configuration
// ---------------------------------------------------------------------------

type envConfig struct {
	getter         func(string) string
	decoder        Decoder
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
func WithEnvDecoder(decoder Decoder) EnvOption {
	return func(c *envConfig) {
		c.decoder = decoder
	}
}

// WithEnvTrimWhitespace removes all whitespace characters (spaces, tabs,
// newlines) from the value before decoding — not just leading/trailing.
// This is intentional for handling multiline encoded formats, but means
// a value like "my key" will silently become "mykey".
func WithEnvTrimWhitespace() EnvOption {
	return func(c *envConfig) {
		c.trimWhitespace = true
	}
}

// WithEnvHex sets hex decoding for the environment variable value.
func WithEnvHex() EnvOption {
	return WithEnvDecoder(hex.DecodeString)
}

// WithEnvBase64 sets base64 decoding (standard encoding) for the
// environment variable value.
func WithEnvBase64() EnvOption {
	return WithEnvDecoder(base64.StdEncoding.DecodeString)
}

// FromEnv returns a Provider that reads a key from an environment variable.
// By default, the key is read as raw bytes. Use options to customize behavior.
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

		return processKey([]byte(val), cfg.trimWhitespace, cfg.decoder)
	}
}

// ---------------------------------------------------------------------------
// File configuration
// ---------------------------------------------------------------------------

// FileSystem abstracts file system operations for testability.
type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
}

// osfs is a FileSystem implementation using the real OS filesystem.
type osfs struct{}

// ReadFile reads a file from the OS filesystem.
func (osfs) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

// Stat returns file info from the OS filesystem.
func (osfs) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

type fileConfig struct {
	filesystem       FileSystem
	decoder          Decoder
	trimWhitespace   bool
	checkPermissions bool
}

// FileOption configures a file key provider.
type FileOption func(*fileConfig)

// WithFilesystem overrides the filesystem implementation (for testing).
func WithFilesystem(filesystem FileSystem) FileOption {
	return func(c *fileConfig) {
		c.filesystem = filesystem
	}
}

// WithFileDecoder sets a decoder for the file contents.
func WithFileDecoder(decoder Decoder) FileOption {
	return func(c *fileConfig) {
		c.decoder = decoder
	}
}

// WithFileTrimWhitespace removes all whitespace characters (spaces, tabs,
// newlines) from the file contents before decoding — not just leading/trailing.
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
	return WithFileDecoder(hex.DecodeString)
}

// WithFileBase64 sets base64 decoding (standard encoding) for the file
// contents.
func WithFileBase64() FileOption {
	return WithFileDecoder(base64.StdEncoding.DecodeString)
}

// FromFile returns a Provider that reads a key from a file.
// By default it uses the OS filesystem and checks that file permissions
// are 0600 or stricter.
//
// Note: the permission check is subject to a TOCTOU race — the file's
// permissions could change between the Stat and ReadFile calls. There is
// no atomic "read-if-permissions-match" operation in Go's standard library.
func FromFile(path string, opts ...FileOption) Provider {
	cfg := &fileConfig{
		filesystem:       osfs{},
		checkPermissions: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func() ([]byte, func(), error) {
		if cfg.checkPermissions {
			info, err := cfg.filesystem.Stat(path)
			if err != nil {
				return nil, func() {}, fmt.Errorf("stat key file: %w", err)
			}
			mode := info.Mode()
			if mode&0o077 != 0 {
				return nil, func() {}, fmt.Errorf("key file %s has insecure permissions %o (must be 0600 or stricter)", path, mode.Perm())
			}
		}

		data, err := cfg.filesystem.ReadFile(path)
		if err != nil {
			return nil, func() {}, fmt.Errorf("read key file: %w", err)
		}

		if len(data) == 0 {
			return nil, func() {}, fmt.Errorf("key file %s is empty", path)
		}

		return processKey(data, cfg.trimWhitespace, cfg.decoder)
	}
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

// processKey handles common key processing: trimming whitespace, decoding,
// and creating a cleanup function. It takes ownership of data — callers must
// not use data after this call, and must call the returned free function to
// zero all key material.
func processKey(data []byte, trimWhitespace bool, decoder Decoder) ([]byte, func(), error) {
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

	if decoder != nil {
		defer clear(data)
		decoded, err := decoder(string(data))
		if err != nil {
			return nil, func() {}, fmt.Errorf("decode key: %w", err)
		}
		if len(decoded) == 0 {
			return nil, func() {}, fmt.Errorf("decoded key is empty")
		}
		data = decoded
	}

	return data, func() { clear(data) }, nil
}
