package nfs

import (
	"net"
	"strings"
	"testing"
)

func TestMountCmdIncludesConfiguredActimeo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	server := &Server{listener: ln, opts: Options{Actimeo: 30}}
	cmd := server.MountCmd("/tmp/code-local")
	if !strings.Contains(cmd, "actimeo=30") {
		t.Fatalf("mount command %q does not contain actimeo=30", cmd)
	}
}

func TestMountCmdDefaultsActimeoToThree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	server := &Server{listener: ln, opts: Options{}}
	cmd := server.MountCmd("/tmp/code-local")
	if !strings.Contains(cmd, "actimeo=3 ") {
		t.Fatalf("mount command %q does not contain actimeo=3", cmd)
	}
}
