# 充值白名单模块

> 所有接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

充值白名单用于测试/联调：被列入白名单的账号在 **Platform 下单时直接支付完成**（无需真实扣款）。

- 白名单记录以**玩家账号（`openId`）**维度存取；支持按 `playerId` 或 `account`（openId）添加。
- 按 `playerId` 添加/删除时**只校验现存账号**：账号不存在或已注销（`openId` 以 `del_` 开头）会返回 `account not found`，不会写入。
- 白名单按 `openId` 存储，账号注销后重新注册会复用相同的原始 `openId`，因此**注销重登后白名单依然生效**。
- 添加、删除操作会写入操作日志。

---

## 添加白名单账号

`POST /api/gmt/whitelist/add`

### Request Body

```typescript
{
  account?: string   // 白名单账号（openId）；与 playerId 至少提供一个
  playerId?: number  // 玩家唯一标识；与 account 至少提供一个（只校验现存账号）
}
```

### 请求约束

- `account` 与 `playerId` **至少提供一个**；两者同时提供时优先使用 `account`。
- 按 `playerId` 添加时，账号不存在或已注销返回 `account not found`。

### Response Data

```typescript
interface WhitelistAddResp {
  openId: string         // 入库/已存在的白名单账号（原始身份）
  alreadyExists: boolean // true 表示该账号已存在，本次为幂等返回
  createdAt: number      // 创建时间，Unix 毫秒时间戳
}
```

### 错误码

| code | 说明 |
|------|------|
| `3400` | 请求参数错误（未提供 account 或 playerId） |
| `3401` | 未认证或令牌无效 |
| `3500` | 服务内部错误（message 为 `account not found` 表示按 playerId 解析不到现存账号） |

---

## 删除白名单账号（物理删除）

`POST /api/gmt/whitelist/delete`

### Request Body

```typescript
{
  openId: string   // 白名单账号（openId），必填（仅支持按账号删除）
}
```

### 请求约束

- `openId` **必填**。
- 幂等语义：账号不在白名单时同样返回成功。

### Response Data

```typescript
null
```

### 错误码

| code | 说明 |
|------|------|
| `3400` | 请求参数错误（未提供 openId） |
| `3401` | 未认证或令牌无效 |
| `3500` | 服务内部错误 |

---

## 查询白名单列表

`GET /api/gmt/whitelist/list`

无需过滤条件，仅返回分页后的全部白名单记录。

### Query Parameters

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `page` | number | 否 | 页码，默认 1 |
| `pageSize` | number | 否 | 每页数量，默认 20，最大 100 |

### Response Data

```typescript
interface WhitelistQueryResp {
  items: WhitelistItem[]
  total: number
  page: number
  pageSize: number
  totalPages: number
}

interface WhitelistItem {
  openId: string    // 白名单账号
  createdAt: number // 加入白名单时间，Unix 毫秒时间戳
}
```

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
| POST | `/api/gmt/whitelist/add` | 添加白名单账号（account 或 playerId） |
| POST | `/api/gmt/whitelist/delete` | 删除白名单账号（物理删除） |
| GET | `/api/gmt/whitelist/list` | 分页查询白名单列表 |