# VS Code Server 协议参考

code-local 通过 VS Code Server 的原生协议与 code-server 通信。本文档记录协议细节，源自对 code-server 源码的逆向分析。

## 1. 认证

code-server 使用密码认证，流程如下：

```
POST /login
Content-Type: application/x-www-form-urlencoded
Body: password=<密码>

→ 302 Found + Set-Cookie: code-server-session=<hashed_password>
```

后续所有请求（包括 WebSocket 升级）需携带此 cookie。

## 2. WebSocket 连接

WebSocket 升级请求需包含：
- `Cookie: code-server-session=<value>`
- `Origin: <与 Host 匹配>`

code-server 将 `without-connection-token: true` 传递给 VS Code Server，因此不需要额外的连接令牌。

## 3. 二进制帧协议

每条消息由 13 字节头部 + 可变长度载荷组成：

```
偏移  大小  字段        说明
0     1B   Type        消息类型
1     4B   ID          消息 ID (Big-Endian)
5     4B   ACK         确认 ID (Big-Endian)
9     4B   DataLen     载荷长度 (Big-Endian)
13    NB   Payload     载荷数据
```

### 消息类型

| 值 | 名称 | 说明 |
|----|------|------|
| 1 | Regular | 应用数据，需确认 |
| 2 | Control | 握手/控制消息，不确认 |
| 3 | Ack | 确认 |
| 5 | Disconnect | 断开连接 |
| 9 | KeepAlive | 心跳，每 5 秒一次 |

## 4. 握手流程

握手使用 Control 消息（Type=2），载荷为 JSON：

```
Client → Server:  {"type":"auth","auth":"00000000000000000000"}
Server → Client:  {"type":"sign","data":"...","signedData":"..."}
Client → Server:  {"type":"connectionType","signedData":"...","desiredConnectionType":1}
Server → Client:  {"type":"ok"}
```

`desiredConnectionType=1` 表示 Management 连接（用于 IPC 调用）。

## 5. IPC 序列化

握手完成后，VS Code Server 发送 Initialize 消息，之后开始 IPC 通信。IPC 消息使用 Regular 帧（Type=1）传输。

### DataType 编码

每个值以 1 字节类型标记开头：

| 值 | 类型 | 编码 |
|----|------|------|
| 0 | Undefined | 无后续数据 |
| 1 | String | VQL(长度) + UTF-8 字节 |
| 2 | Buffer | VQL(长度) + 原始字节 |
| 3 | VSBuffer | VQL(长度) + 原始字节 |
| 4 | Array | VQL(元素数) + 各元素递归序列化 |
| 5 | Object | VQL(长度) + JSON 字符串字节 |
| 6 | Int | VQL(值) |

### VQL (Variable-Length Quantity)

整数编码为变长字节序列，每字节低 7 位存储数据，最高位表示是否有后续字节：

```
值 0:        0x00
值 127:      0x7F
值 128:      0x80 0x01
值 16384:    0x80 0x80 0x01
```

### IPC 请求格式

```
serialize(header) + serialize(body)

header = [requestType, id, channelName, commandName]
body   = arg
```

请求类型：
- `100` = Promise（调用，返回 Promise）
- `102` = EventListen（订阅事件）
- `103` = EventDispose（取消订阅）

### IPC 响应格式

```
serialize(header) + serialize(body)

header = [responseType] 或 [responseType, id]
body   = data
```

响应类型：
- `200` = Initialize（连接就绪）
- `201` = PromiseSuccess（调用成功）
- `202` = PromiseError（调用失败，body 含 message/name/stack）
- `204` = EventFire（事件触发）

## 6. 文件系统操作

Channel 名称：`remoteFilesystem`

### 可用命令

| 命令 | 参数 | 返回值 |
|------|------|--------|
| `stat` | `[UriComponents]` | `{type, mtime, ctime, size, permissions}` |
| `readdir` | `[UriComponents]` | `[[name, fileType], ...]` |
| `readFile` | `[UriComponents]` | `VSBuffer (bytes)` |
| `writeFile` | `[UriComponents, VSBuffer, {create, overwrite, unlock}]` | `void` |
| `mkdir` | `[UriComponents]` | `void` |
| `delete` | `[UriComponents, {recursive, useTrash}]` | `void` |
| `rename` | `[srcUri, dstUri, {overwrite}]` | `void` |
| `watch` | `[sessionId, reqId, UriComponents, {recursive, excludes}]` | `void` |
| `unwatch` | `[sessionId, reqId]` | `void` |

### UriComponents 格式

```json
{"scheme": "file", "authority": "", "path": "/absolute/path"}
```

### FileType 枚举

| 值 | 含义 |
|----|------|
| 0 | Unknown |
| 1 | File |
| 2 | Directory |
| 64 | SymbolicLink |

### 事件：fileChange

订阅参数：`[sessionId]`

事件数据：`[{type, resource: {path, ...}}, ...]`

FileChangeType: `0`=Updated, `1`=Added, `2`=Deleted
