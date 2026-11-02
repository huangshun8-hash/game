# GMT 管理后台 API 文档

> 适用前端：Vue + TypeScript
> Base URL：`/`（由部署环境配置）
> 所有请求与响应均使用 `Content-Type: application/json`

本文档按功能模块拆分，各模块独立成文：

| 模块 | 文档 | 说明 |
|------|------|------|
| 认证 | [auth.md](auth.md) | 管理员登录 |
| 个人资料 | [profile.md](profile.md) | 获取当前管理员信息 / 修改昵称 / 修改密码 |
| 管理员账号 | [admin.md](admin.md) | 列表 / 新增 / 编辑 / 禁用 / 重置密码 |
| 操作日志 | [operation_log.md](operation_log.md) | 分页查询操作日志 |
| 封禁管理 | [ban.md](ban.md) | 批量封禁 / 批量解封 / 记录查询 |
| 充值白名单 | [whitelist.md](whitelist.md) | 添加 / 删除 / 查询白名单账号 |
| 内购订单 | [iap.md](iap.md) | 渠道订单查询 / 业务订单查询 / 手动补单 |
| 配置发布 | [config.md](config.md) | 配置发布任务全流程 |
| 自定义配置 | [custom_config.md](custom_config.md) | 保存 / 查询自定义配置 |
| 邮件管理 | [mail.md](mail.md) | 发送邮件 / 查询模板列表 / 撤回模板 |
| Proto 动态数据 | [proto_api.md](proto_api.md) | Proto 模板上传 / 模板存在性查询 / 玩家分片数据读写 |

---

## 统一响应格式

所有接口均返回以下结构：

```typescript
interface ApiResponse<T = null> {
  code: number     // 0 表示成功，非 0 为业务错误码
  message: string  // 描述信息
  data?: T         // 成功时的业务数据，失败时不存在
}
```

**成功示例**

```json
{
  "code": 0,
  "message": "ok",
  "data": { ... }
}
```

**失败示例**

```json
{
  "code": 3401,
  "message": "invalid username or password"
}
```

---

## 认证方式

除登录接口外，所有接口均需在请求头携带 JWT Token：

```
Authorization: Bearer <token>
```

---

## 错误码说明

| 错误码 | HTTP 状态码 | 说明 |
|--------|------------|------|
| `0` | 200 | 成功 |
| `3400` | 400 | 请求参数错误 |
| `3401` | 401 | 未认证或令牌无效 |
| `3403` | 403 | 无权限（如普通管理员操作超级管理员账号） |
| `3404` | 404 | 管理员不存在 |
| `3409` | 409 | 用户名已存在 |
| `3410` | 400 | 原密码错误 |
| `3411` | 403 | 账号已禁用 |
| `3500` | 500 | 服务内部错误 |

