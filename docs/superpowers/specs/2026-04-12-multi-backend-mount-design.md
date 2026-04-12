# Multi-Backend Mount Design

**Date:** 2026-04-12

## Goal

Allow `code-local` to expose the same remote `code-server` directory through more than one local mount backend. The first supported backends are `nfs` and `webdav`, selectable by the user at runtime.

## Scope

- Add a CLI flag to choose the local mount backend.
- Keep `nfs` as the default backend for backward compatibility.
- Add a local `webdav` server backend that exposes the same remote directory.
- Keep the existing authentication, WebSocket, IPC, and remote filesystem protocol unchanged.
- Add automated tests for backend selection and WebDAV behavior.
- Validate the change against a real `code-server` instance.

## Non-Goals

- No Windows mount flow in this change.
- No live file-watch synchronization changes.
- No protocol changes on the remote `code-server` side.
- No root-required end-to-end system mount automation in CI tests.

## Design

### CLI

Add `--backend` with allowed values `nfs` and `webdav`. The CLI continues to authenticate, connect, and validate the remote path exactly once. After that it creates the selected local backend server and prints the backend-specific mount command.

### Shared Remote Filesystem Boundary

Introduce a small interface in `internal/remotefs` for the subset of file operations needed by local backends:

- `Stat`
- `ReadDir`
- `ReadFile`
- `WriteFile`
- `Mkdir`
- `Delete`
- `Rename`

`remotefs.Client` already supports these operations, so it will satisfy the interface directly. This lets tests inject an in-memory fake without requiring a live IPC connection.

### Backend Abstraction

Introduce a small local backend interface owned by the CLI layer:

- `Addr() string`
- `MountCmd(mountPoint string) string`
- `Serve() error`
- `Close() error`

The `nfs` package keeps its current behavior but changes constructors to accept the shared remote filesystem interface instead of the concrete client type.

The new `webdav` package provides the same lifecycle interface, backed by `golang.org/x/net/webdav` and a local HTTP server bound to `127.0.0.1`.

### WebDAV Filesystem

The WebDAV backend implements `webdav.FileSystem` with the same remote path root handling as the NFS backend:

- Relative request paths are mapped into the chosen remote root.
- File reads are lazy-loaded.
- Writes are buffered and flushed on `Close`.
- Directory listings call remote `ReadDir`.
- Remove and rename map to remote operations.

This keeps semantics close to the current NFS implementation and avoids changing the remote protocol.

### Mount Command Output

- `nfs`: keep current OS-specific command generation.
- `webdav`:
  - macOS: print `mount_webdav`.
  - Linux: print a `davfs`-style command and document that `davfs2` is required.

### Error Handling

- Reject unsupported backend names before any remote work starts.
- Keep remote path validation shared across backends.
- Surface local server startup errors with the selected backend name.

### Testing

- Unit tests for backend selection and command generation.
- Unit tests for the WebDAV filesystem and HTTP handler using a fake remote filesystem.
- Real-service validation against the provided `code-server` instance:
  - login
  - remote path stat
  - backend startup
  - WebDAV HTTP read/write smoke test

## Files

- Modify `cmd/code-local/main.go`
- Add `internal/remotefs/fs.go`
- Modify `internal/nfs/server.go`
- Modify `internal/nfs/filesystem.go`
- Add `internal/webdav/server.go`
- Add `internal/webdav/filesystem.go`
- Add tests under `cmd/code-local` and `internal/webdav`
- Update `README.md` and `docs/usage.md`
