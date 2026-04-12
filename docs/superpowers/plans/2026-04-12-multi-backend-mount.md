# Multi-Backend Mount Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add selectable `nfs` and `webdav` local mount backends without changing the remote `code-server` protocol flow.

**Architecture:** Keep auth, WebSocket, IPC, and remote filesystem access shared. Add a small remote filesystem interface for testability and a small local backend lifecycle interface so the CLI can select `nfs` or `webdav` at runtime.

**Tech Stack:** Go, `willscott/go-nfs`, `golang.org/x/net/webdav`, `net/http`, standard `testing` package

---

### Task 1: Shared Remote Filesystem Interface

**Files:**
- Create: `internal/remotefs/fs.go`
- Modify: `internal/nfs/server.go`
- Modify: `internal/nfs/filesystem.go`

- [ ] **Step 1: Write the failing test**

Create a test-only fake that satisfies the new remote interface and use it from backend packages.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/nfs`
Expected: build failure because the shared interface does not exist yet.

- [ ] **Step 3: Write minimal implementation**

Define the interface in `internal/remotefs/fs.go` and update NFS constructors to accept it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/nfs`
Expected: PASS

### Task 2: Backend Selection at CLI Layer

**Files:**
- Modify: `cmd/code-local/main.go`
- Test: `cmd/code-local/main_test.go`

- [ ] **Step 1: Write the failing test**

Add tests that:
- reject unknown backends
- default to `nfs`
- create the correct backend for `nfs` and `webdav`

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/code-local -run 'Test(Parse|Create)Backend'`
Expected: FAIL because backend parsing and creation logic does not exist.

- [ ] **Step 3: Write minimal implementation**

Add `--backend`, a backend constructor path, and backend-specific startup logging.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/code-local -run 'Test(Parse|Create)Backend'`
Expected: PASS

### Task 3: WebDAV Backend

**Files:**
- Create: `internal/webdav/server.go`
- Create: `internal/webdav/filesystem.go`
- Test: `internal/webdav/server_test.go`

- [ ] **Step 1: Write the failing test**

Add tests that:
- serve directory listing via WebDAV `PROPFIND`
- read a file via `GET`
- write a file via `PUT`
- delete a file via `DELETE`

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/webdav -run TestServer`
Expected: FAIL because the package does not exist.

- [ ] **Step 3: Write minimal implementation**

Create a local HTTP server using `golang.org/x/net/webdav`, backed by the shared remote filesystem interface and bound to `127.0.0.1:<port>`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/webdav -run TestServer`
Expected: PASS

### Task 4: Docs and Command Output

**Files:**
- Modify: `README.md`
- Modify: `docs/usage.md`
- Test: `cmd/code-local/main_test.go`

- [ ] **Step 1: Write the failing test**

Add tests for backend-specific mount command output on supported platforms.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/code-local -run TestMountCmd`
Expected: FAIL because WebDAV command generation does not exist.

- [ ] **Step 3: Write minimal implementation**

Document `--backend`, explain `nfs` vs `webdav`, and print the matching mount command.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/code-local -run TestMountCmd`
Expected: PASS

### Task 5: Verification

**Files:**
- No code changes required if all checks pass

- [ ] **Step 1: Run package tests**

Run: `go test ./cmd/code-local ./internal/nfs ./internal/webdav ./internal/remotefs ./internal/protocol ./internal/cache`
Expected: PASS

- [ ] **Step 2: Run real-service validation**

Run a local `code-local` process against the provided service with `--backend webdav`, confirm startup succeeds, and verify HTTP `GET` and `PUT` against the local WebDAV listener.

- [ ] **Step 3: Run real-service backward-compatibility validation**

Run the same remote startup flow with `--backend nfs` and confirm the server starts and prints the NFS mount command.
