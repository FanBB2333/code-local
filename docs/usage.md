# 使用指南

## 前置条件

- Go 1.21+
- 一个可访问的 code-server 实例（知道 URL 和密码）
- macOS 或 Linux

## 构建

```bash
go build -o code-local ./cmd/code-local/
```

## 运行

```bash
./code-local \
  --url https://your-code-server.example.com \
  --password your-password \
  --mount /tmp/remote \
  --backend nfs \
  --remote-path /home/user/project
```

### 参数说明

| 参数 | 必选 | 默认值 | 说明 |
|------|------|--------|------|
| `--url` | 是 | - | code-server URL |
| `--password` | 是 | - | code-server 登录密码 |
| `--mount` | 是 | - | 本地挂载点路径 |
| `--remote-path` | 否 | `/` | 要挂载的远程目录路径 |
| `--backend` | 否 | `nfs` | 本地挂载后端，支持 `nfs` / `webdav` |
| `--port` | 否 | `10049` | 本地后端服务端口 |
| `--nfs-actimeo` | 否 | `3` | NFS attribute cache 秒数，大项目建议从 `30` 开始调优 |

## 挂载

程序启动后会根据所选后端输出挂载命令。

### NFS

例如：

```
NFS server listening on 127.0.0.1:10049

To mount, run:
  sudo mount -t nfs -o port=10049,mountport=10049,vers=3,tcp,nolock 127.0.0.1:/ /tmp/remote
```

在另一个终端中执行该命令即可完成挂载。

### WebDAV

例如：

```bash
WEBDAV server listening on 127.0.0.1:10049

To mount, run:
  mkdir -p /tmp/remote && mount_webdav -S -v code-local http://127.0.0.1:10049/ /tmp/remote
```

Linux 上会输出 `davfs` 风格命令，需要先安装 `davfs2`。

## 卸载

```bash
# macOS
sudo umount /tmp/remote

# Linux
sudo umount /tmp/remote
```

然后 Ctrl+C 停止 code-local 进程。

## 工作原理

1. **认证**：向 code-server 发送密码，获取 session cookie
2. **WebSocket**：使用 cookie 建立 WebSocket 连接
3. **握手**：完成 VS Code Server 的二进制握手协议
4. **IPC**：通过 VS Code 的 IPC 协议进行文件操作
5. **Backend Server**：在本地启动所选后端服务（NFS 或 WebDAV），将文件操作透传到远程

## 限制

- 文件内容在首次读取时全量加载到内存
- 写操作在 `close()` 时才刷新到远程（非实时）
- 大文件（>100MB）可能导致内存占用较高
- 不支持 symlink 创建
- cache 模块已实现但尚未接入 billy 层（后续优化）
- 双向实时同步（file watch）已有接口但尚未接入 NFS 层

## 大项目优化建议

- 大项目优先使用 `nfs` 后端
- 大目录、读多写少的仓库可以提高 `--nfs-actimeo`（建议 30 秒起）
- 详细性能分析见 [large-project-performance.md](large-project-performance.md)

## 故障排查

### 登录失败
- 确认 URL 可访问（浏览器能打开）
- 确认密码正确
- 检查是否有代理/防火墙阻断

### WebSocket 连接失败
- 确认 URL scheme（`https` → `wss`，`http` → `ws`）
- 检查是否有 WebSocket 代理设置

### NFS 挂载失败
- 确认端口未被占用：`lsof -i :10049`
- macOS 可能需要 `sudo` 权限
- Linux 需安装 `nfs-common`：`sudo apt install nfs-common`

### WebDAV 挂载失败
- 确认端口未被占用：`lsof -i :10049`
- macOS 使用 `mount_webdav`
- Linux 需安装 `davfs2`
