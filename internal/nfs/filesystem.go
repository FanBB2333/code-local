package nfs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

// FileSystem implements billy.Filesystem backed by a remote code-server.
type FileSystem struct {
	remote   *remotefs.Client
	root     string // remote root path (e.g., "/home/user/project")
	chrootAt string // relative chroot prefix within root
}

var _ billy.Filesystem = (*FileSystem)(nil)

func NewFileSystem(remote *remotefs.Client, root string) *FileSystem {
	return &FileSystem{
		remote: remote,
		root:   strings.TrimRight(root, "/"),
	}
}

// abs converts a billy relative path to an absolute remote path.
func (fs *FileSystem) abs(path string) string {
	path = filepath.Clean(path)
	if fs.chrootAt != "" {
		path = filepath.Join(fs.chrootAt, path)
	}
	if strings.HasPrefix(path, "/") {
		return filepath.Join(fs.root, path)
	}
	return filepath.Join(fs.root, "/", path)
}

// --- Basic ---

func (fs *FileSystem) Create(filename string) (billy.File, error) {
	f := &File{
		fs:     fs,
		path:   filename,
		flag:   os.O_RDWR | os.O_CREATE | os.O_TRUNC,
		dirty:  true,
		loaded: true,
	}
	f.buf = new(bytes.Buffer)
	return f, nil
}

func (fs *FileSystem) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

func (fs *FileSystem) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	f := &File{
		fs:   fs,
		path: filename,
		flag: flag,
	}

	if flag&os.O_CREATE != 0 {
		if flag&os.O_TRUNC != 0 {
			f.buf = new(bytes.Buffer)
			f.loaded = true
			f.dirty = true
		}
	}

	return f, nil
}

func (fs *FileSystem) Stat(filename string) (os.FileInfo, error) {
	st, err := fs.remote.Stat(fs.abs(filename))
	if err != nil {
		if remotefs.IsNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	return &fileInfo{name: filepath.Base(filename), stat: st}, nil
}

func (fs *FileSystem) Rename(oldpath, newpath string) error {
	return fs.remote.Rename(fs.abs(oldpath), fs.abs(newpath), true)
}

func (fs *FileSystem) Remove(filename string) error {
	return fs.remote.Delete(fs.abs(filename), false)
}

func (fs *FileSystem) Join(elem ...string) string {
	return filepath.Join(elem...)
}

// --- TempFile ---

func (fs *FileSystem) TempFile(dir, prefix string) (billy.File, error) {
	name := fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	path := filepath.Join(dir, name)
	return fs.Create(path)
}

// --- Dir ---

func (fs *FileSystem) ReadDir(path string) ([]os.FileInfo, error) {
	entries, err := fs.remote.ReadDir(fs.abs(path))
	if err != nil {
		if remotefs.IsNotFound(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}

	infos := make([]os.FileInfo, len(entries))
	for i, e := range entries {
		infos[i] = &fileInfo{
			name: e.Name,
			stat: &remotefs.FileStat{
				Type: e.Type,
			},
		}
	}
	return infos, nil
}

func (fs *FileSystem) MkdirAll(filename string, perm os.FileMode) error {
	return fs.remote.Mkdir(fs.abs(filename))
}

// --- Symlink ---

func (fs *FileSystem) Lstat(filename string) (os.FileInfo, error) {
	return fs.Stat(filename)
}

func (fs *FileSystem) Symlink(target, link string) error {
	return fmt.Errorf("symlink not supported")
}

func (fs *FileSystem) Readlink(link string) (string, error) {
	return "", fmt.Errorf("readlink not supported")
}

// --- Chroot ---

func (fs *FileSystem) Chroot(path string) (billy.Filesystem, error) {
	return &FileSystem{
		remote:   fs.remote,
		root:     fs.root,
		chrootAt: filepath.Join(fs.chrootAt, path),
	}, nil
}

func (fs *FileSystem) Root() string {
	return "/"
}

// fileInfo implements os.FileInfo from remote stat.
type fileInfo struct {
	name string
	stat *remotefs.FileStat
}

func (fi *fileInfo) Name() string      { return fi.name }
func (fi *fileInfo) Size() int64       { return fi.stat.Size }
func (fi *fileInfo) ModTime() time.Time { return fi.stat.ModTime() }
func (fi *fileInfo) IsDir() bool       { return fi.stat.Type == remotefs.FileTypeDirectory }
func (fi *fileInfo) Sys() interface{}   { return nil }

func (fi *fileInfo) Mode() os.FileMode {
	mode := os.FileMode(0644)
	if fi.stat.Type == remotefs.FileTypeDirectory {
		mode = os.FileMode(0755) | os.ModeDir
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
