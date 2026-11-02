# Code Wiki（kemiluo）

## 目录

- [1. 项目概览](#1-项目概览)
- [2. 仓库结构](#2-仓库结构)
- [3. 总体架构](#3-总体架构)
- [4. 分层与依赖注入](#4-分层与依赖注入)
- [5. 服务说明](#5-服务说明)
  - [5.1 GameLogic（逻辑服）](#51-gamelogic逻辑服)
  - [5.2 Platform（平台服）](#52-platform平台服)
  - [5.3 GMT（管理后台）](#53-gmt管理后台)
- [6. 公共基础库（pkg）](#6-公共基础库pkg)
- [7. 配置系统与热更](#7-配置系统与热更)
- [8. 运行方式](#8-运行方式)
- [9. 代码生成与脚本](#9-代码生成与脚本)
- [10. 依赖关系](#10-依赖关系)
- [11. 对外接口与测试](#11-对外接口与测试)
- [12. 开发规范](#12-开发规范)

## 1. 项目概览

本仓库是一个 Go 单体仓库（monorepo），包含三个可独立部署的服务：

- GameLogic：面向客户端的核心游戏业务接口（登录、存档、邮件、封禁、内购业务入口等），同时提供 WebSocket 长连接端点 `GET /ws`（复用 `pkg/wsx`）。
- Platform：平台侧能力聚合（账号/内部鉴权、内购相关能力，对 GameLogic 做内部回调/补偿等）。
- GMT：运营/管理后台（管理员登录、配置发布、封禁、操作日志等）。

各服务共享统一的基础设施层（`pkg/`）与共享领域定义（`services/shared/`），共同依赖 MongoDB 与 Redis；配置数值表通过 `generated/gdconfig` 管理并支持 Redis Pub/Sub 热更。

## 2. 仓库结构

```
okk/
├── configs/                      # 各服务本地配置样例（*_local.yaml）
├── docs/                         # API 文档（login/save/mail/servertime/gmt_iap）
├── generated/gdconfig/           # 游戏配置表访问层（自动生成，支持热更）
├── pb/                           # proto 源文件与生成物（pb/cspb 为生成代码）
├── pkg/                          # 公共基础设施库（httpx/wsx/mongox/redisx/logx/async/...）
├── scripts/                      # 辅助脚本（如 genconfig）
├── services/
│   ├── gamelogic/                # 逻辑服（HTTP + WebSocket 长连接）
│   ├── platform/                 # 平台服
│   ├── gmt/                      # 管理后台
│   └── shared/                   # 跨服务共享 DTO/模型/常量
└── tests/                        # 集成测试（黑盒调用接口）
```

## 3. 总体架构

### 3.1 运行形态

- 每个服务独立 `main.go` 启动：加载配置 → 初始化日志 → 组装 `service` → `Start()` 阻塞运行。
  - GameLogic：`services/gamelogic/main.go`
  - Platform：`services/platform/main.go`
  - GMT：`services/gmt/main.go`

### 3.2 基础设施依赖

- HTTP：Fiber v3（统一封装于 `pkg/httpx`）
- WebSocket：`github.com/gofiber/contrib/v3/websocket`（统一封装于 `pkg/wsx`，GameLogic `/ws` 复用）
- MongoDB：`pkg/mongox`（通过 Fiber v3 Service 管理生命周期）
- Redis：`pkg/redisx`（同上；同时用于限流、Token 存储、内部验签 nonce 去重与 Pub/Sub）
- 日志：`pkg/logx`（zap + lumberjack）
- JWT：`pkg/authx`
- 内部 HTTP Client：`pkg/httpcli`（服务间调用，带内部签名密钥）
- 进程内异步队列：`pkg/async`（channel + worker pool，支持多队列策略）
- 周期任务：`pkg/schedx`

### 3.3 跨服务关系（高层）

- GameLogic → Platform：通过 `httpcli.Service` 调用平台内部接口（例如 IAP/账号相关）。
- Platform → GameLogic：通过 `httpcli.Service` 回调/补偿通知等。
- GMT → Platform / GameLogic：通过 `httpcli.Service` 执行封禁、订单查询等运营操作。
- GMT → Redis Pub/Sub：发布配置正式发布消息到 `config:publish`。
- GameLogic / Platform → Redis Pub/Sub：订阅 `config:publish`，收到后从 `gconfig_db` 全量重新加载配置并原子替换。
- GameLogic 支付回调实例 → Redis Pub/Sub：支付成功后发布消息到 `iap:paySuccess` 跨服广播频道；各 GameLogic 实例订阅该频道并按 `playerId` 路由到本进程 `wsx.Hub`，目标玩家连接在本实例上才下发 proto WS 推送（`cspb.PaySuccessPush`，多服务器下玩家 WS 连接所在实例与支付回调处理实例解耦）。
- GameLogic 业务逻辑 → 客户端：可经 `wsx.Hub`（`PushToPlayer`/`PushToPlayers`）定向/批量推送消息到玩家长连接。
- 客户端 → GameLogic：WebSocket 长连接（`GET /ws`），握手鉴权仅在连接时校验 JWT 签名（query 参数 `token`），`wsx.Hub` 统一管理会话、心跳与推送。

## 4. 分层与依赖注入

### 4.1 目录分层

每个服务遵循统一的目录分层（以 `services/<svc>/internal/` 为边界）：

- `internal/handler/`：HTTP 关注点（路由、绑参、鉴权、错误映射、统一响应）。
- `internal/logic/`：业务用例编排（可调用 Repo、可调用 RPC/HTTP client）。
- `internal/repo/`：数据持久化（MongoDB 访问，常见做法是嵌入 `mongox.BaseRepo[T]`）。
- `internal/model/`：领域模型与错误码（以及少量领域方法）。
- `internal/dto/`：请求/响应 DTO（非 proto 的 JSON 接口通常使用）。

### 4.2 依赖注入模式

- 依赖容器：各服务 `internal/handler/deps.go` 定义 `Deps`，聚合 Mongo/Redis/JWT/Async/httpcli/配置等。
- 统一组装点：各服务 `internal/handler/router.go` 的 `NewRouters(deps)` 在一个地方创建所有 Logic，并注入对应 Handler。
- 路由挂载：`pkg/httpx/router.go` 的 `RegisterRouters` 会逐个调用每个 Handler 的 `Register(r httpx.RouterGroup)` 方法完成路由注册。

## 5. 服务说明

### 5.1 GameLogic（逻辑服）

**入口与组装**

- 入口：`services/gamelogic/main.go`
- 服务组装：`services/gamelogic/service.go`
  - `initInfra()`：Mongo/Redis/Async/JWT/httpcli(platform)
  - `initQueues()`：注册 `log/stat` 队列
  - `initScheduler()`：注册周期任务（关闭超时未支付订单、清理过期超过7天的邮件）
  - `initServer()`：创建 `httpx.Server`、挂载 JWT 中间件（skipPaths 自动包含 WebSocket 升级路径，默认 `/ws`，可配置 `websocket.ws_path`，握手鉴权交由 wsx 处理）与限流中间件
  - `ensureIndexes()`：创建 MongoDB 索引（失败仅告警）
  - `registerRoutes()`：注入依赖并注册 handler；同时注册 WebSocket 升级端点 `GET <ws_path>`（默认 `/ws`，复用 `pkg/wsx`），业务消息回调见「WebSocket 长连接」小节
  - `start()`：启动前必须成功 `loadConfigs()`，否则阻止启动；后台启动两个 Redis 订阅——配置热更订阅（`config:publish`）与支付成功跨服广播订阅（`iap:paySuccess`）

**路由聚合与 handler 列表**

- `services/gamelogic/internal/handler/router.go` 中集中创建并注册：
  - `AuthHandler`（`/auth/login` 等）：`internal/handler/auth_handler.go`
  - `SaveHandler`（存档上传/下载）：`internal/handler/save_handler.go`
  - `MailHandler`（邮件列表/领取/删除等 + GMT 内部发邮件/模板查询/撤回）：`internal/handler/mail_handler.go`
  - `IAPHandler`（订单/收据/用户标签匹配等）：`internal/handler/iap_handler.go`
  - `TimeHandler`（服务器时间）：`internal/handler/time_handler.go`
  - `BanHandler`（封禁相关、GMT 内部接口）：`internal/handler/ban_handler.go`
  - `CustomConfigHandler`（自定义配置/灰度类能力）：`internal/handler/custom_config_handler.go`
  - `FriendHandler`（好友申请/列表/排行榜/删除/关系判断）：`internal/handler/friend_handler.go`
  - `PlayerHandler`（玩家查询：按玩家ID或邀请码返回玩家基本信息）：`internal/handler/player_handler.go`

**关键 logic（示例入口）**

- 邮件用例：`services/gamelogic/internal/logic/mail_logic.go`
  - 邮件标题/正文支持文本类型（`cspb.TextContent`/`model.TextContent`）：`None`（纯文本）直接展示原文本，`Lc`（多语言Key）由客户端按当前语言自行解析；GMT 发送邮件时通过 `titleType`/`contentType` 指定类型
  - 支持预发布：邮件模板含 `startTime`（预发布生效开始）/`endTime`（预发布结束）时间窗口，Lazy Create 仅在服务器时间处于 `[startTime, endTime]` 区间内才下发到玩家邮箱，且同一模板对每个玩家终生只下发一次
  - 支持撤回：GMT 可将模板标记为已撤回（`isRecalled`），Lazy Create 下发时跳过已撤回模板（尚未收到邮件的玩家不再收到）；已下发到玩家邮箱的邮件不受影响
  - `expireTime` 为邮件相对有效期（从收到邮件时刻开始计算），下发时换算为绝对过期时间戳写入玩家邮件；未配置时默认按 7 天计算，保证邮件必有过期时间
  - `templateId` 为雪花ID（int64），超出 JS `Number.MAX_SAFE_INTEGER`；GMT 前端接口（`services/shared/gmtdto`）中 `templateId` 使用 `string` 类型传输，内部逻辑仍以 int64 存取
- 存档用例：`services/gamelogic/internal/logic/save_logic.go`
  - 玩家存档缓存由 `internal/cache` 独立管理：`cache.Manager` 封装缓存 key 集合（`cachedSaveKeys`，当前含 `base`，对应 `cspb.Base`）、正缓存 TTL（24h）/负缓存 TTL（5min）与 `Set`/`SetNotFound`/`Get`/`GetMany`/`Del`/`IsCachedKey`；Redis key 格式 `saveCache:<playerID>:<key>`，值为 `{key, version, sign, content, notFound}` JSON；批量读取走 Redis MGET 一次完成
  - 写入采用**版本守卫**（Lua 脚本 `setIfNewer` 原子执行）：仅当新 `version` 严格大于缓存中已存版本时才覆盖，避免并发/乱序上传交错提交时新版本被旧版本覆盖产生陈旧缓存；负缓存版本记为 0，任何真实存档（version>0）写入都会覆盖它
  - **负缓存**：回源 DB 后对确认无存档的玩家写入 `notFound` 标记（短 TTL），吸收未上传存档玩家的高频读，避免反复回源 DB；读取方（`SaveReader`/`cache.Get`）将 `notFound` 统一视为"无存档"，既不放入结果也不再回源
  - `save_logic.go` 通过 `cache.Manager` 处理缓存：上传成功、`Download` 从 DB 加载到最新存档后对缓存 key 同步回填（版本守卫）；缓存写失败会失效旧缓存防陈旧读，GMT 强制写入后同样失效涉及 key；缓存失败仅告警
  - 上传版本与冲突语义（`SaveLogic.Upload`）：以 `(playerId, key)` 是否有存档头分两条路径。**首次上传**（无存档头）完全忽略 `baseVersion`——服务端此时无该 key 状态，客户端可能携带本地版本号而非协议约定的 0，直接以 `newVersion` 建立首个版本并写入快照，保证首次上传始终成功（并发首次上传由 `UpsertHead` 的 `$setOnInsert` 兜底，未抢到者降级为更新流程走 CAS）。**已有存档**时才做版本校验：强制覆盖（`forceOverwrite=true`）仅要求 `newVersion > 服务端当前版本`（`baseVersion` 不参与），否则返回 2409 冲突；普通上传要求 `newVersion > baseVersion`（不满足返回 2400），且由事务内 CAS（`currentVersion == baseVersion`）保证 `baseVersion` 即服务端当前版本，CAS 未命中返回 2409 并携带服务端最新版本号。`newVersion <= 0`、`baseVersion < 0` 等基础合法性仍由 handler 前置校验
  - 玩家存档读取由独立的 `SaveCacheReader`（存档缓存读取器）负责：优先读缓存，未命中回源 DB 加载最新快照并回填缓存；对外暴露通用接口 `SaveReader`，方法 `Get`/`GetMany` 通过 `key` 传参返回原始存档条目（`cache.Item`，含 gzip 压缩线格式字节的 base64 编码 content，未解压），不绑定具体存档 key 或 message 类型；批量路径一次 Redis MGET + 未命中批量回源 DB，避免逐玩家 N+1 请求；不暴露为 HTTP 接口
  - 存档已取消加解密，仅做 gzip 压缩；玩家专属秘钥不再参与存档编解码，改用于收发包（HTTP protobuf 请求/响应）的传输层加解密（见「收发包加解密」）
  - 分层约定：`SaveReader`/`SaveCacheReader` 等通用底层组件**不打印日志、不接收 requestId**，错误统一返回由持有 requestId 的业务层（logic）记录；`cache`、`repo` 层同样不接收 requestId
  - 解码与字段投影位于业务适配层：`base_data.go` 提供 `loadBase`/`loadBases`（读取并解压解码 `base` 存档为 `cspb.Base`）与 `playerNickname`（昵称兜底默认值）；通用解码辅助 `decodeSaveContent`（泛型：base64 → `savex.Decode` 解压反序列化）与 `LoadSave`/`LoadSaves`（泛型：读取 + 解码一步完成，供任意 key 的 message 复用）供业务层一行调用，解码失败按无存档处理（跳过或空值）；存档取消加解密后 `loadBase`/`loadBases`/`LoadSave`/`LoadSaves` 均不再接收秘钥或秘钥解析器参数；后续新增其他存档 key（如 Settings）时另建适配文件，无需改动 `SaveReader`
  - 玩家等级与昵称不存储于 `Player` 实体，统一从 `base` 存档缓存读取并解码为 `cspb.Base`（未命中回源 DB）：等级取 `cspb.Base.lv`（无存档为 0），昵称取 `cspb.Base.nickname`（无存档或为空时回退默认昵称 `Player<playerID>`），头像取 `cspb.Base.avatar`（无存档或未设置时为空字符串）；供好友列表、申请列表、角色信息、用户标签等级校验、好友排行榜与玩家查询使用；`cspb.Base` 当前含 `lv`、`nickname`、`avatar` 三个字段
  - 存档格式：协议层 `content`（`bytes` 字段）为 protobuf 线格式字节经 gzip（`BestCompression`）压缩后的**原始线格式字节**（非 base64，已取消 AES 加密），`sign` 为压缩后线格式字节的 SHA256 小写十六进制（64 字符）；服务端内部（MongoDB/Redis）以 base64 字符串存储，仅在协议边界转换（上传接收原始字节后编码落库、下载解码后下发）；编解码统一由 `pkg/savex` 提供（`CompressWire`/`DecompressWire`/`Encode`/`Decode`/`ComputeSign`，仅做 gzip 压缩解压与 protobuf 序列化，不含任何秘钥）
  - GMT 接口（`/gmt/save/download`、`/gmt/save/upload`）约定收发「明文」protobuf 线格式字节（base64），gamelogic 内部仅经 `savex.DecompressWire`/`CompressWire` 做解压/压缩，GMT 无需感知存档格式
- 收发包加解密：`pkg/packetx` + `pkg/httpx`
  - 加解密目标：**仅针对 protobuf 类型的客户端请求/响应**（`Content-Type: application/x-protobuf`）。算法为 AES-CBC 加密（每份随机 16 字节 IV 前缀 + PKCS7 填充），不做压缩，统一由 `pkg/packetx` 提供（`GenerateKey`/`DecodeKey`/`NormalizeKey`/`Encode`/`Decode`/`SealWire`/`OpenWire`/`ComputeSign`）
  - 秘钥生命周期：新玩家注册时服务端 `packetx.GenerateKey()` 生成 32 字节随机秘钥（base64）落库 Player 文档 `saveKey` 字段，每位玩家各不相同；登录时经 `LoginData.saveKey`（`bytes`，原始 32 字节）下发给客户端，同时以 `packetKey` 字段内嵌进 JWT claims（`authx.Claims.PacketKey`，JSON tag `packetKey`），服务端经 `httpx.Ctx.PacketKey()` 读取
  - 接入点全部在 `pkg/httpx`（业务 handler/logic 无感知）：`bind.go` 的 `Bind` 在 proto 请求分支先经 `packetx.OpenWire` 解密再 `proto.Unmarshal`；`OK` 与 `response.go` 的 `Fail`（错误响应 `ErrorResp`）经 `marshalProtoWire` 加密后下发。秘钥经 `packetKeyFromCtx`（`Context.PacketKey()` → `packetx.DecodeKey`）解析，为空/非法时返回 nil，走**明文回退**
  - 登录豁免：登录接口在 JWT 跳过列表内、Claims 为 nil，`PacketKey()` 返回空 → 走明文，登录保持现状；管理端/内部 Token 无 `packetKey` claim 同样明文
  - 日志兼容：`Bind`/`OK`/`Fail` 始终把**明文** proto 存入 `Locals("reqBodyProto"/"respBodyProto")`，日志中间件懒序列化打印的仍是明文，加密不影响可读性
- 登录/鉴权：`services/gamelogic/internal/logic/auth_logic.go`
  - 登录链路：客户端 `POST /auth/login` → GameLogic 转发 Platform `/internal/account/auth` 验证身份 → 查找/创建玩家 → 签发 JWT Token（claims 以 `packetKey` 字段内嵌玩家专用收发包加解密秘钥，服务端经 `httpx.Ctx.PacketKey()` 读取用于收发包加解密；登录接口自身不加密，保持明文）→ 设备变更时顶掉旧端（见下条「严格单端与顶号」）
  - 严格单端与顶号（`AuthLogic.kickReplacedSession`）：**登录入口强制要求 `clientInfo.deviceId` 非空**（缺失/空白返回 `2400 deviceId is required`）——deviceId 是换端判定的唯一依据，缺失时静默踢人会误伤正常用户、静默放行又会绕过单端策略。登录时比较本次 `clientInfo.deviceId` 与库中登录前记录的设备标识（必须在 `UpdateLastLogin` 覆盖 `clientInfo` **之前**取出旧值），只有「两次都非空且完全一致」才算同一端。设备变更时：① 先调用 `Hub.KickPlayerWithNoticeExcept` 对本地在线旧连接下发 `cspb.KickedOfflinePush`（`msgId=WsMsgIdKickedOfflinePush`，帧 `bizId` 复用登录 `requestId`，负载含新登录端 `loginAt`/`platform`/`deviceName`/`ip`/`deviceId`；客户端 IP 由 handler 以 `c.IP()` 显式传入 logic，受信反代下即真实 IP），并以**新 Token 的 SHA-256 摘要**作为保留条件——旧端持旧 Token、新端持新 Token，摘要不同可精确区分，避免跨实例广播延迟到达时误踢新连接；**等待通知帧写出后**再以 Close 码 `4002` 断开（`Session.notifyAndClose` + 写协程 FIFO flush 屏障，直接 `KickPlayer` 会先停写协程导致通知帧被丢弃）；② 通过 Redis Pub/Sub 频道 `auth:sessionKicked`（`services/shared/authdto`）广播到其它实例，消息携带 `tokenDigest`（保留新连接）与 `originInstance`（订阅端跳过自身），由持有旧连接的实例执行同样的通知+断开。时机必须在签发 Token 之后、返回响应之前：此时旧 Token 已被 `TokenStore.Save` 覆盖（旧端重连必被拒）。通知/断开失败仅告警，不影响登录结果
  - 顶号错误码：`TokenStore.Validate` 直接把请求 Token 与 Redis 中的当前 Token 比较——**key 存在但值不一致即判定被新登录顶替**（无需额外保存旧 Token：`Save` 覆盖后旧值必然不等于当前值；`Revoke` 删除 key 则走「已撤销/过期」分支），返回 `authx.ErrSessionReplaced`。HTTP 中间件（`httpx.JWTMiddlewareWithTokenStore`）映射为**独立错误码 `10402 ErrCodeSessionReplaced`**（message `session replaced by another login`）；WS 握手（`wsx.HandshakeAuth.CheckSession`）在**升级后**只以 Close 码 `4002` 告知（撤销/过期配 `4006`），作为客户端离线期间错过的顶号通知的补发。两者都与令牌不存在/过期的 `10401` 区分开，客户端据此提示「账号已在其它设备登录」并停止自动重登
  - 身份字段：`LoginReq` 支持 `uuid`（游客）与 `account`（免验证渠道，SDK 已认证，服务端直接信任该第三方账号作为身份标识）；`authToken` 字段仅由 GameLogic 原样转发，未登记渠道一律拒绝
  - 三方数据字段：`LoginReq` 含 `thirdYre`（`ThirdYre` 消息，含 `t` 时间/Unix 毫秒时间戳、`siteCode` 站点编码、`ck` 校验码、`uid` 三方用户ID），由客户端随三方登录透传；GameLogic 通过 `shareddto.ThirdYre` 原样转发至 Platform `/internal/account/auth`
  - 三方数据签名校验：Platform 侧 `AuthLogic.verifyThirdYre` 通过抽象的 `ThirdYreVerifier`（默认实现 `md5ThirdYreVerifier`，位于 `platform/internal/logic/third_yre_verifier.go`）校验 `md5(siteCode + t + key + uid) == ck`（`key` 为 `ThirdPartyKey`，即环境变量 `THIRD_PARTY_KEY`），并校验毫秒时间戳容忍窗口（±5 分钟，防重放）；校验器可通过 `NewAuthLogicWithVerifier` 注入自定义实现
  - 校验策略按渠道抽象配置（`model.IsThirdYreRequired` / `thirdYreRequiredChannels`）：仅强制校验渠道（当前含 `yre`）执行校验，必须携带 `thirdYre`，缺失即拒绝；其余渠道一律跳过（即便携带 `thirdYre` 也不校验）。密钥未配置/时间戳超窗/签名不匹配均返回 400
  - 注册落库：新玩家写入 `openId`、`account`（第三方账号留存）、`serverId`（默认 1 服）与收发包加密密钥 `saveKey`（注册时服务端调用 `packetx.GenerateKey` 生成 32 字节随机密钥并 base64 编码落库）；游客升级绑定同样写入 `account`；`LoginData` 返回 `serverId`（新玩家恒为 1 服）与 `saveKey`（`bytes` 字段，下发前经 `packetx.DecodeKey` 解码为原始 32 字节二进制）；`LoginResp` 不返回昵称（昵称由客户端通过存档 Base 获取）
  - 账号注销（`POST /auth/deactivate`，需 JWT，proto `AuthService.Deactivate`）：`AuthLogic.Deactivate` 编排——**注销不是删除数据，而是永久作废 Account/Player 的登录身份**。原则：原 Player 数据（存档/邮件/好友/订单等）永久保留并永久作废，通过给 `openId` 追加 `del_<unixMilli>_` 前缀释放原登录身份；再次以原身份（游客 UUID / 三方 account）登录时两侧均查不到旧记录，将创建全新的 Account + Player。玩家ID一律取自 JWT 认证上下文（`c.PlayerID()`），禁止客户端伪造，请求体 `DeactivateReq` 无业务字段。执行顺序：① 携带内部签名（HMAC-SHA256 + nonce，复用 `signx.Sign`/`VerifyInternalSignature`）调用 Platform 内部接口 `POST /internal/account/deactivate` 作废 `Account.openId`（登录链路首查在 Platform，先释放此处防串号）→ ② 本地 `PlayerRepo.DeactivateByPlayerID` 作废 `Player.openId` 并置状态 `StatusDeactivated=2`（仅对未注销记录生效，已注销按幂等成功）→ ③ 收尾清理：`tokenStore.Revoke` 撤销 Redis Token + `hub.KickPlayer` 断开长连接使玩家立即下线（失败仅告警，Token 由 TTL 兜底）。两侧 `openId` 唯一索引（`ukOpenId`，partial filter 仅约束非空值）必须同步释放，否则下次登录会命中旧记录串号；`del_` 前缀追加毫秒时间戳保证同一身份多次注销（注销→重登→再注销）不撞唯一索引。Platform 侧 `AuthLogic.Deactivate` + `AccountRepo.DeactivateByPlayerID` 对称作废 Account。**两侧幂等对称**：`记录不存在` 与 `已注销` 在 Platform 与 GameLogic 两侧都按幂等成功处理，保证任一侧身份已释放时仍能继续释放另一侧（若 Platform 对「账号不存在」返回错误，编排会在第一步中止，导致本地 Player 的 openId 永久无法释放、玩家永久注销不掉）；仅真实错误（写库失败等）才中止并返回 `2406 ErrCodeAccountDeactivateFailed`。平台内部错误的识别以**响应体 `code`**（`shareddto.PlatformErrCodeInternal=1500`）为准而非 HTTP 状态码——平台所有响应含 `/internal` 接口均由 `httpx.DefaultErrorHandler` 统一返回 HTTP 200，HTTP 状态码判定不可靠。一致性：两侧跨服务写入无分布式事务，Platform 已作废而本地失败时返回错误由客户端重试，重试时 Platform 幂等放行、本地继续作废后最终收敛
- 内购：`services/gamelogic/internal/logic/iap_logic.go`
- 用户标签匹配：`services/gamelogic/internal/logic/usertag_logic.go`
- 封禁：`services/gamelogic/internal/logic/ban_logic.go`
  - 封禁生效链路（三步齐备才算真正下线）：事务内更新玩家状态与封禁记录 → `revokeToken` 删除 Redis Token → `kickConnections` 调 `wsx.Hub.KickPlayer` 断开已建立的长连接（携带 Close 状态码 `4001`，客户端据此回登录流程而非自动重连）；后两步失败仅告警，不回滚封禁结果
  - 闭环的前提是 **wsx 握手校验器必须接入 TokenStore**（`wsx.NewJWTAuthWithStore` 的 `CheckSession` 阶段）：客户端普遍带自动重连，若握手只验 JWT 签名，被封禁玩家断线后会立刻重连成功，「撤销 Token + 踢连接」只换来一个重连间隔
- 好友：`services/gamelogic/internal/logic/friend_logic.go`
  - 数据模型：`user_friend`（双向冗余存储，A↔B 互为好友时保存 A→B 与 B→A 两条记录）与 `friend_apply`（单向申请记录，记录存在即表示待处理，同意/拒绝/撤销后立即删除，不保存历史状态）
  - 协议定义：请求/响应消息在 `pb/cs.proto` 的 `FriendService` 中定义（`FriendApplyReq`/`FriendApplyListReq`/`FriendListReq`/`FriendRankReq`/`FriendDeleteReq` 等），handler 从认证上下文显式读取 requestID/playerID 传入 logic（proto 消息不嵌入 BaseReq）
  - 接口：`POST /api/friend/apply` 发送申请、`POST /api/friend/apply/handle` 同意/拒绝、`POST /api/friend/apply/cancel` 撤销、`GET /api/friend/apply/in|out` 收到/发出申请列表、`GET /api/friend/list` 好友列表、`POST /api/friend/rank` 好友排行榜、`POST /api/friend/delete` 删除好友（均需 JWT，操作者身份一律取自认证上下文，禁止客户端伪造）
  - 事务边界：发送申请（好友检查 + 插入申请）、同意申请（删除申请 + 双向好友写入）、删除好友（双向删除）使用 MongoDB 事务；拒绝/撤销/列表不使用事务
  - 并发安全：`friend_apply` 的 `(applyUid, targetUid)` 唯一索引保证同一方向最多一条申请（记录存在即待处理）；插入/删除均以"命中唯一记录"为语义，并发 insert/accept/reject/apply 只有一个能成功；E11000 转换为业务错误 `FRIEND_APPLY_ALREADY_PENDING`，不透传数据库异常
  - 重复申请：同方向记录已存在（含并发兜底）返回 `FRIEND_APPLY_ALREADY_PENDING`；存在反向申请（B 已向 A 申请）时 A 不再创建 `A→B`，返回 `FRIEND_APPLY_RECEIVED`；申请创建成功即投递创建事件
  - 分页：好友列表 `(uid, createdAt desc, _id desc)`、申请列表 `(targetUid|applyUid, createdAt desc, _id desc)` 均采用常规位置分页，请求参数 `start`/`end`（从 1 开始的闭区间，如 `(1,20)` 查询前 20 条），服务端换算为 `skip`/`limit`，单页上限 50、skip 上限 100 防止深分页；申请列表以不可变的 createdAt 为排序键（仅插入时设置，处理后记录即删除），保证翻页稳定
  - 幽灵过滤：列表接口批量查玩家并过滤不存在的用户；申请/同意前校验对方玩家存在
  - 好友数量上限：每位玩家好友上限 `maxFriendCount`（100），发送申请时校验本人好友数（满员拒绝发送），同意申请时在事务内校验双方好友数（计数与写入同处事务快照，任一满员返回 `FRIEND_COUNT_LIMIT_EXCEEDED` 并回滚）；属软约束，极端并发（同一玩家同时被多个申请方同意）下允许小幅溢出
  - 排行榜：`POST /api/friend/rank` 为好友私榜（所有排行均在好友范围内）且好友数量少，不引入 Redis/ZSET 等专用排行存储，handler 校验 `type` 必须为已定义类型（非 `RankTypeUnknown`）后交由 logic 按类型分派（当前支持 `RankTypeLevel` 等级排行）。等级排行为读多写少的热路径，好友ID列表与额外好友账号解析结果均走 `FriendCache`（Redis）：好友ID列表以 `friendIds:<playerId>` 缓存（命中即不再回源好友关系，未命中经 `FriendRepo.ListFriendIDs` 回源并回填，该查询仅投影 `friendUid`、不取关系文档其余字段也不排序，同意申请/删除好友后对双方主动删缓存、TTL 10 分钟兜底）；请求携带 `friends`（额外好友 account 列表，超过 `MaxFriendRankExtraFriends`=100 只取前 100 个、不报错）时经 `accountPlayers:<account>` 缓存按账号批量解析（未命中才 `PlayerRepo.FindByAccounts` 回源并回填，含"确认无对应玩家"的负缓存、TTL 1 分钟；空值在 repo 层过滤，已注销玩家按 `status != StatusDeactivated` 排除，`account` 查询显式带 `$gt: ""` 以命中 `idxAccount` 部分索引），与既有好友按 playerId 去重合并为候选；幽灵/存活性校验合并为一次 `PlayerRepo.FindExistingPlayerIDs` 批量查询（`playerId $in` + `status $ne StatusDeactivated`，仅投影 `playerId`，不拉取 `openId`/`saveKey`/`clientInfo` 等无关大字段，省去原先 `FindByPlayerIDs` 的整文档拉取；已在 DB 侧按账号解析出的玩家与本人已在 DB 侧完成过滤，跳过该次校验不再重复查询），随后只对校验通过的可见候选经 `base` 存档缓存批量读分数（等级排行分数=玩家等级，一次 Redis MGET + 未命中批量回源 DB），避免为幽灵好友白读一次存档，再在内存排序。`FriendCache` 在 Redis 未就绪时自动降级为回源 DB，不影响正确性；排序规则为分数倒序、同级按玩家 ID 升序，名次按位置连续编号，列表包含本人（本人恒参与排行、不做幽灵过滤）；排行维度枚举 `RankType` 定义于 `pb/def.proto`，响应 `FriendRankData` 含 `items`（排行列表）与 `self`（本人排名，独立计算、不依赖 items 是否包含本人），元素均为通用排行项 `RankItem`（含昵称与头像 `Base.avatar`，供客户端展示），供后续新增其他排行维度复用
  - 事件扩展点：申请创建、同意/拒绝/删除事件在事务提交后记录日志，后续可接消息队列/WebSocket

**玩家查询**

- 玩家查询：`services/gamelogic/internal/logic/player_logic.go`
  - 协议定义：请求/响应消息在 `pb/cs.proto` 的 `PlayerService` 中定义（`PlayerQueryReq`/`PlayerQueryResp`/`PlayerQueryData`），handler 从认证上下文显式读取 requestID/playerID 传入 logic（proto 消息不嵌入 BaseReq）
  - 接口：`POST /api/player/query`（需 JWT，调用方玩家ID取自认证上下文）；请求参数 `playerId` 与 `inviteCode` 至少提供一个、同时提供时优先 `playerId`，`inviteCode` 通过 `pkg/invitex` 反解为玩家ID
  - 返回：仅玩家基本信息（玩家ID、昵称、等级、头像）；等级取 `Base.lv`（无存档为 0），昵称取 `Base.nickname`（无存档或为空时回退默认昵称 `Player<playerID>`），头像取 `Base.avatar`（无存档或未设置时为空字符串），均经 `base` 存档缓存读取（未命中回源 DB）
  - 错误语义：参数缺失/邀请码不合法返回 2450/2451，玩家不存在返回 2404 `ErrCodePlayerNotFound`，查询失败/未知错误分别返回 2452 `ErrCodePlayerQueryFailed` 与 2500

**Repo（DAO）层与索引**

- 索引集中管理：`services/gamelogic/internal/repo/indexes.go`（`EnsureIndexes`）
- Repo 示例：
  - 玩家：`services/gamelogic/internal/repo/player_repo.go`
  - 存档头/快照：`services/gamelogic/internal/repo/save_head_repo.go`、`save_snapshot_repo.go`
  - 邮件：`services/gamelogic/internal/repo/player_mail_repo.go`、`mail_template_repo.go`
  - 好友关系：`services/gamelogic/internal/repo/friend_repo.go`（`user_friend`）
  - 好友申请：`services/gamelogic/internal/repo/friend_apply_repo.go`（`friend_apply`，插入申请/按方向删除/位置分页列表）

**内购订单与用户标签（IAP）**

- 业务订单状态机（`pay_orders` 集合，状态流转全部走单文档 CAS 保证并发安全）：
  - 业务状态：`WAIT_PAY` → `PAID` → `DELIVERED`（终态）；`WAIT_PAY` → `CLOSED`（超时关闭）；`CLOSED` → `PAID`（`ReopenClosed`，超时关闭后 **GMT 后台手动补单不受限**，**渠道延迟回调**携带有效三方交易号时同样自动重新激活（处理与 GMT 补单一致，不做时间窗口限制）；激活后订单可继续流转 `DELIVERED`）
  - 核心链路：`CreateOrder` **不复用历史未支付订单**，每次请求均由雪花算法生成全新订单号并在本地落单（30 分钟过期），使每笔支付意图都有独立订单可追溯；订单号唯一索引冲突时直接返回重试错误，不查取复用已有订单。`award` 为奖励内容字符串，仅随订单原样持久化与在创建响应、待领取订单清单、GMT 业务订单清单及支付成功 WS 推送中回传。落单后调 Platform 建支付单，`payId` 在业务订单中沿用存量字段 `productId` 持久化，Platform `CreatePaymentOrder` 对同 orderId + playerId 幂等复用已有支付单，故重复调用安全、不产生重复支付单。创建接口按玩家固定窗口限流为每 5 秒一次（Redis 不可用时按公共限流组件策略失败放行）。之后依次为 `HandlePayNotify` 回调处理 → `ClaimReward` 单文档 CAS 置 `DELIVERED`（status==PAID 条件更新，天然并发幂等，无需事务与额外流水）
  - `HandlePayNotify` 处理规则：先按 orderId 查订单，`req.PlayerID` 与订单归属不一致返回 2428（`ErrOrderNotOwned`）拒绝处理；`PAID`/`DELIVERED` 视为幂等确认直接 ACK；`CLOSED` 的重开资格为「GMT 来源（后台手动补单）不受限」或「渠道来源且携带有效 `thirdTransactionId`」（重开处理与 GMT 补单一致，不做时间窗口限制）——满足则调用 `ReopenClosed`（CAS `CLOSED→PAID`，被并发推进时回查终态视为幂等成功），渠道来源缺失三方交易号时记日志拒绝并返回 2422（`ErrOrderStatusInvalid`），需运营介入手动补单；`WAIT_PAY` 调用 `MarkPaid`（CAS）置 `PAID`（不校验来源）；处理成功即失效付费统计缓存（`payStats:<playerID>`）；订单真实流转为 `PAID`（首次入账或重开，CAS 成功后才走到）后经 `publishPaySuccess` 向 Redis 频道 `iap:paySuccess` 跨服广播 WS 推送通知玩家可领奖（含 `playerId` 与业务负载，订阅实例转换为 proto `cspb.PaySuccessPush` 下发），幂等 ACK / 并发回查路径不推送，避免客户端重复通知
- 金额落库：Platform 支付成功回调携带的 `amount`（分）在 `HandlePayNotify` 一并写入 `pay_orders.amount`，作为用户标签付费统计的数据源
- 跨服务内购接口 DTO 统一在 `services/shared/iapdto` 共享定义（支付通知、创建支付单、支付成功跨服广播），两侧共用同一结构，禁止各服务各自定义（避免 JSON 契约漂移静默丢字段）；`PayNotifyReq` 携带 `source`（`channel`/`gmt`）标识通知来源，Platform 首次通知时连同 `notifyId` 一并固化到 `payment_orders.notifySource`（GMT 补单可覆盖为 `gmt`），GameLogic 据此判定已关闭订单是否允许重开；支付成功广播的频道常量 `PaySuccessPushChannel="iap:paySuccess"` 与消息结构 `PaySuccessPushMessage{playerId,data}` / `PaySuccessPushData{orderId,productId,channel,awardTag,award,paidAt}` 也在 iapdto 统一声明，保证发布侧（`publishPaySuccess`）与订阅侧（GameLogic 各实例）契约一致；`PaySuccessPushData` 是 **Redis 内部传输契约（JSON）**，客户端下发负载须转换为 proto 契约 `cspb.PaySuccessPush`（内部 `productId` 映射为客户端 `payId`，其余字段一一对应，定义见 `pb/cs.proto`）
- 用户标签匹配（`POST /api/iap/userTags`）：`usertag_logic.go` 的 `matchConditionImpl` 已按「分组、付费、等级」逐项校验配置条件，`MatchTags` 遍历配置行调用——
  - 统计口径：`PAID`/`DELIVERED` 的订单，单位分
  - 指标：最近 1 笔（SLastPay/ELastPay）、最近 4 笔合计（SLast4Pay/ELast4Pay）、最近 30 天合计（SMonthPay/EMonthPay）、累积付费（SPay/EPay）
  - 性能：`FindPaidStatsByPlayerID` 按 `playerId + status` 索引，单次 `$match + $group` 一次扫描完成（totalPay/monthPay 用 `$sum`，30天窗口以 `paidAt` 时间条件约束；最近4笔用 `$topN` 按 `paidAt` 倒序取4条金额），无全量 sort 缓冲、回传仅3个聚合值。付费统计更新频率极低（仅订单支付成功时变化），故走 Redis 缓存（`cache.PayStatsCache`，key `payStats:<playerID>`，TTL 30 分钟）：标签匹配请求优先读缓存，未命中才回源 DB 聚合并回填；`HandlePayNotify` 支付成功后主动失效该玩家缓存，保证最新付费即时反映，失效失败由 TTL 兜底最终一致；查询失败不写缓存，避免把"查询异常"固化为"无付费统计"；仅当配置含付费条件时才触发读取，查询失败降级为空统计（付费条件按不满足处理，不阻塞接口）；每 enum 按配置 `Id` 升序取第一条满足的 tag；区间上下限为 0 表示不限制（`$topN` 需 MongoDB 5.2+）
  - 付费区间匹配语义：配置了上限（upper > 0）但玩家对应维度付费为 0 时，视为不满足条件（未付费玩家不应命中付费区间标签）
  - 等级条件（SLv~ELv）：玩家等级从 `base` 存档缓存读取（`SaveReader.Get` 取 `base` 存档，未命中回源 DB，再解码 `cspb.Base.lv`）；仅当配置含等级条件时才读取等级，等级数据不可用时按不满足处理
  - 用户分组（AbGroup）：请求 `UserTagsReq` 携带 `group` 字段（int32，玩家 AB 分组，0 表示未分组）；配置行 `AbGroup` 为空切片表示仅命中未分组玩家（group == 0），非空时玩家分组必须命中其一，否则该行不参与匹配；区间判定中「0 表示该侧不限制」，未配置的条件不做校验

- GMT 玩家充值统计查询（`GET /api/gmt/iap/payStats` → GameLogic `GET /gmt/iap/payStats`）：复用 `FindPaidStatsByPlayerID` 的同一聚合口径（仅 `PAID`/`DELIVERED` 订单、单位分、最近1笔/最近4笔/最近30天/累积），直接回源 DB 而不读取标签匹配用的 Redis 缓存，避免运营看到滞后数值；跨服务 DTO 在 `services/shared/gmtdto/gmt_pay_stats_dto.go` 共享定义，GMT 转发逻辑见 `services/gmt/internal/logic/iap_logic.go` 的 `QueryPayStats`

**WebSocket 长连接（`GET /ws`）**

- 升级端点复用 `pkg/wsx`：`services/gamelogic/service.go` 的 `registerRoutes()` 调用 `wsx.Register` 注册两段式握手鉴权 + 升级处理器；握手令牌取自 query 参数 `token`（浏览器 WebSocket API 无法携带 Authorization 头），使用 `wsx.NewJWTAuthWithStore(jwt, tokenStore)`——**升级前只验 JWT 签名**（伪造令牌拿不到 `101`，不消耗连接资源），**升级后建会话前再比对 Redis Token 存储**（顶号/撤销只以 Close 码 `4002`/`4006` 送达，浏览器端才读得到），使踢人/封禁对新握手同样即时生效（Redis 异常时失败放行，避免存储抖动导致全量玩家无法建连）
- 连接参数取自配置文件 `websocket` 段（见 `configs/gamelogic_local.yaml`），由 `config.loadWSConfig` 以 `wsx.DefaultConfig()` 为基线逐项覆盖，漏配项沿用默认值而非被清零；各项亦支持环境变量覆盖（键名去点大写，如 `WEBSOCKET_ORIGINS`、`WEBSOCKET_MAX_CONNECTIONS_PER_PLAYER`、`WEBSOCKET_WS_PATH`，`origins` 用逗号分隔）。升级路径为 `websocket.ws_path`（`Config.WSPath`，默认 `/ws`，必须 `/` 开头，`config.Validate()` 拦截裸路径）。`wsx.Config.Validate()` 在 `config.Validate()` 中被调用，把「读超时非正」「开启服务端心跳时读超时不大于心跳间隔」等会让所有连接建立即断的组合挡在启动前。非本地环境若 `websocket.origins` 仍为通配，启动时打印告警（CSWSH 防御未启用）。端到端测试见 `services/gamelogic/internal/config/config_load_test.go`（yaml 段生效、漏配回落默认值、环境变量覆盖、非法组合与裸路径被拦截）
- 升级路径（`Config.WSPath`，默认 `/ws`）会在启动时**自动联动追加到全局 `JWTMiddlewareWithTokenStore` 的 skipPaths**（精确路径匹配，`service.go` 构造 skipJWT 列表后 append `s.cfg.WSPath`），升级鉴权完全交给 wsx 握手流程；自定义 `websocket.ws_path` 后无需再维护白名单，两端天然一致
- 会话由 `wsx.Hub` 统一注册/注销与推送管理，`hub` 已追加进 `serverCfg.Services`，服务退出时随 `fiber.Service` 生命周期顺带关闭所有长连接（下发 Close 状态码 `4007`）
- 上行业务帧（`business` 类型）通过 `Hub.SetMessageHandler` 注入 `handleWSMessage` 回调，参数为 `(playerID, msgType, bizID, data)`——`bizID` 取自帧的 `bizId` 字段，是业务侧唯一可用的路由与幂等依据（分发已收窄到只有 `business` 一种帧类型，`msgType` 恒为 `business`）。当前尚未接入业务分发，仅以 `Debug` 级别记录（`userId`/`msgType`/`bizId`/`msgSize`）用于联调；**生产环境日志级别为 `info` 时该日志不输出，即上行业务帧目前确实会被丢弃**，这是「协议通道已就绪、业务分发待接入」的过渡状态，接入后应按 `bizID` 路由
- 封禁联动：`BanLogic.BatchBan` 撤销 Redis Token 后还会调用 `Hub.KickPlayer` 强制断开该玩家全部长连接（Close 状态码 `4001`）。仅撤销 Token 无法影响已建立的连接（握手校验不作用于存量连接），必须显式断开；而只断开连接也不够——客户端自动重连会立刻恢复，故握手校验器必须接入 TokenStore，两者共同构成闭环
- 支付成功跨服推送（首个业务下行推送，端到端链路见「内购订单与用户标签（IAP）」）：**每实例 `wsx.Hub` 仅管理本进程内的连接**，多服务器部署下处理支付回调的实例与玩家连接所在实例可能不同，故支付成功实例不直接 `PushToPlayer`，而是把内部契约消息（`playerId` + JSON 业务负载，见 `iapdto.PaySuccessPushMessage`）发布到 Redis 频道 `iap:paySuccess`；各 GameLogic 实例由 `runPaySuccessSubscriber()` 订阅（断线自动 5s 重连，模式同 `runConfigSubscriber`），`handlePaySuccessPushMessage` 解析后把负载转换为**客户端 proto 契约** `cspb.PaySuccessPush`，再调 `s.hub.PushToPlayer(playerId, &wsx.PushPayload{BizID: orderId, Data: &cspb.PaySuccessPush{...}})`（`wsx.encodePayload` 按 proto.Message 走 protobuf 编码）——`PushResult.Online` 为 0 的实例（玩家不在本进程）自然跳过，仅玩家连接所在实例完成实际投递；推送帧 `bizId` 复用 `orderId`，客户端据此幂等去重；推送帧 `msgId` 由 wsx 按负载类型自动填充为 `WsMsgIdPaySuccessPush`，客户端据此识别 `data` 为 `PaySuccessPush`
- 顶号跨服广播（严格单端）：登录实例在本进程踢完旧连接后，把 `authdto.SessionKickedMessage{playerId,loginAt,platform,deviceName,deviceId,ip,tokenDigest,originInstance,requestId}` 发布到 Redis 频道 `auth:sessionKicked`；各实例由 `runSessionKickedSubscriber()` 订阅（断线 5s 重连），`handleSessionKickedMessage` 先按 `originInstance` 跳过本实例消息，再调 `Hub.KickPlayerWithNoticeExcept(..., msg.TokenDigest)`——持有旧连接的实例先下发 `cspb.KickedOfflinePush`（`msgId=WsMsgIdKickedOfflinePush`，负载含新登录端 IP 与设备ID）再以 `4002` 断开，**令牌摘要与 `tokenDigest` 一致的新登录端连接会被保留**（防止广播延迟到达误踢新连接）。频道常量与消息结构统一在 `services/shared/authdto` 声明，避免发布/订阅两侧契约漂移
- 消息协议：所有上下行消息统一以 `cspb.WsMessage` 消息帧（protobuf 二进制帧）承载，帧定义见 `pb/ws.proto`；握手成功后服务端下发 `handshake` 帧（负载 `WsHandshakeInfo{playerId,serverTime}`）；心跳为双向 `heartbeat`/`heartbeatAck` 帧（负载 `WsHeartbeatReq`/`WsHeartbeatResp`，服务端主动心跳默认关闭、可按配置开启，客户端可随时主动探测，读超时 90s 判定失联并以关闭码 `4005` 断开），收到任何客户端消息即刷新活跃时间；业务推送为 `push` 帧（负载字节，二进制消息类型）

### 5.2 Platform（平台服）

**入口与组装**

- 入口：`services/platform/main.go`
- 服务组装：`services/platform/service.go`
  - `initInfra()`：Mongo/Redis/Async/httpcli(gamelogic)
  - `initScheduler()`：注册支付成功但通知未达的补通知任务；local/dev 环境额外注册模拟补单任务（每 5 秒，将待支付订单模拟标记为已支付并回调 GameLogic）
  - `initServer()`：创建 `httpx.Server`，并在非本地环境对敏感接口跳过 body 日志
  - `ensureIndexes()`：创建 MongoDB 索引（失败仅告警）
  - `loadConfigs()`：配置加载失败仅告警降级（与 GameLogic 不同）
  - `runConfigSubscriber()`：订阅 `config:publish` 做配置热更

**路由聚合与 handler 列表**

- `services/platform/internal/handler/router.go`：
  - `AuthHandler`：`services/platform/internal/handler/auth_handler.go`
  - `IAPHandler`：`services/platform/internal/handler/iap_handler.go`（并组合 gmtLogic 处理部分 GMT 侧入口）
  - `WhitelistHandler`：`services/platform/internal/handler/whitelist_handler.go`（充值白名单管理，GMT-facing，内部签名校验）

**充值白名单（pay_whitelist）**

- 用途：被列入白名单的账号在 Platform **下单时直接支付完成**（无需真实扣款，便于测试/联调）。接入点位于 `IAPLogic.CreatePaymentOrder` 插入 WAIT 订单成功后，若订单所属账号命中白名单则：
  - `isWhitelistedPayment` 按订单 `playerId` 查询账号 `openId`，再以 openId 查白名单；账号/白名单查询失败一律按非白名单处理，不阻断正常下单；
  - `markPaidForWhitelist` 复用 `CASMarkPaySuccess`（WAIT→SUCCESS，交易号 `whitelist_<orderId>`）+ `notifyGamelogic`（`NotifySourceChannel`）走完整发奖链路，与真实支付 / GMT 补单共用同一收单路径，CAS 未命中/失败仅告警。
- 数据模型：`pay_whitelist` 集合，**仅记录 `openId` 与 `createdAt`**，`openId` 唯一索引。按 openId 存储即天然保证「注销重登仍生效」——账号注销将 openId 改写为 `del_<ts>_<openId>`，重新注册复用相同的原始 openId。
- 管理接口（GMT-facing，内部签名校验）：`WhitelistLogic`（`services/platform/internal/logic/whitelist_logic.go`）提供 `Add`/`Delete`/`Query`，路由 `POST /gmt/whitelist/add`、`POST /gmt/whitelist/delete`、`GET /gmt/whitelist/list`。添加支持按 `playerId` 或 `account`（openId）定位并**只校验现存账号**（按 playerId 解析时仅当账号存在且未注销才落库，否则返回 `account not found`，不做已注销身份还原）；**删除仅按账号 openId**（必填），为**物理删除**（`DeleteOne`）。

**关键 logic（示例入口）**

- 内购与对 GameLogic 通知：`services/platform/internal/logic/iap_logic.go`
- 平台认证/账号：`services/platform/internal/logic/auth_logic.go`
  - 认证路由：`guest` 走游客认证；免验证白名单内渠道（`model.IsDirectAccountChannel`，当前含 `yre`）直接信任 `account` 作为 openID；未登记渠道一律拒绝返回 `unsupported login type`；`findOrCreateAccount` 对已存在账号校验 loginType 一致，防止跨渠道碰撞串号
  - 账号注销内部接口（`POST /internal/account/deactivate`，仅供 GameLogic 携带内部签名调用，HMAC-SHA256 + nonce 验签，不挂 JWT/限流）：`AuthLogic.Deactivate` + `AccountRepo.DeactivateByPlayerID` 将 `Account.openId` 改写为 `del_<unixMilli>_<原openId>` 释放登录身份并置状态 `StatusDeactivated=2`，原 Account 永久保留（供审计/对账）。仅对未注销账号执行实际作废；注销属删除型幂等操作，`账号不存在`（`mongox.ErrNotFound`）与 `已注销`（`ErrNoRowsUpdated`）一律返回成功（`deactivatedOpenId` 为空），不返回 `ErrAccountNotFound`——否则 GameLogic 编排会在第一步中止使本地 Player 身份永久无法释放。与 GameLogic 侧作废 Player 对称，两侧 `openId` 唯一索引须同步释放以防下次登录串号（详见 GameLogic「账号注销」）
- GMT 聚合逻辑：`services/platform/internal/logic/gmt_logic.go`

**Repo（DAO）层与索引**

- 索引集中管理：`services/platform/internal/repo/indexes.go`
- Repo 示例：
  - 账号：`services/platform/internal/repo/account_repo.go`
    - 玩家 ID 生成（`AccountRepo.NextPlayerID`）：以 `counters` 集合 `_id="playerId"` 的 `seq` 实现原子自增（`FindOneAndUpdate` + upsert + `returnDocument=after`）。自增用**聚合管道**而非 `$inc`：`seq = max(seq ?? 0, 15000000) + 1`，故新库首个注册玩家 `playerId = 15000001`（基准常量 `playerIDSeqBase=15000000`，其下低位序号段预留不用，便于与内部账号、迁移数据区分）；存量计数器若已高于基准则在既有值上顺延，绝不回退，避免与已发放 ID 冲突。调用方为 `AuthLogic.createAccountWithOpenID`（游客与三方登录新建账号的唯一入口）。集成测试见 `account_repo_test.go`（起始值、低于/高于基准的存量计数器三种场景）
  - 支付订单：`services/platform/internal/repo/payment_order_repo.go`

**三方充值回调接口（`GET/POST /pay/notify`）**

- 调用方：游戏方（运营商），请求方式 GET（query）或 POST（`application/x-www-form-urlencoded`）均可，无需 JWT/内部签名
- 请求参数：`agent`（运营商标识）、`server`（服务器代码）、`passport`（玩家账号）、`money`（金额，单位为元，可带小数如 `1.99`，handler 层按四舍五入转为分）、`order`（充值订单号，唯一）、`time`（毫秒时间戳）、`param`（透传参数，可选，携带平台支付订单ID）、`goodid`（渠道商品ID，字符串 SKU 如 `gtkuj.roma.rstcte.1.99`，参与签名且为必校验项）、`sign`
- 验签：`md5(passport+order+key+server+time+agent+money+goodid)`（小写），`key` 为三方平台共用签名密钥，由环境变量 `THIRD_PARTY_KEY` 配置
- 处理流程：解析参数 → 验签（含 24 小时时间窗防重放，兼容玩家支付流程长/渠道回调延迟，重放防护仍由验签与订单幂等兜底）→ 从 `param`（透传参数）解析平台支付订单ID（为空则返回无效充值信息）→ 严格校验订单（必须已存在，由客户端内购下单流程预先创建；回调 money（元）按四舍五入转为分后与订单金额（分）比对，不一致直接拒绝；回调 `goodid` 必须与订单固化的渠道商品ID（下单时按渠道取配置 `corre1`/`corre2`，缺失则下单失败）严格一致，不一致或缺失直接拒绝，无跳过/降级分支；玩家账号 `passport` 按订单 playerId 实时查询账号表取 OpenID 校验归属，账号不存在/不匹配直接拒绝；订单不存在不自动创建；订单已处理完成时视为重复回调直接返回成功，不再重复通知）→ CAS 标记支付成功（仅 WAIT 状态订单，CAS 过滤含 `paymentStatus=WAIT`，`thirdTransactionId` 存储三方充值订单号 `req.Order`）并记录归因信息（agent/server/param）→ 复用 `notifyGamelogic` 通知 GameLogic 发放（透传 agent/server/param；通知失败不影响充值成功响应，补通知任务兜底重试）
- 响应：`{"code":0,"content":"充值成功"}`，code 语义与接口文档一致（0 成功，-1 无效账号、-4 重复订单、-5 无效充值信息、-255 未知错误等）
- `notifyGamelogic` 通知判定：HTTP 200 且响应体 `code == 0` 才视为通知成功，避免对不存在订单的空转重试；通知请求携带来源 `source`（三方回调/模拟补单=channel、GMT 补单=gmt），首次通知时连同 `notifyId` 一并固化到 `payment_orders.notifySource`，补通知沿用已固化来源（GMT 补单可覆盖为 gmt 以激活已关闭订单）
- 实现：处理器 `services/platform/internal/handler/iap_handler.go` 的 `Notify`（含验签与 `{code,content}` 响应）；业务逻辑 `services/platform/internal/logic/iap_logic.go` 的 `Notify`；`payment_orders` 含 `goodId/agent/server/param/notifySource` 字段（无收据验证流程）；`goodId` 在下单（`CreatePaymentOrder`）时按 `payId` + 渠道从 `IAPProductGlobal` 配置读取固化（apple=`corre1`、google=`corre2`），配置缺失直接下单失败，回调时严格校验不允许降级；充值错误码与哨兵错误见 `services/platform/internal/model/iap_errors.go`

**LunPlay 平台角色查询接口（`GET /lunplay/role/*`）**

- 调用方：LunPlay 平台（游戏原厂提供），无需 JWT，通过 MD5 签名验证
- 签名规则：`ck = md5(拼接串 + key)`，`key` 为三方平台共用签名密钥，由环境变量 `THIRD_PARTY_KEY` 配置（与充值回调共用，未配置时接口对全部请求返回验证错误）；`t` 为秒级时间戳，5 分钟容忍窗口防重放
- 接口列表（均返回 HTTP 200，`code` 语义与平台文档一致：0 成功 / 1 验证错误 / 2 查询失败 / 3 服务器错误）：
  - `GET /lunplay/role/query`：按区服+passport 查单角色，`ck=md5(passport+t+key+serverCode)`，响应为扁平结构
  - `GET /lunplay/role/list`：按区服+passport 查多角色，响应 `data` 数组
  - `GET /lunplay/role/byRoleId`：按 roleid 查角色，`ck=md5(roleid+t+key)`，响应 `data` 数组（含 passport/serverCode）
  - `GET /lunplay/role/serverList`：按 roleid 查区服，`ck=md5(roleid+t+key)`，返回该角色所在区服
- passport 映射：LunPlay 平台用户ID（passport）= 玩家 `openId`；roleid = 玩家 `playerId`；`playerName` 返回 URL 编码（标准百分号编码，空格为 `%20`），时间字段格式 `yyyyMMdd HH:mm:ss`（UTC）
- 实现：处理器 `services/platform/internal/handler/auth_handler.go`（`QueryRoleSingle`/`QueryRoleList`/`QueryRoleByRoleID`/`QueryServerList`）；业务逻辑 `services/platform/internal/logic/lunplay_logic.go`（验签 + 跨服调用 GameLogic）；响应 DTO `services/platform/internal/dto/lunplay_dto.go`；跨服角色 DTO `services/shared/shareddto/role_info.go`
- 数据来源：角色/区服数据由 platform 携带内部签名（HMAC-SHA256 + nonce）跨服调用 GameLogic 内部接口 `POST /internal/role/queryByPassport`、`/internal/role/queryByRoleId`、`/internal/role/serverList`（见 `services/gamelogic/internal/handler/role_handler.go`、`services/gamelogic/internal/logic/role_logic.go`），区服名以 `s{id}` 兜底

### 5.3 GMT（管理后台）

**入口与组装**

- 入口：`services/gmt/main.go`
- 服务组装：`services/gmt/service.go`
  - `initInfra()`：Mongo/Redis/Async/JWT/httpcli(platform+gamelogic)
  - `initServer()`：创建 `httpx.Server`，默认开启 JWT + TokenStore 校验（跳过 `/health`、`/version` 与 `/api/admin/login`），并对登录/改密/重置密码等接口跳过 body 日志
  - `initServer()` 同时支持托管 Vue 前端打包产物：通过环境变量 `GMT_WEB_DIST` 指定静态目录时，GMT 会在业务路由/JWT 之前挂载静态托管中间件，直接提供后台页面（SPA history 回退）；`GMT_WEB_PROXY_TARGET` 可配置后台 API 反向代理目标（为空时 `/api` 由同端口 GMT 路由处理，实现同源集成）。实现在 `pkg/httpx/static.go`。注意静态资源走的是 HTTP 传输层语义：命中返回真实 MIME 与缓存头，未命中返回真实 `404`，不套用「对外接口统一 200 + code 信封」的业务约定，否则浏览器会因 MIME 不符拒绝加载 ES module
  - `ensureIndexes()`：用临时连接创建索引；仅在显式提供 `GMT_BOOTSTRAP_ADMIN_*` 环境变量时初始化超管账号
  - `registerRoutes()`：注册 handler

**路由聚合与 handler 列表**

- `services/gmt/internal/handler/router.go`：
  - `AuthHandler`：管理员登录/鉴权
  - `ProfileHandler`：管理员资料/密码等
  - `AdminHandler`：管理员管理
  - `OperationLogHandler`：操作日志
  - `IAPHandler`：运营侧订单查询/对账等（对 platform/gamelogic 做聚合），含玩家充值统计查询 `/api/gmt/iap/payStats`（转发 GameLogic，口径与用户标签付费统计一致）
  - `ConfigHandler`：配置发布流程（与 Redis Pub/Sub 配合）
  - `CustomConfigHandler`：自定义配置管理
  - `BanHandler`：封禁相关（对 gamelogic 发起调用）
  - `MailHandler`：邮件管理（对 gamelogic 发起调用，如运营发邮件）
  - `WhitelistHandler`：充值白名单管理（对 platform 发起调用，`/api/gmt/whitelist/add|delete|list`）——添加支持按 `playerId` 或 `account`（openId），平台侧只校验现存账号，**删除仅按账号 openId**，删除为物理删除；被列入白名单的账号下单时直接支付完成（详见 Platform「充值白名单」）
  - `ProtoHandler`：动态 Proto 模板与玩家存档分片管理（`/api/proto/upload`、`/api/proto/exists`、`/api/proto/playerData/get`、`/api/proto/playerData/save`）；上传模板含 `Player` 根消息时，以其直接、非 map 的 message 字段名作为存档 key（如 `base`、`item`、`board`），并自动使用字段引用的 message 类型编码和解析分片数据；查询全部存档分片时，响应 `items` 按存档 key（`messageName`）升序返回

**关键 logic（示例入口）**

- 配置发布链路：`services/gmt/internal/logic/config_logic.go`
- 封禁链路：`services/gmt/internal/logic/ban_logic.go`
- 操作日志：`services/gmt/internal/logic/operation_log_logic.go`
- 邮件发送链路：`services/gmt/internal/logic/mail_logic.go`（转发至 GameLogic `/gmt/mail/send`，创建系统邮件模板按 Lazy Create 下发；模板支持预发布 `startTime`/`endTime` 窗口，`expireTime` 为收到邮件后计算的相对有效期）
- 邮件模板查询/撤回：`services/gmt/internal/logic/mail_logic.go`（转发至 GameLogic `/gmt/mail/list` 分页查询模板列表、`/gmt/mail/recall` 撤回模板；撤回后未收到邮件的玩家不再下发）

**Repo（DAO）层与索引**

- 索引集中管理：`services/gmt/internal/repo/indexes.go`（以及配置库索引 `EnsureConfigIndexes`）
- Repo 示例：
  - 管理员：`services/gmt/internal/repo/admin_repo.go`
  - 操作日志：`services/gmt/internal/repo/operation_log_repo.go`
  - 配置发布：`services/gmt/internal/repo/config_repo.go`

## 6. 公共基础库（pkg）

### 6.1 HTTP（pkg/httpx）

- `pkg/httpx/server.go`：`ServerConfig` + `NewServer`，统一挂载 recover/requestId/serverTime/log/cors，并配置 sonic 作为 JSON 编解码。支持 `StaticDir` / `StaticProxyTarget` 配置，用于托管 Vue 等 SPA 前端页面（见 `pkg/httpx/static.go`）。内置免鉴权端点：`GET /health`（健康检查，返回 `{status,time}`）与 `GET /version`（版本核对，返回 `{service,version,time}`，版本号由编译期 ldflags `-X kemiluo/pkg/httpx.Version` 注入，默认 `dev`）。
- `pkg/httpx/version.go`：定义包级变量 `Version`（编译期 ldflags 注入，供 `/version` 接口返回，配合生产发布/回滚核对运行版本）。
- `pkg/httpx/router.go`：`Router` / `RouterGroup` 抽象与 `RegisterRouters`。
- `pkg/httpx/bind.go`：请求绑定与参数解析（并配合日志中间件记录 proto/json 请求体）；proto 请求/响应的收发包加解密接入点（`Bind` 解密请求、`OK` 加密响应、`packetKeyFromCtx`/`marshalProtoWire` 辅助），详见「收发包加解密」。
- `pkg/httpx/middleware.go`：JWT 中间件、日志中间件、以及与 TokenStore 的组合版本。日志中间件跳过请求/响应体记录的两个来源：`LogBodySkipPaths` 配置的敏感路径（登录、改密、上传等），以及下游中间件写入 `Locals("staticAsset")` 的标记（静态托管直接响应时使用，见 `pkg/httpx/static.go`）。带 `staticAsset` 标记且状态码 < 400 的请求，访问日志降级为 `Debug`：一次后台页面加载会产生数十个资源请求，全按 Info 记录会淹没业务日志；4xx/5xx 仍按 `Warn`/`Error` 记录，保证「产物缺失」这类故障可查。
  - **query 脱敏**：`maskSensitiveQuery` 对敏感参数只保留键名、值替换为 `***`。清单见 `sensitiveQueryKeys`，覆盖登录/会话凭证（`token`/`accessToken`/`refreshToken`/`idToken`/`authorization`/`sessionId`/`ticket`）、口令与密钥（`password`/`passwd`/`pwd`/`secret`/`apiKey`/`appSecret`）、验签参数（`sign`/`signature`/`ck`——`ck` 是 LunPlay 系列接口的 `md5(...+key)` 签名，与 `sign` 同级）、账号标识（`passport`，可直接定位到具体账号）与 OAuth 授权码（`code`）。这一步是必需的：`skipBodyPaths` 只作用于请求/响应体，而 WebSocket 握手等场景只能通过 query 携带登录令牌（`/ws?token=...`），原样落盘即构成凭证泄露。
    - 实现为**单遍扫描**：逐个切分 `key=value`，把键名折叠成小写后查表（`map[string(byteSlice)]` 由编译器优化为免分配查找，折叠用栈上缓冲），未命中任何敏感键时直接返回原始串。有意不用「先整串搜索敏感子串、命中才重组」的两段式写法：那样要为每个敏感键各扫一遍全串（更慢），且隐含「清单键名恒为小写」的前提——一旦有人加入大写键就会**静默漏脱敏**且无任何报错。该约定由 `TestSensitiveQueryKeysAreLowercase` 固化为测试。超过 `maskKeyLookupLimit`（64 字节）的键名一律视为非敏感，避免异常请求把脱敏逻辑变成放大器。测试见 `pkg/httpx/middleware_test.go`。
  - **业务错误日志完整性**：Fiber 的全局错误处理器要等整条中间件链返回之后才执行，而业务 handler 通常直接 `return httpx.NewAppError(...)`，此时日志中间件拿到的响应体仍是空的。为此 `LogMiddleware` 在 `c.Next()` 返回错误后**先调用 `c.App().ErrorHandler`** 把最终响应（JSON 信封或 proto `ErrorResp`）落到 `Response` 上，再采集 `respBody`。这样错误请求的日志同样具备 `reqBody`/`respBody`，与客户端实际收到的一致，不会只剩 `status=200` + `error` 文案而无法判断业务结果；错误处理成功后中间件吞掉该错误（响应已写出），仅当错误处理本身失败才交回 Fiber 兜底。响应体已包含 `code`/`message` 时**不再重复打印错误文案**（`error` 字段仅在敏感路径不记录响应体、或响应体缺失时保留）。回归用例：`TestLogMiddlewareLogsJSONBusinessError`、`TestLogMiddlewareLogsProtoBusinessError`。
- `pkg/httpx/ratelimit.go`：基于 Redis 的固定窗口限流。
- `pkg/httpx/response.go`：统一响应信封 `Response{code,message,data}` 与 `AppError`，以及 `Fail/OK` 等输出约定（对外接口 HTTP 状态码统一 200）。
- `pkg/httpx/errcode.go`：中间件层通用错误码（401/403/429/500 等映射为业务 code；`10402 ErrCodeSessionReplaced` 专用于「被新登录顶替」，与 401 令牌过期区分）。
- `pkg/httpx/static.go`：`StaticServer` 静态托管（Vue 等 SPA）中间件，`Middleware()` 在业务路由/JWT 之前注册。要点：
  - **路径解析与穿越防护**：`resolveFile` 先 `url.PathUnescape` 解码（Fiber 的 `UnescapePath` 默认关闭，`c.Path()` 返回原始转义串，不解码则含空格/中文的产物文件永远 404），再用 `path.Clean`（正斜杠语义）清洗、去前导斜杠后拼到 `DistDir`，最后以 `isWithin`「结果必须等于根目录或位于根目录之下」做穿越防护。**URL 恒以 `/` 开头属正常情况，绝不能用 `filepath.IsAbs` 判定**——`filepath.IsAbs("/assets/x.js")` 在 Linux 上恒为 `true`（Windows 需盘符）。根目录前缀检查在 Windows 上也是必需的：`path.Clean` 不把 `\` 当分隔符，`/..\..\secret` 能穿过 Clean 后被 `filepath.Join` 当作分隔符逃出根目录。注意前导 `..` 会被 `path.Clean` 折叠回根内（`/../secret.js` → `<root>/secret.js`），属于归一化而非拒绝，结论仍安全。
  - **符号链接与隐藏文件加固**：`os.Stat` 会跟随符号链接，仅靠字符串前缀检查发现不了 `dist/link -> /etc/passwd` 这类逃逸，故命中后再用 `filepath.EvalSymlinks` 解析并与 `rootReal` 比对（`rootReal` 为根目录自身解析软链接后的真实路径，部署侧用软链接切版本时不可少；启动时解析失败则退化为仅前缀检查）。`hasHiddenSegment` 拒绝任何以 `.` 开头的路径段（`.env`、`.git/config` 被误拷进产物目录时不可下载），`.well-known` 为唯一白名单例外。
  - **仅 GET/HEAD 提供静态内容**：其余方法直接 `Next()` 放行给路由层给出正确语义，避免 `POST /assets/x.js`、`POST /任意路径` 被静默回以 200 + 文件内容而掩盖客户端调用错误。
  - **根路径预计算**：`DistDir` 的绝对路径、真实路径与 `index.html` 路径在 `NewStaticServer` 中一次性解析并缓存（`rootAbs` / `rootReal` / `indexPath`），避免每请求调用 `filepath.Abs`（相对路径时会触发 `os.Getwd` 系统调用）。启动时输出一条诊断日志（`distDir` / `rootAbs` / `rootReal` / `hasIndexHtml` / `proxyTarget`），产物未挂载或缺 `index.html` 可据此排查。
  - **资源未命中返回真实 404**：带静态资源扩展名（`.js`/`.mjs`/`.css`/`.wasm`/图片/字体等，见 `hasAssetExtension`，不含 `.html` 以保留 SPA 深链回退）的请求未命中真实文件时，经 `sendNotFound` → `sendPlainStatus` 直接输出 `HTTP 404` + `text/plain` + `Cache-Control: no-store`，**不返回 `fiber.ErrNotFound`**——后者会被 `DefaultErrorHandler` 统一转成 HTTP 200 + `application/json` 业务信封，浏览器加载 ES module（`Accept: */*`）时依旧触发严格 MIME 校验失败（`Failed to load module script`），且 DevTools 显示 200 掩盖问题。静态资源属传输层，不套用「对外接口统一 200」的业务信封约定。`SendFile` 因发版覆盖产物产生的 TOCTOU 404 与读取失败都走 `wrapSendFileError`，前者输出 404、后者输出纯文本 `500`（同样不回落业务信封）。
  - **缓存策略按状态码收口**：命中后统一由 `cacheControlFor(mustRevalidate, status)` 决定取值——仅 `200`/`206`/`304` 下发缓存策略，其余（含 fasthttp FS 内部错误写出的 `5xx`）一律 `no-store`。**这一收口是必需的**：fiber 的 `SendFile` 只把 `404` 转成 error 返回，fasthttp FS 内部写 `500` 时 `SendFile` 返回 `nil`，若无条件覆写缓存头，`500` 响应会被打上一年 `immutable`，被浏览器与 CDN 长期复用。带内容 hash 的资源下发 `public, max-age=31536000, immutable`（`assetCacheControl`）；`isRevalidatePath` 命中的路径下发 `no-cache, no-store, must-revalidate`（`revalidateCacheControl`），覆盖 HTML 文档（根路径、SPA 回退、`.html`/`.htm`，含子目录 `index.html`）与 `/.well-known` 下的校验文件——两者都不是 hash 命名，长缓存会让发版后引用到已删除的旧资源、或让证书续期校验失败。
  - **Content-Type 显式映射（跨平台确定性）**：`sendAsset` 在 `SendFile` 之后按 `assetContentTypes`（`contentTypeForAsset`，键与 `staticAssetExtensions` 一一对应）覆写 `Content-Type`。**不能依赖 fasthttp 的 `mime.TypeByExtension`**：该函数读宿主机 MIME 库（Windows 读注册表、Linux 读 `/etc/mime.types` 与内建表），结果随机器而变——Go 内建表无 `.mjs`/`.cjs`，Windows 上会回落 `text/plain`，浏览器对 ES module 严格 MIME 校验时直接拒绝执行（控制台 `Failed to load module script`、页面白屏）。显式映射保证同一产物在任何宿主机上都以 `text/javascript` 返回。覆写仅在成功状态码（`200`/`206`/`304`）下进行，未命中映射（如 `/.well-known` 校验文件）沿用 `SendFile` 的结果。
  - **Range 支持**：两份固定的 `SendFile` 配置均开启 `ByteRange`，才会下发 `Accept-Ranges` 并支持 `206`；未开启时视频与大体积产物的拖动会退化为整包下载。配置固定为两份的原因：fiber 按 `SendFile` 配置内容缓存 fasthttp FS handler（`app.sendfiles`），随请求变化的配置会让缓存条目无上限增长。
  - **不做响应压缩**：`SendFile` 的 `Compress` 保持关闭——fasthttp 需在产物目录旁写 `.fasthttp.gz` 压缩副本，而部署侧 `/data/web` 是只读挂载，写不进去只会退化为不压缩并对每个文件打一行告警。压缩统一放在 nginx 层（`scripts/install.sh` 生成的反代配置已含 `gzip on` + `gzip_proxied any` + 显式 `gzip_types`；`gzip_proxied` 默认 `off`、`gzip_types` 默认只压 `text/html`，两者缺一都不生效）。
  - **静态请求日志降噪**：命中静态文件、404 与传输层 500 时写入 `Locals("staticAsset")`，`LogMiddleware` 据此跳过 `reqBody`/`respBody` 记录，并把成功响应降级为 `Debug`。跳过 body 的必要性在于 `isBinaryContentType` 只过滤 protobuf/octet-stream/multipart，`text/javascript`、`text/css`、`image/png`、`font/woff2` 等都会被 `bodyLogField` 全量写入——数 MB 的 bundle 与二进制乱码会迅速打满日志盘。
  - **API 放行**：`isAPIPath` 按路径段边界匹配（`/api` 或 `/api/` 前缀），避免 `/apifoo` 这类前缀相近的静态路径被误放行。放行后未配置 `ProxyTarget` 时同源处理，配置时经 `proxy.Do` 反向代理到后台服务。
  - **已知取舍**：`.map` 在资源扩展名清单内，产物里若带 source map 即可被公开下载（这是「有文件就送出」的固有行为，非本层引入），生产构建不应输出 `.map`。
- `pkg/httpx/static_test.go`：静态托管回归测试，覆盖 `resolveFile` 命中（含 percent 转义、`.well-known`）与拒绝（前导 `..`、Windows 反斜杠、编码穿越、非法转义、隐藏文件、目录）、符号链接逃逸（Windows 无权限创建软链接时 `Skip`）、`.js`/`.mjs` 的 MIME 与缓存头、`cacheControlFor` 状态码矩阵、Range `206`、缺失资源必须为真实 404（非 200 信封）、隐藏文件不泄露、SPA history 回退、非 GET/HEAD 不响应静态内容、`/api` 放行与 `/apifoo` 不误判、`staticAsset` 标记、产物目录缺失时放行、未启用时放行。`newTestDist` 有意不用 `t.TempDir()`：fasthttp FS 会按 `CacheDuration`（默认 10s）缓存已打开的文件句柄，Windows 上句柄未释放前无法删除文件。
- `pkg/httpx/static_content_type_test.go`：锁定 `assetContentTypes` 与 `staticAssetExtensions` 双向一致（任一扩展名缺失显式类型即报错，防止 `mjs` 类问题复发）、映射键全小写、`.js`/`.mjs`/`.cjs` 必为 javascript、未知扩展名与无扩展名返回空串。

### 6.2 配置（pkg/confx）

- `pkg/confx/confx.go`：统一配置加载
  - 命名：`{serviceLower}_{env}.yaml`（如 `platform_local.yaml`）
  - 优先级：环境变量 > 配置文件 > 默认值
  - 环境变量映射：把 key 的 `.` 替换为 `_` 并大写（如 `mongo.uri` → `MONGO_URI`）
  - 双源对应：`.env.local.example` 与 `configs/*.yaml` 中的同名键为同一配置键的两层来源（如 `MONGO_URI`↔`mongo.uri`、`REDIS_ADDR`↔`redis.addr`、`REDIS_PASSWORD`↔`redis.password`），env 非空即覆盖 YAML；密码类/密钥类键仅支持 env 方式设置，对应关系已标注在各模板与样例配置的注释中
  - `LoadBase(...)` 返回基础配置；`LoadBaseWithSource(...)` 额外返回 `*Source`，供各服务在**同一份**配置文件/环境变量中读取自己的专属配置段（如 gamelogic 的 `websocket` 段），避免为一个服务专属配置另建一套加载流程或把 viper 依赖扩散到业务代码
  - `Source` 的所有取值方法（`Int`/`Int64`/`Bool`/`String`/`Duration`/`StringSlice`）在「键未配置」时返回调用方给出的默认值，从而支持「以代码默认值为基线、按需覆盖」；接收者为 nil 时同样返回默认值，不关心专属配置的服务可继续用 `LoadBase`。`StringSlice` 兼容 YAML 列表与环境变量的逗号分隔串（环境变量无法表达列表结构，与 `TRUSTED_PROXIES` 约定一致），并在配置值全为空元素时回落到默认值，避免把 Origin 白名单这类安全配置误清空成「全部拒绝」。测试见 `pkg/confx/confx_test.go`
- `pkg/confx/validate.go`：配置安全校验
  - 基础校验：Mongo/Redis/JWT 配置不能为空
  - 密钥校验：JWT 与内部调用密钥必须显式提供，且至少 32 位，同时拒绝已知弱值
  - URL 校验：非 `local` 环境禁止把内部服务地址指向 `localhost/127.0.0.1`

### 6.3 MongoDB（pkg/mongox）

- `pkg/mongox/service.go`：Fiber Service 生命周期托管
- `pkg/mongox/client.go`：连接与 Database/Collection 访问
- `pkg/mongox/base_repo.go`：`BaseRepo[T]` 泛型 CRUD 基座（Repo 层常用嵌入）
- `pkg/mongox/crud.go`：泛型 CRUD 实现与重复键幂等判定（`IsDuplicateKeyError` 识别任意重复键错误、`IsDuplicateKeyOnlyError` 判断批量写入是否为纯重复键）
- `pkg/mongox/index.go`：索引创建能力（各服务 `internal/repo/indexes.go` 统一调用）

### 6.4 Redis（pkg/redisx）

- `pkg/redisx/service.go`：Fiber Service 生命周期托管
- `pkg/redisx/client.go`：连接创建与 Raw client 暴露

### 6.5 日志（pkg/logx）

- `pkg/logx/logger.go`：zap 初始化 + lumberjack 原生轮转/保留/压缩。全局实例用 `atomic.Pointer` 保存，`Init(cfg)` 与「未 Init 就调用日志」的兜底初始化各自持有独立的 `sync.Once`：**两者必须分离**——若共用一个 `once`，任何早于 `Init` 的日志调用（如构造函数里的启动诊断日志）都会先以 `DefaultConfig()` 占位，随后业务的 `Init(cfg)` 退化为空操作，级别/目录/轮转配置被静默丢弃。兜底仅在实例仍为空时写入，`Init` 则无条件覆盖，保证业务配置最终生效；`Init` 自身保持幂等。
- **日志文件名与清理策略（关键约束）**：当前文件恒为 `<filename>.log`（固定名，**不按天切换文件名**），写满 `max_size`（默认 200MB）后由 lumberjack 重命名为 `<filename>-<2006-01-02T15-04-05.000>.log`，再由 `max_age`（默认 7 天）依据文件名中的时间戳删除超期文件、由 `compress`（默认 true）压缩为 `.gz`；`max_backups` 默认 0（不按份数限制），使保留策略只由 `max_age` 决定。**曾有过的按天命名实现（`<filename>-YYYY-MM-DD.log` + 跨天 `Rotate`）已废弃且不可恢复**：lumberjack 只识别「当前文件前缀 + 时间戳」的备份名，而按天命名让前缀逐日变化（`GameLogic-2026-09-14.log` → `GameLogic-2026-09-15-`），前一天的文件永远匹配不上当天前缀，`max_age`/`max_backups` 对它们完全失效——这正是「超过 7 天自动删除不生效」的根因。**时间口径统一为 UTC**：日志 JSON 的 `time` 字段使用自定义 `utcISO8601TimeEncoder`（`time.UTC()` 后格式化，偏移为 0 时以 `Z` 结尾），lumberjack 备份名通过 `LocalTime=false` 也使用 UTC；由于 lumberjack 解析备份名时固定按 UTC，生成侧同为 UTC 才能让 `max_age` 的 `now - max_age*24h` 口径精确成立，保留时长不再随服务器本地时区偏移（否则 UTC+8 会多留 8 小时、UTC-8 会少留 8 小时）。
- **清理/压缩的触发时机**：lumberjack 原生只在「启动后首次写入」（`openExistingOrNew`）与「每次写满 `max_size` 轮转」（`rotate`）两个时点执行 mill（删除 + 压缩），普通写入不触发。为避免「日志量小的服务一天写不满 200MB、长期不清理」导致超期日志堆积，`permFixWriter` 增加**每日兜底轮转**：`MaxAge > 0` 时，距上次兜底轮转超过 `dailyCleanupInterval`（24h）后的首次写入会主动调用 `lj.Rotate()` 触发 mill，从而保证「超过 `max_age` 的日志必定被删除」；用 `atomic.Int64` 记录上次触发时刻并以 CAS 去重，并发写入下每周期只轮转一次。代价是低流量服务每天会多出一份备份（含压缩），体积很小。压缩为 lumberjack 原生语义，在轮转时立即执行，当天较早轮转出的文件也会被压缩，**不支持「延迟 N 天再压缩」**。
- **存量迁移**：切换前按天命名的历史文件不符合 lumberjack 命名，`max_age` 永远清理不掉，需一次性手工删除（如 `find /data/logs -maxdepth 1 -name '*-20??-??-??.log' -mtime +7 -delete`）。注意该命令按 mtime 判断，与 lumberjack 按备份名时间戳、cutoff 为 `now - max_age*24h` 的口径不完全等价，仅作为一次性清理足够。
- **权限放宽**：lumberjack 建文件固定 0600，`permFixWriter` 在写入后 chmod 为 0644；轮转会复制当前文件权限，故首次校正后长期有效，另每累计 1024 条复检一次以自愈「当前文件被外部脚本删除后由 lumberjack 以 0600 重建」的场景。其 `Sync()` 关闭底层句柄（lumberjack 直写 `os.File` 无用户态缓冲，关闭即落盘），供进程退出前调用；**运行期删除当前日志文件不会立即重建**——lumberjack 会继续写入已 unlink 的 inode，直到下一次轮转才按原文件名新建，此期间日志不可见，故禁止在服务运行中删除 `<filename>.log`。
- `pkg/logx/logger_test.go`：锁定初始化行为——先触发兜底再 `Init`，校验实例被替换、级别生效、日志写入 `Init` 指定目录，以及重复 `Init` 幂等；另以 `TestEncodeTimeIsUTC` 锁定日志 `time` 字段恒为 UTC（传入 `+08:00` 时间仍编码为 `Z` 结尾），防止后续换回 `zapcore.ISO8601TimeEncoder` 导致跨地区日志时间漂移。用例自建临时目录并尽力清理：兜底 logger 的 lumberjack 句柄不会被关闭（`logx.Sync()` 只关 `Init` 实例的句柄），Windows 上句柄未释放前无法删除文件，故不能用 `t.TempDir()`。
- `pkg/logx/logger_rotate_test.go`：在 writer 层直接构造 lumberjack 断言轮转语义（不触碰全局 `Init`，避免影响 `TestInitOverridesFallback` 依赖的单次初始化顺序）——按大小轮转出的备份使用 lumberjack 原生命名、超期备份被删除而未超期保留、`compress` 启用时备份被压缩为 `.gz` 且不残留源文件（mill 异步，用轮询等待）、写入后当前文件权限为 0644（Windows 跳过）、以及**每日兜底轮转**：缩短 `dailyCleanupInterval` 后，未到周期不清理、超过周期后的首次写入触发轮转并删除超期备份。
- `pkg/logx/fiberlog.go`：Fiber 日志适配

### 6.6 异步队列（pkg/async）

- `pkg/async/service.go`：队列注册、生命周期管理
- `pkg/async/queue.go`：worker pool + 溢出策略（Drop/Block/DropSilent）与重试

### 6.7 JWT（pkg/authx）

- `pkg/authx/jwt.go`：签发与解析（HS256），对外暴露 `GenerateToken`、`ParseToken`；`Claims` 含 `packetKey` 可选项（`Claims.PacketKey`，玩家收发包加密秘钥，base64 字符串）——登录签发时内嵌，中间件解析后写入请求上下文，服务端经 `httpx.Ctx.PacketKey()` 读取，用于对 protobuf 收发包做加解密；GMT 等内部签发（无 packetKey）不受影响，走明文
- `pkg/authx/token_store.go`：TokenStore（常用于“踢人/封禁即时失效”等能力，配合中间件）。同一时刻每玩家仅一个有效 Token（`token:<playerId>`）；`Validate` 用请求 Token 与当前 Token 直接比较：key 不存在（撤销/过期）返回 `ErrTokenNotFound`，值不一致（已被新登录覆盖）返回 `ErrSessionReplaced`，供上层返回不同错误码；`Revoke` 删除该 key。单测以内存假实现覆盖上述分支与 Redis 异常分支（`token_store_test.go`），无需真实 Redis

### 6.8 服务间调用（pkg/httpcli）

- `pkg/httpcli/service.go`：Fiber Service 生命周期托管
- `pkg/httpcli/client.go`：HTTP client 实现

### 6.9 邀请码（pkg/invitex）

- `pkg/invitex/invite_code.go`：玩家ID与6位邀请码互转
  - 字符集 32 个（8 数字 + 24 大写，剔除易混淆的 0/O、1/I），32 进制取模编码，容量上限 `MaxUID = 32^6 - 1 = 2^30 - 1 = 1073741823`，超出返回错误
  - 编码前对玩家ID做 30 位域内可逆扰乱（`scramble`：30 位循环左移 + XOR 常数），破坏小玩家ID高位连续为 0 导致的邀请码单调、可顺序枚举问题；扰乱为双射，仍可无损反解
  - `UID2InviteCode(uid)`：玩家ID编码为固定 6 位邀请码，不足 6 位高位自动补字符集首字符
  - `InviteCode2UID(code)`：邀请码反解为玩家ID，长度或字符不合法时返回错误
  - 注意：字符集顺序与扰乱常数（`scrambleXor`）定稿后不可变更，否则历史邀请码全部失效

### 6.10 WebSocket（pkg/wsx）

WebSocket 长连接网关公共封装，基于 `github.com/gofiber/contrib/v3/websocket`（适配 Fiber v3），封装风格与 `pkg/httpx` 对齐；GameLogic 的 `/ws` 端点复用此包。

- 帧协议：`pb/ws.proto`（`import "pb/def.proto"`）定义统一消息帧 `cspb.WsMessage{type,data,timestamp,bizId,msgId}`（帧类型枚举 `WsMsgType`：none/heartbeat/heartbeatAck/handshake/push/error/business）及其负载消息——心跳 `WsHeartbeatReq{senderTime}`（字段语义为「探测发起方发送时间」，客户端与服务端均可发起探测）/`WsHeartbeatResp{serverTime}`（客户端应答服务端探测时服务端不解析该负载，可留空）、欢迎 `WsHandshakeInfo{playerId,serverTime}`、错误 `WsErrorInfo{code,message}`；业务推送消息标识枚举 `WsMsgId`（none/paySuccessPush）定义于 `pb/def.proto`（通用枚举统一放此文件），Push 帧内标识 `data` 的具体业务消息类型，与 `bizId` 的幂等去重职责相互独立；所有上下行帧统一 protobuf 二进制编码（`proto.Marshal/Unmarshal`），负载字节按帧类型解析；业务负载既支持 proto.Message 也支持任意对象（sonic JSON 回退，AGENTS.md 要求 JSON 统一走 sonic）
- `pkg/wsx/config.go`：`Config` 连接参数与 `DefaultConfig()`——握手超时 10s、服务端主动心跳默认关闭（`HeartbeatInterval=0`，连接活性由客户端主动心跳维持，可按配置开启）、读超时 90s（超过未收到任何消息判定失联）、写超时 10s、下行发送缓冲 256 条、单玩家最大并发连接数 5（`MaxConnectionsPerPlayer`，<=0 不限制，超限踢除最早连接）、单条上行消息上限 64KB、单连接上行限流 50 条/秒 + 突发 100 条（`MaxMessagesPerSecond` / `InboundBurst`，<=0 不限流，超限断开）、默认开启 permessage-deflate 压缩、Origin 白名单 `Origins`（默认 `["*"]` 放行所有来源，有 Web 客户端接入时应按环境收紧）与 `AllowEmptyOrigin`（默认 true，放行不带 Origin 头的原生客户端；收紧 `Origins` 时必须保持开启，否则 Unity 等原生客户端会被拒绝）
  - 所有参数均可由服务的配置文件覆盖（GameLogic 为 `websocket` 段），无需改代码重编译；这是 `Origins` 能真正起防御作用的前提——底层升级中间件对 `["*"]` 走「完全不校验 Origin 头」的快路径，不可配置就等于永久关闭
  - `Config.Validate()` 供服务启动期调用，拦截会让连接不可用的组合：`ReadWait` 非正、开启服务端主动心跳（`HeartbeatInterval > 0`）时 `ReadWait <= HeartbeatInterval`（连接会在首次心跳探测前就被判失联）、`HeartbeatInterval` 为负、各类超时/缓冲非正、以及「收紧 `Origins` 却把 `AllowEmptyOrigin` 置 false」（会把所有不发 Origin 头的原生客户端拒之门外）
  - `Config.HasOriginWildcard()` 判定白名单是否为通配（含空列表），供服务在非本地环境启动时告警「CSWSH 防御未启用」。测试见 `pkg/wsx/config_test.go`
- `pkg/wsx/hub.go`：`Hub` 会话管理器，以 playerID 为维度维护会话集合（同端可多连接，跨端由登录顶号收敛）；`SetMessageHandler` 用 `atomic.Pointer` 存储回调（不与 `sessions` 共用锁，避免上行热路径与注册/注销争锁），`dispatchMessage` **对业务回调做 recover 兜底**（回调在读协程内同步执行，panic 会击穿 goroutine 顶层终止进程并绕过会话清理），并把帧的 `bizId` 一并传给回调
  - `SendToPlayer`/`PushToPlayer`/`PushToPlayers` 定向与批量推送：推送前取会话快照，**锁外执行网络 IO**；帧与负载**全程只序列化一次**，跨玩家跨连接复用同一份只读字节，避免万人广播产生 N×M 次重复编码；批量推送经 `snapshotMany` **单次加读锁**取全量快照（返回平铺的会话列表 + 每玩家连接数），把「每个目标玩家一次 RLock + 一次切片分配」压缩为一次，避免万人广播与注册/注销路径反复争锁；三者对 nil 帧/空负载均有防御：记录告警并返回零送达，绝不向连接写入空帧（push 帧构建 `buildPushFrame` 对 nil 负载返回 nil，调用方随即跳过）
  - `KickPlayerWithNotice(playerID, payload)` / `KickPlayerWithNoticeExcept(playerID, payload, exceptTokenDigest)`：顶号专用——先向该玩家在线连接推送通知帧（`cspb.KickedOfflinePush`），再以 `4002` 断开。`Except` 版本跳过令牌摘要一致的会话（即新登录端已建立的连接），跨实例广播必须使用它，否则延迟到达的广播会误踢新连接。`notifyAndClose` 通过写协程的 FIFO flush 屏障等待通知帧真正写出后才下发 Close 帧（直接 `KickPlayer` 会先关闭 `done` 让写协程退出，缓冲中的通知帧被丢弃）；通知序列化失败/超时仍会断开，客户端以关闭码兜底。`KickPlayer` 断开指定玩家全部连接（供封禁/踢人联动，Close 码 `4001`）、`Stats` 在线统计、`CloseAll` 全量断开（Close 码 `4007`）。两者都经 `closeSessions` 以固定并发度（`closeWorkers`=64）关闭：下发 Close 帧需争抢连接写锁（受 `closeFrameWriteWait` 约束），串行关闭会把停机/批量踢人拖成 N×等待时间；并发上限同时避免万人级停机瞬间创建同等数量协程
  - `register` 在单玩家在线连接数超过 `Config.MaxConnectionsPerPlayer` 时踢除该玩家最早建立的连接（Close 码 `4008`，与顶号的 `4002` 区分：此处账号仍在本端，只是异常客户端反复建连），防止异常客户端反复建连耗尽内存。踢除对象由 `pickEvictTarget` 在锁内选出、锁外关闭（避免与 `unregister` 的锁重入），排序依据是 `Hub` 分配的**单调递增 `seq`**（`connSeq`）而非毫秒精度的 `connectedAt`——同一毫秒内建立的多条连接用时间戳无法比较，会让「踢最早」退化成「随机踢一条」
  - 实现 `fiber.Service` 接口（`Start`/`State`/`Terminate`）随应用生命周期优雅启停，服务退出时统一关闭所有长连接
- `pkg/wsx/session.go`：`Session` 单连接会话——记录 `tokenDigest`（握手令牌 SHA-256 摘要，不存明文），供顶号踢线时区分旧端与新登录端连接（不受跨实例时钟偏差影响）；独立下行缓冲通道 + 唯一写协程串行写底层连接（规避并发写导致的协议解析错乱）；读协程 `proto.Unmarshal` 解析 `cspb.WsMessage` 帧并处理：`heartbeat` 帧应答 `heartbeatAck`（负载 `WsHeartbeatResp`）、`heartbeatAck` 仅刷新活跃时间、`business` 业务帧连同 `bizId` 交业务回调，其余类型（`handshake`/`push`/`error` 等服务端下行专用）视为非法上行、收敛攻击面回 `error` 帧；非法帧/不支持类型回 `error` 帧（负载 `WsErrorInfo`，错误码 `httpx.ErrCodeBadRequest`）后继续而不中断连接；写协程默认不主动下发心跳；仅当 `HeartbeatInterval > 0`（服务端主动心跳开启）时按该间隔发送 `heartbeat` 帧（负载 `WsHeartbeatReq`）并检查失联；心跳关闭时静默连接由读协程读超时 `ReadWait` 兜底——读协程识别读超时错误（`net.Error.Timeout`）后同样以关闭码 `4005` 断开并记录日志，保证心跳开/关两种模式下失联断开的关闭码与可观测性一致；下行缓冲满判定客户端消费过慢产生背压，主动断开连接防内存膨胀；会话关闭仅关闭 `done` 信号通道（**绝不关闭 send 通道**），发送端经 `select { case send<-data; case <-done }` 感知关闭并直接返回，从根本上杜绝向已关闭 channel 发送导致的 panic；写入使用二进制消息类型（protobuf 帧）；会话日志统一携带 `requestId`（取自握手请求，见 `ConnID()`）与 `userId`
- **上行限流采用令牌桶**（`allowInbound`）：令牌按 `MaxMessagesPerSecond` 匀速补充，桶容量 `InboundBurst` 决定允许的瞬时突发（建连时为满桶，客户端可立即发一轮请求），长期平均速率严格受限。有意不用固定窗口计数——固定窗口会在窗口边界放行两倍配额。桶状态仅由读协程串行访问，无需同步原语。测试见 `TestInboundBurstAllowsShortSpikeThenThrottles`
- **服务端主动断开必须告知原因**：`CloseCodeXxx` 定义 RFC 6455 私有区间（4000~4999）的关闭状态码——`4001` 被踢下线（封禁/GM）、`4002` 被新设备顶号踢线（已建连时先发 `KickedOfflinePush` 再发本码；客户端离线错过通知时，重连会补发本码）、`4003` 上行限流超限、`4005` 读超时/心跳超时判定失联（读协程读超时与写协程心跳检查统一使用该码）、`4006` 握手身份非法（含握手令牌已撤销/过期）、`4007` 服务停机、`4008` 单玩家并发连接数超限踢除最早连接（同端重复建连，与顶号的 `4002` 区分）。这些码属于**客户端协议契约**，新增须同步通知客户端。不告知原因时客户端无法区分「被封禁」与「网络抖动」，只会无脑自动重连：对被封禁玩家形成重连风暴，对限流场景则立刻再次触发同样的问题
  - 唯一例外是「下行缓冲积压」（背压）：刻意不下发 Close 帧而直接断开。一是缓冲被写满恰恰说明客户端没在消费下行数据，Close 帧本就送不到；二是此时写协程正卡在写入上、握着连接写锁，下发 Close 帧必然等满 `closeFrameWriteWait` 才失败——而 `enqueue` 会被 Hub 的广播循环调用，每条积压连接都白等一次会把整轮广播拖慢
  - 关闭流程拆成三个幂等步骤：`signalClose`（注销会话 + 关 `done`，不触碰底层连接）、`sendCloseFrame`（经 `WriteControl` 下发 Close 帧，等写锁上限 `closeFrameWriteWait`=100ms，拥塞即放弃告知而非拖慢批量收尾）、`closeConn`（关底层连接）。`signalOnce` 与 `connOnce` **必须分开**，否则无法拆出「读协程独占连接」的窗口
  - `closeWith(reason)`：供任意协程调用（Hub 踢人/停机、写协程、`Run` 收尾），三步顺序执行
  - `closeAfterDrain(reason)`：**仅供读协程调用**，多两步不可省略的处理。① 先 `signalClose` 并等写协程退出（等待上限 100ms，超时即直接断开）：写协程与之操作同一条连接，而下发 Close 帧后底层库会把连接标记为「已发送关闭」，写协程随后任何写入都会失败并走「写失败即断开」分支直接关连接，排空尚未开始就被打断。② 再排空对端在途数据（上限 `closeDrainWait`=200ms）：读协程主动断开的典型场景是上行限流，此时客户端正在高频发送、接收缓冲必然有未读数据，此时关闭 socket 内核会发出 **RST 而非 FIN**，对端接收缓冲连同刚收到的 Close 帧一起被丢弃，客户端只会看到「连接被重置」。这两步缺任一步，限流断开都无法把 `4003` 传达给客户端（已由 `TestCloseReasonIsSentToClient` 覆盖）
- **`Session.Run` 的收尾时序（关键约束）**：`defer` 中先 `close()`（关 `done` 并断开底层连接，令在途写入立即失败）再 **阻塞等待写协程退出信号 `writeDone`**，最后才返回。两点缺一不可：① 必须用 `defer` 收尾，否则业务回调 panic 时读协程向上 unwind 会跳过清理，会话永久残留在 Hub 中成为幽灵会话；② 必须等写协程退出，因为 Fiber 的 websocket 中间件在升级回调返回后立即执行 `releaseConn`（把 `*websocket.Conn` 的底层连接置 nil 并归还 `sync.Pool`），此时若仍有写入在途，要么命中 nil 底层连接导致**进程崩溃**，要么写进被复用后属于其他玩家的连接造成**数据串号**。回归测试见 `pkg/wsx/session_test.go` 的 `TestRunWaitsWritePumpBeforeReturn`
- `pkg/wsx/message.go`：推负载与统计结构——`PushPayload{type,data,bizId}`（帧类型缺省自动按 `push`，负载支持 proto.Message/`[]byte`/任意对象）供进程内推送与外部内部接口复用，`buildPushFrame` 构建推送帧（负载为 nil 时返回 nil，调用方跳过推送；`msgId` 由 `payloadMsgID` 按负载类型自动推导，客户端据此识别 `data` 内的具体业务消息，注册新的推送消息时需同步补充该映射），`encodePayload` 按负载类型选择编码，`buildErrorFrameBytes` 构建错误帧字节（会话内错误应答与建连阶段拒绝应答共用）；推送结果 `PushResult`/`BatchPushResult` 与在线统计 `Stats`
- `pkg/wsx/auth.go`：两段式握手鉴权（`HandshakeAuth{Validate, CheckSession}`）
  - 为什么要分两段：浏览器 WebSocket API 读不到握手失败（非 `101`）的响应体，只能拿到 `1006` 异常关闭码；「被顶号/被撤销」必须展示给用户，只能放在升级后以 `error` 帧 + Close 码送达。同时伪造令牌仍要在升级前被廉价挡掉——升级后拒绝要多付一次协议升级 + 连接缓冲 + 排空（`closeDrainWait`=200ms），把签名校验留在升级前是成本最小的组合。
  - `NewJWTAuthWithStore(jwt, tokenStore)`：**生产环境应使用此构造器**。`Validate` 只做 `jwt.ParseToken`（签名 + 有效期），`CheckSession` 比对 Redis Token 存储（`TokenStore.Validate`），使踢人/封禁/顶号对新握手同样即时生效。这是封禁链路闭环的必要条件——`Hub.KickPlayer` 只断开当前连接，而客户端普遍带自动重连，若握手只验签名，被封禁玩家会立刻重连成功，「撤销 Token + 踢连接」只换来一个重连间隔。容错策略与 `httpx.JWTMiddlewareWithTokenStore` 一致：Redis 查询异常时**失败放行**（仅告警），避免存储抖动导致全量玩家无法建连；明确判定 Token 不存在/已撤销时返回 `ErrTokenRevoked`（升级后配 Close `4006`），被新登录顶替时返回 `ErrSessionReplaced`（升级后配 Close `4002`，即离线错过的顶号通知补发）
  - `NewJWTAuth(jwt)`：等价于 `NewJWTAuthWithStore(jwt, nil)`，只有 `Validate`、无 `CheckSession`，退化为纯签名校验，仅适用于 Redis 未就绪的场景
  - `AuthMiddleware(validate TokenValidator)` **只挂升级前的签名校验**：从 query 参数 `token` 取令牌（浏览器 WebSocket API 无法设置 Authorization 头），校验通过写 `Locals("userId")`（键名与 httpx JWT 中间件一致），升级后经 `conn.Locals("userId")` 取回；失败返回统一 JSON 错误信封（HTTP 200 + `code`）。**令牌被撤销与签名无效给出不同提示语**（`token has been revoked` / `invalid or expired token`），客户端据此决定「重新登录」而非「重试握手」
  - 注意 query 携带令牌会流入访问日志，故 `httpx.LogMiddleware` 对 `token` 等敏感 query 参数统一脱敏
  - 测试见 `pkg/wsx/auth_test.go`（撤销/无效/缺失令牌的提示语与业务码、`validate` 为 nil 时放行、升级后顶号 `10402`+`4002` 与撤销 `10401`+`4006` 的端到端断言）
- `pkg/wsx/upgrade.go`：`UpgradeHandler` 对非升级请求（普通 GET/curl）返回错误信封，避免进入协议解析；升级成功后**先执行 `auth.CheckSession`（升级后会话校验），再校验玩家ID → 创建会话 → 注册 Hub** → `Session.Run(onReady)` 先启动写协程、再经 `onReady` 回调下发 `handshake` 帧（负载 `WsHandshakeInfo`，确保写协程已就绪而非依赖发送缓冲隐式容量）→ 进入阻塞式读循环；**会话校验与玩家ID校验都必须在 `Hub.register` 之前**：若放到注册之后，被顶号的旧连接会占用该玩家的连接配额，并把合法的第 6 条连接当作「最早连接」踢掉；拒绝路径不创建 `Session`、不起协程，成本仅为一次升级 + 错误帧 + Close 帧。**玩家ID 非正整数时经 `rejectConnection` 直接拒绝建连**（否则所有异常连接都会落进 `playerID=0` 的同一会话桶，彼此触发连接数超限踢除，并被 `PushToPlayer(0)` 误当作同一玩家广播）；`Locals`/`Query` 均由中间件在升级时从请求上下文拷贝进 `conn`，连接存活期间读取安全（`requestId` 即由此取得并作为连接追踪ID）；`Register(route, path, hub, cfg, auth *HandshakeAuth)` 一步注册两段鉴权 + 升级（`auth` 为 nil 跳过鉴权）；**注意**：服务若挂载全局 JWT 中间件（精确路径匹配），须将升级路径加入其 skipPaths，Header 鉴权不会拦截 query 方式携带令牌的握手
  - `rejectConnection` 只下发 Close 控制帧（状态码 `4002` 顶号补发、`4006` 身份非法/撤销），不再下发业务错误帧——握手阶段的结论已由关闭码完整表达，多写一帧只会增加拒绝成本；关闭前排空对端在途数据，接收缓冲残留未读数据时关闭 socket 会触发 RST，刚下发的 Close 帧会连同对端接收缓冲一起被丢弃
- `pkg/wsx/session_test.go`：会话生命周期与并发时序测试——`Run` 收尾契约（写协程先退出再返回）、断连瞬间并发推送不残留幽灵会话、业务回调 panic 后连接仍可用、非法玩家ID 拒绝建连（含 Close 码断言）、单玩家连接数上限、`pickEvictTarget` 按 `seq` 选出最早连接（构造同一毫秒的多条会话，验证不退化为随机）、上行令牌桶限流（突发放行 + 长期限速 + 关闭限流）、`KickPlayer`、顶号「先通知后关闭」顺序与 `KickPlayerWithNoticeExcept` 按令牌摘要保留新连接（`TestKickPlayerWithNotice`/`TestKickPlayerWithNoticeSkipsReplacedToken`）、四类主动断开都能把 Close 状态码送达客户端（`TestCloseReasonIsSentToClient`）、批量推送复用编码字节、非升级请求错误信封、nil 帧/空负载防御（`TestNilFrameGuard`/`TestSendFrameNilGuard`）
- `pkg/wsx/config_test.go`：默认配置合法性、`Validate()` 对非法组合的拦截、`HasOriginWildcard` 判定、`inboundBurst` 缺省推导
- `pkg/wsx/auth_test.go`：握手鉴权失败的业务码与提示语区分（签名无效 / 缺少令牌 / 跳过鉴权）、升级前签名闸门拦截与升级后会话校验的两段行为（**被新登录顶替 → Close 4002（补发）**、令牌被撤销 → Close 4006，且断言不夹带业务帧），以及 `mapTokenStoreError` 的映射与「存储异常失败放行」分支

### 6.11 存档压缩与签名（pkg/savex）

- `pkg/savex/savex.go`：玩家存档的 protobuf 序列化、gzip 压缩与 SHA256 签名工具
  - `Encode(msg)`：protobuf 线格式 → gzip（`BestCompression`）→ 输出压缩后的线格式字节
  - `Decode(wire, newMsg)`：解压并反序列化指定的 protobuf 消息（base64 解码由调用方负责）
  - `CompressWire`/`DecompressWire`：供已持有 protobuf 原始字节的通用存档读写链路使用
  - `ComputeSign(wire)`：线格式字节的 SHA256 小写十六进制（64 字符）

### 6.12 收发包编解码（pkg/packetx）

- `pkg/packetx/packetx.go`：客户端 protobuf 收发包的加解密线格式；由 `pkg/httpx` 在收发包边界（`Bind`/`OK`/`Fail`）按玩家专属秘钥调用（详见「收发包加解密」）
  - `Encode(msg, key)`：protobuf 线格式 → AES-CBC 加密（每份数据随机 16 字节 IV 前缀 + PKCS7 填充）→ 输出加密后的线格式字节
  - `Decode(wire, key, newMsg)`：逆过程（base64 解码由调用方负责）；`SealWire`/`OpenWire` 支持直接处理原始字节
  - `ComputeSign(wire)`：加密后线格式字节的 SHA256 小写十六进制（64 字符）
  - `GenerateKey()`：生成 32 字节随机密钥并 base64 编码；熵源不可用时自动降级为确定性派生，保证必定返回有效密钥。`DecodeKey` 反向解析，`NormalizeKey` 允许 AES-128/192/256 长度，其余长度使用 SHA256 规范化
- `pkg/packetx/packetx_test.go`：单元测试——密钥生成与规范化、AES-CBC 编解码往返、错误密钥/篡改数据拒绝、原始字节与 protobuf 编解码互通、SHA256 签名稳定性
- `pkg/httpx/packet_test.go`：收发包加解密集成测试——携带秘钥时请求需解密/响应被加密/错误响应被加密、无秘钥（登录）时明文回退

## 7. 配置系统与热更

### 7.1 配置数据与内存访问层

- 配置访问层自动生成代码位于 `generated/gdconfig/`
  - `ConfigManager`：每张表用 `atomic.Pointer` 存快照，支持无锁热更：`generated/gdconfig/manager.go`
  - `SwapTables(dst, src)`：逐表原子替换，保证持有 `dst` 指针的所有读者无感知

### 7.2 配置发布消息与频道约定

- 频道常量与消息结构统一在 `services/shared/configx/configx.go`
  - `PublishChannel = "config:publish"`
  - `PublishMessage` 字段包含 `releaseId/tables/operator/occurredAt`

### 7.3 热更链路（订阅侧：GameLogic / Platform）

- GameLogic：`services/gamelogic/service.go` 中 `runConfigSubscriber()` → `handleConfigPublishMessage()`
  - 收到消息后创建 `newManager` 全量加载 `gconfig_db`
  - `gdconfig.SwapTables(s.gdConfig, newManager)` 原子替换
- Platform：`services/platform/service.go` 中 `runConfigSubscriber()`（流程与 GameLogic 一致）
- 同类订阅模式复用：GameLogic 另有 `runPaySuccessSubscriber()`（订阅 `iap:paySuccess`，把支付成功消息路由到本进程 Hub 下发 WS 推送）与 `runSessionKickedSubscriber()`（订阅 `auth:sessionKicked`，把顶号事件路由到本进程 Hub 下发通知并断开旧连接），断线重连策略一致，频道常量与消息结构分别见 `services/shared/iapdto`、`services/shared/authdto`，详见 5.1「WebSocket 长连接」

### 7.4 发布侧（GMT）

- 发布侧核心业务通常在 `services/gmt/internal/logic/config_logic.go`（包含配置发布、构建、发布等用例）
- 发布完成后向 `config:publish` 发布消息，驱动订阅侧热更

## 8. 运行方式

### 8.1 前置依赖

- Go：见 `go.mod`（当前声明 `go 1.25.6`）
- MongoDB：默认 `mongodb://localhost:27017`
- Redis：默认 `localhost:6379`

### 8.2 配置文件约定

- 配置目录默认 `configs/`，可通过 `CONFIG_DIR` 覆盖。
- 环境默认 `local`，可通过 `APP_ENV` 覆盖（如 `prod`/`staging`）。
- 目标配置文件为：`{serviceLower}_{env}.yaml`
  - 例：`configs/platform_local.yaml`
- 关键敏感项（如 `JWT_SIGNING_KEY`、`INTERNAL_SECRET`、服务间 URL）不允许使用代码默认值，缺失时服务会在启动前直接失败

### 8.3 本地启动（示例）

在仓库根目录执行（可按需覆盖环境变量）：

```bash
$env:JWT_SIGNING_KEY="请替换为至少32位强密钥"
$env:INTERNAL_SECRET="请替换为至少32位强密钥"
go run ./services/platform
go run ./services/gamelogic
go run ./services/gmt
```

也可以使用 `.vscode/launch.json` 中的调试配置启动，但密钥类环境变量应从当前终端/系统环境继承，禁止把真实密钥直接写入仓库文件。

### 8.4 关键环境变量（摘录）

- 基础通用
  - `APP_ENV`：环境名（默认 local）
  - `CONFIG_DIR`：配置目录（默认 configs）
  - `SERVER_PORT`：服务端口（也可在 YAML 里配置 `server.port`）
  - `MONGO_URI` / `MONGO_DATABASE`
  - `REDIS_ADDR` / `REDIS_PASSWORD` / `REDIS_DB`
  - `JWT_SIGNING_KEY`：JWT 签名密钥（所有环境必须显式注入，建议至少 32 位）
- 跨服务调用
  - Platform：`GAMELOGIC_URL`、`INTERNAL_SECRET`
  - GameLogic：`PLATFORM_URL`、`INTERNAL_SECRET`
  - GMT：`PLATFORM_URL`、`GAMELOGIC_URL`、`INTERNAL_SECRET`
- GMT 安全增强
  - `GMT_BOOTSTRAP_ADMIN_USERNAME` / `GMT_BOOTSTRAP_ADMIN_PASSWORD` / `GMT_BOOTSTRAP_ADMIN_NICKNAME`：仅首次初始化超管时显式提供；未设置则不会自动创建超管
- GMT 前端托管
  - `GMT_WEB_DIST`：Vue 前端打包后的静态文件目录；未配置则不启用静态托管
  - `GMT_WEB_PROXY_TARGET`：可选，后台 API 反向代理目标；为空时 `/api` 由同端口 GMT 路由处理（同源集成）

### 8.5 生产部署

- 生产发布采用「软链接原子切换 + Docker Compose」方案：部署根目录为 `/data/servers`，其中 `releases/<svc>/<version>` 存放版本二进制（编译后可执行程序），`current/<svc>` 软链接指向当前生效版本，容器只读挂载软链接目录，日志目录挂载宿主机 `/data/logs`，发布 = 原子切换软链接 + `docker compose restart`，支持秒级回滚。
- GMT 前端同源托管在部署侧的落地：宿主机 `/data/web`（Vue 打包产物，含 `index.html`）只读挂载到 gmt 容器 `/data/web`，并以 `GMT_WEB_DIST=/data/web` 指向该目录，三者路径须一致；新增该挂载或改动 compose 需 `docker compose up -d gmt` 重建容器（`restart` 不重读挂载）。未挂载产物时 gmt 退化为纯 API 服务，访问 `/` 会因 JWT 返回 `10401`。详见 `docs/deploy.md` 5.6。
- 前端页面白屏 / 控制台报 `Failed to load module script ... MIME type of "text/html"` 的排查顺序：① 启动日志中的「启用前端静态托管」一行确认 `rootAbs` / `rootReal` / `hasIndexHtml`（产物是否真的挂上）；② `curl -I http://127.0.0.1:8082/assets/<hash>.js` 检查 `Content-Type` 是否为 `text/javascript`——返回 `text/html` 说明资源未命中而走了 SPA 回退，返回 `404` 说明产物目录里确实没有该文件（多为前端产物与 `index.html` 版本不一致，需整目录一起替换）。发版覆盖产物后最多有 10s 的旧文件句柄缓存（fasthttp FS `CacheDuration`），属正常现象。注意静态资源成功命中的访问日志为 `Debug` 级别，生产默认 `info` 时看不到，排查请以 `curl -I` 的响应头为准，或临时把日志级别调到 `debug`。
- 静态资源被浏览器缓存住导致「改了产物但页面没变」时，先看响应头：`index.html` 与 `/.well-known` 下的文件应为 `no-cache, no-store, must-revalidate`，带 hash 的资源应为 `public, max-age=31536000, immutable`，`404` 与传输层 `500` 应为 `no-store`。若 hash 资源的内容变了而文件名没变（构建未开启内容 hash），长缓存会让浏览器一年内不回源——这是构建配置问题，不要靠改服务端缓存头绕过。
- 首次部署前用 `scripts/install.sh` 预装 gzip/gunzip（gunzip 供 upload.sh 在服务端解压上传的二进制，缺失或安装失败不中断、upload.sh 会自动回退为不压缩上传）、创建 `/data/servers` 与 `/data/logs` 目录结构，并同步 `compose.yaml` / `deploy.sh` / `rollback.sh` 到部署目录——同步源为「install.sh 所在目录」（本地仓库 scripts/ 随 install.sh 同目录上传的文件），仅本地文件复制、不拉取任何远程 git 仓库。Docker 相关环境（curl、Docker Engine 与 docker compose 插件、registry 镜像加速、运行时镜像 `alpine:3.22` 预拉取）抽离在 `scripts/install_docker.sh`：install.sh 以兄弟脚本方式调用并原样透传 `--mirror` / `--registry-mirror` / `--skip-docker` / `--skip-pull`，也可单独执行 `sudo ./scripts/install_docker.sh`；Docker 已存在但缺 compose 插件时会自动补装，`--registry-mirror` 配置镜像加速时对已存在的 `/etc/docker/daemon.json` 采用合并而非覆盖，保留其他已有配置。镜像本地缓存后，发布/回滚使用的 `docker compose restart` 不再依赖外网。若 `compose.yaml` / `deploy.sh` / `rollback.sh` 缺失（如上传漏文件，脚本依赖 `install.sh` 的兄弟文件），同步失败仅打印 `[WARN]` 告警，docker/镜像/目录/nginx 等环境初始化照常完成、脚本不中断；收尾会提示补齐文件后再执行 `docker compose up -d` 与发布/回滚，重跑 install.sh 可自动补同步（已存在文件不会被覆盖）。
- 首次部署需上传到目标服务器的文件：A 组 scripts 五件套 `install.sh` / `install_docker.sh` / `compose.yaml` / `deploy.sh` / `rollback.sh`（须同目录上传）；B 组生产配置 `config/<svc>_prod.yaml` ×3 + `server.env`（写入 `/data/servers/config/`，权限 600）；C 组构建产物二进制 `platform` / `gamelogic` / `gmt`（scp 到服务器 `/tmp/`，由 deploy.sh `--binary` 投放）；D 组 GMT 前端产物（可选，解压到 `/data/web`）。详见 `docs/deploy.md` 5.2。
- Docker 环境预装逻辑抽离为独立脚本 `scripts/install_docker.sh`（根权限校验 + 发行版识别 + curl/Docker Engine/docker compose 插件安装校验 + registry 镜像加速 + 运行时镜像预拉取），可单独执行；install.sh 调用它时通过环境变量传入已探测的 `OS_FAMILY`/`OS_ID`，避免子脚本重复解析 `/etc/os-release`。
- install.sh 支持 `--owner <用户>` 把 `/data/servers` 与 `/data/logs` 属主收归为非 root 运维用户（非 root 运维模型），配合 docker 组实现免 sudo 发布；敏感配置 `server.env` 与三个 `*_prod.yaml` 统一权限 600。详见 `docs/deploy.md` 5.5。
- install.sh 生成的 nginx 反代 server 块内置 gzip 压缩（`gzip on` / `gzip_vary on` / `gzip_proxied any` / `gzip_min_length 1024` / 显式 `gzip_types` 覆盖 JS/CSS/JSON/XML/wasm/svg/ttf）：前端产物的压缩刻意放在网关层而非 Go 服务层（Go 侧需在只读挂载的产物目录旁写压缩副本，写不进去）。两个默认值容易踩坑——`gzip_proxied` 默认 `off`（反代响应不压缩）、`gzip_types` 默认只含 `text/html`，缺任意一项 JS/CSS 都不会被压缩。已自带压缩的 `woff2`/`png`/`jpg`/`webp`/`mp4` 不重复压缩。已存在的反代配置默认不被覆盖，需要套用新模板时加 `--force-nginx-conf`（会先备份 `.bak.<时间戳>`）。
- 配套脚本：`scripts/install.sh` + `scripts/install_docker.sh`（云主机环境预装：gzip/gunzip、Docker 与 docker compose 插件、镜像加速与运行时镜像预拉取、目录结构与部署文件同步）、`scripts/build.sh`（交叉编译 + ldflags 注入版本号）、`scripts/upload.sh`（gzip 压缩二进制后上传，远端解压并按 sha256 校验后才替换正式文件）、`scripts/deploy.sh`（发布 + 健康/版本校验 + 失败自动回滚）、`scripts/rollback.sh`（手动回滚）。
- 完整方案评估、目录结构、compose 示例与操作手册见 `docs/deploy.md`。

## 9. 代码生成与脚本

### 9.1 Protobuf

- proto 源文件：`pb/cs.proto`、`pb/def.proto`、`pb/ws.proto`（`def.proto` 与其余文件同包名 `cspb`，定义通用结构供 `cs.proto`/`ws.proto` 引用——文本内容 `TextContent`/`TextType`、WebSocket 业务推送消息标识枚举 `WsMsgId`）
- 生成代码目录：`pb/cspb/`（生成代码禁止手改）
- 生成脚本：`gen_proto.sh`
- 工具链版本（已在脚本内锁定并做启动校验，版本不匹配直接报错退出）：
  - `protoc` 30.2（生成文件头部注释显示为 `v6.30.2`），下载地址 <https://github.com/protocolbuffers/protobuf/releases/tag/v30.2>
  - `protoc-gen-go` v1.36.6，安装命令 `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6`
  - 运行时依赖 `google.golang.org/protobuf`（go.mod）版本需 ≥ v1.36.6
- 可执行文件查找顺序（不含任何个人机器专属路径）：`PROTOC` / `PROTOC_GEN_GO` 环境变量 → `PROTOC_HOME/bin`（protoc 解压目录）→ `GOPATH/bin`（`go install` 默认落点，仅 protoc-gen-go）→ `PATH`。多版本共存时用环境变量精确指定，避免受 `PATH` 顺序影响

### 9.2 配置访问层（gdconfig）

- 生成工具：`scripts/genconfig/main.go`
- 使用说明：`scripts/genconfig/README.md`
- 输出目录：`generated/gdconfig/`
- 数据写入归一化：`services/gmt/internal/logic/config_logic.go` 的 `buildDocs` 在写入暂存/生产集合前，依据 Schema 字段类型对数组（`[]T`）与映射（`map[K]V`）字段做归一化——上传端会把空单元格序列化为空字符串，若直接入库，读取端 `bson.Unmarshal` 进 `[]T` / `map[K]V` 字段会失败或产生脏数据，故空单元格统一写成空数组（`bson.A{}`）/ 空文档（`bson.D{}`）。例如 `Usertag.abGroup`（`[]int32`）留空时入库为 `[]` 而非 `""`。

## 10. 依赖关系

### 10.1 Go Module 依赖（核心）

见 `go.mod`，关键依赖包括：

- `github.com/gofiber/fiber/v3`：HTTP 框架
- `github.com/gofiber/contrib/v3/websocket`：WebSocket 服务端升级中间件（封装于 `pkg/wsx`，GameLogic `/ws` 复用）
- `github.com/fasthttp/websocket`：上述中间件的底层 WebSocket 实现，`pkg/wsx` 的测试用它作客户端拨号（仅测试代码直接引用）
- `github.com/bytedance/sonic`：JSON 编解码
- `go.mongodb.org/mongo-driver/v2`：MongoDB driver
- `github.com/redis/go-redis/v9`：Redis 客户端
- `go.uber.org/zap` + `gopkg.in/natefinch/lumberjack.v2`：日志
- `github.com/golang-jwt/jwt/v5`：JWT

### 10.2 服务间依赖（调用方向）

- GameLogic 调用 Platform：`services/gamelogic/service.go` 中初始化 `httpcli.NewService(..., "platform")`
- Platform 调用 GameLogic：`services/platform/service.go` 中初始化 `httpcli.NewService(..., "gamelogic")`
- GMT 同时调用 Platform 与 GameLogic：`services/gmt/service.go` 中初始化两套 `httpcli.NewService`

## 11. 对外接口与测试

### 11.1 API 文档与路由清单

- 仓库已有 API 文档：
  - `docs/login_api.md`
  - `docs/save_api.md`
  - `docs/transport_encryption.md`
  - `docs/servertime_api.md`
  - `docs/friend_api.md`（好友系统客户端接口：发送/处理/撤销申请、申请列表、好友列表、好友排行榜、删除好友）
  - `docs/ws_api.md`（WebSocket 长连接接入文档：连接建立与握手鉴权、消息帧协议 `cspb.WsMessage`、心跳机制、上行业务消息、服务端推送、关闭状态码与客户端处理建议、服务端 `websocket` 配置项）
  - `docs/gmt_api/iap.md`（GMT 内购订单管理接口：渠道支付订单查询、业务订单查询、玩家充值统计查询、手动补单，以及 local/dev 环境模拟补单任务说明）
- 运营侧/内部接口补充说明：
  - GMT：`docs/gmt_api/`（按功能模块拆分：认证/个人资料/管理员/操作日志/封禁/内购/配置发布/自定义配置/Proto/邮件，入口 `docs/gmt_api/README.md`）
  - Platform（GMT 聚合相关）：`services/platform/internal/handler/GMT_API.md`

### 11.2 测试

- 集成测试：`tests/`
  - `system_test.go`：健康检查与版本接口，校验服务标识、非空版本和 Unix 毫秒时间戳
  - `ws_test.go`：WebSocket JWT 建连、握手、客户端主动心跳，以及本地模拟支付后的 `PaySuccessPush` 推送
  - `login_test.go`：游客登录的 JSON/protobuf 成功路径，以及无效 UUID、未登记登录类型、缺少平台参数的前置校验
  - `save_test.go`：存档上传/下载、多版本、多 key、强制覆盖，以及 CAS 冲突保护和下载 key 去重
  - `servertime_test.go`、`iap_test.go`：服务器时间、IAP 内购
  - `friend_test.go`：好友申请、撤销、同意、拒绝、列表、排行、删除等状态流转
  - `mail_test.go`：邮件列表、一键领取、删除，以及单封阅读/领取不存在邮件时的错误响应
  - `player_test.go`：按 playerId / inviteCode 查询，以及缺少参数、邀请码非法和 playerId 优先级
  - `whitelist_test.go`：充值白名单管理（GMT 管理后台添删查）与白名单订单自动支付（需 GMT/GameLogic/Platform 与已引导超管）
  - `custom_config_test.go`：自定义配置查询（无鉴权）

> 运行集成测试需要已启动的 GameLogic、Platform、MongoDB（副本集）和 Redis，并应指向专用测试数据库。GameLogic 对 `/auth/login` 固定按 IP 限流为每分钟 10 次；完整套件会创建大量独立游客账号，因此应使用限流计数已清空的独立测试环境，或按测试模块分批执行并间隔至少一个限流窗口，避免把限流业务码 `10429` 误判为功能回归。
- 常规单元测试：散落在 `pkg/*/*_test.go`

## 12. 开发规范

项目强约束见 `AGENTS.md`（包括命名规范、日志字段、时间戳单位、JSON 使用 sonic、HTTP 返回约定等）。在改动业务代码前建议通读并以其为准。

