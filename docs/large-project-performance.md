# Large Project Mount Performance

详细实施计划见：`docs/superpowers/plans/2026-04-14-large-project-performance.md`

## 背景

当前 `code-local` 在启动挂载时，只会完成一次认证、一次 WebSocket 握手、一次 IPC 初始化，以及一次对远端根目录的 `stat` 校验。真正的性能压力通常不在挂载启动阶段，而是在挂载完成后，本地客户端开始扫描目录、读取元数据和访问文件内容时出现。

对较大的项目目录，Finder、IDE、语言服务、索引器、`ls -R` 等操作会集中触发大量 `readdir`、`stat`、`readFile` 请求。当前实现中，这些请求最终都会汇聚到 `internal/remotefs.Client` 的远端 RPC 调用上，因此大项目场景下的延迟主要来自挂载后的高频元数据访问和内容读取。

## 当前调用链

典型读路径如下：

```text
本地客户端
  -> NFS / WebDAV 挂载点
  -> 本地后端服务
  -> internal/remotefs.Client
  -> internal/protocol.IPCClient
  -> WebSocket
  -> code-server remoteFilesystem channel
```

对应入口与核心实现：

- 启动流程：`cmd/code-local/main.go`
- 远端文件系统 RPC：`internal/remotefs/client.go`
- NFS 文件系统：`internal/nfs/filesystem.go`、`internal/nfs/file.go`
- WebDAV 文件系统：`internal/webdav/filesystem.go`
- IPC 和连接层：`internal/protocol/ipc.go`、`internal/protocol/conn.go`

## 主要瓶颈

### 1. 协议层高并发下有丢帧风险

`internal/protocol/conn.go` 在接收 WebSocket 消息后，将 Regular 和 Control frame 投递到固定容量 channel。当前写法在 channel 满时直接走 `default` 分支，相当于丢弃响应帧。

这在大项目目录扫描场景下风险很高：

- 本地客户端并发触发大量元数据请求
- 远端响应短时间集中返回
- 本地接收队列打满后开始丢包
- 上层 `IPCClient.Call()` 一直等不到对应响应，最终超时

这类问题表现出来通常不是直接报错，而是“整体很慢”“偶发卡住”“有些目录打开特别慢”。

### 2. 文件读写模型是全量传输

当前 `remotefs.Client` 只暴露：

- `readFile`
- `writeFile`

对应本地后端实现也都是：

- 首次读取时整文件拉到本地内存
- 写入时在本地内存缓冲
- `Close()` 时整文件回写到远端

这意味着：

- 大文件读取成本高
- 小改动也可能触发整文件写回
- 多个工具反复读取同一文件时，容易重复走高成本路径

### 3. 元数据访问容易被放大

`readdir` 返回的信息只有 `name + type`，不包含完整 `size`、`mtime`、权限等元数据。

许多本地客户端在拿到目录列表后，还会继续对每个条目做 `stat`，于是会形成：

```text
一次 ReadDir
  + N 次 Stat
```

目录越大，这种放大越明显。

### 4. WebDAV 当前比 NFS 更容易触发额外请求

WebDAV 路径目前没有接入和 NFS 等价的元数据缓存，而且 `OpenFile` 时会先 `Stat`，部分元数据获取还会进一步触发内容加载。

对大项目来说，这会使 WebDAV 在以下场景下更吃亏：

- 大量目录浏览
- IDE 初始化扫描
- 文件属性频繁读取

### 5. 缓存策略偏保守，且没有事件驱动失效

NFS 路径虽然已经接了 `statCache` 和 `dirCache`，但：

- TTL 较短
- 容量偏保守
- WebDAV 路径没有复用同样策略
- `watch/unwatch` 接口虽然已存在，但还没有接入缓存失效链路

这导致当前缓存更多是“短期缓冲”，还不能支撑大目录下的稳定交互体验。

### 6. 挂载参数对元数据回源较频繁

