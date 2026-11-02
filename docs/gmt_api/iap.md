# 内购订单管理

> 以下接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / TypeScript 类型定义）见 [README.md](README.md)。

GMT 作为 BFF（Backend For Frontend），接收 Web 前端的 JWT 鉴权请求，转发至后端服务并签名：

- **渠道支付视角**（`orders` / `replenish`）转发至 Platform，关注渠道支付状态与通知。
- **业务履约视角**（`gameOrders` / `payStats`）转发至 GameLogic，关注业务状态机、发奖、奖励内容与玩家充值统计。

两侧数据互为补充，前端订单详情页可同时展示。所有接口均需携带 `Authorization: Bearer <token>`。

> 所有面向客户端的接口 HTTP 状态码恒为 `200`，业务结果统一通过响应体 `code` 字段区分：`0` 表示成功，非 `0` 为业务错误码。

## 内购错误码

| 错误码 | 来源 | 说明 |
|--------|------|------|
| `10401` | GMT 本身 | 未认证或令牌无效（JWT 中间件拒绝） |
| `3400` | GMT 本身 | 请求参数错误 |
| `1420` | Platform | 订单不存在 |
| `1422` | Platform | 订单状态不允许此操作 |
| `1426` | Platform | 乐观锁冲突，稍后重试 |
| `1427` | Platform | 渠道交易ID已被其他订单使用 |
| `3500` | GMT 本身 | 服务器内部错误（含 Platform / GameLogic 服务不可用） |

- `1420` / `1422` / `1426` / `1427` 由 GMT 原样透传 Platform 返回，仅出现在转发至 Platform 的接口（`orders` / `replenish`）。
- `gameOrders` 转发至 GameLogic，GameLogic 侧仅返回参数错误 / 内部错误，GMT 将其统一屏蔽为 `3500`，不透传业务细节。

---

## GET /api/gmt/iap/orders — 渠道支付订单查询

转发至 Platform，分页查询渠道支付订单。`orderId` 非空时精确匹配并忽略其他条件。

**Query 参数**

| 参数名          | 类型   | 必填 | 说明 |
|-----------------|--------|------|------|
| `orderId`       | string | 否   | 订单号，精确匹配；同时匹配业务订单号与三方订单号（渠道侧交易ID），输入任一种均可命中 |
| `playerId`      | number | 否   | 玩家唯一标识，`0` 表示不过滤 |
| `paymentStatus` | string | 否   | 支付状态：`WAIT` / `SUCCESS` / `FAILED` |
| `channel`       | string | 否   | 支付渠道：`apple` / `google` |
| `startTime`     | number | 否   | 创建时间下限（Unix 毫秒） |
| `endTime`       | number | 否   | 创建时间上限（Unix 毫秒） |
| `page`          | number | 否   | 页码，从 `1` 开始，默认 `1` |
| `pageSize`      | number | 否   | 每页条数，默认 `20`，最大 `100` |

> 筛选条件为「与」关系，可组合使用；仅当 `orderId` 非空时其他条件被忽略。

