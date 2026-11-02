# 认证模块

> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 管理员登录

`POST /api/admin/login`

> 无需认证（公开接口）

**Request Body**

```typescript
{
  username: string  // 登录账号，必填
  password: string  // 登录密码（明文，传输层依赖 HTTPS 加密），必填
}
```

**Response Data**

```typescript
LoginResp
```

**示例**

```typescript
// 请求
const res = await axios.post<ApiResponse<LoginResp>>('/api/admin/login', {
  username: 'admin',
  password: 'password123'
})

// 响应
{
  "code": 0,
  "message": "ok",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expire": 86400
  }
}
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 参数缺失或格式错误 |
| `3401` | 用户名或密码错误 |
| `3411` | 账号已禁用 |
