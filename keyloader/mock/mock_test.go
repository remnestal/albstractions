package mock_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/remnestal/albstractions/keyloader/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticProvider(t *testing.T) {
	t.Parallel()

	t.Run("returns provided data", func(t *testing.T) {
		t.Parallel()

		provider := mock.StaticProvider([]byte("test-key"))
		key, free, err := provider()

		require.NoError(t, err)
		assert.Equal(t, []byte("test-key"), key)
		assert.NotNil(t, free)

		free()
	})

	t.Run("returns nil data when given nil", func(t *testing.T) {
		t.Parallel()

		provider := mock.StaticProvider(nil)
		key, free, err := provider()

		require.NoError(t, err)
		assert.Nil(t, key)
		assert.NotNil(t, free)

		free()
	})
}

func TestErrorProvider(t *testing.T) {
	t.Parallel()

	t.Run("returns the given error", func(t *testing.T) {
		t.Parallel()

		provider := mock.ErrorProvider(errors.New("provider failed"))
		key, free, err := provider()

		assert.Error(t, err)
		assert.Equal(t, "provider failed", err.Error())
		assert.Nil(t, key)
		assert.NotNil(t, free)

		free()
	})
}

func TestFileSystem(t *testing.T) {
	t.Parallel()

	mfs := &mock.FileSystem{
		Files: map[string]mock.File{
			"/secret.key": {Data: []byte("key-data"), Mode: 0600},
		},
	}

	t.Run("ReadFile returns data for existing file", func(t *testing.T) {
		t.Parallel()

		data, err := mfs.ReadFile("/secret.key")

		require.NoError(t, err)
		assert.Equal(t, []byte("key-data"), data)
	})

	t.Run("ReadFile returns error for missing file", func(t *testing.T) {
		t.Parallel()

		_, err := mfs.ReadFile("/missing.key")

		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("Stat returns file info for existing file", func(t *testing.T) {
		t.Parallel()

		info, err := mfs.Stat("/secret.key")

		require.NoError(t, err)
		assert.Equal(t, fs.FileMode(0600), info.Mode())
		assert.Equal(t, int64(8), info.Size())
	})

	t.Run("Stat returns error for missing file", func(t *testing.T) {
		t.Parallel()

		_, err := mfs.Stat("/missing.key")

		assert.ErrorIs(t, err, fs.ErrNotExist)
	})
}
