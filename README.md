# code-local

Mount remote [code-server](https://github.com/coder/code-server) directories to your local filesystem via NFS.

## How it works

code-local connects to a running code-server instance using its native VS Code WebSocket protocol, then exposes the remote filesystem as a local NFS v3 mount. No SSH, no extensions, no server-side changes required — just a URL and password.

```
Local                              Remote
┌──────────┐  NFS v3  ┌─────────┐  WebSocket  ┌─────────────┐
│ /mnt/xxx │◄────────►│code-local│◄───────────►│ code-server │
└──────────┘          └─────────┘              └─────────────┘
```

## Quick start

```bash
go build -o code-local ./cmd/code-local/

./code-local \
  --url https://your-server:8080 \
  --password yourpass \
  --mount /tmp/remote \
  --remote-path /home/user/project

# Then run the mount command printed by code-local:
sudo mount -t nfs -o port=10049,mountport=10049,vers=3,tcp,nolock 127.0.0.1:/ /tmp/remote
```

## Documentation

- [Usage Guide](docs/usage.md)
- [Architecture](docs/architecture.md)
- [Protocol Reference](docs/protocol.md)
