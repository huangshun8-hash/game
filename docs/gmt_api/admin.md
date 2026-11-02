# 管理员账号管理模块

> 以下接口均需 JWT 认证。
> **创建、编辑、禁用、重置密码** 操作仅限超级管理员；**查询列表** 所有管理员均可访问。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 获取管理员列表

`GET /api/admins`

**Request**：无参数

**Response Data**

```typescript
AdminItem[]
```

**示例**

```typescript
const res = await axios.get<ApiResponse<AdminItem[]>>('/api/admins')

// 响应
{
  "code": 0,
  "message": "ok",
  "data": [
    {
      "id": "6661234abcdef1234567890a",
      "username": "admin",
      "nickname": "超级管理员",
      "status": 1,
      "isSuper": true,
      "lastLoginIp": "192.168.1.1",
      "lastLoginAt": 1719504000,
      "createdAt": 1700000000
    }
  ]
}
```

**错误码**

| code | 说明 |
|------|------|
| `3500` | 服务内部错误 |

---

## 新增管理员

`POST /api/admins`

> 仅超级管理员可操作

**Request Body**

```typescript
{
  username: string   // 登录账号，必填，不可重复
  nickname?: string  // 用户昵称，选填
  password: string   // 初始密码（明文，至少 6 位），必填
}
```

**Response Data**

```typescript
AdminItem  // 新创建的管理员信息
```

**示例**

```typescript
const res = await axios.post<ApiResponse<AdminItem>>('/api/admins', {
  username: 'editor01',
  nickname: '编辑员',
  password: 'pass123',
  isSuper: false
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或密码不足 6 位 |
| `3403` | 无权限（非超级管理员） |
| `3409` | 用户名已存在 |

---

## 编辑管理员信息

`PUT /api/admins/:id`

> 仅超级管理员可操作

**Path Parameters**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string | 管理员 ID |

**Request Body**

```typescript
{
  nickname: string      // 新昵称，必填
  status: AdminStatus   // 账号状态：1=正常，0=禁用，必填
}
```

**Response Data**：`null`

**示例**

```typescript
await axios.put<ApiResponse>('/api/admins/6661234abcdef1234567890a', {
  nickname: '新昵称',
  status: 1
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | nickname 为空或 status 值非法（不为 0 或 1） |
| `3403` | 无权限（非超级管理员） |
| `3404` | 管理员不存在 |

---

## 禁用管理员账号

`DELETE /api/admins/:id`

> 仅超级管理员可操作。系统不支持物理删除，DELETE 语义为禁用账号。

**Path Parameters**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string | 管理员 ID |

**Request**：无 Body

**Response Data**：`null`

**示例**

```typescript
await axios.delete<ApiResponse>('/api/admins/6661234abcdef1234567890a')
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | id 为空 |
| `3403` | 无权限（非超级管理员） |
| `3404` | 管理员不存在 |

---

## 重置管理员密码

`PUT /api/admins/:id/password`

> 仅超级管理员可操作，用于重置其他管理员的密码

**Path Parameters**

| 参数 | 类型 | 说明 |
|------|------|------|
| `id` | string | 目标管理员 ID |

**Request Body**

```typescript
{
  newPassword: string  // 新密码（明文，至少 6 位），必填
}
```

**Response Data**：`null`

**示例**

```typescript
await axios.put<ApiResponse>('/api/admins/6661234abcdef1234567890a/password', {
  newPassword: 'newPass456'
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或新密码不足 6 位 |
| `3403` | 无权限（非超级管理员） |
| `3404` | 管理员不存在 |
