package webdav

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"

	xwebdav "golang.org/x/net/webdav"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

// Server wraps a local WebDAV server backed by the remote filesystem.
type Server struct {
	listener net.Listener
	server   *http.Server
	fs       *FileSystem
}

func NewServer(remote remotefs.FS, remotePath string, port int) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	fs := NewFileSystem(remote, remotePath)
	handler := &xwebdav.Handler{
		Prefix:     "/",
		FileSystem: fs,
		LockSystem: xwebdav.NewMemLS(),
	}

	return &Server{
		listener: listener,
		server: &http.Server{
			Handler: handler,
		},
		fs: fs,
	}, nil
}

func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

func (s *Server) Serve() error {
	err := s.server.Serve(s.listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) Close() error {
	return s.server.Close()
}

func (s *Server) MountCmd(mountPoint string) string {
	url := fmt.Sprintf("http://%s/", s.Addr())
	if runtime.GOOS == "darwin" {
		return fmt.Sprintf("mkdir -p %s && mount_webdav -S -v code-local %s %s", mountPoint, url, mountPoint)
	}
	return fmt.Sprintf("mkdir -p %s && sudo mount -t davfs -o uid=$(id -u),gid=$(id -g) %s %s", mountPoint, url, mountPoint)
}
