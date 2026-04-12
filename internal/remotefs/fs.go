package remotefs

// FS is the subset of remote filesystem operations required by local mount backends.
type FS interface {
	Stat(path string) (*FileStat, error)
	ReadDir(path string) ([]DirEntry, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, create, overwrite bool) error
	Mkdir(path string) error
	Delete(path string, recursive bool) error
	Rename(oldPath, newPath string, overwrite bool) error
}

var _ FS = (*Client)(nil)
