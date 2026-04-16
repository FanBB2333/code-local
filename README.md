# code-local

Mount remote [code-server](https://github.com/coder/code-server) directories to your local filesystem via NFS or WebDAV.

## How it works

code-local connects to a running code-server instance using its native VS Code WebSocket protocol, then exposes the remote filesystem through a selectable local mount backend. Today it supports `nfs` and `webdav`. No SSH, no extensions, no server-side changes required — just a URL and password.

```
Local                               Remote
┌──────────┐  NFS/WebDAV  ┌─────────┐  WebSocket  ┌─────────────┐
│ /mnt/xxx │◄────────────►│code-local│◄───────────►│ code-server │
└──────────┘              └─────────┘              └─────────────┘
```

## Quick start

```bash
go build -o code-local ./cmd/code-local/

./code-local \
  --url https://your-server:8080 \
  --password yourpass \
  --mount /tmp/remote \
  --backend nfs \
  --nfs-actimeo 30 \
  --remote-path /home/user/project

# Then run the mount command printed by code-local:
sudo mount -t nfs -o port=10049,mountport=10049,vers=3,tcp,nolock 127.0.0.1:/ /tmp/remote
```

For WebDAV:

```bash
./code-local \
  --url https://your-server:8080 \
  --password yourpass \
  --mount /tmp/remote \
  --backend webdav \
  --remote-path /home/user/project

# Then run the mount command printed by code-local.
```

## Remote terminal

Open an interactive shell session inside the remote code-server environment directly in your local terminal — no SSH required:

```bash
./code-local terminal \
  --url https://your-server:8080 \
  --password yourpass \
  --cwd /home/user/project
```

The local terminal is bridged to a `/bin/bash` process running on the remote host. Terminal resize (`SIGWINCH`) is forwarded automatically. Press `Ctrl+C` to exit.

### Terminal flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--url` | yes | - | code-server URL |
| `--password` | yes | - | code-server login password |
| `--cwd` | no | `/` | Working directory for the remote shell |
| `--debug` | no | `false` | Enable debug logging |

## Mount flags

| Flag | Required | Default | Description |
|------|----------|---------|-------------|
| `--url` | yes | - | code-server URL |
| `--password` | yes | - | code-server login password |
| `--mount` | yes | - | Local mount point path |
| `--remote-path` | no | `/` | Remote directory to mount |
| `--backend` | no | `nfs` | Local mount backend: `nfs` or `webdav` |
| `--port` | no | `10049` | Local backend server port |
| `--nfs-actimeo` | no | `3` | NFS attribute cache timeout in seconds (try `30` for large repos) |
| `--debug` | no | `false` | Enable debug logging |

## Backends

- `nfs`: recommended for large repositories — stronger metadata caching and configurable attribute cache timeout.
- `webdav`: compatibility backend when NFS tooling is unavailable.

## Documentation

- [Usage Guide](docs/usage.md)
- [Architecture](docs/architecture.md)
- [Protocol Reference](docs/protocol.md)
