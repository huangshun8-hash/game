# 项目设计文档

> 定位：本文档只沉淀**必要且稳定的设计决策**（架构分层、关键机制、跨服务约定、安全要点）。
> 详细文件清单与逐模块说明见 `docs/code_wiki.md`，接口约定见 `docs/` 下各 API 文档，开发规范见 `AGENTS.md`。

## 项目概览

Go 单体仓库（monorepo），三个可独立部署的服务共用一套基础设施（`pkg/`）与共享定义（`services/shared/`）：

| 服务 | 职责 |
| ---- | ---- |
| `gamelogic` | 面向客户端的核心业务：登录、存档、邮件、好友、内购入口、封禁等；同时提供 WebSocket 长连接（`GET /ws`，`pkg/wsx`） |
| `platform` | 平台侧能力：账号鉴权、内购收据校验、GMT 数据支持；持有登录链路的第一道身份查询（Account） |
| `gmt` | 运营管理后台：管理员登录、配置发布、封禁、操作日志、自定义配置等 |

共享存储：MongoDB（业务库）、Redis（限流 / Token / nonce 去重 / Pub-Sub）、配置表独立存放于 `gconfig_db`（`configs` 配置 + `generated/gdconfig` 访问层，支持热更）。

## 技术栈

- HTTP：Fiber v3，统一封装于 `pkg/httpx`（生命周期、路由、绑定、中间件、统一响应与错误处理）
- WebSocket：`pkg/wsx`（会话 Hub、心跳、推送、踢线）
- MongoDB / Redis：`pkg/mongox` / `pkg/redisx`，均通过 Fiber v3 Service 机制管理生命周期
- 认证：`pkg/authx`（JWT HMAC-SHA256 + Redis 会话 Token）
- 日志：`pkg/logx`（zap + lumberjack，固定文件名 `<filename>.log`，按大小轮转、按 `max_age` 保留、轮转即压缩；低流量服务由每日兜底轮转保证超期日志被清理）
- 服务间调用：`pkg/httpcli`（内部签名），签名与防重放见 `pkg/signx` + `pkg/httpx` 内部接口校验
- 其余：`pkg/async`（进程内异步队列）、`pkg/schedx`（周期任务）、`pkg/packetx` / `pkg/savex`（存档与传输加密）、`pb/`（proto 定义与生成物）

## 分层与依赖注入

### 分层

每个服务统一划分为五层（`services/<svc>/internal/` 为边界），依赖自上而下单向：

| 层 | 职责 | 允许 |
| ---- | ---- | ---- |
| `handler` | HTTP 关注点：路由、绑参、鉴权、错误映射 | 仅简单校验，无业务逻辑 |
| `logic` | 业务用例编排 | 经 Repo 访问 Mongo、经 httpcli 调其他服务 |
| `repo` | 数据持久化（多嵌入 `mongox.BaseRepo[T]`） | 仅数据访问 |
| `model` | 领域模型、常量与错误码 | 少量领域方法（如 `IsBanned`） |
| `dto` | 请求/响应对象（非 proto 的 JSON 接口） | — |

### 依赖注入

- `pkg/httpx.Context` 只承载请求级数据（RequestID、UserID/Claims、PacketKey），不暴露服务级依赖。
- 每个服务用 `handler/deps.go` 的 `Deps` 聚合服务级依赖（Mongo/Redis/JWT/Async/httpcli/Config）。
- `handler/router.go` 的 `NewRouters(deps)` 统一创建 Logic 并注入 Handler，作为唯一组装点；`RegisterRouters` 逐个调用 `Handler.Register(r)` 完成路由挂载。

### 配置管理

- `pkg/confx`：公共配置加载，`Base` 覆盖 Server/Mongo/Redis/JWT/Log；敏感项经环境变量注入（如 `JWT_SIGNING_KEY`、`INTERNAL_SECRET`）。
- 各服务 `internal/config` 嵌入 `confx.Base`，叠加业务配置（限流阈值、路径、自定义集合等），本地默认值见 `configs/*_local.yaml`。
- 游戏数值配置独立于业务库存放在 `gconfig_db`：启动时全量加载进内存，运行期通过 Redis Pub-Sub（`config:publish`）热更。

### 服务启动

每个服务统一遵循 `main.go` 精简入口 + `service.go` 组装：

```
main → service.Start
  initInfra     基础设施（Mongo/Redis/Async/JWT/httpcli）
  initQueues    注册异步队列
  initScheduler 注册周期任务（gamelogic：关单、清理过期邮件等）
  initServer    创建 httpx.Server（中间件、限流）
  ensureIndexes 建索引（失败仅告警）
  registerRoutes 注册 handler（gamelogic 另注册 WS 升级端点）
  start         阻塞运行
```

## 项目结构

