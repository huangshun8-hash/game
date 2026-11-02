# Proto 动态数据管理接口文档

> 本文档供 GMT Web 端对接使用，描述 Proto 模板与玩家分片数据相关 API。

## 基本约定

| 项目 | 说明 |
|------|------|
| Base URL | GMT 服务地址，如 `http://gmt-host:port` |
| 认证方式 | JWT Token，通过请求头 `Authorization: Bearer <token>` 传递 |
| 响应格式 | 统一 JSON，业务结果通过 `code` 字段区分 |

### 统一响应结构

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

---

## 1. 查询 Proto 模板是否存在

`GET /api/proto/exists`

用于在页面初始化时判断当前是否已上传全局 Proto 模板。

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "exists": true,
    "protoName": "player",
    "messageList": ["base", "item", "board", "cacheItem"],
    "md5": "d41d8cd98f00b204e9800998ecf8427e",
    "updateTime": 1689321600000
  }
}
```

当模板不存在时：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "exists": false
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| exists | bool | 是否存在全局 Proto 模板 |
| protoName | string | 模板名称；不存在时为空 |
| messageList | string[] | `Player` 直接 message 字段名组成的存档 key 列表 |
| md5 | string | Proto 源码 MD5 |
| updateTime | int64 | 最后更新时间（毫秒时间戳） |

---

## 2. 上传 Proto 文件

`POST /api/proto/upload`

上传或覆盖全局唯一的 Proto 模板文件。

### 请求

| 项目 | 值 |
|------|------|
| Method | `POST` |
| Path | `/api/proto/upload` |
| Content-Type | `multipart/form-data` |

### 表单字段

| 字段名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| protoName | string | 是 | Proto 模板唯一业务标识 |
| file | file | 是 | Proto 文件，最大 1MB |

### Player 分片映射

当上传的模板包含顶层 `Player` message 时，服务端将其直接、非 map 的 message 类型字段名作为玩家存档 key，并将该字段引用的 message 作为分片数据类型。以 `Base base = 1;` 为例：`messageList` 与后续请求中的 `messageName` 都应使用 `base`，其数据按 `Base` 的字段定义编码；`item`、`board` 等字段遵循相同规则。不要传入 `Base` 或 `DefaultData` 作为存档 key。

### 约束

- Proto 文件内不允许 `import`
- 文件中必须包含至少一个可用的 message
- 若系统已存在模板，上传的 `protoName` 必须与现有一致
- 覆盖上传时会校验兼容性，不允许破坏性变更
- 若文件内容 MD5 与当前完全一致，拒绝重复上传

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "protoName": "player",
    "messageList": ["base", "item", "board", "cacheItem"],
    "md5": "d41d8cd98f00b204e9800998ecf8427e",
    "updateTime": 1689321600000
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| protoName | string | Proto 模板名称 |
| messageList | string[] | `Player` 直接 message 字段名组成的存档 key 列表 |
| md5 | string | Proto 源码 MD5 校验值 |
| updateTime | int64 | 更新时间（毫秒时间戳） |

---

## 3. 查询玩家分片数据

`GET /api/proto/playerData/get`

获取指定玩家的存档分片。支持按 `Player` 字段名查询单个分片或查询全部分片。

### Query 参数

| 参数名 | 类型 | 必填 | 说明 |
|--------|------|------|------|
| playerId | int64 | 是 | 玩家唯一 ID |
| messageName | string | 否 | `Player` 直接字段名，即存档 key；传入时仅返回该分片，为空时返回全部分片 |

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "playerId": 10001,
    "items": [
      {
        "messageName": "base",
        "data": {
          "lv": 10,
          "gold": 5000
        },
        "updateTime": 1689321600000
      }
    ]
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| playerId | int64 | 玩家唯一 ID |
| items | array | 分片数据列表 |
| items[].messageName | string | `Player` 直接字段名，即存档 key |
| items[].data | object | JSON 格式分片数据 |
| items[].updateTime | int64 | 分片最后修改时间（毫秒时间戳） |

---

## 4. 批量保存玩家分片数据

`POST /api/proto/playerData/save`

将 JSON 格式的分片数据写回玩家数据。

### 请求体

```json
{
  "playerId": 10001,
  "items": [
    {
      "messageName": "base",
      "data": {
        "lv": 15,
        "gold": 8000
      }
    }
  ]
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| playerId | int64 | 是 | 玩家唯一 ID |
| items | array | 是 | 待保存的分片数据列表，不可为空 |
| items[].messageName | string | 是 | `Player` 直接字段名，即存档 key |
| items[].data | object | 是 | JSON 格式分片数据；服务端按 `Player.<messageName>` 自动解析对应 message 类型 |

### 约束

- `items` 不可为空，单次最多 50 条
- 同一请求中 `messageName` 不可重复
- `messageName` 必须是已上传模板中 `Player` 的直接 message 字段名
- 必须先上传 Proto 模板后才能保存数据

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "updateTime": 1689321600000
  }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| updateTime | int64 | 本次操作完成时间（毫秒时间戳） |

---

## 错误码表

| code | 说明 |
|------|------|
| `3700` | 请求参数错误 |
| `3701` | Proto 语法校验失败 |
| `3702` | Proto 文件包含 import |
| `3703` | Proto 文件未定义任何可用 message |
| `3704` | 检测到破坏性变更 |
| `3705` | 文件内容未变化 |
| `3706` | Proto 模板不存在 |
| `3707` | `Player` 字段或存档 key 不存在 |
| `3708` | 玩家分片数据不存在 |
| `3709` | 数据反序列化失败 |
| `3710` | 数据序列化失败 |
| `3711` | 模板名称冲突 |
| `3750` | 服务内部错误 |

---

## 使用流程

```
1. GET /api/proto/exists
       ↓
2. 如不存在或需要更新，则 POST /api/proto/upload
       ↓
3. GET /api/proto/playerData/get?playerId=xxx
       ↓
4. 编辑 JSON 数据
       ↓
5. POST /api/proto/playerData/save
```
