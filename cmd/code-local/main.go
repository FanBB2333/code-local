package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FanBB2333/code-local/internal/auth"
)

func main() {
	url := flag.String("url", "", "code-server URL (e.g. https://example.com:8080)")
	password := flag.String("password", "", "code-server password")
	remotePath := flag.String("remote-path", "/", "remote directory to mount")
	mountPoint := flag.String("mount", "", "local mount point")

	flag.Parse()

	if *url == "" || *password == "" || *mountPoint == "" {
		fmt.Fprintln(os.Stderr, "Usage: code-local --url <url> --password <password> --mount <mount-point> [--remote-path <path>]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Step 1: Authenticate
	authClient, err := auth.NewClient(*url, *password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Logging in to %s...\n", *url)
	if err := authClient.Login(); err != nil {
		fmt.Fprintf(os.Stderr, "Login failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Login successful.")

	// TODO: Step 2 - WebSocket connection
	// TODO: Step 3 - IPC client
	// TODO: Step 4 - Remote FS
	// TODO: Step 5 - NFS server mount
	_ = remotePath
	_ = mountPoint
	_ = authClient
}
