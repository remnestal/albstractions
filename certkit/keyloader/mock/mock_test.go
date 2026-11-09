package mock_test

import (
	"errors"
	"testing"

	"github.com/remnestal/albstractions/certkit/keyloader/mock"
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
