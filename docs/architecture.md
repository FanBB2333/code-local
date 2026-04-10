# Architecture

code-local 通过 VS Code Server 的原生 WebSocket 协议访问远程 code-server 上的文件系统，在本地启动 NFS v3 服务器将远程目录挂载为本地目录。

## 整体架构

```
Local Machine                                    Remote
┌──────────┐  NFS v3   ┌───────────┐  WebSocket  ┌─────────────┐
│ /mnt/xxx │◄─────────►│ NFS Server│◄───────────►│ code-server │
│ (mount)  │           │ (Go)      │  Binary IPC │ (VS Code)   │
└──────────┘           └─────┬─────┘             └─────────────┘
                             │
                       ┌─────▼─────┐
                       │   Cache   │
                       └───────────┘
```

## 模块分层

```
cmd/code-local/main.go           CLI 入口，串联所有层
internal/
  auth/client.go                 POST /login → session cookie
  protocol/
    frame.go                     13 字节二进制帧 (Type+ID+ACK+Len)
    conn.go                      WebSocket 连接、握手、KeepAlive
    serialize.go                 VQL 编码 + DataType 序列化
    ipc.go                       IPC 请求/响应匹配、事件订阅
  remotefs/
    types.go                     FileStat, FileType, UriComponents
    client.go                    文件操作 → remoteFilesystem channel
  nfs/
    file.go                      billy.File (内存缓冲 + write-back)
    filesystem.go                billy.Filesystem (代理到远程)
    server.go                    NFS v3 服务器封装
  cache/
    cache.go                     TTL + LRU 缓存
```

## 数据流

```
用户程序 → NFS mount → NFS Server → billy.Filesystem → RemoteFS Client
    → IPC Client → WebSocket Conn → code-server → VS Code Server → 磁盘
```

### 写操作

1. 用户程序写入 NFS 挂载目录中的文件
2. NFS Server 调用 `billy.File.Write()` 将数据写入内存缓冲区
3. `billy.File.Close()` 时，将完整内容通过 `writeFile` IPC 命令发送到远程
4. VS Code Server 写入远程磁盘

### 读操作

1. 用户程序读取 NFS 挂载目录中的文件
2. NFS Server 调用 `billy.File.Read()`
3. 首次读取时触发 lazy load：通过 `readFile` IPC 命令从远程获取完整内容
4. 后续读取从内存缓冲区提供

## 依赖

| 依赖 | 用途 |
|------|------|
| `gorilla/websocket` | WebSocket 客户端 |
| `willscott/go-nfs` | NFS v3 服务器 |
| `go-git/go-billy/v5` | 文件系统抽象接口 |
