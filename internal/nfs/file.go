package nfs

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

// File implements billy.File backed by remote file operations.
// Content is buffered in memory; writes are flushed on Close.
type File struct {
	fs       *FileSystem
	path     string
	flag     int
	buf      *bytes.Buffer
	pos      int64
	dirty    bool
	closed   bool
	mu       sync.Mutex
	loaded   bool
}

func (f *File) Name() string {
	return f.path
}

func (f *File) load() error {
	if f.loaded {
		return nil
	}
	data, err := f.fs.remote.ReadFile(f.fs.abs(f.path))
	if err != nil {
		if remotefs.IsNotFound(err) {
			f.buf = bytes.NewBuffer(nil)
			f.loaded = true
			return nil
		}
		return err
	}
	f.buf = bytes.NewBuffer(data)
	f.loaded = true
	return nil
}

func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fmt.Errorf("file closed")
	}
	if err := f.load(); err != nil {
		return 0, err
	}
	data := f.buf.Bytes()
	if f.pos >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[f.pos:])
	f.pos += int64(n)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fmt.Errorf("file closed")
	}
	if err := f.load(); err != nil {
		return 0, err
	}
	data := f.buf.Bytes()
	if off >= int64(len(data)) {
		return 0, io.EOF
	}
	n := copy(p, data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fmt.Errorf("file closed")
	}
	if err := f.load(); err != nil {
		return 0, err
	}
	data := f.buf.Bytes()

	// Extend if necessary
	end := f.pos + int64(len(p))
	if end > int64(len(data)) {
		extended := make([]byte, end)
		copy(extended, data)
		data = extended
	}
	copy(data[f.pos:], p)
	f.buf = bytes.NewBuffer(data)
	f.pos = end
	f.dirty = true
	return len(p), nil
}

func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return 0, err
	}
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = f.pos + offset
	case io.SeekEnd:
		newPos = int64(f.buf.Len()) + offset
	}
	if newPos < 0 {
		return f.pos, fmt.Errorf("negative seek position")
	}
	f.pos = newPos
	return f.pos, nil
}

func (f *File) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return err
	}
	data := f.buf.Bytes()
	if size < int64(len(data)) {
		f.buf = bytes.NewBuffer(data[:size])
	} else if size > int64(len(data)) {
		extended := make([]byte, size)
		copy(extended, data)
		f.buf = bytes.NewBuffer(extended)
	}
	f.dirty = true
	return nil
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.dirty {
		return f.fs.remote.WriteFile(f.fs.abs(f.path), f.buf.Bytes(), true, true)
	}
	return nil
}

func (f *File) Lock() error   { return nil }
func (f *File) Unlock() error { return nil }
