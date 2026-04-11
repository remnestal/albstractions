// Package mock provides test doubles for keyloader types.
package mock

import (
	"io/fs"
	"time"

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

// File represents a file in a FileSystem.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// FileSystem implements [keyloader.FileSystem] backed by an in-memory map.
//
// Use it with [keyloader.WithFilesystem] to test code that loads keys from
// files without touching the real filesystem.
type FileSystem struct {
	Files map[string]File
}

// ReadFile returns the data for the named file, or a path error if not found.
func (m *FileSystem) ReadFile(name string) ([]byte, error) {
	f, ok := m.Files[name]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return f.Data, nil
}

// Stat returns file info for the named file, or a path error if not found.
func (m *FileSystem) Stat(name string) (fs.FileInfo, error) {
	f, ok := m.Files[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return &fileInfo{name: name, size: int64(len(f.Data)), mode: f.Mode}, nil
}

type fileInfo struct {
	name string
	size int64
	mode fs.FileMode
}

func (m *fileInfo) Name() string       { return m.name }
func (m *fileInfo) Size() int64        { return m.size }
func (m *fileInfo) Mode() fs.FileMode  { return m.mode }
func (m *fileInfo) ModTime() time.Time { return time.Time{} }
func (m *fileInfo) IsDir() bool        { return false }
func (m *fileInfo) Sys() any           { return nil }