NFS 挂载命令当前固定包含 `actimeo=3`。这个值比较保守，会让客户端更频繁地重新校验属性。

对需要高一致性的场景这是安全的，但对大项目的浏览和索引场景，会增加远端元数据访问压力。

## 优化角度

### 1. 先解决协议层可靠性

这是优先级最高的一层。只要高并发下仍会丢响应帧，上层再多缓存和预取也会被放大成超时问题。

建议：

- 去掉接收分发中的 `default` 丢帧逻辑，改为带背压的投递
- 明确记录 channel 深度、超时数、响应等待时长
- 必要时增大接收缓冲，但不要只靠扩大缓冲掩盖问题

### 2. 强化元数据缓存

目标不是无限放大 TTL，而是把“热点目录”和“热点文件属性”尽量挡在本地。

建议：

- 为 NFS 和 WebDAV 统一接入 `stat` / `readdir` 缓存
- 增大大项目场景下的缓存容量
- 将纯 TTL 缓存改成更明确的 LRU + TTL
- 对同一路径的并发请求加 `singleflight`，避免同一时刻重复回源

### 3. 用 watch 事件做缓存失效，而不是只靠短 TTL

当前 `watch` 能力已经存在，但没有真正接到挂载层。

建议：

- 为挂载根目录建立 watch
- 收到 `fileChange` 后，按路径精确失效 `statCache` 和 `dirCache`
- 对目录变更同时失效父目录缓存

这样可以把缓存 TTL 提高到更适合大项目的水平，而不明显牺牲一致性。

### 4. 减少目录遍历时的元数据放大

这是大项目体验优化的关键部分。

建议：

- `ReadDir` 返回后异步预热子项 `stat`
- 对目录项的 `stat` 做批量预取或并发受控预取
- 优先缓存 IDE/Finder 最常访问的字段

如果远端协议无法直接提供更丰富的 `readdir` 元数据，那么本地至少应该把 `readdir` 之后的 `stat` 风暴吸收掉一部分。

### 5. 优化文件内容读取策略

短期内，如果不能修改远端协议，可以先做两件事：

- 给热点文件增加内容缓存
- 避免因元数据查询而触发整文件加载

中长期更理想的方向是支持分块读取、范围读取或本地落盘缓存，否则大文件场景会一直受限于“全量拉取”模型。

### 6. 调整挂载后端和挂载参数

基于当前实现：

- 大项目优先推荐 `nfs`
- `webdav` 更适合作为兼容性后端，而不是性能优先后端
- 让 `actimeo` 可配置，而不是固定写死

对于“目录很大但写入不频繁”的项目，可以允许用户显式提高属性缓存时间，换取更快的目录浏览和索引速度。

### 7. 建立可观测性

如果没有指标，很难知道慢在哪里。

建议最少补以下指标或调试日志：

- `Stat` / `ReadDir` / `ReadFile` 调用次数
- 平均延迟、P95、P99
- cache hit / miss
- IPC 超时次数
- WebSocket 接收队列堆积情况

这部分对后续判断“瓶颈在协议层、缓存层还是远端服务层”很重要。

## 优先级建议

### P0

- 修复 `internal/protocol/conn.go` 的接收分发丢帧问题

### P1

- 统一 NFS / WebDAV 的元数据缓存
- 接入 `watch` 做缓存失效
- 为同一路径请求增加去重

### P2

- 目录预热和受控预取
- 将 `actimeo` 等挂载参数改为可配置
- 增加性能指标和调试日志

### P3

- 重新设计文件内容读取模型，支持分块或本地落盘缓存

## 如果只改一件

优先修改 `internal/protocol/conn.go`，去掉当前高并发场景下的响应丢弃逻辑。

原因：

- 这是最底层的可靠性问题
- 会直接放大成用户可感知的卡顿和超时
- 修复后，后续缓存和预取优化才有稳定收益

在此基础上，下一步最值得做的是把 `watch` 接入缓存失效链路，并统一 NFS / WebDAV 的元数据缓存策略。