```
okk/
├── AGENTS.md / design.md / docs/       # 规范、本文档、API 文档（含 code_wiki）
├── pb/                                 # proto 源文件与生成物（pb/cspb 为自动生成，勿手改）
├── generated/gdconfig/                 # 配置表访问层（生成，支持热更）
├── pkg/                                # 公共基础设施（见下）
├── services/
│   ├── gamelogic/                      # 逻辑服（HTTP + WS）
│   ├── platform/                       # 平台服
│   ├── gmt/                            # 运营后台
│   └── shared/                         # 跨服务共享 DTO / 模型 / 常量
├── configs/                            # 各服务本地配置样例
├── scripts/                            # 辅助脚本（gen_proto.sh、genconfig 等）
└── tests/                              # 黑盒集成测试（调用已启动服务的接口）
```

`pkg/` 按职责分三组：

- **生命周期与框架**：`httpx`、`confx`
- **数据与中间件**：`mongox`、`redisx`、`authx`、`httpcli`、`wsx`
- **业务支撑**：`async`、`schedx`、`logx`、`signx`、`packetx`/`savex`、`idx`、`invitex`、`validx`

## 关键机制

### 统一响应与错误约定

- 所有客户端接口固定 HTTP 200，业务结果只体现在响应体 `code`（0=成功）；业务错误经 `httpx.AppError` 携带业务码，由 `DefaultErrorHandler` 统一输出。
- 请求/响应通过 `httpx` 统一绑定与序列化（JSON 走 sonic；proto 消息按 Content-Type 切换）。
- 客户端文案一律英文；路径、JSON 字段、日志字段统一 camelCase；时间戳统一毫秒。

### 会话与即时失效（pkg/authx）

- JWT（HMAC-SHA256）签发登录令牌，Claims 携带玩家存档加密密钥（base64），鉴权后即可直接解密收发包。
- 会话 Token 以 Redis 单会话存储：新登录 `Save` 覆盖旧值，中间件每次请求 `Validate` 比对——被新登录覆盖或 `Revoke`（踢人/封禁/注销）后立即失效；Redis 异常时"失败放行"仅告警，避免缓存抖动导致全量用户被拒。
- WS 握手鉴权分两段执行（`wsx.NewJWTAuthWithStore`）：**升级前**只验 JWT 签名（廉价闸门，伪造令牌拿不到 `101`），**升级后、建会话前**再比对 Redis 会话 Token——因为浏览器 WebSocket API 读不到非 `101` 响应的响应体，「被顶号/被撤销」只能通过升级后的 Close 码送达客户端；已建立的连接不受握手校验影响，踢线/封禁/顶号必须显式 `hub.KickPlayer` / `hub.KickPlayerWithNotice` 断开。
- 严格单端（顶号）：**登录入口强制要求 `clientInfo.deviceId`**（缺失返回 `2400 deviceId is required`），登录时比较本次 `clientInfo.deviceId` 与登录前记录的设备标识（`UpdateLastLogin` 覆盖前取出），不一致则先向旧连接下发 `KickedOfflinePush` 通知帧（含新登录端 `platform`/`deviceName`/`ip`/`deviceId`）再以 Close 码 `4002` 断开；本地按**新 Token 的 SHA-256 摘要**过滤，只踢旧 Token 的连接；跨实例经 Redis 频道 `auth:sessionKicked` 广播（消息带 `tokenDigest` 与 `originInstance`，订阅端跳过自身与新连接），由持有旧连接的实例执行。被判定的旧 Token 被覆盖后，HTTP 请求返回独立错误码 `10402 ErrCodeSessionReplaced`，WS 握手则在升级后以 Close 码 `4002` 补发同一顶号结论（客户端离线时收不到原本的 4002），两者都与令牌过期的 `10401` 区分（客户端据此提示「账号已在其它设备登录」，不得自动重登，否则多端互相顶号）。

### 服务间内部调用（pkg/httpcli + pkg/httpx）

- 内部接口（`/internal/...`）凭 Header `X-Signature / X-Timestamp / X-Nonce` 验签：HMAC-SHA256 + 时间戳容忍窗口 + Redis SETNX nonce 去重（`httpx.VerifyInternalSignature`，Redis 异常时 nonce 去重失败放行，签名本身强制校验）。
- 平台内部接口由调用方解析响应体 `code` 判定成败（平台所有响应统一 HTTP 200，内部错误码见 `shareddto.PlatformErrCodeInternal`），HTTP 状态码仅兜底识别网关层 5xx。

### 进程内异步队列（pkg/async）

适用于"失败不影响主流程"的场景（日志、埋点、统计、审计），不适用于事务性主流程。模型：`Service → Queue（独立配置）→ Worker Pool（固定协程）→ Task`；Fiber 启动/关闭时自动 Start/Terminate（优雅关闭）。

各服务按需注册队列：

| 服务 | 队列 | 用途 | 溢出/重试 |
| ---- | ---- | ---- | ---- |
| gamelogic | `log` / `stat` | 登录日志埋点 / 在线统计 | 丢弃 / 静默丢弃 |
| platform | `log` / `audit` | 日志埋点 / 操作审计（GMT） | 丢弃 / 阻塞+重试 3 次 |

