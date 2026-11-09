package keyloader_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/remnestal/albstractions/certkit/keyloader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeKey writes data to path and sets its mode explicitly, so the umask
// cannot tighten a deliberately loose mode.
func writeKey(t *testing.T, path string, data []byte, mode fs.FileMode) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, data, mode))
	require.NoError(t, os.Chmod(path, mode))
}

func TestWithFilesystem(t *testing.T) {
	t.Parallel()

	// base/outside.key exists and is 0600, so a rejection of it below is the
	// root refusing the path, not a missing file or a failed permission check.
	base := t.TempDir()
	dir := filepath.Join(base, "keys")
	require.NoError(t, os.Mkdir(dir, 0o700))
	writeKey(t, filepath.Join(base, "outside.key"), []byte("outside"), 0o600)
	writeKey(t, filepath.Join(dir, "server.key"), []byte("inside"), 0o600)
	writeKey(t, filepath.Join(dir, "loose.key"), []byte("loose"), 0o644)
	require.NoError(t, os.Symlink(filepath.Join("..", "outside.key"), filepath.Join(dir, "escape.key")))
	require.NoError(t, os.Symlink("loose.key", filepath.Join(dir, "loose-link.key")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "server.key"), filepath.Join(dir, "absolute-link.key")))

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = root.Close() })
	fsys := root.FS()

	t.Run("os.Root reads a key by its path relative to the root", func(t *testing.T) {
		t.Parallel()

		key, free, err := keyloader.FromFile("server.key", keyloader.WithFilesystem(fsys))()
		defer free()

		require.NoError(t, err)
		assert.Equal(t, []byte("inside"), key)
	})

	t.Run("os.Root rejects absolute and escaping paths", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			path string
		}{
			{"parent directory", "../outside.key"},
			{"absolute path", filepath.Join(base, "outside.key")},
			{"absolute path inside the root", filepath.Join(dir, "server.key")},
			{"symbolic link out of the root", "escape.key"},
			{"absolute symbolic link into the root", "absolute-link.key"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				key, free, err := keyloader.FromFile(tt.path, keyloader.WithFilesystem(fsys))()
				defer free()

				require.Error(t, err)
				assert.NotErrorIs(t, err, fs.ErrNotExist)
				assert.NotContains(t, err.Error(), "insecure permissions")
				assert.Nil(t, key)
			})
		}
	})

	t.Run("os.Root still enforces the permission check", func(t *testing.T) {
		t.Parallel()

		for _, path := range []string{"loose.key", "loose-link.key"} {
			t.Run(path, func(t *testing.T) {
				t.Parallel()

				key, free, err := keyloader.FromFile(path, keyloader.WithFilesystem(fsys))()
				defer free()

				require.Error(t, err)
				assert.Contains(t, err.Error(), "insecure permissions")
				assert.Nil(t, key)
			})
		}
	})
}
