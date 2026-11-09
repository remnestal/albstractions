package keyloader_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/remnestal/albstractions/certkit/keyloader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fsFunc adapts a function to [fs.FS].
type fsFunc func(name string) (fs.File, error)

func (f fsFunc) Open(name string) (fs.File, error) { return f(name) }

// sizelessFile reports a size of zero, as a special file does, so that reading
// it cannot rely on the size.
type sizelessFile struct{ fs.File }

func (f sizelessFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return sizelessInfo{info}, nil
}

type sizelessInfo struct{ fs.FileInfo }

func (sizelessInfo) Size() int64 { return 0 }

func mockEnvGetter(envVars map[string]string) func(string) string {
	return func(key string) string {
		return envVars[key]
	}
}

func TestFromEnv(t *testing.T) {
	t.Parallel()

	t.Run("reads raw bytes from environment variable", func(t *testing.T) {
		t.Parallel()

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "my-secret-key",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("my-secret-key"), key)
		assert.NotNil(t, free)

		free()
	})

	t.Run("returns error when environment variable is empty", func(t *testing.T) {
		t.Parallel()

		getter := func(key string) string {
			return ""
		}

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter))
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "not set or empty")
	})

	t.Run("decodes hex-encoded key", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")
		encodedKey := hex.EncodeToString(secretKey)

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": encodedKey,
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvHex())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)

		free()
	})

	t.Run("decodes base64-encoded key", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key-32-bytes-long!!")
		encodedKey := base64.StdEncoding.EncodeToString(secretKey)

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": encodedKey,
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvBase64())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)

		free()
	})

	t.Run("trims whitespace when requested", func(t *testing.T) {
		t.Parallel()

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "  \n\tmy-key\r\n  ",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvTrimWhitespace())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("my-key"), key)

		free()
	})

	t.Run("returns error for invalid hex encoding", func(t *testing.T) {
		t.Parallel()

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "not-valid-hex!",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvHex())
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "decode key")
	})

	t.Run("returns error for invalid base64 encoding", func(t *testing.T) {
		t.Parallel()

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "not-valid-base64!@#$",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvBase64())
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "decode key")
	})

	t.Run("free function zeros the key bytes", func(t *testing.T) {
		t.Parallel()

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "secret",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("secret"), key)

		free()

		for i, b := range key {
			assert.Equal(t, byte(0), b, "byte at index %d should be zeroed", i)
		}
	})

	t.Run("zeros a partly decoded key when the decoder fails", func(t *testing.T) {
		t.Parallel()

		// "sec" in hex, then an invalid digit: hex.DecodeString returns the
		// three bytes it decoded along with its error.
		var partial []byte
		spy := func(s string) ([]byte, error) {
			var err error
			partial, err = hex.DecodeString(s)
			return partial, err
		}

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "736563zz",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvDecoder(spy))
		key, free, err := provider()
		defer free()

		assert.ErrorContains(t, err, "decode key")
		assert.Nil(t, key)
		assert.Equal(t, make([]byte, 3), partial)
	})

	t.Run("custom decoder option works", func(t *testing.T) {
		t.Parallel()

		customDecoder := func(s string) ([]byte, error) {
			return []byte(s + "-decoded"), nil
		}

		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "test",
		})

		provider := keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter), keyloader.WithEnvDecoder(customDecoder))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("test-decoded"), key)

		free()
	})
}

