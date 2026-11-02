# 玩家查询接口文档

> 客户端：Unity (C#)
> 协议：HTTP/1.1，JSON / Protobuf
> Base URL：由部署环境决定

---

## 通用约定

- 该接口需要登录，需携带 `Authorization: Bearer <token>`
- 业务错误统一通过响应体中的 `code` 字段表示
- 所有时间戳均为 Unix 毫秒时间戳

---

## 查询玩家信息

### `POST /api/player/query`

按玩家 ID 或邀请码查询玩家基本信息。

### 请求体

```json
{
  "playerId": 10001,
  "inviteCode": ""
}
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `playerId` | int64 | 否 | 玩家 ID；与 `inviteCode` 二选一，同时传入时优先使用该字段 |
| `inviteCode` | string | 否 | 邀请码；与 `playerId` 二选一 |

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "playerId": 10001,
    "nickname": "Player10001",
    "level": 15,
    "avatar": "https://example.com/avatar.png"
  }
}
```

### 响应字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `playerId` | int64 | 玩家 ID |
| `nickname` | string | 玩家昵称 |
| `level` | int32 | 玩家等级 |
| `avatar` | string | 玩家头像 |

### 错误码

| code | message | 说明 |
|------|---------|------|
| `2401` | `missing authorization token` | 未登录或令牌无效 |
| `2404` | `player not found` | 玩家不存在 |
| `2450` | `playerId or inviteCode is required` | `playerId` 与 `inviteCode` 均未提供 |
| `2451` | `invalid inviteCode` | 邀请码不合法 |
| `2452` | `player query failed` | 玩家查询失败 |
| `2500` | `internal server error` | 服务内部错误 |
