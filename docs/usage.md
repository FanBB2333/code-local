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
  --remote-path /home/user/project
```

### 参数说明

| 参数 | 必选 | 默认值 | 说明 |
|------|------|--------|------|
| `--url` | 是 | - | code-server URL |
| `--password` | 是 | - | code-server 登录密码 |
| `--mount` | 是 | - | 本地挂载点路径 |
| `--remote-path` | 否 | `/` | 要挂载的远程目录路径 |
| `--port` | 否 | `10049` | 本地 NFS 服务器端口 |

## 挂载

程序启动后会输出挂载命令，例如：

```
NFS server listening on 127.0.0.1:10049

To mount, run:
  sudo mount -t nfs -o port=10049,mountport=10049,vers=3,tcp,nolock 127.0.0.1:/ /tmp/remote
```

在另一个终端中执行该命令即可完成挂载。

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
5. **NFS**：在本地启动 NFS v3 服务器，将文件操作透传到远程

## 限制

- 文件内容在首次读取时全量加载到内存
- 写操作在 `close()` 时才刷新到远程（非实时）
- 大文件（>100MB）可能导致内存占用较高
- 不支持 symlink 创建
- cache 模块已实现但尚未接入 billy 层（后续优化）
- 双向实时同步（file watch）已有接口但尚未接入 NFS 层

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