> 内购模块另有独立错误码，见 [iap.md](iap.md#内购错误码)；配置模块错误码见 [config.md](config.md)。

---

## TypeScript 类型定义

```typescript
// 统一响应包装
interface ApiResponse<T = null> {
  code: number
  message: string
  data?: T
}

// 管理员账号状态
type AdminStatus = 0 | 1  // 0=禁用，1=正常

// 登录响应
interface LoginResp {
  token: string   // JWT 访问令牌
  expire: number  // 令牌有效期（秒）
}

// 当前登录管理员信息
interface ProfileResp {
  id: string
  username: string
  nickname: string
  isSuper: boolean
}

// 管理员列表项
interface AdminItem {
  id: string
  username: string
  nickname: string
  status: AdminStatus
  isSuper: boolean
  lastLoginIp: string
  lastLoginAt?: number   // Unix 毫秒时间戳，可能为空
  createdAt: number      // Unix 毫秒时间戳
}

// 新增管理员请求
interface CreateAdminReq {
  username: string
  nickname?: string
  password: string
}

// 编辑管理员请求
interface UpdateAdminReq {
  nickname: string
  status: AdminStatus
}

// 重置密码请求
interface ResetPasswordReq {
  newPassword: string
}

// 修改个人资料请求
interface UpdateProfileReq {
  nickname: string
}

// 修改密码请求
interface ChangePasswordReq {
  oldPassword: string
  newPassword: string
}
```

> 各模块专属类型（如操作日志、业务订单、配置发布、邮件等）见对应模块文档。

---

## 接口汇总

| 方法 | 路径 | 说明 | 需认证 | 仅超管 |
|------|------|------|--------|--------|
| POST | `/api/admin/login` | 管理员登录 | ✗ | ✗ |
| GET | `/api/admin/profile` | 获取当前管理员信息 | ✓ | ✗ |
| PUT | `/api/admin/profile` | 修改个人昵称 | ✓ | ✗ |
| POST | `/api/admin/changePassword` | 修改密码 | ✓ | ✗ |
| GET | `/api/admins` | 获取管理员列表 | ✓ | ✗ |
| POST | `/api/admins` | 新增管理员 | ✓ | ✓ |
| PUT | `/api/admins/:id` | 编辑管理员信息 | ✓ | ✓ |
| DELETE | `/api/admins/:id` | 禁用管理员账号 | ✓ | ✓ |
| PUT | `/api/admins/:id/password` | 重置管理员密码 | ✓ | ✓ |
| GET | `/api/operationLogs` | 分页查询操作日志 | ✓ | ✗ |
| POST | `/api/gmt/ban/batchBan` | 批量封禁玩家 | ✓ | — |
| POST | `/api/gmt/ban/batchUnban` | 批量解封玩家 | ✓ | — |
| GET | `/api/gmt/ban/records` | 分页查询封禁记录 | ✓ | — |
| POST | `/api/gmt/whitelist/add` | 添加白名单账号 | ✓ | — |
| POST | `/api/gmt/whitelist/delete` | 删除白名单账号（物理删除） | ✓ | — |
| GET | `/api/gmt/whitelist/list` | 分页查询白名单列表 | ✓ | — |
| GET | `/api/gmt/iap/orders` | 渠道支付订单查询 | ✓ | — |
| GET | `/api/gmt/iap/gameOrders` | 业务订单查询 | ✓ | — |
| GET | `/api/gmt/iap/payStats` | 玩家充值统计查询（口径同用户标签付费统计） | ✓ | — |
| POST | `/api/gmt/iap/replenish` | 手动补单 | ✓ | — |
| GET | `/api/config/manifest` | 获取线上版本清单（供 Uploader） | ✓ | — |
| POST | `/api/config/releases` | 创建发布任务 | ✓ | — |
| GET | `/api/config/releases` | 分页查询任务列表 | ✓ | — |
| GET | `/api/config/releases/:id` | 查询任务详情（含流水） | ✓ | — |
| POST | `/api/config/releases/:id/upload` | 上传配置表到暂存区（供 Uploader） | ✓ | — |
| POST | `/api/config/releases/:id/close` | 关闭发布任务 | ✓ | — |
| POST | `/api/config/releases/:id/publish` | 发布任务到生产 | ✓ | — |
| POST | `/api/customConfig/save` | 保存自定义配置 | ✓ | — |
| GET | `/api/customConfig/get` | 查询自定义配置 | ✓ | — |
| POST | `/api/proto/upload` | 上传 Proto 模板 | ✓ | — |
| GET | `/api/proto/exists` | 查询 Proto 模板是否存在 | ✓ | — |
| GET | `/api/proto/playerData/get` | 查询玩家分片数据 | ✓ | — |
| POST | `/api/proto/playerData/save` | 保存玩家分片数据 | ✓ | — |
| POST | `/api/gmt/mail/send` | 发送邮件（全服/指定玩家） | ✓ | ✗ |
| POST | `/api/gmt/mail/list` | 查询邮件模板列表（分页+过滤） | ✓ | ✗ |
| POST | `/api/gmt/mail/recall` | 撤回邮件模板（不再向未收到邮件的玩家下发） | ✓ | ✗ |
