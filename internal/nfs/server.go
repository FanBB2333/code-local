package nfs

import (
	"fmt"
	"net"
	"runtime"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

// Server wraps a go-nfs server backed by a remote code-server filesystem.
type Server struct {
	listener net.Listener
	fs       *FileSystem
}

// NewServer creates an NFS server on the given port, exposing the remote path.
func NewServer(remote remotefs.FS, remotePath string, port int) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	fs := NewFileSystem(remote, remotePath)

	return &Server{
		listener: listener,
		fs:       fs,
	}, nil
}

// Addr returns the listener address.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Serve starts the NFS server (blocking).
func (s *Server) Serve() error {
	handler := nfshelper.NewNullAuthHandler(s.fs)
	cacheHandler := nfshelper.NewCachingHandler(handler, 1024)
	return nfs.Serve(s.listener, cacheHandler)
}

// Close stops the NFS server.
func (s *Server) Close() error {
	return s.listener.Close()
}

// MountCmd returns the OS-specific mount command for the user to run.
func (s *Server) MountCmd(mountPoint string) string {
	_, port, _ := net.SplitHostPort(s.Addr())
	lockOpt := "nolock" // Linux
	if runtime.GOOS == "darwin" {
		lockOpt = "nolocks" // macOS
	}
	return fmt.Sprintf("sudo mount -t nfs -o port=%s,mountport=%s,vers=3,tcp,%s,actimeo=3 127.0.0.1:/ %s",
		port, port, lockOpt, mountPoint)
}

// Ensure FileSystem satisfies billy.Filesystem at compile time.
var _ billy.Filesystem = (*FileSystem)(nil)
