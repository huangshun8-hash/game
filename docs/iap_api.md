# 内购接口文档

> 客户端：Unity (C#)
> 协议：HTTP/1.1，JSON 编码
> Base URL：由部署环境决定，本地开发默认 `http://192.168.2.65:8081`

---

## 目录

-   [通用约定](#通用约定)
-   [创建订单](#创建订单)
-   [领取奖励](#领取奖励)
-   [查询待领取订单列表](#查询待领取订单列表)
-   [查询订单状态](#查询订单状态)
-   [用户标签匹配](#用户标签匹配)
-   [内购流程](#内购流程)
-   [订单状态机](#订单状态机)
-   [错误处理](#错误处理)

---

## 通用约定

### HTTP 状态码约定

所有客户端接口业务错误**始终返回 HTTP status 200**。客户端通过 body 中的 `code` 字段区分业务结果，无需关注 HTTP 状态码。

### 请求头

| Header          | 必填 | 说明                                                                                   |
| --------------- | ---- | -------------------------------------------------------------------------------------- |
| `Content-Type`  | 是   | `application/json`（默认）或 `application/x-protobuf` / `application/protobuf`（可选） |
| `Authorization` | 是   | `Bearer <token>`，Token 由登录接口获取                                                 |

> 响应格式与请求 `Content-Type` 保持一致：发 JSON 收 JSON，发 protobuf 收 protobuf。

每个响应均携带以下 Header：

| Header          | 说明                                                |
| --------------- | --------------------------------------------------- |
| `X-Server-Time` | 服务器当前时间戳（Unix 毫秒），可用于客户端时间校准 |

### 序列化格式

服务端统一根据请求的 `Content-Type` 决定上下行的序列化格式，请求与响应格式保持一致。

| Content-Type                                      | 请求解析方式    | 响应序列化方式  |
| ------------------------------------------------- | --------------- | --------------- |
| `application/json`（或不填）                      | JSON            | JSON            |
| `application/x-protobuf` / `application/protobuf` | Protobuf 二进制 | Protobuf 二进制 |

Proto 定义文件见 `pb/cs.proto`，C# 命名空间为 `Cspb`，相关消息：`CreateOrderReq`、`CreateOrderResp`、`ClaimRewardReq`、`ClaimRewardResp`、`ListPaidOrdersReq`、`ListPaidOrdersResp`、`GetOrderStatusReq`、`GetOrderStatusResp`、`UserTagsReq`、`UserTagsResp`、`ErrorResp`。

> **错误响应的 protobuf 兼容性**：所有 `*Resp` 消息的前两个字段（tag 1 = `code`，tag 2 = `message`）与 `ErrorResp` 保持一致，客户端可直接用具体 `*Resp` 类型解析错误响应，无需额外处理。

### 统一响应结构（JSON 模式）

```json
{
    "code": 0,
    "message": "ok",
    "data": {}
}
```

| 字段      | 类型   | 说明                                    |
| --------- | ------ | --------------------------------------- |
| `code`    | int    | `0` 表示成功，非 `0` 为业务错误码       |
| `message` | string | 结果描述，成功时为`"ok"`                |
| `data`    | object | 成功时的业务数据，失败时为`null` 或省略 |

### 支付渠道(暂定，等接入 SDK 之后才能定)

| 渠道值   | 说明            |
| -------- | --------------- |
| `apple`  | Apple App Store |
| `google` | Google Play     |

### 订单状态

| 状态值      | 说明                               |
| ----------- | ---------------------------------- |
| `WAIT_PAY`  | 等待支付                           |
| `PAID`      | 支付成功，等待客户端主动领奖       |
| `DELIVERED` | 发奖成功，业务闭环                 |
| `CLOSED`    | 已关闭（超时未支付，系统自动关闭） |

所有时间戳均为 **Unix 毫秒**。

---

## 创建订单

### `POST /api/iap/order`

客户端发起内购时调用，创建一笔业务订单。订单创建后有效期为 **30 分钟**，超时未支付将被系统自动关闭。同一玩家使用相同 `payId` 与 `awardTag` 创建时，如存在 `WAIT_PAY` 历史订单，接口直接返回该订单数据而不会创建新订单；`award` 仅随订单原样持久化与回传，不参与重复订单判定。

认证玩家每 **5 秒** 最多调用一次本接口。超出限制时仍返回 HTTP 200，业务响应的 `code` 为 `10429`。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求体

```json
{
    "payId": "diamond_60",
    "channel": "apple",
    "awardTag": "weekly_pack_1",
    "award": "diamond:60,gold:1000"
}
```

| 字段       | 类型   | 必填 | 说明                                                                                      |
| ---------- | ------ | ---- | ----------------------------------------------------------------------------------------- |
| `payId`    | string | 是   | 支付商品 ID（如`diamond_60`），对应后台配置的商品标识，详见：sever_IAPProduct_global.xlsx |
| `channel`  | string | 是   | 支付渠道，暂定`apple` 或 `google`                                                         |
| `awardTag` | string | 否   | 奖项标识，原样持久化，用于标记本笔订单对应的活动/礼包。订单完成时领奖用                   |
| `award`    | string | 否   | 奖励内容，原样持久化与回传，不参与重复订单判定                                            |

#### 成功响应

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "orderId": "1234567890123456789",
        "payId": "diamond_60",
        "channel": "apple",
        "awardTag": "weekly_pack_1",
        "award": "diamond:60,gold:1000"
    }
}
```

| 字段       | 类型   | 说明                         |
| ---------- | ------ | ---------------------------- |
| `orderId`  | string | 全局唯一订单号               |
| `payId`    | string | 商品 ID（历史订单原样回传）  |
| `channel`  | string | 支付渠道（历史订单原样回传） |
| `awardTag` | string | 奖项标识（历史订单原样回传） |
| `award`    | string | 奖励内容（历史订单原样回传） |

#### 错误响应

| 业务错误码 | message                                     | 说明                         |
| ---------- | ------------------------------------------- | ---------------------------- |
| `2400`     | `invalid request parameters`                | 请求体解析失败               |
| `2400`     | `payId is required`                         | 缺少支付商品 ID              |
| `2400`     | `channel must be apple or google`           | 支付渠道不合法               |
| `2401`     | `missing authorization token`               | 未携带有效 Token，需重新登录 |
| `2423`     | `product not found`                         | 商品配置不存在               |
| `10429`    | `too many requests, please try again later` | 5 秒内重复创建订单           |
| `2500`     | `internal server error`                     | 服务端内部错误               |

---

## 领取奖励

### `POST /api/iap/claim`

客户端在确认支付成功后，主动调用此接口领取内购奖励，发奖走的客户端，此接口服务器做订单发奖记录和后期审计使用。服务端会校验订单状态，仅允许状态为 `PAID` 的订单领取。

领取操作具有**幂等性**：已领取（`DELIVERED`）的订单重复调用将直接返回失败。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求体

```json
{
    "orderId": "1234567890123456789",
    "reward": {
        "diamond": 60,
        "gold": 1000
    }
}
```

| 字段      | 类型              | 必填 | 说明                                                     |
| --------- | ----------------- | ---- | -------------------------------------------------------- |
| `orderId` | string            | 是   | 要领取奖励的订单号                                       |
| `reward`  | map<string,int64> | 否   | 本次客户端领取的奖励内容，服务端仅记录（不做校验和发放） |

#### 成功响应

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "orderId": "1234567890123456789",
        "status": "DELIVERED"
    }
}
```

| 字段      | 类型   | 说明                                |
| --------- | ------ | ----------------------------------- |
| `orderId` | string | 订单号                              |
| `status`  | string | 当前订单状态，领取成功为`DELIVERED` |

#### 错误响应

| 业务错误码 | message                                   | 说明                               |
| ---------- | ----------------------------------------- | ---------------------------------- |
| `2400`     | `invalid request parameters`              | 请求体解析失败                     |
| `2400`     | `orderId is required`                     | 缺少订单号                         |
| `2401`     | `missing authorization token`             | 未携带有效 Token，需重新登录       |
| `2420`     | `order not found`                         | 订单不存在或不属于当前玩家         |
| `2422`     | `order is not ready for this operation`   | 订单状态不允许领取（非 PAID）      |
| `2424`     | `delivery failed, please try again later` | 发奖事务失败，订单保持 PAID 可重试 |
| `2426`     | `order reward already claimed`            | 订单已领取，不可重复领奖           |
| `2500`     | `internal server error`                   | 服务端内部错误                     |

---

## 查询待领取订单列表

### `GET /api/iap/orders/paid`

查询当前玩家所有已支付但尚未领取（状态为 `PAID`）的订单。客户端可在游戏启动或支付完成后调用，检查是否有待领取的奖励。
玩家登录调用一次，ws 重连调用一次。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求参数

无请求参数，玩家身份由 Token 解析。

#### 成功响应

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "orders": [
            {
                "orderId": "1234567890123456789",
                "payId": "diamond_60",
                "channel": "apple",
                "awardTag": "weekly_pack_1",
                "award": "diamond:60,gold:1000",
                "paidAt": 1689580800000
            }
        ]
    }
}
```

`data.orders` 为待领取订单列表，每个条目结构如下：

| 字段       | 类型   | 说明                      |
| ---------- | ------ | ------------------------- |
| `orderId`  | string | 订单号                    |
| `payId`    | string | 商品 ID                   |
| `channel`  | string | 支付渠道                  |
| `awardTag` | string | 奖项标识                  |
| `award`    | string | 奖励内容（原样回传）      |
| `paidAt`   | int64  | 支付成功时间（Unix 毫秒） |

> 若无待领取订单，`orders` 为空数组 `[]`。

#### 错误响应

| 业务错误码 | message                       | 说明                         |
| ---------- | ----------------------------- | ---------------------------- |
| `2401`     | `missing authorization token` | 未携带有效 Token，需重新登录 |
| `2500`     | `internal server error`       | 服务端内部错误               |

---

## 查询订单状态

### `GET /api/iap/order/status`

查询单个订单的当前状态，可用于客户端轮询确认支付结果。仅能查询属于当前玩家的订单。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求参数（Query String）

| 参数      | 类型   | 必填 | 说明         |
| --------- | ------ | ---- | ------------ |
| `orderId` | string | 是   | 查询的订单号 |

示例：`GET /api/iap/order/status?orderId=1234567890123456789`

#### 成功响应

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "orderId": "1234567890123456789",
        "payId": "diamond_60",
        "channel": "apple",
        "status": "PAID"
    }
}
```

| 字段      | 类型   | 说明                                                   |
| --------- | ------ | ------------------------------------------------------ |
| `orderId` | string | 订单号                                                 |
| `payId`   | string | 商品 ID                                                |
| `channel` | string | 支付渠道                                               |
| `status`  | string | 业务状态：`WAIT_PAY` / `PAID` / `DELIVERED` / `CLOSED` |

#### 错误响应

| 业务错误码 | message                       | 说明                         |
| ---------- | ----------------------------- | ---------------------------- |
| `2400`     | `invalid request parameters`  | 请求参数解析失败             |
| `2400`     | `orderId is required`         | 缺少订单号                   |
| `2401`     | `missing authorization token` | 未携带有效 Token，需重新登录 |
| `2420`     | `order not found`             | 订单不存在或不属于当前玩家   |
| `2500`     | `internal server error`       | 服务端内部错误               |

---

## 用户标签匹配

### `POST /api/iap/userTags`

根据指定的枚举分组，查询当前玩家匹配的用户标签列表。用于内购礼包/活动的个性化展示（A/B 分组、等级区间、付费区间等条件筛选）。

每个 `enum` 分组仅返回第一条满足条件的 `tag`。

AB 分组匹配规则：配置行 `AbGroup` 为空时仅命中未分组玩家（`group` 为 0）；非空时玩家 `group` 必须命中其一，否则该行不参与匹配。付费/等级区间条件中 0 表示该侧不限制。

#### 请求头

需要携带 `Authorization: Bearer <token>`。可选携带 `Content-Type: application/x-protobuf` 切换为 protobuf 模式（见[通用约定 - 序列化格式](#序列化格式)）。

#### 请求体

```json
{
    "enum": "weekly_gift",
    "group": 1
}
```

| 字段    | 类型   | 必填 | 说明                                                                  |
| ------- | ------ | ---- | --------------------------------------------------------------------- |
| `enum`  | string | 否   | 礼包/活动枚举值，用于筛选对应分组；为空时返回所有分组的匹配结果       |
| `group` | int32  | 否   | 用户分组（AB 分组），0 表示未分组（缺省即 0）；空配置仅命中未分组玩家 |

#### 成功响应

```json
{
    "code": 0,
    "message": "ok",
    "data": {
        "tags": {
            "weekly_gift": "tag_high_pay"
        }
    }
}
```

| 字段   | 类型             | 说明                                                                 |
| ------ | ---------------- | -------------------------------------------------------------------- |
| `tags` | object（键值对） | enum → tag 映射，key 为配置 enum，value 为命中的 tag；无匹配时为`{}` |

#### 错误响应

| 业务错误码 | message                       | 说明                         |
| ---------- | ----------------------------- | ---------------------------- |
| `2400`     | `invalid request parameters`  | 请求体解析失败               |
| `2401`     | `missing authorization token` | 未携带有效 Token，需重新登录 |
| `2500`     | `internal server error`       | 服务端内部错误               |

---

## 内购流程

客户端完整的内购购买流程：

```
玩家点击购买商品
         │
         ▼
POST /api/iap/order  { payId, channel, awardTag, award }
         │
         ├─ code:0 → 获取 orderId
         │       │
         │       ▼
         │  调用渠道 SDK 发起支付（传入 orderId）
         │       │
         │       ├─ 支付成功
         │       │       │
         │       │       ▼
         │       │  （可选）订单到账通知：服务端经 WebSocket 推送
         │       │  cspb.PaySuccessPush（见 ws_api.md「支付成功推送」）
         │       │  （可选）轮询 GET /api/iap/order/status
         │       │  等待 status 变为 PAID
         │       │       │
         │       │       ▼
         │       │  POST /api/iap/claim { orderId, reward }
         │       │       │
         │       │       ├─ code:0 → 领取成功，status = DELIVERED
         │       │       │
         │       │       ├─ code:2422 → 状态不允许，可能还未收到渠道通知
         │       │       │             稍后重试或轮询 order/status
         │       │       │
         │       │       ├─ code:2424 → 发奖失败，订单保持 PAID
         │       │       │             提示玩家重试
         │       │       │
         │       │       └─ code:2426 → 订单已领取，不可重复领奖
         │       │
         │       ├─ 支付取消
         │       │       → 无需额外操作，订单超时自动关闭
         │       │
         │       └─ 支付失败
         │               → 无需额外操作，订单超时自动关闭
         │
         └─ code:非0 → 显示错误提示

---

游戏启动 / 切回前台
         │
         ▼
GET /api/iap/orders/paid
         │
         ├─ orders 不为空
         │       → 逐个调用 POST /api/iap/claim 领取
         │
         └─ orders 为空
                 → 无待领取奖励
```

---

## 订单状态机

```
            ┌─────────────────────────────────────────────┐
            │                                             │
            ▼                                             │
  ┌─────────────┐    渠道支付成功    ┌──────────┐    客户端领奖    ┌───────────┐
  │  WAIT_PAY   │ ─────────────────→ │   PAID   │ ──────────────→ │ DELIVERED │
  └─────────────┘                    └──────────┘                  └───────────┘
         │
         │ 超时未支付
         ▼
  ┌─────────────┐
  │   CLOSED    │
  └─────────────┘
```

-   **WAIT_PAY → PAID**：Platform 收到渠道支付成功通知后，回调 GameLogic 标记为 PAID。
-   **PAID → DELIVERED**：客户端主动调用 `/api/iap/claim` 领取奖励，在事务内完成发奖并标记 DELIVERED。
-   **WAIT_PAY → CLOSED**：订单创建 30 分钟后未支付，系统定时任务自动关闭。

---

## 错误处理

### 客户端建议处理策略

| 错误码                    | 处理方式                                                                               |
| ------------------------- | -------------------------------------------------------------------------------------- |
| `2400`                    | 检查请求参数，修复后重试；通常是客户端逻辑错误                                         |
| `2401`                    | Token 失效，重新调用`/auth/login` 获取新 Token 后重试                                  |
| `2420`                    | 订单不存在：确认 orderId 正确，如果是过期订单需重新创建                                |
| `2422`                    | 订单状态不允许操作：可能支付通知尚未到达，稍后轮询`order/status` 确认后重试            |
| `2423`                    | 商品不存在：检查 payId 是否正确，可能后台下架了该商品                                  |
| `2424`                    | 发奖失败：订单仍为 PAID 状态，提示玩家点击"重试"再次领取                               |
| `2426`                    | 订单已领取：奖励已发放，客户端无需再次请求，直接视为成功即可                           |
| `2500`                    | 服务端内部错误：弹出提示，提供"重试"按钮；可上报客户端日志辅助排查                     |
| 网络错误（超时 / 无响应） | 检查网络连接后可重新创建；相同`payId` 与 `awardTag` 的未支付订单会直接返回历史订单数据 |

### 幂等性说明

| 接口                        | 幂等性说明                                                                                                  |
| --------------------------- | ----------------------------------------------------------------------------------------------------------- |
| `POST /api/iap/order`       | 同一玩家相同`payId` 与 `awardTag` 存在 `WAIT_PAY` 订单时返回该历史订单；否则创建新订单；每 5 秒最多调用一次 |
| `POST /api/iap/claim`       | 已领取（DELIVERED）的订单重复调用直接返回成功，不会重复发奖                                                 |
| `GET /api/iap/orders/paid`  | 查询接口，天然幂等                                                                                          |
| `GET /api/iap/order/status` | 查询接口，天然幂等                                                                                          |
| `POST /api/iap/userTags`    | 查询接口，天然幂等                                                                                          |
