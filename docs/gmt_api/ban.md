# 封禁管理模块

> 所有接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 批量封禁玩家

`POST /api/gmt/ban/batchBan`

### Request Body

```typescript
{
  playerIds: number[]
  reason: string
}
```

### 请求约束

- `playerIds` 至少 1 个，最多 100 个
- `reason` 必填

### Response Data

```typescript
interface BatchBanResp {
  successIds: number[]
  failedItems?: BanFailedItem[]
}

interface BanFailedItem {
  playerId: number
  message: string
}
```

### 错误码

| code | 说明 |
|------|------|
| `3400` | 请求参数错误 |
| `3401` | 未认证或令牌无效 |
| `3500` | 服务内部错误 |

---

## 批量解封玩家

`POST /api/gmt/ban/batchUnban`

### Request Body

```typescript
{
  playerIds: number[]
  reason: string
}
```

### 请求约束

- `playerIds` 至少 1 个，最多 100 个
- `reason` 必填

### Response Data

```typescript
interface BatchUnbanResp {
  successIds: number[]
  failedItems?: BanFailedItem[]
}
```

### 错误码

| code | 说明 |
|------|------|
| `3400` | 请求参数错误 |
| `3401` | 未认证或令牌无效 |
| `3500` | 服务内部错误 |

---

## 查询封禁记录

`GET /api/gmt/ban/records`

### Query Parameters

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `playerId` | number | 否 | 按玩家 ID 过滤 |
| `page` | number | 否 | 页码，默认 1 |
| `pageSize` | number | 否 | 每页数量，默认 20，最大 100 |

### Response Data

```typescript
interface QueryBanRecordsResp {
  items: BanRecordItem[]
  total: number
  page: number
  pageSize: number
  totalPages: number
}

interface BanRecordItem {
  id: string
  playerId: number
  reason: string
  createdAt: number
}
```

### 字段说明

| 字段 | 说明 |
|------|------|
| `createdAt` | 封禁时间，Unix 毫秒时间戳 |

### 错误码

| code | 说明 |
|------|------|
| `3400` | 请求参数错误 |
| `3401` | 未认证或令牌无效 |
| `3500` | 服务内部错误 |

---

## 接口汇总

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/gmt/ban/batchBan` | 批量封禁玩家 |
| POST | `/api/gmt/ban/batchUnban` | 批量解封玩家 |
| GET | `/api/gmt/ban/records` | 分页查询封禁记录 |