**成功响应** `code = 0`，`data` 为分页结构（`items` / `total` / `page` / `pageSize` / `totalPages`，`items` 按创建时间倒序）：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "orderId": "20260630153045123456",
        "playerId": 10001,
        "productId": "diamond_60",
        "channel": "apple",
        "thirdTransactionId": "GPA.3388-1234-5678-90123",
        "paymentStatus": "SUCCESS",
        "notifyStatus": "SUCCESS",
        "notifyCount": 1,
        "amount": 600,
        "currency": "USD",
        "createTime": 1751280645000,
        "payTime": 1751280700000
      }
    ],
    "total": 128,
    "page": 1,
    "pageSize": 20,
    "totalPages": 7
  }
}
```

**OrderItem 字段说明**（对应 Platform 的 `GmtOrderItem`）

| 字段                   | 类型   | 说明 |
|------------------------|--------|------|
| `orderId`              | string | 业务订单号 |
| `playerId`             | number | 玩家唯一标识 |
| `productId`            | string | 商品ID |
| `channel`              | string | 支付渠道：`apple` / `google` |
| `thirdTransactionId`   | string | 渠道侧交易ID（Apple transactionId / Google purchaseToken） |
| `paymentStatus`        | string | 渠道支付状态：`WAIT` / `SUCCESS` / `FAILED` |
| `notifyStatus`         | string | 对 GameLogic 的通知状态：`WAIT` / `SUCCESS` / `FAILED` |
| `notifyCount`          | number | 累计通知次数 |
| `amount`               | number | 支付金额（分） |
| `currency`             | string | 币种（如 `USD` / `CNY`） |
| `createTime`           | number | 创建时间（Unix 毫秒） |
| `payTime`              | number | 支付成功时间（Unix 毫秒），`0` 表示未支付 |

---

## GET /api/gmt/iap/gameOrders — 业务订单查询

转发至 GameLogic，分页查询游戏侧业务订单。`orderId` 非空时精确匹配并忽略其他条件。

**Query 参数**

| 参数名         | 类型   | 必填 | 说明 |
|----------------|--------|------|------|
| `orderId`      | string | 否   | 业务订单号，精确匹配 |
| `playerId`     | number | 否   | 玩家唯一标识，`0` 表示不过滤 |
| `status`       | string | 否   | 业务状态：`WAIT_PAY` / `PAID` / `DELIVERED` / `CLOSED` |
| `channel`      | string | 否   | 支付渠道：`apple` / `google` |
| `startTime`    | number | 否   | 创建时间下限（Unix 毫秒） |
| `endTime`      | number | 否   | 创建时间上限（Unix 毫秒） |
| `page`         | number | 否   | 页码，从 `1` 开始，默认 `1` |
| `pageSize`     | number | 否   | 每页条数，默认 `20`，最大 `100` |

**成功响应** `code = 0`，`data` 为分页结构，`items` 为 `GameOrderItem` 列表：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "orderId": "20260630153045123456",
        "playerId": 10001,
        "productId": "diamond_60",
        "channel": "apple",
        "status": "DELIVERED",
        "awardTag": "first_purchase_bonus",
        "award": "diamond:60,gold:1000",
        "notifyId": "0f8c2a1e-9b3d-4f6a-8c2e-1a2b3c4d5e6f",
        "amount": 600,
        "paidAt": 1751280700000,
        "deliveredAt": 1751280720000,
        "expireTime": 1751282445000,
        "createTime": 1751280645000,
        "updateTime": 1751280720000
      }
    ],
    "total": 128,
    "page": 1,
    "pageSize": 20,
    "totalPages": 7
  }
}
```

**GameOrderItem 字段说明**（对应 GameLogic 的 `GmtGameOrderItem`）

| 字段           | 类型   | 说明 |
|----------------|--------|------|
| `orderId`      | string | 业务订单号 |
| `playerId`     | number | 玩家唯一标识 |
| `productId`    | string | 商品ID |
| `channel`      | string | 支付渠道：`apple` / `google` |
| `status`       | string | 业务状态：`WAIT_PAY` 待支付 / `PAID` 已支付待领取 / `DELIVERED` 已发奖 / `CLOSED` 已关闭 |
| `awardTag`     | string | 奖项标识，客户端创建订单时传入，原样回传 |
| `award`        | string | 奖励内容，客户端创建订单时传入，原样回传 |
| `notifyId`     | string | 最后一次收到的 Platform 支付通知唯一ID |
| `amount`       | number | 支付金额（分），由 Platform 支付成功回调写入，`0` 表示未支付 |
| `paidAt`       | number | 支付成功时间（Unix 毫秒），`0` 表示未支付 |
| `deliveredAt`  | number | 发奖成功时间（Unix 毫秒），`0` 表示未发奖 |
| `expireTime`   | number | 订单过期时间（Unix 毫秒） |
| `createTime`   | number | 创建时间（Unix 毫秒） |
| `updateTime`   | number | 最后更新时间（Unix 毫秒） |

---

## GET /api/gmt/iap/payStats — 玩家充值统计查询

转发至 GameLogic，查询指定玩家的充值（付费）统计。统计口径与用户标签匹配接口（`POST /api/iap/userTags`）使用的付费统计**完全一致**，运营可据此核对标签命中的付费依据。

