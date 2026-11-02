# 存档接口文档

> 客户端：Unity (C#)  
> 协议：HTTP/1.1，JSON 编码  
> Base URL：由部署环境决定，本地开发默认 `http://192.168.2.65:8081`

---

## 目录

- [通用约定](#通用约定)
- [存档编码与签名](#存档编码与签名)
- [上传存档](#上传存档)
- [下载存档](#下载存档)
- [存档上传流程](#存档上传流程)
- [冲突处理策略](#冲突处理策略)
- [错误处理](#错误处理)

---

## 通用约定

### HTTP 状态码约定

所有客户端接口业务错误**始终返回 HTTP status 200**。客户端通过 JSON body 中的 `code` 字段区分业务结果，无需关注 HTTP 状态码。

### 请求头

| Header          | 必填 | 说明                                                                                    |
|-----------------|------|-----------------------------------------------------------------------------------------|
| `Content-Type`  | 是   | `application/json`（默认）或 `application/x-protobuf` / `application/protobuf`（可选） |
| `Authorization` | 是   | `Bearer <token>`，Token 由登录接口获取                                                  |

> 响应格式与请求 `Content-Type` 保持一致：发 JSON 收 JSON，发 protobuf 收 protobuf。

每个响应均携带以下 Header：

| Header           | 说明                                                |
|------------------|-----------------------------------------------------|
| `X-Server-Time`  | 服务器当前时间戳（Unix 毫秒），可用于客户端时间校准 |

### 序列化格式

服务端统一根据请求的 `Content-Type` 决定上下行的序列化格式，请求与响应格式保持一致。

| Content-Type                                              | 请求解析方式      | 响应序列化方式  |
|-----------------------------------------------------------|-------------------|-----------------|
| `application/json`（或不填）                              | JSON              | JSON            |
| `application/x-protobuf` / `application/protobuf`        | Protobuf 二进制   | Protobuf 二进制 |

Proto 定义文件见 `pb/cs.proto`，C# 命名空间为 `Cspb`，相关消息：`SaveUploadReq`、`SaveUploadResp`、`SaveDownloadReq`、`SaveDownloadResp`、`ErrorResp`。

> **错误响应的 protobuf 兼容性**：所有 `*Resp` 消息的前两个字段（tag 1 = `code`，tag 2 = `message`）与 `ErrorResp` 保持一致，客户端可直接用具体 `*Resp` 类型解析错误响应，无需额外处理。

### 统一响应结构（JSON 模式）

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

| 字段      | 类型   | 说明                                     |
|-----------|--------|------------------------------------------|
| `code`    | int    | `0` 表示成功，非 `0` 为业务错误码       |
| `message` | string | 结果描述，成功时为 `"ok"`                |
| `data`    | object | 成功时的业务数据，失败时为 `null` 或省略 |

### 版本号与 Key 机制

每个存档由 `(playerID, key)` 二元组唯一标识。`key` 必须使用 `Player` message 的直接字段名，例如 `base`（其 payload 为 `Base`）、`item` 与 `board`（其 payload 均为 `DefaultData`）。

存档通过**单调递增的版本号**（`version`）追踪历史和检测冲突：

- **`baseVersion`**：客户端发起上传时，所持有的上一次服务器版本号。
  - **首次上传（服务端无该 `key` 存档）时该字段被服务端忽略**：服务端此时没有该 `key` 的版本状态，客户端可能携带本地版本号而非 `0`，因此不参与冲突校验，直接以 `newVersion` 建立首个版本，保证首次上传始终成功。
  - 服务端已存在该 `key` 的存档时，普通上传要求 `newVersion > baseVersion`；`forceOverwrite=true` 时该字段不参与校验。
- **`newVersion`**：客户端期望写入的新版本号，必须满足 `newVersion > 0`。
- 服务端使用 CAS（Compare-And-Swap）机制：服务端已存在该 `key` 存档时，仅当当前版本 == `baseVersion` 才允许写入，否则返回冲突（`2409`）；首次上传与 `forceOverwrite=true` 不走 CAS。

### 存档编码与签名

存档内容采用 **protobuf → gzip 压缩** 的两段编码流程，得到的线格式字节即协议 `content` 字段内容（`bytes`，**原始二进制**，**明文、无存档级加密**）：

1. 将存档数据序列化为 protobuf 线格式字节；
2. 使用 gzip 压缩。

压缩后的线格式字节即 `content`。使用 JSON 传输时，按 JSON 规范 `bytes` 字段以 base64 字符串呈现（示例中的 `content` 即为该形式）；protobuf 模式下则直接传输原始字节。

签名 `sign` 为**线格式字节（gzip 压缩后）**的 SHA256 小写十六进制（固定 64 字符）。客户端上传时传入该值，下载后可据此校验内容完整性。

> 关于全局 `saveKey`、protobuf 模式下的传输层加解密规则，见 `docs/transport_encryption.md`。

所有时间戳均为 **Unix 毫秒**。

---

## 上传存档

### `POST /api/save/upload`

上传玩家本地存档到服务端。支持普通上传（带 CAS 冲突检测）和强制覆盖两种模式。

每个玩家每个 `key` 在服务端最多保留最近 **5** 个历史快照，超出时自动清理旧版本。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求体

```json
{
  "key": "base",
  "baseVersion": 5,
  "newVersion": 6,
  "sign": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
  "content": "H4sIAAAAAAAAClPyK0pLTFUAAJ6zBM8fAAAA",
  "forceOverwrite": false
}
```

| 字段             | 类型   | 必填 | 说明                                                                                              |
|------------------|--------|------|---------------------------------------------------------------------------------------------------|
| `key`            | string | 是   | 存档分类标识，区分不同内容（如 `"base"`、`"item"`）                                          |
| `baseVersion`    | int64  | 是   | 客户端修改时所基于的版本号。服务端已有该 `key` 存档时用于 CAS 冲突检测；**首次上传（服务端无该 `key` 存档）时该字段被忽略**，可传客户端本地版本号或 `0` |
| `newVersion`     | int64  | 是   | 期望写入的新版本号，必须 `> 0`；服务端已有该 `key` 存档且为普通上传时还须 `> baseVersion`                    |
| `sign`           | string | 是   | 存档签名：线格式字节（gzip 压缩后）的 SHA256 小写十六进制（固定 64 字符），服务端透传保存，用于下载后完整性校验 |
| `content`        | bytes | 是   | 存档内容（`bytes`）：gzip 压缩后的 protobuf 线格式字节（**明文，无存档级加密**；JSON 模式下以 base64 字符串呈现），大小不超过 **1MB（1,048,576 字节）** |
| `forceOverwrite` | bool   | 否   | 强制覆盖标志，默认 `false`；为 `true` 时跳过 CAS 冲突检测直接写入（适用于本地存档损坏等场景）   |

#### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "version": 6
  }
}
```

| 字段      | 类型  | 说明                       |
|-----------|-------|----------------------------|
| `version` | int64 | 服务端确认写入的版本号     |

#### 冲突响应

仅当服务端**已存在该 `key` 的存档**且版本校验不通过时返回 `code: 2409`，响应体携带服务端最新版本号。客户端需先下载最新存档合并后再重试。

触发条件：普通上传时 `baseVersion` 与服务端当前版本不一致；或 `forceOverwrite=true` 时 `newVersion` 未超过服务端当前版本。首次上传（服务端无该 `key` 存档）不会返回 2409。

```json
{
  "code": 2409,
  "message": "save conflict",
  "data": {
    "currentVersion": 8
  }
}
```

| 字段             | 类型  | 说明                     |
|------------------|-------|--------------------------|
| `currentVersion` | int64 | 服务端当前最新版本号     |

> 注意：冲突响应的 `code` 在 `data` 字段内（而非顶层），HTTP status 仍为 200。

#### 错误响应

| 业务错误码 | message                                       | 说明                                             |
|------------|-----------------------------------------------|--------------------------------------------------|
| `2400`     | `key is required`                             | 缺少 `key` 字段                                  |
| `2400`     | `newVersion must be greater than 0`           | `newVersion` 不合法                              |
| `2400`     | `sign is required`                            | 缺少签名                                         |
| `2400`     | `content is required`                         | 缺少存档内容                                     |
| `2400`     | `content exceeds size limit`                  | 存档内容超过 1MB 限制                            |
| `2400`     | `baseVersion must be non-negative`            | `baseVersion` 为负数                             |
| `2400`     | `newVersion must be greater than baseVersion` | 服务端已有该 `key` 存档时，普通上传 `newVersion` 未大于 `baseVersion`（首次上传不受此限制） |
| `2401`     | `missing authorization token`                 | 未携带有效 Token，需重新登录                     |
| `2409`     | `save conflict`                               | 版本冲突，`data.currentVersion` 为最新版本（仅服务端已有该 `key` 存档时返回） |
| `2500`     | `internal server error`                       | 服务端内部错误                                   |

---

## 下载存档

### `POST /api/save/download`

批量下载玩家的最新存档。支持同时请求多个 `key`，返回顺序与请求 `keys` 一致。

若某个 `key` 在服务端无存档记录（如新玩家），对应条目的 `version` 为 `0`、`content` 为空（protobuf 下为空字节，JSON 下为空字符串），客户端按首次初始化处理，**不会**返回错误码。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求体

```json
{
  "keys": ["base", "item"]
}
```

| 字段   | 类型            | 必填 | 说明                                     |
|--------|-----------------|------|------------------------------------------|
| `keys` | array\<string\> | 是   | 要下载的存档分类标识列表，不可为空       |

#### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "key": "base",
        "version": 6,
        "sign": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
        "content": "H4sIAAAAAAAAClPyK0pLTFUAAJ6zBM8fAAAA"
      },
      {
        "key": "item",
        "version": 0,
        "sign": "",
        "content": ""
      }
    ]
  }
}
```

`data.items` 顺序与请求 `keys` 一致，每个条目结构如下：

| 字段      | 类型   | 说明                                             |
|-----------|--------|--------------------------------------------------|
| `key`     | string | 存档分类标识                                     |
| `version` | int64  | 当前版本号；`0` 表示服务端无此 key 的存档记录   |
| `sign`    | string | 存档签名：线格式字节（gzip 压缩后）的 SHA256 小写十六进制，用于下载后完整性校验 |
| `content` | bytes | 存档内容（`bytes`）：gzip 压缩后的 protobuf 线格式字节（**明文，无存档级加密**；JSON 模式下以 base64 字符串呈现）；无存档时为空 |

#### 错误响应

| 业务错误码 | message                         | 说明                               |
|------------|---------------------------------|------------------------------------|
| `2400`     | `keys is required`              | `keys` 为空                        |
| `2401`     | `missing authorization token`   | 未携带有效 Token，需重新登录       |
| `2500`     | `internal server error`         | 服务端内部错误                     |

---

## 存档上传流程

客户端完整的存档同步决策流程：

```
游戏启动 / 存档同步时机
         │
         ▼
POST /api/save/download  { keys: ["base"] }
         │
         ├─ items[0].version == 0
         │       → 服务端无存档，直接使用本地存档（或初始化）
         │         如需上传本地存档，baseVersion 可传当前本地版本号（服务端忽略该字段）
         │
         └─ items[0].version > 0
                  │  保存 serverVersion = items[0].version
                  ▼
         比较 serverVersion 与 localVersion
                  │
                  ├─ serverVersion == localVersion
                  │       → 版本一致，无需同步
                  │
                  ├─ serverVersion > localVersion
                  │       → 服务端更新，提示玩家：
                  │         "检测到云端存档比本地更新，是否使用云端存档？"
                  │              │
                  │              ├─ 确认 → 应用服务端存档，覆盖本地
                  │              │
                  │              └─ 取消 → 保留本地存档
                  │                        可选：以 forceOverwrite=true 强制上传
                  │
                  └─ serverVersion < localVersion
                          → 本地更新，上传本地存档
                          → baseVersion = serverVersion
                          → newVersion  = localVersion
                          │
                          ▼
                  POST /api/save/upload
                          │
                          ├─ code:0    → 上传成功，更新本地 baseVersion = newVersion
                          │
                          └─ code:2409 → 版本冲突
                                  │
                                  ▼
                          重新 POST /api/save/download
                          获取最新存档（data.items[0].currentVersion）
                                  │
                                  ▼
                          根据业务逻辑合并存档
                          （优先本地 / 优先服务端 / 手动选择）
                                  │
                                  ▼
                          重新上传合并后的存档
                          baseVersion = currentVersion
                          newVersion  > currentVersion
```

---

## 冲突处理策略

### `forceOverwrite` 使用场景

```
本地存档损坏 / 无法完成正常合并
         │
         ▼
POST /api/save/upload
{
  "key": "base",
  "forceOverwrite": true,
  "baseVersion": <任意>,        ← 服务端忽略此字段做冲突检测
  "newVersion":  <必须 > 服务端 currentVersion>,
  "sign": "...",
  "content": "..."
}
         │
         ├─ code:0    → 覆盖成功
         └─ code:2409 → newVersion 未超过服务端版本，先下载最新版本后重试
```

> **注意**：`forceOverwrite=true` 会放弃冲突保护。多设备同时使用此标志时，后提交者会覆盖先提交者，请仅在确实需要强制写入时使用。

---

## 错误处理

### 客户端建议处理策略

| 错误码 | 处理方式                                                                                     |
|--------|----------------------------------------------------------------------------------------------|
| `2400` | 检查请求参数，修复后重试；通常是客户端逻辑错误                                               |
| `2401` | Token 失效，重新调用 `/auth/login` 获取新 Token 后重试                                       |
| `2409` | 版本冲突：下载最新存档，按业务逻辑合并后重新上传                                             |
| `2500` | 服务端内部错误：弹出提示，提供"重试"按钮；可上报客户端日志辅助排查                          |
| 网络错误（超时 / 无响应） | 检查网络连接，提示用户重试；建议对上传操作做幂等重试                        |

### 存档大小限制

单次上传的 `content` 字段最大为 **1MB（1,048,576 字节）**。超出限制将返回 `code: 2400`，客户端应在上传前做本地校验。内容较大时，建议客户端压缩后上传。
