package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FanBB2333/code-local/internal/auth"
	nfsserver "github.com/FanBB2333/code-local/internal/nfs"
	"github.com/FanBB2333/code-local/internal/protocol"
	"github.com/FanBB2333/code-local/internal/remotefs"
)

func main() {
	urlFlag := flag.String("url", "", "code-server URL (e.g. https://example.com:8080)")
	password := flag.String("password", "", "code-server password")
	remotePath := flag.String("remote-path", "/", "remote directory to mount")
	mountPoint := flag.String("mount", "", "local mount point")
	port := flag.Int("port", 10049, "local NFS server port")

	flag.Parse()

	if *urlFlag == "" || *password == "" || *mountPoint == "" {
		fmt.Fprintln(os.Stderr, "Usage: code-local --url <url> --password <password> --mount <mount-point> [--remote-path <path>]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if err := run(*urlFlag, *password, *remotePath, *mountPoint, *port); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(serverURL, password, remotePath, mountPoint string, port int) error {
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

	// Step 4: Remote FS client
	remote := remotefs.NewClient(ipc)

	// Quick test: stat the remote path
	st, err := remote.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("stat remote path %q: %w", remotePath, err)
	}
	if st.Type != remotefs.FileTypeDirectory {
		return fmt.Errorf("remote path %q is not a directory", remotePath)
	}
	fmt.Printf("Remote path %q OK (directory).\n", remotePath)

	// Step 5: Start NFS server
	nfs, err := nfsserver.NewServer(remote, remotePath, port)
	if err != nil {
		return fmt.Errorf("create NFS server: %w", err)
	}

	// Ensure mount point exists
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("create mount point: %w", err)
	}

	fmt.Printf("NFS server listening on %s\n", nfs.Addr())
	fmt.Printf("\nTo mount, run:\n  %s\n\n", nfs.MountCmd(mountPoint))
	fmt.Println("Press Ctrl+C to stop.")

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- nfs.Serve()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("NFS server: %w", err)
	case sig := <-sigCh:
		fmt.Printf("\nReceived %v, shutting down...\n", sig)
		nfs.Close()
		return nil
	}
}