- 统计范围：业务状态为 `PAID` / `DELIVERED` 的订单；
- 金额单位：分；
- 数据来源：GameLogic `pay_orders` 集合实时聚合（不读取标签匹配使用的 Redis 缓存，避免展示滞后）；
- 玩家无任何已支付订单时，各金额字段均返回 `0`。

**Query 参数**

| 参数名     | 类型   | 必填 | 说明 |
|------------|--------|------|------|
| `playerId` | number | 是   | 玩家唯一标识，必须 `> 0` |

**成功响应** `code = 0`：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "playerId": 10001,
    "lastPay": 600,
    "last4Pay": 2400,
    "monthPay": 3600,
    "totalPay": 12800
  }
}
```

**PlayerPayStats 字段说明**（对应 GameLogic 的 `GmtPlayerPayStatsResp`）

| 字段        | 类型   | 说明 |
|-------------|--------|------|
| `playerId`  | number | 玩家唯一标识 |
| `lastPay`   | number | 最近 1 笔充值金额（分），`0` 表示无已支付订单 |
| `last4Pay`  | number | 最近 4 笔充值金额合计（分） |
| `monthPay`  | number | 最近 30 天充值金额合计（分） |
| `totalPay`  | number | 累积充值金额合计（分） |

> `playerId` 缺失或 `<= 0` 时返回 `3400`；GameLogic 查询失败时 GMT 统一返回 `3500`。

---

## POST /api/gmt/iap/replenish — 手动补单

转发至 Platform，模拟三方支付回调，将指定订单强制标记为支付成功并触发 GameLogic 发货通知。

仅允许对 `WAIT` 状态订单补单；对已支付（`SUCCESS`）订单执行时幂等返回成功，并触发一次补偿通知（防止通知丢失），不再重复扣款。

**请求体**

```json
{
  "orderId": "20260630153045123456",
  "thirdTransactionId": "GPA.3388-1234-5678-90123",
  "reason": "player paid but order not updated, confirmed on channel backend"
}
```

| 字段                 | 类型   | 必填 | 说明 |
|----------------------|--------|------|------|
| `orderId`            | string | 是   | 业务订单号 |
| `thirdTransactionId` | string | 是   | 渠道交易ID（渠道后台获取，需未被其他订单使用） |
| `reason`             | string | 是   | 补单原因，用于审计 |
| `operatorId`         | string | 否   | 忽略请求体传入值，操作人ID由服务端从 JWT 登录态注入，防止伪造 |

**成功响应** `HTTP 200`

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "orderId": "20260630153045123456",
    "isSuccess": true,
    "message": "replenished successfully"
  }
}
```

`data` 为 `GmtReplenishOrderResp`（`orderId` / `isSuccess` / `message`）：
- `isSuccess = true`、`message = "replenished successfully"`：补单成功并已通知 GameLogic；
- 订单已支付时同样返回 `isSuccess = true`，`message = "order already paid, notification resent"`。

## local/dev 环境模拟补单（非接口）

> 该功能**不是接口**，而是 Platform 服务内置的开发辅助定时任务（`SimulateMarkPaid`），用于本地联调"渠道已支付但平台未收到回调"的补单链路。

**启用条件**

- 仅 `APP_ENV=local` 或 `APP_ENV=dev` 时注册并运行（见 `services/platform/service.go` 中 `initScheduler` 的条件注册逻辑，通过 `IsLocalOrDev()` 判定）；`staging` / `prod` 等其他环境不注册该任务，不影响真实支付流程。

**行为说明**

| 项目     | 说明 |
|----------|------|
| 触发周期 | 每 5 秒执行一次，单次最多处理 10 个订单 |
| 扫描范围 | 所有 `paymentStatus == WAIT` 的支付订单 |
| 处理动作 | 自动模拟渠道支付成功：`thirdTransactionId = mock_tx_{orderId}`，走与真实支付一致的乐观锁（CAS）标记链路，随后回调 GameLogic 通知 |
| 后续状态 | 订单落入正常 `SUCCESS` 链路；若回调失败，由现有补通知定时任务（`retryPendingNotifications`）继续补偿 |