func TestFromFile(t *testing.T) {
	t.Parallel()

	t.Run("reads raw bytes from file", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("my-secret-key"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("my-secret-key"), key)
		assert.NotNil(t, free)

		free()
	})

	t.Run("checks and reads the file it opened", func(t *testing.T) {
		t.Parallel()

		// The first Open serves a private key, any later one a world-readable
		// file in its place, as a swap between the check and the read would.
		private := fstest.MapFS{"key.txt": &fstest.MapFile{Data: []byte("private"), Mode: 0600}}
		swapped := fstest.MapFS{"key.txt": &fstest.MapFile{Data: []byte("swapped"), Mode: 0644}}
		opens := 0
		fsys := fsFunc(func(name string) (fs.File, error) {
			opens++
			if opens == 1 {
				return private.Open(name)
			}
			return swapped.Open(name)
		})

		key, free, err := keyloader.FromFile("key.txt", keyloader.WithFilesystem(fsys))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, []byte("private"), key)
		assert.Equal(t, 1, opens)
	})

	t.Run("reads a file longer than its reported size", func(t *testing.T) {
		t.Parallel()

		data := bytes.Repeat([]byte("k"), 2000)
		mfs := fstest.MapFS{"key.txt": &fstest.MapFile{Data: data, Mode: 0600}}
		fsys := fsFunc(func(name string) (fs.File, error) {
			f, err := mfs.Open(name)
			if err != nil {
				return nil, err
			}
			return sizelessFile{f}, nil
		})

		key, free, err := keyloader.FromFile("key.txt", keyloader.WithFilesystem(fsys))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, data, key)
	})

	t.Run("returns error when file does not exist", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{}

		provider := keyloader.FromFile("nonexistent.txt", keyloader.WithFilesystem(mfs))
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("returns error for insecure file permissions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			mode fs.FileMode
		}{
			{"world readable", 0644},
			{"group readable", 0640},
			{"world writable", 0606},
			{"group writable", 0660},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				mfs := fstest.MapFS{
					"key.txt": &fstest.MapFile{Data: []byte("secret"), Mode: tt.mode},
				}

				provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
				key, free, err := provider()

				assert.Error(t, err)
				assert.Nil(t, key)
				assert.NotNil(t, free)
				assert.Contains(t, err.Error(), "insecure permissions")
			})
		}
	})

	t.Run("accepts secure file permissions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			mode fs.FileMode
		}{
			{"owner read only", 0400},
			{"owner read/write", 0600},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				mfs := fstest.MapFS{
					"key.txt": &fstest.MapFile{Data: []byte("secret"), Mode: tt.mode},
				}

				provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
				key, free, err := provider()

				require.NoError(t, err)
				assert.Equal(t, []byte("secret"), key)

				free()
			})
		}
	})

	t.Run("skips permission check when disabled", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("secret"), Mode: 0644},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithoutPermissionCheck())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("secret"), key)

		free()
	})

	t.Run("returns error when file is empty", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte{}, Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "is empty")
	})

	t.Run("decodes hex-encoded key from file", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")
		encodedKey := hex.EncodeToString(secretKey)

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte(encodedKey), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileHex())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)

		free()
	})

	t.Run("decodes base64-encoded key from file", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key-32-bytes-long!!")
		encodedKey := base64.StdEncoding.EncodeToString(secretKey)

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte(encodedKey), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileBase64())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)

		free()
	})

	t.Run("trims whitespace when requested", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")
		encodedKey := base64.StdEncoding.EncodeToString(secretKey)
		withWhitespace := "\n  " + encodedKey + "\n\t\r\n"

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte(withWhitespace), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt",
			keyloader.WithFilesystem(mfs),
			keyloader.WithFileBase64(),
			keyloader.WithFileTrimWhitespace())
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)

		free()
	})

	t.Run("returns error when trimmed file is empty", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("  \n\t\r\n  "), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileTrimWhitespace())
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "empty after trimming whitespace")
	})

	t.Run("returns error for invalid hex encoding in file", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("not-valid-hex!"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileHex())
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "decode key")
	})

	t.Run("returns error for invalid base64 encoding in file", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("not-valid-base64!@#$"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileBase64())
		key, free, err := provider()

		assert.Error(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)
		assert.Contains(t, err.Error(), "decode key")
	})

	t.Run("free function zeros the key bytes", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("secret"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("secret"), key)

		free()

		for i, b := range key {
			assert.Equal(t, byte(0), b, "byte at index %d should be zeroed", i)
		}
	})

	t.Run("zeros a partly decoded key when the decoder fails", func(t *testing.T) {
		t.Parallel()

		// "sec" in hex, then an invalid digit: hex.DecodeString returns the
		// three bytes it decoded along with its error.
		var partial []byte
		spy := func(s string) ([]byte, error) {
			var err error
			partial, err = hex.DecodeString(s)
			return partial, err
		}

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("736563zz"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileDecoder(spy))
		key, free, err := provider()
		defer free()

		assert.ErrorContains(t, err, "decode key")
		assert.Nil(t, key)
		assert.Equal(t, make([]byte, 3), partial)
	})

	t.Run("custom decoder option works", func(t *testing.T) {
		t.Parallel()

		customDecoder := func(s string) ([]byte, error) {
			return []byte(s + "-decoded"), nil
		}

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("test"), Mode: 0600},
		}

		provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs), keyloader.WithFileDecoder(customDecoder))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("test-decoded"), key)

		free()
	})
}
