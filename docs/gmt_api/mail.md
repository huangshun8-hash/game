# 邮件管理模块

> 所有接口均需 JWT 认证。GMT 作为 BFF，接收 Web 前端的 JWT 鉴权请求，签名转发至 GameLogic。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 发送邮件

`POST /api/gmt/mail/send`

创建系统邮件模板（采用 **Lazy Create** 模式：玩家拉取邮件列表时按需生成玩家邮件）。
支持三种目标类型：**全服** / **指定单个玩家** / **指定玩家列表**。

**Request Body**

```typescript
{
  title: string             // 邮件标题（纯文本或多语言Key），必填
  titleType: 0 | 1          // 邮件标题类型：0=纯文本（None） 1=多语言Key（Lc），必填
  content: string           // 邮件正文（纯文本或多语言Key），必填
  contentType: 0 | 1        // 邮件正文类型：0=纯文本（None） 1=多语言Key（Lc），必填
  sender?: string           // 发送者名称，选填，空表示默认发送者
  rewards?: Reward[]        // 附件奖励列表，选填，为空表示纯通知邮件
  targetType: 0 | 1 | 2     // 0=全服 1=指定单个UID 2=指定UID列表，必填
  targetUIDs?: number[]     // 目标玩家ID列表，targetType 为 1/2 时必填，最多 1000 个
  startTime?: number        // 预发布生效开始时间戳（Unix 毫秒），0 或省略表示立即生效
  endTime?: number          // 预发布结束时间戳（Unix 毫秒），0 或省略表示不限制；到达后不再下发
  expireTime?: number       // 邮件相对有效期（毫秒），从邮件下发（玩家收到）时刻开始计算，0 或省略时默认按 7 天计算，上限 2 个月
}

interface Reward {
  itemId: number   // 道具 ID，必填且大于 0
  count: number    // 数量，必填且大于 0
}
```

> `titleType` / `contentType` 说明：
> - `0`（None）：纯文本，`title` / `content` 为原始文本内容，直接展示。
> - `1`（Lc）：多语言 Key，`title` / `content` 为多语言 Key，由客户端按当前语言环境自行解析展示。

> 预发布（`startTime` / `endTime`）说明：
> - 支持预发布：`startTime` 可设置为未来时间，仅当**服务器时间处于 `[startTime, endTime]` 区间内**时，邮件模板才会在玩家拉取邮件列表时下发（Lazy Create）。
> - `startTime` 为 `0` 表示不限制生效开始时间（立即生效）；`endTime` 为 `0` 表示不限制预发布结束时间。
> - 若同时设置 `startTime` 与 `endTime`，必须满足 `startTime < endTime`。
> - 同一模板对每个玩家**终生只下发一次**（`playerId + templateId` 唯一去重），附件也终生只能领取一次。

> `expireTime`（相对有效期）说明：
> - `expireTime` 为**相对时长（毫秒）**，从邮件下发到玩家邮箱（玩家收到）那一刻开始计算；服务端会换算为绝对过期时间戳下发给客户端。
> - 例如设置 `expireTime = 604800000`（7 天），玩家在第 1 天收到，邮件将于第 8 天过期；玩家在第 3 天收到，则第 10 天过期。
> - `expireTime` 未配置（`0` 或省略）时，服务端默认按 **7 天**（`604800000` 毫秒）计算，即邮件必有到期时间。
> - `expireTime` 上限为 **2 个月**（按 60 天计，`5184000000` 毫秒），超过上限的请求会被拒绝。
> - 它与预发布窗口（`startTime`/`endTime`）无关：窗口结束后，已下发的邮件仍按自身相对有效期继续存活。

**Response Data**

```typescript
interface SendMailResp {
  templateId: string   // 邮件模板业务 ID（字符串格式，避免 JS 整数精度丢失）
  createTime: number   // 创建时间戳（Unix 毫秒）
}
```

**示例**

