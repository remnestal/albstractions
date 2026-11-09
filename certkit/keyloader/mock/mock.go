// Package mock provides test doubles for keyloader types.
package mock

import (
	"github.com/remnestal/albstractions/certkit/keyloader"
)

// StaticProvider returns a [keyloader.Provider] that always yields the given
// data.
//
// The free function is a no-op.
func StaticProvider(data []byte) keyloader.Provider {
	return func() ([]byte, func(), error) {
		return data, func() {}, nil
	}
}

// ErrorProvider returns a [keyloader.Provider] that always fails with the
// given error.
//
// The free function is a no-op (non-nil), satisfying the Provider contract.
func ErrorProvider(err error) keyloader.Provider {
	return func() ([]byte, func(), error) {
		return nil, func() {}, err
	}
}
