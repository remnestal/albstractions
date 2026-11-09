package keyloader_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/remnestal/albstractions/certkit/keyloader"
	"github.com/remnestal/albstractions/certkit/keyloader/mock"
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
			// A single write or execute bit, with no read bit to fall back on.
			{"group writable only", 0620},
			{"group executable only", 0610},
			{"world writable only", 0602},
			{"world executable only", 0601},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				mfs := fstest.MapFS{
					"key.txt": &fstest.MapFile{Data: []byte("secret"), Mode: tt.mode},
				}

				provider := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))
				key, free, err := provider()

				assert.ErrorContains(t, err, "insecure permissions")
				assert.Nil(t, key)
				assert.NotNil(t, free)
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

}

func TestDecode(t *testing.T) {
	t.Parallel()

	t.Run("decodes the key from the source provider", func(t *testing.T) {
		t.Parallel()

		upper := func(encoded []byte) ([]byte, error) {
			return bytes.ToUpper(encoded), nil
		}

		key, free, err := keyloader.Decode(mock.StaticProvider([]byte("key")), upper)()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, []byte("KEY"), key)
	})

	t.Run("keeps a key the decoder sliced from its input", func(t *testing.T) {
		t.Parallel()

		// FromFile zeroes its buffer on free, which Decode calls before
		// returning, so the key must not share memory with it.
		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("  key\n"), Mode: 0600},
		}
		trim := func(encoded []byte) ([]byte, error) {
			return bytes.TrimSpace(encoded), nil
		}

		key, free, err := keyloader.Decode(keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs)), trim)()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, []byte("key"), key)
	})

	t.Run("leaves the source provider's bytes untouched", func(t *testing.T) {
		t.Parallel()

		source := []byte("key")
		clobber := func(encoded []byte) ([]byte, error) {
			decoded := bytes.Clone(encoded)
			clear(encoded)
			return decoded, nil
		}
		provider := keyloader.Decode(mock.StaticProvider(source), clobber)

		for range 2 {
			key, free, err := provider()
			require.NoError(t, err)
			assert.Equal(t, []byte("key"), key)
			free()
		}
		assert.Equal(t, []byte("key"), source)
	})

	t.Run("frees the source's key once it is decoded", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte(hex.EncodeToString([]byte("my-secret-key"))), Mode: 0600},
		}
		var read []byte
		source := func() ([]byte, func(), error) {
			key, free, err := keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs))()
			read = key
			return key, free, err
		}

		key, free, err := keyloader.Hex(source)()
		defer free()

		// The decoded key is a separate buffer, so the bytes read from the
		// file must be zeroed already, before free is called.
		require.NoError(t, err)
		assert.Equal(t, []byte("my-secret-key"), key)
		assert.Equal(t, make([]byte, len(read)), read)
	})

	t.Run("zeroes its copy of the encoded key and the decoder's result", func(t *testing.T) {
		t.Parallel()

		var input, output []byte
		spy := func(encoded []byte) ([]byte, error) {
			input = encoded
			output = bytes.ToUpper(encoded)
			return output, nil
		}

		key, free, err := keyloader.Decode(mock.StaticProvider([]byte("key")), spy)()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, []byte("KEY"), key)
		assert.Equal(t, make([]byte, 3), input)
		assert.Equal(t, make([]byte, 3), output)
	})

	t.Run("zeros a partly decoded key when the decoder fails", func(t *testing.T) {
		t.Parallel()

		// "sec" in hex, then an invalid digit: hex.Decode writes the three
		// bytes it decoded before reporting the error.
		var partial []byte
		spy := func(encoded []byte) ([]byte, error) {
			partial = make([]byte, hex.DecodedLen(len(encoded)))
			n, err := hex.Decode(partial, encoded)
			return partial[:n], err
		}

		key, free, err := keyloader.Decode(mock.StaticProvider([]byte("736563zz")), spy)()
		defer free()

		assert.ErrorContains(t, err, "decode key")
		assert.Nil(t, key)
		assert.Equal(t, make([]byte, 4), partial)
	})

	t.Run("free function zeros the key bytes", func(t *testing.T) {
		t.Parallel()

		key, free, err := keyloader.Hex(mock.StaticProvider([]byte("736563")))()
		require.NoError(t, err)

		free()

		assert.Equal(t, make([]byte, 3), key)
	})

	t.Run("returns the source provider's error", func(t *testing.T) {
		t.Parallel()

		errLoad := errors.New("load failed")

		key, free, err := keyloader.Hex(mock.ErrorProvider(errLoad))()
		defer free()

		assert.ErrorIs(t, err, errLoad)
		assert.Nil(t, key)
	})

	t.Run("returns error when the decoded key is empty", func(t *testing.T) {
		t.Parallel()

		empty := func([]byte) ([]byte, error) { return nil, nil }

		key, free, err := keyloader.Decode(mock.StaticProvider([]byte("key")), empty)()
		defer free()

		assert.ErrorContains(t, err, "decoded key is empty")
		assert.Nil(t, key)
	})
}

func TestHex(t *testing.T) {
	t.Parallel()

	t.Run("decodes a hex-encoded key", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")

		key, free, err := keyloader.Hex(mock.StaticProvider([]byte(hex.EncodeToString(secretKey))))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)
	})

	t.Run("skips whitespace", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")
		encodedKey := hex.EncodeToString(secretKey)
		getter := mockEnvGetter(map[string]string{
			"TEST_KEY": "  \n\t" + encodedKey[:8] + " " + encodedKey[8:] + "\r\n  ",
		})

		key, free, err := keyloader.Hex(keyloader.FromEnv("TEST_KEY", keyloader.WithEnvGetter(getter)))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)
	})

	t.Run("returns error when the value holds only whitespace", func(t *testing.T) {
		t.Parallel()

		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("  \n\t\r\n  "), Mode: 0600},
		}

		key, free, err := keyloader.Hex(keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs)))()
		defer free()

		assert.ErrorContains(t, err, "decoded key is empty")
		assert.Nil(t, key)
	})

	t.Run("returns error for invalid hex", func(t *testing.T) {
		t.Parallel()

		key, free, err := keyloader.Hex(mock.StaticProvider([]byte("not-valid-hex!")))()
		defer free()

		assert.ErrorContains(t, err, "decode key")
		assert.Nil(t, key)
	})
}

func TestBase64(t *testing.T) {
	t.Parallel()

	t.Run("decodes a base64-encoded key", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key-32-bytes-long!!")

		key, free, err := keyloader.Base64(mock.StaticProvider([]byte(base64.StdEncoding.EncodeToString(secretKey))))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)
	})

	t.Run("skips whitespace", func(t *testing.T) {
		t.Parallel()

		secretKey := []byte("my-secret-key")
		encodedKey := base64.StdEncoding.EncodeToString(secretKey)
		mfs := fstest.MapFS{
			"key.txt": &fstest.MapFile{Data: []byte("\n  " + encodedKey[:8] + " \t" + encodedKey[8:] + "\n\t\r\n"), Mode: 0600},
		}

		key, free, err := keyloader.Base64(keyloader.FromFile("key.txt", keyloader.WithFilesystem(mfs)))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, secretKey, key)
	})

	t.Run("returns error for invalid base64", func(t *testing.T) {
		t.Parallel()

		key, free, err := keyloader.Base64(mock.StaticProvider([]byte("not-valid-base64!@#$")))()
		defer free()

		assert.ErrorContains(t, err, "decode key")
		assert.Nil(t, key)
	})
}
