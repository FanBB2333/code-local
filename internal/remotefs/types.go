package remotefs

import (
	"fmt"
	"strings"
	"time"
)

const ChannelName = "remoteFilesystem"

type FileType int

const (
	FileTypeUnknown   FileType = 0
	FileTypeFile      FileType = 1
	FileTypeDirectory FileType = 2
	FileTypeSymLink   FileType = 64
)

type FilePermission int

const (
	PermReadonly   FilePermission = 1
	PermLocked     FilePermission = 2
	PermExecutable FilePermission = 4
)

type FileStat struct {
	Type        FileType
	Mtime       int64 // milliseconds since epoch
	Ctime       int64
	Size        int64
	Permissions FilePermission
}

func (s *FileStat) ModTime() time.Time {
	return time.UnixMilli(s.Mtime)
}

type FileChangeType int

const (
	FileChangeUpdated FileChangeType = 0
	FileChangeAdded   FileChangeType = 1
	FileChangeDeleted FileChangeType = 2
)

type FileChange struct {
	Type     FileChangeType
	Resource string // file path
}

type DirEntry struct {
	Name string
	Type FileType
}

// UriComponents matches VS Code's UriComponents for wire format.
type UriComponents struct {
	Scheme    string `json:"scheme"`
	Authority string `json:"authority"`
	Path      string `json:"path"`
	Query     string `json:"query,omitempty"`
	Fragment  string `json:"fragment,omitempty"`
}

func FileURI(path string) UriComponents {
	return UriComponents{
		Scheme: "file",
		Path:   path,
	}
}

type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func IsNotFound(err error) bool {
	if re, ok := err.(*RemoteError); ok {
		code := strings.ToLower(re.Code)
		msg := strings.ToLower(re.Message)
		return strings.Contains(code, "notfound") ||
			strings.Contains(code, "not found") ||
			strings.Contains(msg, "notfound") ||
			strings.Contains(msg, "not found")
	}
	return false
}