**注意事项**

- **local/dev 环境下所有待支付订单都会被自动"模拟支付"**，无需真实渠道回调即可完成支付流转。
- 若在 local/dev 环境同时联调**真实支付流程**（创建订单后等待渠道验证收据），模拟补单可能在渠道验证前抢先标记订单，导致回调 GameLogic 的交易ID 为 `mock_tx_` 前缀的模拟值，订单无法与渠道侧对账。此时应改用非 local/dev 环境，或暂时停止该服务进行真实支付联调。
- 与手动补单接口 `POST /api/gmt/iap/replenish` 相互独立：补单接口需人工传入渠道交易ID，模拟补单使用自动生成的模拟交易ID，两者均可将 `WAIT` 订单标记为支付成功。

## TypeScript 类型定义

```typescript
// 通用响应包装
interface ApiResponse<T = null> {
  code: number      // 0 表示成功，非 0 为业务错误码
  message: string
  data?: T          // 成功时存在，失败时不存在
}

// 渠道支付订单查询请求参数
interface QueryOrderParams {
  orderId?: string   // 同时匹配业务订单号与三方订单号
  playerId?: number
  paymentStatus?: 'WAIT' | 'SUCCESS' | 'FAILED'
  channel?: 'apple' | 'google'
  startTime?: number
  endTime?: number
  page?: number
  pageSize?: number
}

// 渠道支付订单（Platform 侧）
interface OrderItem {
  orderId: string
  playerId: number
  productId: string
  channel: 'apple' | 'google'
  thirdTransactionId: string
  paymentStatus: 'WAIT' | 'SUCCESS' | 'FAILED'
  notifyStatus: 'WAIT' | 'SUCCESS' | 'FAILED'
  notifyCount: number
  amount: number               // 单位：分
  currency: string
  createTime: number           // Unix 毫秒
  payTime: number              // Unix 毫秒，0 表示未支付
}

// 渠道支付订单查询响应
interface QueryOrderResp {
  items: OrderItem[]
  total: number
  page: number
  pageSize: number
  totalPages: number
}

// 业务订单查询请求参数
interface QueryGameOrderParams {
  orderId?: string
  playerId?: number
  status?: 'WAIT_PAY' | 'PAID' | 'DELIVERED' | 'CLOSED'
  channel?: 'apple' | 'google'
  startTime?: number
  endTime?: number
  page?: number
  pageSize?: number
}

// 业务订单（GameLogic 侧）
interface GameOrderItem {
  orderId: string
  playerId: number
  productId: string
  channel: 'apple' | 'google'
  status: 'WAIT_PAY' | 'PAID' | 'DELIVERED' | 'CLOSED'
  awardTag: string
  award: string
  notifyId: string
  amount: number               // 支付金额（分），0 表示未支付
  paidAt: number               // Unix 毫秒，0 表示未支付
  deliveredAt: number          // Unix 毫秒，0 表示未发奖
  expireTime: number           // Unix 毫秒
  createTime: number           // Unix 毫秒
  updateTime: number           // Unix 毫秒
}

// 业务订单查询响应
interface QueryGameOrderResp {
  items: GameOrderItem[]
  total: number
  page: number
  pageSize: number
  totalPages: number
}

// 玩家充值统计查询请求参数
interface QueryPayStatsParams {
  playerId: number             // 必填，> 0
}

// 玩家充值统计（与用户标签匹配的付费统计口径一致）
interface PlayerPayStats {
  playerId: number
  lastPay: number              // 最近 1 笔充值金额（分）
  last4Pay: number             // 最近 4 笔充值金额合计（分）
  monthPay: number             // 最近 30 天充值金额合计（分）
  totalPay: number             // 累积充值金额合计（分）
}

// 手动补单请求参数
interface ReplenishOrderParams {
  orderId: string
  thirdTransactionId: string
  reason: string
  operatorId?: string          // 可选，服务端忽略并覆盖为登录态操作人
}

// 手动补单响应
interface ReplenishOrderResp {
  orderId: string
  isSuccess: boolean
  message: string
}
```