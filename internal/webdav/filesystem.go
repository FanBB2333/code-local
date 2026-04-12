package webdav

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	xwebdav "golang.org/x/net/webdav"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

// FileSystem implements webdav.FileSystem backed by the remote code-server filesystem.
type FileSystem struct {
	remote remotefs.FS
	root   string
}

func NewFileSystem(remote remotefs.FS, root string) *FileSystem {
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}
	return &FileSystem{
		remote: remote,
		root:   root,
	}
}

func (fs *FileSystem) abs(name string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(name))
	if cleaned == "/" {
		return fs.root
	}
	return path.Join(fs.root, cleaned)
}

func (fs *FileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	_ = ctx
	_ = perm
	return fs.remote.Mkdir(fs.abs(name))
}

func (fs *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (xwebdav.File, error) {
	_ = ctx
	_ = perm

	absName := fs.abs(name)
	st, err := fs.remote.Stat(absName)
	switch {
	case err == nil:
		fi := newFileInfo(path.Base(path.Clean(name)), st)
		return &File{
			fs:      fs,
			name:    name,
			absName: absName,
			flag:    flag,
			info:    fi,
			isDir:   st.Type == remotefs.FileTypeDirectory,
		}, nil
	case remotefs.IsNotFound(err) && flag&os.O_CREATE != 0:
		info := newFileInfo(path.Base(path.Clean(name)), &remotefs.FileStat{
			Type:  remotefs.FileTypeFile,
			Mtime: time.Now().UnixMilli(),
			Ctime: time.Now().UnixMilli(),
		})
		file := &File{
			fs:      fs,
			name:    name,
			absName: absName,
			flag:    flag,
			info:    info,
			buf:     bytes.NewBuffer(nil),
			loaded:  true,
		}
		if flag&os.O_TRUNC != 0 {
			file.dirty = true
		}
		return file, nil
	default:
		if remotefs.IsNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
}

func (fs *FileSystem) RemoveAll(ctx context.Context, name string) error {
	_ = ctx
	absName := fs.abs(name)
	st, err := fs.remote.Stat(absName)
	if err != nil {
		if remotefs.IsNotFound(err) {
			return os.ErrNotExist
		}
		return err
	}
	return fs.remote.Delete(absName, st.Type == remotefs.FileTypeDirectory)
}

func (fs *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	_ = ctx
	return fs.remote.Rename(fs.abs(oldName), fs.abs(newName), true)
}

func (fs *FileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	_ = ctx
	st, err := fs.remote.Stat(fs.abs(name))
	if err != nil {
		if remotefs.IsNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return newFileInfo(path.Base(path.Clean(name)), st), nil
}

type File struct {
	fs      *FileSystem
	name    string
	absName string
	flag    int
	info    *fileInfo

	buf       *bytes.Buffer
	pos       int64
	loaded    bool
	dirty     bool
	closed    bool
	isDir     bool
	dirLoaded bool
	dirPos    int
	dirInfos  []os.FileInfo
	mu        sync.Mutex
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.isDir || !f.dirty {
		return nil
	}
	data := f.buf.Bytes()
	if err := f.fs.remote.WriteFile(f.absName, data, true, true); err != nil {
		return err
	}
	now := time.Now()
	f.info.stat.Size = int64(len(data))
	f.info.stat.Mtime = now.UnixMilli()
	return nil
}

func (f *File) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isDir {
		return 0, io.EOF
	}
	if err := f.ensureLoadedLocked(); err != nil {
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

func (f *File) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isDir {
		return 0, fmt.Errorf("cannot seek directory")
	}
	if err := f.ensureLoadedLocked(); err != nil {
		return 0, err
	}
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = f.pos + offset
	case io.SeekEnd:
		next = int64(f.buf.Len()) + offset
	default:
		return 0, fmt.Errorf("invalid whence %d", whence)
	}
	if next < 0 {
		return 0, fmt.Errorf("negative seek position")
	}
	f.pos = next
	return next, nil
}

func (f *File) Readdir(count int) ([]os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.isDir {
		return nil, fmt.Errorf("not a directory")
	}
	if err := f.ensureDirLoadedLocked(); err != nil {
		return nil, err
	}
	if f.dirPos >= len(f.dirInfos) {
		if count > 0 {
			return nil, io.EOF
		}
		return []os.FileInfo{}, nil
	}
	if count <= 0 {
		remaining := append([]os.FileInfo(nil), f.dirInfos[f.dirPos:]...)
		f.dirPos = len(f.dirInfos)
		return remaining, nil
	}
	end := f.dirPos + count
	if end > len(f.dirInfos) {
		end = len(f.dirInfos)
	}
	out := append([]os.FileInfo(nil), f.dirInfos[f.dirPos:end]...)
	f.dirPos = end
	if f.dirPos >= len(f.dirInfos) {
		return out, io.EOF
	}
	return out, nil
}

func (f *File) Stat() (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isDir {
		return f.info, nil
	}
	if err := f.ensureLoadedLocked(); err != nil {
		return nil, err
	}
	f.info.stat.Size = int64(f.buf.Len())
	return f.info, nil
}

func (f *File) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.isDir {
		return 0, io.EOF
	}
	if err := f.ensureLoadedLocked(); err != nil {
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
	if f.isDir {
		return 0, fmt.Errorf("cannot write directory")
	}
	if err := f.ensureLoadedLocked(); err != nil {
		return 0, err
	}
	data := f.buf.Bytes()
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

func (f *File) ensureLoadedLocked() error {
	if f.loaded {
		return nil
	}
	data, err := f.fs.remote.ReadFile(f.absName)
	if err != nil {
		if remotefs.IsNotFound(err) && f.flag&os.O_CREATE != 0 {
			f.buf = bytes.NewBuffer(nil)
			f.loaded = true
			return nil
		}
		if remotefs.IsNotFound(err) {
			return os.ErrNotExist
		}
		return err
	}
	f.buf = bytes.NewBuffer(data)
	f.loaded = true
	return nil
}

func (f *File) ensureDirLoadedLocked() error {
	if f.dirLoaded {
		return nil
	}
	entries, err := f.fs.remote.ReadDir(f.absName)
	if err != nil {
		if remotefs.IsNotFound(err) {
			return os.ErrNotExist
		}
		return err
	}
	f.dirInfos = make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		f.dirInfos = append(f.dirInfos, newFileInfo(entry.Name, &remotefs.FileStat{Type: entry.Type}))
	}
	f.dirLoaded = true
	return nil
}

type fileInfo struct {
	name string
	stat *remotefs.FileStat
}

func newFileInfo(name string, stat *remotefs.FileStat) *fileInfo {
	return &fileInfo{name: name, stat: stat}
}

func (fi *fileInfo) Name() string       { return fi.name }
func (fi *fileInfo) Size() int64        { return fi.stat.Size }
func (fi *fileInfo) ModTime() time.Time { return fi.stat.ModTime() }
func (fi *fileInfo) IsDir() bool        { return fi.stat.Type == remotefs.FileTypeDirectory }
func (fi *fileInfo) Sys() interface{}   { return nil }

func (fi *fileInfo) Mode() os.FileMode {
	mode := os.FileMode(0644)
	if fi.stat.Type == remotefs.FileTypeDirectory {
		mode = os.ModeDir | 0755
	}
	if fi.stat.Type == remotefs.FileTypeSymLink {
		mode |= os.ModeSymlink
	}
	if fi.stat.Permissions&remotefs.PermReadonly != 0 {
		mode &^= 0222
	}
	if fi.stat.Permissions&remotefs.PermExecutable != 0 {
		mode |= 0111
	}
	return mode
}
