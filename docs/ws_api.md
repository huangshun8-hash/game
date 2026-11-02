# WebSocket 长连接接入文档

> 客户端：Unity (C#) / 浏览器 / 原生网络库\
> 协议：WebSocket（RFC 6455）+ Protobuf 二进制帧\
> 端点：`/ws`（GameLogic 逻辑服，默认值，**可由服务端配置** **`websocket.ws_path`** **自定义**），地址与端口由部署环境决定，本地开发默认 `ws://192.168.2.65:8081/ws`\
> Proto 定义文件：`pb/ws.proto`，C# 命名空间为 `Cspb`

***

## 目录

- [概述](#概述)

- [连接建立](#连接建立)

- [消息帧协议](#消息帧协议)

- [连接生命周期](#连接生命周期)

- [上行业务消息](#上行业务消息)

- [服务端业务推送](#服务端业务推送)

- [关闭状态码与客户端处理建议](#关闭状态码与客户端处理建议)

- [服务端配置项](#服务端配置项)

- [客户端示例](#客户端示例)

***

## 概述

WebSocket 长连接用于承载客户端与服务器之间的**实时双向通信**：服务器可主动向在线玩家推送消息（邮件、封禁通知、版本强更等），客户端也可通过长连接上行低延迟业务请求。

- 服务端网关实现见 `pkg/wsx`（Hub 会话管理、心跳、限流、推送统一封装）。

- 所有上下行消息均为**一条 protobuf 二进制帧** `cspb.WsMessage`；帧内 `data` 字段再按帧类型承载具体负载。

- 连接建立（握手）走 JWT 鉴权；可用性要求：客户端必须正确处理**心跳**与**服务端主动断开时的关闭状态码**。

***

## 连接建立

### 连接 URL

```
ws://<host>:<port>/ws?token=<jwt>&platform=<platform>
```

| Query 参数   | 必填 | 说明                                                           |
| ---------- | -- | ------------------------------------------------------------ |
| `token`    | 是  | 登录令牌（JWT）。登录成功后由登录接口返回                                       |
| `platform` | 否  | 客户端平台标识（如 `ios` / `android` / `unity_editor`），用于服务端统计与排查，可为空 |

> 浏览器 WebSocket API 无法设置 `Authorization` 请求头，因此登录令牌统一由 URL query 参数 `token` 携带。\
> 路径段 `/ws` 为服务端默认值，可通过服务端配置 `websocket.ws_path`（环境变量 `WEBSOCKET_WS_PATH`）自定义；**自定义后客户端接入 URL 需同步调整**，服务端全局 JWT 跳过白名单会自动联动该路径。

### 握手鉴权流程

握手鉴权分两段执行，目的是让客户端**能读到**「被顶号」「被撤销」这两种必须展示给用户的原因：

1. **升级前（廉价闸门）**：客户端发起 WebSocket 升级请求，服务端只校验 `token` 的 **JWT 签名与有效期**。任何伪造/篡改令牌在拿到 `101` 之前就被拒绝，不消耗连接资源。
2. **升级后（会话校验）**：握手返回 `101` 后、服务端创建会话**之前**，比对 **Redis Token 存储**：令牌被撤销（封禁、踢人）或被新登录覆盖（顶号）时以 Close 码断开。容错：Redis 抖动时失败放行，仅告警。

> 为什么必须分两段：浏览器 WebSocket API 读不到握手失败（非 `101`）的响应体，只能拿到 `1006` 异常关闭码。会话失效的结论如果放在升级前，客户端只会把它当成网络异常并无限重连（顶号场景还会多端互踢）。

### 握手失败的表现

失败分为两类，客户端必须同时处理：

**A. 升级前被拒绝**（未进入 WebSocket 流程）：服务端返回统一的 JSON 错误信封（HTTP 200 + `code`），客户端需按普通 HTTP 响应解析（浏览器端拿不到该响应体，只能看到 `1006`）：

| 场景                        | code                         | message                       |
| ------------------------- | ---------------------------- | ----------------------------- |
| 请求非 WebSocket 升级（如普通 GET） | 10400（`ErrCodeBadRequest`）   | `websocket upgrade required`  |
| 缺失 `token` 参数             | 10401（`ErrCodeUnauthorized`） | `missing authorization token` |
| 令牌签名无效 / 已过期              | 10401                        | `invalid or expired token`    |

**B. 升级后会话校验失败**（已返回 `101`）：服务端**只下发 Close 控制帧**（不带业务错误帧），客户端按关闭码处理：

| 场景                  | Close 码 | reason                              |
| ------------------- | ------- | ----------------------------------- |
| 令牌已被新登录顶替（顶号）       | **`4002`** | `session replaced by another login` |
| 令牌已被撤销（封禁/注销后）      | `4006`  | `token has been revoked`            |

> 顶号的 Close 码是**补发**：客户端当时离线，收不到本该收到的 `4002`；重连时服务端发现该 Token 已被新登录覆盖，于是补发同一个 `4002`。因此 `4002` 的语义始终是「被新设备顶号踢线」，客户端无需解析业务帧即可提示「账号已在其它设备登录」。

连接建立**成功**的标志是收到服务端下发的 `Handshake` 帧（见下文），而非收到 101 状态码即视为完成。

***

## 消息帧协议

### 统一消息帧 WsMessage

所有上下行消息均为一条二进制帧，内容为 protobuf `cspb.WsMessage`：

| 字段          | 类型          | 说明                                    |
| ----------- | ----------- | ------------------------------------- |
| `type`      | `WsMsgType` | 消息帧类型（见下表）                            |
| `data`      | `bytes`     | 负载字节，按 `type` 进一步解析                   |
| `timestamp` | `int64`     | 发送方生成时间（**Unix 毫秒**）                  |
| `bizId`     | `string`    | 业务消息 ID（可选），用于客户端幂等去重/服务端路由           |
| `msgId`     | `WsMsgId`   | 业务推送消息标识（可选），用于客户端识别 `data` 的具体业务消息类型 |

### 消息帧类型 WsMsgType

| 值 | 名称                      | 方向        | 说明                  |
| - | ----------------------- | --------- | ------------------- |
| 0 | `WsMsgTypeNone`         | 非法        | 未指定/不可用类型，收发双方均视为非法 |
| 1 | `WsMsgTypeHeartbeat`    | 双向        | 心跳探测请求（客户端或服务端均可发起） |
| 2 | `WsMsgTypeHeartbeatAck` | 双向        | 心跳应答                |
| 3 | `WsMsgTypeHandshake`    | 服务端 → 客户端 | 连接建立成功后的握手帧         |
| 4 | `WsMsgTypePush`         | 服务端 → 客户端 | 服务端主动下发的业务推送帧       |
| 5 | `WsMsgTypeError`        | 服务端 → 客户端 | 错误通知帧               |
| 6 | `WsMsgTypeBusiness`     | 客户端 → 服务端 | 客户端的上行业务消息帧         |

### 业务推送消息标识 WsMsgId

| 值 | 名称                      | `data` 对应的业务消息               |
| - | ----------------------- | ---------------------------- |
| 0 | `WsMsgIdNone`           | 未指定/非法标识                     |
| 1 | `WsMsgIdPaySuccessPush` | `PaySuccessPush`（支付成功推送，见下文） |
| 2 | `WsMsgIdKickedOfflinePush` | `KickedOfflinePush`（被新登录顶替下线通知，见下文） |

> `msgId` 仅用于**下发推送**，标识 `data` 内的具体业务消息类型；`bizId` 则是该消息的业务唯一标识（如订单号），客户端按 `bizId` 幂等去重。二者职责相互独立。该枚举定义于 `pb/def.proto`（与 `pb/ws.proto` 同包 `cspb`，由 `ws.proto` 引用）。

### 负载消息（承载于 WsMessage.data）

| 帧类型            | 负载                | 字段                                                     |
| -------------- | ----------------- | ------------------------------------------------------ |
| `Heartbeat`    | `WsHeartbeatReq`  | `senderTime`（探测发起方发送时间，Unix 毫秒）                        |
| `HeartbeatAck` | `WsHeartbeatResp` | `serverTime`（服务端应答时填充服务端时间；客户端应答服务端探测时可不填）             |
| `Handshake`    | `WsHandshakeInfo` | `playerId`（当前连接所属玩家 ID）、`serverTime`（服务端时间）            |
| `Error`        | `WsErrorInfo`     | `code`（错误码，语义与 HTTP 接口 `code` 一致）、`message`（英文错误描述）    |
| `Push`         | 业务负载              | 业务协议字节（proto 或 JSON），按 `msgId` 识别具体业务消息，`bizId` 用于幂等去重 |
| `Business`     | 业务负载              | 业务协议字节（proto 或 JSON），按 `bizId` 路由到对应业务处理逻辑             |

> 服务端推送负载可传 `proto.Message`、`[]byte` 或任意对象（JSON 序列化）；`msgId` 由服务端按负载类型自动填充（见 `pkg/wsx/message.go` 的 `payloadMsgID`），客户端据此解析 `data`。

***

## 连接生命周期

### 1. 建连与握手帧

升级成功后，服务端创建会话并下发 `Handshake` 帧：

```
WsMessage {
  type = WsMsgTypeHandshake (3)
  data = WsHandshakeInfo { playerId = <玩家ID>, serverTime = <服务端时间戳> }
}
```

- 客户端**必须以收到** **`Handshake`** **帧作为建连完成的标志**；`Handshake` 同时携带服务端时间，可用于时间校准。

- 若握手身份非法（`token` 解析出的玩家 ID 不是正整数），服务端在升级后立即下发 `Error` 帧（`invalid player identity`）并以关闭码 `4006` 断开。

### 2. 心跳机制（关键）

心跳用于维持连接活性与探测链路健康，**双向都可发起探测**：

- **服务端主动探测（默认关闭）**：服务端主动心跳默认不开启（`websocket.heartbeat_interval` 为 0）。若运维显式配置为正值，服务端按该间隔下发一条 `Heartbeat` 帧（`data = WsHeartbeatReq`）；此时客户端收到后**必须立即应答**一条 `HeartbeatAck` 帧（`data` 可留空）。

- **客户端主动探测（推荐）**：由于服务端默认不主动下发心跳，客户端应自行周期发送 `Heartbeat` 帧维持连接活性，服务端应答 `HeartbeatAck` 并携带 `serverTime`，可用于测量 RTT。

**超时判定**：只要在读超时窗口（默认 90 秒）内收到客户端**任意**一条帧（业务帧或心跳应答均算），即刷新活性。超过窗口未收到任何帧，服务端判定失联并以关闭码 `4005` 断开——无论服务端主动心跳是否开启，失联断开的关闭码与日志行为一致，客户端据此区分「网络异常可重连」与「被封禁需重新登录」。

> 经验法则：服务端主动心跳关闭时，客户端主动心跳是唯一维持活性的手段，探测频率必须低于读超时窗口（如每 30 秒一次，小于默认 90 秒）；即使客户端业务帧发送频率很高、不靠心跳也能维持活性，仍建议如实应答，避免网络路径单向故障时靠业务帧"假活"。若服务端开启了主动心跳，客户端主动探测频率建议低于服务端心跳间隔的一半（例如每 15 秒），避免与服务端探测叠加放大。

### 3. 上行消息限制

| 限制项        | 默认值             | 说明                                     |
| ---------- | --------------- | -------------------------------------- |
| 单条上行消息大小   | 64 KB           | 超过上限的连接立刻被断开                           |
| 上行消息频率     | 50 条/秒，突发 100 条 | 令牌桶限流；长期平均速率严格不超过该值，超限连接以关闭码 `4003` 断开 |
| 单玩家最大并发连接数 | 5               | 超出后**踢除该玩家最早建立的连接**（关闭码 `4008`）        |

客户端应避免高频刷帧；收到 `4003` 后必须降低发送频率再重连，否则重连后会再次触发限流。

### 4. 服务端主动断开

服务端主动断开（踢人、封禁、停机、限流、读超时判定失联）时，会先下发 **WebSocket Close 控制帧**并携带关闭状态码（见 [关闭状态码](#关闭状态码与客户端处理建议)）。客户端必须读取 Close 帧中的状态码区分断开原因，而不是把所有断开都当作网络异常无脑重连。

***

## 上行业务消息

客户端上行业务消息使用 `Business` 帧：

```
WsMessage {
  type = WsMsgTypeBusiness (6)
  data = <业务负载字节>
  bizId = <业务协议 ID>       // 服务端路由与幂等依据
  timestamp = <Unix 毫秒>
}
```

| 字段      | 约定                                                  |
| ------- | --------------------------------------------------- |
| `bizId` | 业务协议标识，服务端据此路由到对应业务处理逻辑并做幂等去重。**上行帧必须填写**，否则业务侧无法分发 |
| `data`  | 业务负载字节，按 `bizId` 对应的协议解析（proto 或 JSON）              |

规则：

- 客户端**只能上行** `Business` 与心跳类帧。上行 `Handshake`/`Push`/`Error` 等服务端下行专用类型，会收到错误帧（`unsupported message type`），连接不受影响。

- 无法解析的非法帧（非 protobuf、`type` 缺失或为 `WsMsgTypeNone`）会收到错误帧（`invalid message frame`），连接不断开。

- 错误帧负载 `WsErrorInfo` 的 `code` 语义与 HTTP 接口一致（`0` 成功，非 `0` 为业务错误码）。

***

## 服务端业务推送

服务端业务逻辑可随时向在线玩家推送消息，客户端接收到的推送帧格式统一为：

```
WsMessage {
  type = WsMsgTypePush (4)
  data = <业务负载字节>
  bizId = <业务消息 ID>       // 可选，用于客户端幂等 / 去重
  msgId = <业务推送消息标识>    // 可选，标识 data 内的具体业务消息类型
  timestamp = <Unix 毫秒>
}
```

- 推送帧的 `bizId` 为业务消息唯一标识（如邮件 ID）；对同一 `bizId` 的重复推送，客户端应做去重处理。

- 推送帧的 `msgId` 标识 `data` 内的具体业务消息类型（如 `WsMsgIdPaySuccessPush`），客户端据此将 `data` 反序列化为对应消息；`msgId` 由服务端按负载类型自动填充，业务侧无需感知。

- 同一账号**严格单端**：登录时若 `clientInfo.deviceId` 与上次登录记录不一致，服务端会在旧连接上下发 `KickedOfflinePush` 通知帧，随后以关闭码 `4002` 断开（详见下文「被顶号下线」）。同一设备在重连过渡期可能短暂存在多条连接，推送仍会投递到该玩家的全部在线连接；客户端按 `bizId` 去重即可避免重复处理。

- 服务端侧推送入口：`Hub.PushToPlayer(playerID, payload)` 定向推送、`Hub.PushToPlayers(playerIDs, payload)` 批量推送。

### 业务推送负载约定

- 业务负载统一使用 **protobuf** 消息（承载于 `data` 字节），在 `pb/cs.proto` 中为各业务单独定义推送消息；`msgId` 作为负载类型标识，`bizId` 作为业务消息唯一标识（如内购推送为订单号），客户端按 `bizId` 去重。

- 推送到客户端的负载**必须走 proto**；服务端内部模块/进程间传输（如跨服广播）可用 JSON，但必须在推送到客户端前转换为 proto。

### 支付成功推送（IAP）

订单支付成功后，服务端向玩家推送一条 `cspb.PaySuccessPush` 消息，客户端据此提示可领取内购奖励：

```
WsMessage {
  type = WsMsgTypePush (4)
  data = PaySuccessPush {
    orderId   = <订单号>       // 同时标识推送帧 bizId
    payId     = <支付商品ID>
    channel   = <支付渠道>
    awardTag  = <奖项标识>
    award     = <奖励内容>
    paidAt    = <支付成功时间，Unix毫秒>
  }
  bizId = <orderId>   // 客户端据此幂等去重
  msgId = WsMsgIdPaySuccessPush (1)  // 标识 data 为 PaySuccessPush
  timestamp = <Unix 毫秒>
}
```

- 订单的完整字段定义见 `pb/cs.proto` 的 `PaySuccessPush`。

- 仅订单真实流转为 `PAID` 时下发一次；幂等重复通知不会再推送，客户端收到后仍建议按 `bizId`（订单号）去重兜底。

- 多服务器部署下，支付回调处理实例与玩家 WS 连接实例可能不同，服务端经 Redis Pub/Sub（频道 `iap:paySuccess`）跨服广播后由玩家连接所在实例下发，对客户端表现为一次普通推送。

### 被顶号下线（严格单端）

同一账号在新设备登录且 `clientInfo.deviceId` 与上次登录记录不一致时，服务端会主动踢掉旧端。旧端客户端的处理顺序是**先收到通知帧、再收到关闭帧**：

```
// 第 1 步：业务通知帧（保证在关闭前送达）
WsMessage {
  type = WsMsgTypePush (4)
  data = KickedOfflinePush {
    loginAt    = <新登录时间，Unix 毫秒>
    platform   = <新登录端平台，如 ios / android>
    deviceName = <新登录端设备名，可能为空>
    ip         = <新登录端客户端 IP>
    deviceId   = <新登录端设备唯一标识，可能为空>
  }
  bizId = <登录请求ID>              // 便于追溯/去重
  msgId = WsMsgIdKickedOfflinePush (2)
  timestamp = <Unix 毫秒>
}

// 第 2 步：连接被关闭
Close 码 = 4002
```

客户端要求：

- 收到 `WsMsgIdKickedOfflinePush` 或关闭码 `4002` 后**必须停止自动重连与自动重登**，提示用户「账号已在其它设备登录」（可用通知帧中的 `ip` / `deviceId` / `platform` / `deviceName` 展示登录来源）。
- 若仍使用旧 Token 发起 HTTP 请求，服务端返回**独立错误码 `10402`**（`session replaced by another login`，HTTP 200 + `code`），与令牌过期的 `10401` 区分；重新发起 WebSocket 握手则直接以补齐的关闭码 `4002` 断开（顶号通知的补发，见上一节）。
- 通知帧为「尽力送达」：若下行缓冲拥塞或写超时，服务端仍会以 `4002` 断开，客户端必须同时处理「只收到关闭码、没收到通知帧」的情况。

***

## 关闭状态码与客户端处理建议

服务端主动断开时通过 WebSocket Close 控制帧携带以下状态码（RFC 6455 私有区间 4000\~4999）：

| 状态码    | 含义                             | 客户端处理建议                                |
| ------ | ------------------------------ | -------------------------------------- |
| `4001` | 被服务端踢下线（封禁 / GM 踢人）            | **停止自动重连**，回到登录流程重新登录；此时旧令牌已失效，重连也必然失败 |
| `4002` | 被新设备顶号踢线（同一账号在其它设备登录）：已建连时先收到 `KickedOfflinePush` 再收到本码；客户端离线错过通知时，重连会**补发**本码 | **停止自动重连与自动重登**，提示「账号已在其它设备登录」 |
| `4003` | 上行消息频率超限                       | 降低发送频率，**指数退避后重连**；否则重连后会立刻再次触发限流      |
| `4005` | 读超时/心跳超时：超过读超时窗口未收到客户端任何帧，判定失联（网络异常） | 可直接重连                                  |
| `4006` | 握手身份非法（含握手令牌已被撤销/过期）           | 回到登录流程重新获取令牌后再连接                       |
| `4007` | 服务端正在停机                        | **退避后重连**（重连通常会被调度到新实例）                |
| `4008` | 单玩家并发连接数超限，最早建立的连接被新连接踢除        | **不应重连**；账号仍在本端使用，不提示「账号已在其它设备登录」，按重复建连排查客户端 |

> 唯一的例外是「下行缓冲积压」（客户端消费过慢导致服务端发送缓冲被写满）：此时服务端直接断开底层连接、**不下发 Close 帧**——缓冲已满恰恰说明客户端没有消费下行数据，Close 帧本就无法送达。客户端应将这种"无状态码的异常断开"视为网络异常处理。

***

## 服务端配置项

GameLogic 的 `websocket` 配置段（环境变量同名覆盖，如 `WEBSOCKET_ORIGINS`）可调整连接行为，未配置项沿用默认值。配置说明供运维与联调参考，客户端无需感知：

| 配置键                                    | 默认值     | 说明                                                 |
| -------------------------------------- | ------- | -------------------------------------------------- |
| `websocket.handshake_timeout`          | 10s     | 握手升级超时                                             |
| `websocket.heartbeat_interval`         | 0（不开启） | 服务端主动心跳探测间隔；0 表示不开启（默认，由客户端主动心跳维持活性）          |
| `websocket.read_wait`                  | 90s     | 读超时；开启服务端主动心跳时须 > 心跳间隔                            |
| `websocket.write_wait`                 | 10s     | 单次写入超时                                             |
| `websocket.send_buffer_size`           | 256     | 下行发送缓冲（条）                                          |
| `websocket.max_connections_per_player` | 5       | 单玩家最大并发连接数                                         |
| `websocket.max_message_size`           | 65536   | 单条上行消息大小上限（字节）                                     |
| `websocket.max_messages_per_second`    | 50      | 单连接上行速率上限（条/秒，≤0 不限流）                              |
| `websocket.inbound_burst`              | 100     | 上行限流瞬时突发容量（条）                                      |
| `websocket.enable_compression`         | true    | 是否启用 permessage-deflate 压缩（客户端需支持）                 |
| `websocket.origins`                    | `["*"]` | Origin 白名单（Web 端接入时生产环境应收紧为具体域名以防御跨站 WebSocket 劫持） |
| `websocket.allow_empty_origin`         | true    | 是否放行不携带 Origin 头的连接（Unity 等原生客户端必须有）               |
| `websocket.read_buffer_size`           | 1024    | 底层读缓冲（字节）                                          |
| `websocket.write_buffer_size`          | 1024    | 底层写缓冲（字节）                                          |

***

## 客户端示例

以下以 C#（Unity）为例给出关键流程，覆盖建连、握手、心跳应答与断开处理。

### 连接

```csharp
// 使用 WebSocket 客户端库（如 NativeWebSocket / WebSocketSharp）连接：
// 注意：token 必须来自登录接口；platform 可选
string url = $"ws://192.168.2.65:8081/ws?token={loginToken}&platform=ios";
```

### 帧构造

```csharp
// 构造并序列化一条 message；bytes 字段顺序需与 pb/ws.proto 保持一致（tag 顺序：type=1, data=2, timestamp=3, bizId=4, msgId=5）
byte[] BuildBusinessFrame(string bizId, byte[] payload)
{
    var msg = new WsMessage
    {
        Type = WsMsgType.Business,          // 6
        Data = ByteString.CopyFrom(payload),
        Timestamp = CurrentTimeMillis(),     // Unix 毫秒
        BizId = bizId
    };
    return msg.ToByteArray();
}
```

### 接收循环

```csharp
void OnMessage(byte[] bytes)
{
    var msg = WsMessage.Parser.ParseFrom(bytes);
    switch (msg.Type)
    {
        case WsMsgType.Handshake:
            // 建连完成标志：解析 WsHandshakeInfo（playerId / serverTime），可校准本地时间
            OnConnected();
            break;
        case WsMsgType.Heartbeat:
            // 服务端探测：立即应答 HeartbeatAck（data 可留空）
            Send(new WsMessage { Type = WsMsgType.HeartbeatAck, Timestamp = CurrentTimeMillis() });
            break;
        case WsMsgType.HeartbeatAck:
            // 客户端主动探测的应答（可读取 WsHeartbeatResp.serverTime 计算 RTT）
            break;
        case WsMsgType.Push:
            // 服务端业务推送：按 msg.MsgId 识别负载类型并解析 msg.Data，注意按 BizId 去重
            switch (msg.MsgId)
            {
                case WsMsgId.PaySuccessPush:
                    var pay = PaySuccessPush.Parser.ParseFrom(msg.Data);
                    // 订单支付成功提示（订单号 pay.OrderId），按 BizId=pay.OrderId 去重
                    OnPaySuccess(pay);
                    break;
                case WsMsgId.KickedOfflinePush:
                    var kicked = KickedOfflinePush.Parser.ParseFrom(msg.Data);
                    // 账号在其它设备登录：停止自动重连与自动重登，回到登录界面
                    OnKickedOffline(kicked);
                    break;
            }
            break;
        case WsMsgType.Error:
            // 错误通知：读取 WsErrorInfo（code / message），决定重试或修正上行
            break;
    }
}
```

### 断开处理

```csharp
void OnClose(CloseStatusCode code, string reason)
{
    switch ((int)code)
    {
        case 4001: // 被踢下线（封禁/GM）：停止重连，回登录流程
        case 4002: // 被新设备顶号（含重连补发）：停止重连与自动重登，提示「账号已在其它设备登录」
        case 4006: // 身份非法（含令牌已撤销）：重新登录后携带新令牌再连
        case 4008: // 并发连接数超限被踢（同端重复建连）：不重连，不提示顶号
            break;
        case 4003: // 限流：降低发送频率，指数退避后重连
        case 4007: // 服务停机：退避后重连
            ScheduleReconnectWithBackoff();
            break;
        default:   // 4005 读超时判定失联 / 无状态码的异常断开：视为网络异常，直接重连，超过一定次数失败后指数退避重连
            ReconnectImmediately();
            break;
    }
}
```