```typescript
// 纯文本邮件（7 天相对有效期，从玩家收到邮件时刻开始计算）
const res = await axios.post<ApiResponse<SendMailResp>>('/api/gmt/mail/send', {
  title: '版本更新补偿',
  titleType: 0,
  content: '感谢您的支持，以下是本次更新的补偿奖励！',
  contentType: 0,
  sender: '运营团队',
  rewards: [{ itemId: 1001, count: 100 }],
  targetType: 0,
  expireTime: 604800000   // 相对有效期（毫秒）= 7 天
})

// 多语言邮件（title/content 为多语言 Key）
await axios.post<ApiResponse<SendMailResp>>('/api/gmt/mail/send', {
  title: 'mail_version_update_title',
  titleType: 1,
  content: 'mail_version_update_content',
  contentType: 1,
  sender: '运营团队',
  targetType: 1,
  targetUIDs: [10001]
})

// 预发布邮件：指定预发布窗口 [startTime, endTime]，区间内才下发；收到后 7 天过期
await axios.post<ApiResponse<SendMailResp>>('/api/gmt/mail/send', {
  title: '7月活动补偿',
  titleType: 0,
  content: '活动开启后发放的补偿奖励！',
  contentType: 0,
  sender: '运营团队',
  targetType: 0,
  startTime: 1781000000000,   // 预发布生效开始时间（Unix 毫秒）
  endTime: 1783000000000,     // 预发布结束时间（Unix 毫秒）
  expireTime: 604800000       // 相对有效期（毫秒）= 7 天
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或非法（title/content 为空、titleType/contentType 非法、targetType 非法、targetUIDs 缺失或超限、reward 非法、startTime/endTime 非法或 startTime 不早于 endTime 等） |
| `3401` | 未认证 |
| `3500` | 服务内部错误（含 GameLogic 服务不可用） |

## 查询邮件模板列表

`POST /api/gmt/mail/list`

分页查询系统邮件模板，支持按模板ID / 目标类型 / 撤回状态 / 创建时间范围过滤。

**Request Body**

```typescript
{
  templateId?: string       // 按模板业务 ID 精确过滤，选填，空字符串或省略表示不过滤
  targetType?: number       // 按目标类型过滤，选填，省略表示不过滤；0=全服 1=指定单个UID 2=指定UID列表
  recalled?: boolean        // 按撤回状态过滤，选填，省略表示不过滤；false=未撤回 true=已撤回
  startTime?: number        // 创建时间范围起点（Unix 毫秒），选填，0 或省略表示不过滤
  endTime?: number          // 创建时间范围终点（Unix 毫秒），选填，0 或省略表示不过滤
  page?: number             // 页码，从 1 开始，默认 1
  pageSize?: number         // 每页数量，默认 20，最大 200
}
```

**Response Data**

```typescript
interface ListTemplatesResp {
  items: MailTemplateItem[]   // 当前页模板列表（按创建时间倒序）
  total: number               // 符合条件的总记录数
  page: number                // 当前页码
  pageSize: number            // 每页数量
  totalPages: number          // 总页数
}

interface MailTemplateItem {
  templateId: string          // 模板业务 ID（字符串格式，避免 JS 精度丢失）
  title: string               // 邮件标题文本（纯文本或多语言 Key）
  titleType: 0 | 1            // 标题类型：0=纯文本 1=多语言Key
  content: string             // 邮件正文文本（纯文本或多语言 Key）
  contentType: 0 | 1          // 正文类型：0=纯文本 1=多语言Key
  sender: string              // 发送者名称
  rewards?: Reward[]          // 附件奖励列表
  targetType: 0 | 1 | 2       // 0=全服 1=指定单个UID 2=指定UID列表
  targetUIDs?: number[]       // 目标玩家 ID 列表
  createTime: number          // 创建时间戳（Unix 毫秒）
  startTime: number           // 预发布生效开始时间戳（Unix 毫秒），0 表示立即生效
  endTime: number             // 预发布结束时间戳（Unix 毫秒），0 表示不限制
  expireTime: number          // 邮件相对有效期（毫秒），0 表示永不过期
  isRecalled: boolean         // 模板是否已撤回
  recallTime: number          // 撤回时间戳（Unix 毫秒），未撤回时为 0
}
```

**示例**

```typescript
// 查询全部未撤回模板的第一页
const res = await axios.post<ApiResponse<ListTemplatesResp>>('/api/gmt/mail/list', {
  recalled: false,
  page: 1,
  pageSize: 20
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或非法 |
| `3401` | 未认证 |
| `3500` | 服务内部错误（含 GameLogic 服务不可用） |

## 撤回邮件模板

`POST /api/gmt/mail/recall`

撤回系统邮件模板。**撤回语义**：

- 仅标记模板为已撤回，**已下发到玩家邮箱的邮件不受影响**（仍可正常查看/领取）。
- 撤回后，**尚未收到该邮件的玩家将不再收到**（Lazy Create 下发时跳过已撤回模板）。
- 对已撤回的模板重复撤回视为成功（幂等）。

**Request Body**

```typescript
{
  templateId: string   // 要撤回的邮件模板业务 ID，必填且非空字符串
}
```

**Response Data**

```typescript
interface RecallMailResp {
  templateId: string   // 模板业务 ID（字符串格式，避免 JS 精度丢失）
  isRecalled: boolean  // 模板当前撤回状态（撤回成功或已处于撤回状态均为 true）
  recallTime: number   // 撤回时间戳（Unix 毫秒）
}
```

**示例**

```typescript
const res = await axios.post<ApiResponse<RecallMailResp>>('/api/gmt/mail/recall', {
  templateId: "123456"
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或非法（templateId 为空） |
| `3401` | 未认证 |
| `3500` | 服务内部错误（含 GameLogic 服务不可用、模板不存在） |

**接口汇总**

| 方法 | 路径 | 说明 | 需认证 | 仅超管 |
|------|------|------|--------|--------|
| POST | `/api/gmt/mail/send` | 发送邮件（全服/指定玩家） | ✓ | ✗ |
| POST | `/api/gmt/mail/list` | 查询邮件模板列表（分页+过滤） | ✓ | ✗ |
| POST | `/api/gmt/mail/recall` | 撤回邮件模板（不再向未收到邮件的玩家下发） | ✓ | ✗ |
