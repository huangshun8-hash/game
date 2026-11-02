# 个人资料模块

> 以下接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 获取当前登录管理员信息

`GET /api/admin/profile`

**Request**：无 Body

**Response Data**

```typescript
ProfileResp
```

**示例**

```typescript
const res = await axios.get<ApiResponse<ProfileResp>>('/api/admin/profile')

// 响应
{
  "code": 0,
  "message": "ok",
  "data": {
    "id": "6661234abcdef1234567890a",
    "username": "admin",
    "nickname": "超级管理员",
    "isSuper": true
  }
}
```

**错误码**

| code | 说明 |
|------|------|
| `3401` | 未认证 |
| `3404` | 管理员不存在 |

---

## 修改个人昵称

`PUT /api/admin/profile`

**Request Body**

```typescript
{
  nickname: string  // 新昵称，必填
}
```

**Response Data**：`null`

**示例**

```typescript
await axios.put<ApiResponse>('/api/admin/profile', {
  nickname: '新昵称'
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | nickname 为空 |
| `3404` | 管理员不存在 |

---

## 修改密码

`POST /api/admin/changePassword`

**Request Body**

```typescript
{
  oldPassword: string  // 原密码（明文），必填
  newPassword: string  // 新密码（明文，至少 6 位），必填
}
```

**Response Data**：`null`

**示例**

```typescript
await axios.post<ApiResponse>('/api/admin/changePassword', {
  oldPassword: 'old123',
  newPassword: 'new456'
})
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或新密码不足 6 位 |
| `3404` | 管理员不存在 |
| `3410` | 原密码错误 |