### 泛型仓库（pkg/mongox.BaseRepo）

业务 Repo 嵌入 `mongox.BaseRepo[T]` 获得基础 CRUD（Insert / Find / Update / Delete / Count / Exists / Paginate），只需实现业务特有查询；索引由各服务 `repo/indexes.go` 集中声明（含 TTL 索引，建失败仅告警）。基础能力不足时用 `Collection()` 直取底层集合；`mongox` 亦提供事务辅助（当前业务按需使用，非默认路径）。

### WebSocket 会话（pkg/wsx）

- `Hub` 统一管理连接、心跳与在线状态，按 `playerId` 定向/批量推送（`PushToPlayer`），跨服场景依赖 Redis Pub-Sub 广播到目标实例。
- 关闭携带 close code 区分「被踢下线需重登」与网络抖动，避免客户端盲目自动重连。
- 顶号链路要求「先通知后断开」：`Session.notifyAndClose` 经写协程 FIFO 屏障确认通知帧已写出，再下发 Close 帧；直接关闭会让缓冲中的通知帧被丢弃。

## 跨服务一致性约定

platform 与 gamelogic 各自持有身份字段（openId、loginType、status 等），关键约定：

- **PlayerID 同源**：playerId 在 platform 侧 `counters` 自增生成（起始 `15000001`，`15000000` 及以下低位序号段预留不用），登录时下发，两侧按同一 playerId 关联记录。
- **登录链路**：platform 是第一道身份查询（Account）与三方签名校验方；gamelogic 再按返回的 openId 查询/创建 Player 并签发 Token。
- **游客升级（最终一致）**：升级涉及 platform、gamelogic 两次独立写入，无跨服务事务。任一侧失败本次降级游客身份继续，下次登录自动重试修复；以 Error 级日志跟踪长期未完成升级的账号。
- **账号注销（身份作废，非删除）**：给两侧 `openId` 追加 `del_<unixMilli>_` 前缀释放原登录身份（防串号、防二次注销撞唯一索引），Player/Account 数据永久保留供审计；玩家ID一律取自认证上下文。编排顺序：先作废 platform Account（防串号）→ 再作废本地 Player → 撤销 Token 并踢 WS。两侧「记录不存在/已注销」均按幂等成功处理，保证任一侧已释放仍能收敛；仅真实写库错误才中止（业务码 2406，客户端可重试）。`del_` 前缀约定统一在 `services/shared/sharedmodel/deactivate.go`。
- **封禁权威源（待统一）**：当前两侧各自维护 `status` 并分别校验，封禁操作必须同时更新两侧。后续计划收敛为以 platform 为唯一权威源，封禁时经会话机制让在线令牌即时失效，gamelogic 不再独立维护封禁态。

## 数据设计概要

业务库名按服务隔离（默认 `logic_db` / `platform_db` / `gmt_db`，经 env 覆盖）；游戏数值配置独立在 `gconfig_db`。主要集合：

| 服务 | 集合 |
| ---- | ---- |
| gamelogic | `players`、`save_heads`、`save_snapshots`、`player_mails`、`mail_templates`、`pay_orders`、`ban_records`、`user_friend`、`friend_apply` |
| platform | `accounts`、`counters`、`payment_orders` |
| gmt | `admins`、`operation_logs`、`custom_configs`、`config_schema`、`config_manifest`、`config_releases`、`proto_schema` |

索引设计要点：身份类字段建唯一索引并尽量用 partial filter 约束范围（如仅非空 openId 参与唯一约束），使"身份释放"类操作（注销/升级）可通过改写字段值而非删除文档完成；过期数据用 TTL 索引自动清理。

## 安全设计要点

- 密钥一律环境变量注入，禁止硬编码；`INTERNAL_SECRET` 供服务间验签，`JWT_SIGNING_KEY` 供令牌签发。
- 登录/游客等匿名接口按 IP 维度限流（`httpx.RateLimitMiddleware`）。
- 敏感或大报文接口（登录、存档、收据、内部认证等）经 `LogBodySkipPaths` 跳过请求/响应体日志，防令牌泄漏。
- 内部接口验签 + nonce 防重放；客户端错误信息统一由 handler 映射，不暴露内部细节。
- 注销/踢人/封禁路径即时撤销 Token 并断开长连接。

## 测试与文档约定

- 单测：`*_test.go` 同包存放；依赖真实 Mongo 的用例用 `//go:build integration` 隔离（连本地 Mongo，独立库 + 清理）。
- 黑盒集成测试：`tests/` 直接调用已启动服务，走真实加解密与鉴权（登录→带 Token 调用→注销→断言）。
- proto 变更只改 `pb/*.proto`，由 `gen_proto.sh` 重新生成，生成代码禁止手改。
- 文档同步是硬性要求：新增/变更功能须同步更新 `docs/code_wiki.md`。
