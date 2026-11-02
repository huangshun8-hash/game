# 三方充值回调 & LunPlay 角色查询接口文档

> 本文档描述 Platform 服务对外的三方对接接口。这些接口均采用 MD5 验签、不挂 JWT / 内部签名中间件，HTTP 状态码恒为 200，业务结果通过响应体 `code` 字段区分。

## 通用说明

- 签名密钥：以下所有接口共用同一个「三方平台密钥」（配置项 `thirdPartyKey`）。
- 充值回调时间戳 `time` 为毫秒级；LunPlay 角色查询时间戳 `t` 为秒级。
- 响应体文案：充值回调按三方契约返回中文 `content`；LunPlay 接口仅返回 `code` 及数据字段。

---

## 1. 三方充值回调

游戏方发起充值请求，平台校验签名后处理充值。

- 方法 / 路径：`GET /pay/notify` 或 `POST /pay/notify`
- 请求格式：`GET` 使用 query 参数；`POST` 使用 `application/x-www-form-urlencoded`
- 验签规则：`sign = md5(passport + order + key + server + time + agent + money + goodid)`
  - `key` 为三方平台共用签名密钥
  - 使用常量时间比较，防时序侧信道
- 防重放：`time` 为毫秒时间戳，须落在当前时间 ±5 分钟窗口内

### 请求参数

| 参数 | 必填 | 说明 |
|------|------|------|
| `agent` | 是 | 渠道/代理标识 |
| `server` | 是 | 区服标识 |
| `passport` | 是 | 账号 |
| `money` | 是 | 充值金额，单位为元（可带小数，如 `1.99`）；平台按四舍五入转为分后与订单金额（分）严格比对 |
| `order` | 是 | 订单号（用于去重） |
| `time` | 是 | 毫秒级时间戳 |
| `sign` | 是 | MD5 签名 |
| `param` | 否 | 附加透传参数 |
| `goodid` | 是 | 物品 ID，字符串 SKU（如 `gtkuj.roma.rstcte.1.99`）；参与签名计算，并与下单时按 `payId` + 渠道从 `IAPProductGlobal` 配置固化的渠道内购 ID（apple 取 `corre1`、google 取 `corre2`）严格比对，不一致或缺失一律按「无效的充值信息」拒绝，无跳过/降级分支 |

### 响应

```json
{ "code": 0, "content": "充值成功" }
```

### 响应码表

| code | content | 含义 |
|------|---------|------|
| 0 | 充值成功 | 成功 |
| -1 | 无效的帐号名 | 账号不存在 |
| -2 | 无效的角色名 | 角色无效 |
| -3 | 无效的充值目标标识 | 充值目标无效 |
| -4 | 重复的订单号 | 订单重复 |
| -5 | 无效的充值信息 | 参数/金额/时间戳非法或订单不存在 |
| -6 | 无效的附加充值信息 | 附加信息无效 |
| -7 | 与游戏服通讯异常 | 调用游戏服失败 |
| -8 | 游戏服处理异常 | 游戏服处理失败 |
| -255 | 未知错误 | 签名失败/密钥未配置/其他未知错误 |

---

## 2. LunPlay 角色查询

平台侧调用，用于查询角色与区服信息。全部为 `GET`，签名字段名为 `ck`。

### 通用响应码

| code | 含义 |
|------|------|
| 0 | 成功 |
| 1 | 验证错误（参数缺失或验签失败） |
| 2 | 查询角色信息失败 |
| 3 | 服务器错误 |

角色对象字段（`playerName` 为 URL 编码，日期格式 `yyyyMMdd HH:mm:ss`，时间为 UTC）：

| 字段 | 说明 |
|------|------|
| `playerLevel` | 角色等级 |
| `playerName` | 角色名（URL 编码） |
| `createDate` | 创建时间 |
| `loginDate` | 最后登录时间 |
| `roleid` | 角色 ID（字符串） |

### 2.1 单服务器单角色查询

- 路径：`GET /lunplay/role/query`
- 参数：`serverCode`(int)、`passport`、`t`(秒)、`ck`
- 验签：`ck = md5(passport + t + key + serverCode)`
- 成功响应（扁平结构，`code` 与字段同级）：

```json
{
  "code": 0,
  "playerLevel": 30,
  "playerName": "...",
  "loginDate": "...",
  "createDate": "...",
  "roleid": "..."
}
```

> 未查询到角色时返回 `{"code":2}`。

### 2.2 单服务器多角色查询

- 路径：`GET /lunplay/role/list`
- 参数：`serverCode`(int)、`passport`、`t`(秒)、`ck`
- 验签：`ck = md5(passport + t + key + serverCode)`
- 成功响应：

```json
{
  "code": 0,
  "data": [ { "playerLevel": 30, "playerName": "...", "createDate": "...", "loginDate": "...", "roleid": "..." } ]
}
```

### 2.3 按角色 ID 查询

- 路径：`GET /lunplay/role/byRoleId`
- 参数：`roleid`、`t`(秒)、`ck`
- 验签：`ck = md5(roleid + t + key)`
- 成功响应（含 `passport` / `serverCode`）：

```json
{
  "code": 0,
  "data": [ { "playerLevel": 30, "playerName": "...", "createDate": "...", "loginDate": "...", "roleid": "...", "passport": "1111", "serverCode": "1" } ]
}
```

### 2.4 按角色 ID 查询区服

- 路径：`GET /lunplay/role/serverList`
- 参数：`roleid`、`t`(秒)、`ck`
- 验签：`ck = md5(roleid + t + key)`
- 成功响应（返回首个区服，`serverName` 为 URL 编码）：

```json
{
  "code": 0,
  "serverCode": 30,
  "serverName": "..."
}
```

> 角色不存在或无区服时返回 `{"code":2}`。
