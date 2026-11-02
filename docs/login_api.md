# 登录接口文档

> 客户端：Unity (C#)\
> 协议：HTTP/1.1，JSON 编码\
> Base URL：由部署环境决定，本地开发默认 `http://192.168.2.65:8081`

***

## 目录

- [通用约定](#通用约定)

- [登录接口](#登录接口)

- [登录流程](#登录流程)

- [错误处理](#错误处理)

***

## 通用约定

### HTTP 状态码约定

所有客户端接口**始终返回 HTTP 200**。客户端通过 JSON body 中的 `code` 字段区分业务结果，无需关注 HTTP 状态码。

### 请求头

| Header         | 必填 | 说明                                                                            |
| -------------- | -- | ----------------------------------------------------------------------------- |
| `Content-Type` | 是  | `application/json`（默认）或 `application/x-protobuf` / `application/protobuf`（可选） |

登录接口**无需**携带 `Authorization` Header。

> 响应格式与请求 `Content-Type` 保持一致：发 JSON 收 JSON，发 protobuf 收 protobuf。

每个响应均携带以下 Header：

| Header          | 说明                           |
| --------------- | ---------------------------- |
| `X-Server-Time` | 服务器当前时间戳（Unix 毫秒），可用于客户端时间校准 |

### 序列化格式

服务端统一根据请求的 `Content-Type` 决定上下行的序列化格式，请求与响应格式保持一致。

| Content-Type                                      | 请求解析方式       | 响应序列化方式      |
| ------------------------------------------------- | ------------ | ------------ |
| `application/json`（或不填）                           | JSON         | JSON         |
| `application/x-protobuf` / `application/protobuf` | Protobuf 二进制 | Protobuf 二进制 |

Proto 定义文件见 `pb/cs.proto`，C# 命名空间为 `Cspb`，相关消息：`LoginReq`、`ThirdYre`、`LoginResp`、`ErrorResp`。

> **错误响应的 protobuf 兼容性**：所有 `*Resp` 消息的前两个字段（tag 1 = `code`，tag 2 = `message`）与 `ErrorResp` 保持一致，客户端可直接用具体 `*Resp` 类型解析错误响应，无需额外分支。

### 统一响应结构（JSON 模式）

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

| 字段        | 类型     | 说明                       |
| --------- | ------ | ------------------------ |
| `code`    | int    | `0` 表示成功，非 `0` 为业务错误码    |
| `message` | string | 结果描述，成功时为 `"ok"`         |
| `data`    | object | 成功时的业务数据，失败时为 `null` 或省略 |

***

## 登录接口

### `POST /auth/login`

统一登录入口，支持**游客登录**和**三方渠道登录**（如 `yre`）。每次启动游戏或Token过期均需重新调用此接口获取 Token。

#### 请求体

```json
{
  "loginType": "guest",
  "platform": "ios",
  "uuid": "550e8400-e29b-41d4-a716-446655440000",
  "authToken": "",
  "account": "",
  "thirdYre": {
    "t": 1755561600000,
    "siteCode": "site001",
    "ck": "checksum-xxxx",
    "uid": "third-user-id-xxxx"
  },
  "clientInfo": {
    "deviceId": "abc123",
    "deviceName": "iPhone 15",
    "brand": "Apple",
    "model": "iPhone15,2",
    "os": "iOS",
    "osVersion": "17.4",
    "language": "zh-Hans",
    "country": "CN",
    "timezone": "Asia/Shanghai",
    "network": "WiFi",
    "screenWidth": 393,
    "screenHeight": 852,
    "clientVersion": "1.0.0",
    "bundleVersion": "100",
    "channel": "AppStore",
    "sdkVersion": "1.0.0",
    "packageName": "com.example.game",
    "installId": "install-uuid-xxxx",
    "isEmulator": false
  }
}
```

#### 请求字段说明

| 字段           | 类型     | 必填 | 说明                                                                                                                                     | <br />             |
| ------------ | ------ | -- | -------------------------------------------------------------------------------------------------------------------------------------- | :----------------- |
| `loginType`  | string | 是  | 登录渠道标签，当前仅支持：`guest`（游客）、`yre`（yre 渠道，**必须携带并校验** **`thirdYre`**）；`apple` / `google` 等三方登录已下线，其他未注册渠道一律返回 `2400 authentication failed` | <br />             |
| `platform`   | string | 是  | 客户端平台：`ios` / `android`                                                                                                                | <br />             |
| `uuid`       | string | 条件 | **游客登录必填**；Unity 首次启动时生成并持久化（如：`System.Guid.NewGuid().ToString()`）                                                                     | 暂不强制uuid标准格式，简单校验。 |
| `authToken`  | string | 否  | 三方登录凭证（可选）；服务端已不再校验 token（apple / google 凭证验证已下线），当前所有渠道均为免验证，该字段仅为兼容保留，可传空或不传                                                         | <br />             |
| `account`    | string | 条件 | 第三方账号；免验证渠道（如 `yre`）必填，作为身份标识（openId）查询或创建账号，登录成功后写入玩家档案；`guest` 登录不填                                                                  | <br />             |
| `thirdYre`   | object | 否  | 三方数据（可选）；三方渠道登录时透传，详见下表                                                                                                                | <br />             |
| `clientInfo` | object | 是  | 客户端信息；**必须携带 `deviceId`**（严格单端/顶号判定依据，缺失返回 `2400 deviceId is required`）                                                                                   | <br />             |

#### thirdYre 字段说明

三方渠道登录时透传，GameLogic 原样转发至 Platform 服务进行签名校验。

| 字段         | 类型     | 必填 | 说明                                       |
| ---------- | ------ | -- | ---------------------------------------- |
| `t`        | int64  | 是  | 时间（Unix 毫秒时间戳），需落在服务端容忍窗口内（±5 分钟），超时视为无效 |
| `siteCode` | string | 是  | 站点编码                                     |
| `ck`       | string | 是  | 校验码，计算规则见下                               |
| `uid`      | string | 是  | 三方用户 ID                                  |

> **签名校验规则**：`ck = md5(siteCode + t + key + uid)`（小写十六进制），`key` 为三方平台共用签名密钥（服务端配置，不下发客户端）。`t` 为拼接时使用的原始毫秒时间戳字符串。
>
> **校验策略**（按渠道配置）：
>
> - `loginType=yre` 等强制校验渠道：**必须携带** **`thirdYre`**，缺失或校验不通过（密钥未配置、时间戳超窗、签名不匹配）均返回 `code:2400`（`third party verification failed`）。
>
> - 其他渠道：一律跳过校验（即使携带 `thirdYre` 也不校验）。

#### clientInfo 字段说明

| 字段              | 类型     | Unity API                               | 说明                              |
| --------------- | ------ | --------------------------------------- | ------------------------------- |
| `deviceId`      | string | `SystemInfo.deviceUniqueIdentifier`     | 设备唯一标识，**必填**：严格单端/顶号判定依据，缺失或空白返回 `2400 deviceId is required` |
| `deviceName`    | string | `SystemInfo.deviceName`                 | 设备名称                            |
| `brand`         | string | —                                       | 设备品牌                            |
| `model`         | string | `SystemInfo.deviceModel`                | 设备型号                            |
| `os`            | string | —                                       | 操作系统（`iOS` / `Android`）         |
| `osVersion`     | string | `SystemInfo.operatingSystem`            | 系统版本                            |
| `language`      | string | `Application.systemLanguage.ToString()` | 系统语言                            |
| `country`       | string | —                                       | 国家/地区                           |
| `timezone`      | string | `TimeZoneInfo.Local.Id（注意：真机取值不一样）`     | 时区                              |
| `network`       | string | `Application.internetReachability`      | 网络类型                            |
| `screenWidth`   | int    | `Screen.width`                          | 屏幕宽度（像素）                        |
| `screenHeight`  | int    | `Screen.height`                         | 屏幕高度（像素）                        |
| `clientVersion` | string | `Application.version`                   | 客户端版本号                          |
| `bundleVersion` | string | —                                       | 构建版本号                           |
| `channel`       | string | —                                       | 渠道（`AppStore` / `GooglePlay` 等） |
| `sdkVersion`    | string | —                                       | SDK 版本                          |
| `packageName`   | string | `Application.identifier`                | 包名                              |
| `installId`     | string | —                                       | 安装标识                            |
| `isEmulator`    | bool   | —                                       | 是否模拟器                           |

#### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "playerId": 10001,
    "nickname": "Guest_a1b2c3",
    "saveKey": "c2F2ZUtleS1iYXNlNjQtZXhhbXBsZQ==",
    "isNew": true,
    "isBound": false,
    "serverId": 1
  }
}
```

| 字段         | 类型     | 说明                                                                                                         |
| ---------- | ------ | ---------------------------------------------------------------------------------------------------------- |
| `token`    | string | 会话令牌（JWT）；需要认证的接口放入 `Authorization: Bearer <token>` Header                                                 |
| `playerId` | int64  | 玩家唯一标识，贯穿所有业务接口                                                                                            |
| `nickname` | string | 玩家昵称                                                                                                       |
| `saveKey`  | string | 玩家专属传输加密秘钥；登录成功后下发，用于 protobuf 模式下 HTTP 请求/响应 body 的传输加解密；该字段为 `bytes`，JSON 模式下以 base64 字符串呈现。详见 `docs/transport_encryption.md` |
| `isNew`    | bool   | `true` 表示首次注册的新玩家                                                                                          |
| `isBound`  | bool   | `true` 表示本次登录完成了游客账号升级绑定到三方账号                                                                              |
| `serverId` | int64  | 所在服务器ID；新玩家注册默认 `1` 服                                                                                      |

#### 错误响应

所有业务错误均返回 HTTP status 200，通过 `code` 字段标识：

```json
{ "code": 2400, "message": "uuid is required for guest login" }
```

| 业务错误码  | message                            | 说明                                                                             |
| ------ | ---------------------------------- | ------------------------------------------------------------------------------ |
| `2400` | `uuid is required for guest login` | 游客登录未传 `uuid`                                                                  |
| `2400` | `authentication failed`            | 平台认证被拒绝（未注册渠道（含已下线 apple / google）/ 免验证渠道未传 `account` / `thirdYre` 校验失败（yre）等） |
| `2400` | `platform is required`             | 未传平台字段                                                                         |
| `2400` | `deviceId is required`             | 未传 `clientInfo.deviceId`（或为空白），无法进行严格单端/顶号判定                              |
| `2400` | `invalid uuid format`              | UUID 格式非法                                                                      |
| `2403` | `account is banned`                | 账号已被封禁                                                                         |
| `2404` | `player not found`                 | 玩家记录不存在                                                                        |
| `2500` | `auth service error`               | 服务端内部错误                                                                        |

***

## 账号注销接口

### `POST /auth/deactivate`

注销当前登录账号。**注销不是删除数据，而是永久作废账号的登录身份**：原玩家数据（存档、邮件、好友、订单等）永久保留但被永久作废，原登录身份（游客 UUID / 三方 account）被释放。注销后再次以**同一身份**登录，将创建一个**全新的账号**（全新玩家数据），无法找回原账号数据。

> 该接口**需要认证**，请在 Header 携带 `Authorization: Bearer <token>`。玩家身份完全取自 Token，请求体无需携带任何身份字段。

#### 请求头

| Header          | 必填 | 说明                                                          |
| --------------- | -- | ----------------------------------------------------------- |
| `Content-Type`  | 是  | `application/json`（默认）或 `application/x-protobuf`（可选）        |
| `Authorization` | 是  | `Bearer <token>`                                            |

#### 请求体

请求体无业务字段（JSON 传空对象 `{}` 即可；protobuf 为空 `DeactivateReq`）。

```json
{}
```

Proto 消息：`DeactivateReq`（空）、`DeactivateResp`。

#### 成功响应

```json
{
  "code": 0,
  "message": "ok"
}
```

注销成功后，当前 Token 立即失效、已建立的 WebSocket 长连接被强制断开，客户端应清理本地会话并回到登录流程。若为游客账号且客户端仍保留原 UUID，再次登录会创建全新账号。

#### 错误响应

| 业务错误码  | message                     | 说明                            |
| ------ | --------------------------- | ----------------------------- |
| `2401` | `missing authorization token` | 未携带 Token 或 Token 无效          |
| `2406` | `account deactivate failed` | 注销失败（作废登录身份时出错），可稍后重试         |
| `2400` | `account deactivate failed` | 注销请求被拒绝（参数或身份异常）              |
| `2500` | `auth service error`        | 服务端内部错误                       |

***

## 登录流程

```
Unity 客户端                         GameLogic 服务                    Platform 服务
     │                                      │                                 │
     │  SDK 完成登录认证                     │                                 │
     │  （取得 account，无需 authToken）    │                                 │
     │                                      │                                 │
     │──── POST /auth/login ───────────────▶│                                 │
     │  { loginType:"yre",                  │                                 │
     │    platform:"ios",                   │                                 │
     │    uuid:"550e84...",  ←（可选，      │                                 │
     │    仅用于游客升级绑定）              │                                 │
     │    account:"third-user-id-xxx",      │                                 │
     │    thirdYre:{ t, siteCode,           │                                 │
     │               ck, uid } }           │                                 │
     │                                      │────── 校验 thirdYre 签名 ──────▶│
     │                                      │                                 │
     │                                      │◀─── { openId, playerId } ───────│
     │                                      │  （Platform 校验三方签名，      │
     │                                      │   并按 account 查询/创建账号）  │
     │                                      │                                 │
     │                                      │  签发 JWT Token                 │
     │                                      │                                 │
     │◀─── HTTP 200 ───────────────────────│                                 │
     │  { code:0, data:{                    │                                 │
     │    token, playerId,                  │                                 │
     │    nickname, saveKey, isNew,         │                                 │
     │    isBound:true } }  ←（绑定升级时） │                                 │
```

### 客户端完整启动决策流程

```
游戏每次启动
      │
      ▼
本地是否有持久化的 UUID？
      ├─ 无 → 生成新 UUID 并持久化
      │       System.Guid.NewGuid().ToString()
      └─ 有 → 直接使用
      │
      ▼
决定登录类型
      ├─ 游客登录
      │     └─ 直接使用本地 UUID
      │
      └─ 三方登录（如 yre）
            └─ 调用 SDK 完成登录认证，取得 account
                   同时携带本地 UUID（支持游客账号升级绑定）
                   及 SDK 返回的 thirdYre（服务端签名校验）
      │
      ▼
POST /auth/login
      │
      ├─ code:0（成功）
      │     ├─ isNew:true  → 初始化新玩家数据
      │     ├─ isBound:true → 可提示玩家绑定成功
      │     └─ 进入游戏主流程
      │
      ├─ code:2400 → 检查请求参数是否正确
      │
      ├─ code:2403 → 弹出封禁提示，终止流程
      │
      └─ code:2500 → 提示服务器异常，引导稍后重试
```

> **Token 使用说明**
>
> - Token 除登录外的所有请求都需要Token。
>
> - Token 暂定过期时间24小时，只支持单点，不支持多设备同时登录：同一账号在新设备登录时，服务端会向旧端的 WebSocket 下发 `KickedOfflinePush` 通知帧并以关闭码 `4002` 断开（详见 `docs/ws_api.md`）。
>
> - 每次重新启动游戏，重新调用 `/auth/login` 获取新 Token。
>
> - 需要认证的接口在 Header 中携带：`Authorization: Bearer <token>`
>
> - 收到 `code:2401`（Token 无效/过期）时，重新调用 `/auth/login`。
>
> - 收到 `code:10402`（会话已被新登录顶替）时，**不得自动重新登录**，应提示「账号已在其它设备登录」并回到登录界面，由用户确认后再登录；否则会与其它设备互相顶号。WebSocket 侧同一结论通过关闭码 `4002` 下发（离线错过通知时的补发，详见 `docs/ws_api.md`）。

***

## 错误处理

### 客户端建议处理策略

| 错误码                  | 处理方式                                                |
| -------------------- | --------------------------------------------------- |
| `2400`               | 常规错误返回。检查 UUID 格式是否合法、`loginType` / `platform` 是否缺失 |
| `2401`               | Token失效，需要重新登录获取token                               |
| `10402`              | 会话已被新登录顶替（账号在其它设备登录）：停止自动重连/自动重登，提示用户后回到登录界面；WebSocket 侧为 Close 码 `4002`（离线错过顶号通知时的补发） |
| `2403`               | 弹出封禁提示弹窗，禁止用户继续操作                                   |
| `2404`               | 玩家记录不存在，引导重新登录                                      |
| `2500`               | 弹出错误提示，提供"重试"按钮；可上报错误日志辅助排查                         |
| 网络错误（`code:-1` 或无响应） | 检查网络连接，提示用户重试                                       |
