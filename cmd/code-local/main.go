package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/FanBB2333/code-local/internal/auth"
	nfsserver "github.com/FanBB2333/code-local/internal/nfs"
	"github.com/FanBB2333/code-local/internal/protocol"
	"github.com/FanBB2333/code-local/internal/remotefs"
	webdavserver "github.com/FanBB2333/code-local/internal/webdav"
)

type backendKind string

const (
	backendNFS    backendKind = "nfs"
	backendWebDAV backendKind = "webdav"
)

type localBackend interface {
	Addr() string
	MountCmd(mountPoint string) string
	Serve() error
	Close() error
}

func main() {
	urlFlag := flag.String("url", "", "code-server URL (e.g. https://example.com:8080)")
	password := flag.String("password", "", "code-server password")
	remotePath := flag.String("remote-path", "/", "remote directory to mount")
	mountPoint := flag.String("mount", "", "local mount point")
	port := flag.Int("port", 10049, "local NFS server port")
	backend := flag.String("backend", string(backendNFS), "local mount backend: nfs or webdav")
	nfsActimeo := flag.Int("nfs-actimeo", 3, "NFS attribute cache timeout in seconds")
	debug := flag.Bool("debug", false, "enable debug logging")

	flag.Parse()

	if *urlFlag == "" || *password == "" || *mountPoint == "" {
		fmt.Fprintln(os.Stderr, "Usage: code-local --url <url> --password <password> --mount <mount-point> [--remote-path <path>] [--backend <nfs|webdav>]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	parsedBackend, err := parseBackend(*backend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := run(*urlFlag, *password, *remotePath, *mountPoint, parsedBackend, *port, *nfsActimeo, *debug); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func parseBackend(raw string) (backendKind, error) {
	normalized := backendKind(strings.ToLower(strings.TrimSpace(raw)))
	if normalized == "" {
		return backendNFS, nil
	}
	switch normalized {
	case backendNFS, backendWebDAV:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported backend %q (expected nfs or webdav)", raw)
	}
}

func createBackend(ctx context.Context, backend backendKind, remote remotefs.FS, remotePath string, port int, nfsOpts nfsserver.Options) (localBackend, error) {
	_ = ctx
	switch backend {
	case backendNFS:
		return nfsserver.NewServer(remote, remotePath, port, nfsOpts)
	case backendWebDAV:
		return webdavserver.NewServer(remote, remotePath, port)
	default:
		return nil, fmt.Errorf("unsupported backend %q", backend)
	}
}

// wrapRemoteFS wraps a remote FS with a shared cache and starts a recursive watch.
func wrapRemoteFS(remote remotefs.FS, remotePath string) (remotefs.FS, func(), error) {
	cached := remotefs.NewCachedFS(remote, remotefs.DefaultCacheConfig())
	stop, err := cached.StartWatch(remotePath)
	if err != nil {
		return nil, nil, fmt.Errorf("start remote watch: %w", err)
	}
	return cached, stop, nil
}

func run(serverURL, password, remotePath, mountPoint string, backend backendKind, port, nfsActimeo int, debug bool) error {
	// Step 1: Authenticate
	authClient, err := auth.NewClient(serverURL, password)
	if err != nil {
		return fmt.Errorf("create auth client: %w", err)
	}

	fmt.Printf("Logging in to %s...\n", serverURL)
	if err := authClient.Login(); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	fmt.Println("Login successful.")

	// Step 2: WebSocket connection
	wsURL, err := authClient.WebSocketURL()
	if err != nil {
		return fmt.Errorf("websocket URL: %w", err)
	}

	fmt.Println("Connecting WebSocket...")
	ctx := context.Background()
	conn, err := protocol.Dial(ctx, wsURL, authClient.CookieHeader(), authClient.Origin())
	if err != nil {
		return fmt.Errorf("websocket connect: %w", err)
	}
	defer conn.Close()
	conn.SetDebug(debug)

	fmt.Println("Performing handshake...")
	if err := conn.Handshake(); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	fmt.Println("Handshake complete.")

	// Step 3: IPC client
	ipc := protocol.NewIPCClient(conn)
	if err := ipc.WaitInitialized(10 * time.Second); err != nil {
		return fmt.Errorf("IPC init: %w", err)
	}
	fmt.Println("IPC initialized.")

	// Step 4: Remote FS client with shared cache
	baseRemote := remotefs.NewClient(ipc)

	// Quick test: stat the remote path
	st, err := baseRemote.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("stat remote path %q: %w", remotePath, err)
	}
	if st.Type != remotefs.FileTypeDirectory {
		return fmt.Errorf("remote path %q is not a directory", remotePath)
	}
	fmt.Printf("Remote path %q OK (directory).\n", remotePath)

	remote, stopRemote, err := wrapRemoteFS(baseRemote, remotePath)
	if err != nil {
		return fmt.Errorf("prepare remote filesystem: %w", err)
	}
	defer stopRemote()

	// Step 5: Start local backend server
	server, err := createBackend(ctx, backend, remote, remotePath, port, nfsserver.Options{Actimeo: nfsActimeo})
	if err != nil {
		return fmt.Errorf("create %s server: %w", backend, err)
	}

	// Ensure mount point exists
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	fmt.Printf("%s server listening on %s\n", strings.ToUpper(string(backend)), server.Addr())
	fmt.Printf("\nTo mount, run:\n  %s\n\n", server.MountCmd(mountPoint))
	fmt.Println("Press Ctrl+C to stop.")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("%s server: %w", backend, err)
	case sig := <-sigCh:
		fmt.Printf("\nReceived %v, shutting down...\n", sig)
		server.Close()
		return nil
	}
}
