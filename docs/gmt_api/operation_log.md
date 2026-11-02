# 操作日志模块

> 以下接口均需 JWT 认证，所有已登录管理员均可访问。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 分页查询操作日志

`GET /api/operationLogs`

**Query Parameters**

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `uid` | string | 否 | 按操作管理员 ID 过滤 |
| `module` | string | 否 | 按操作模块过滤（如 `Admin`、`Auth`、`Profile`） |
| `action` | string | 否 | 按操作动作过滤（如 `Login`、`Create`、`Update`、`Delete`） |
| `startTime` | number | 否 | 时间范围起点，Unix 时间戳（毫秒） |
| `endTime` | number | 否 | 时间范围终点，Unix 时间戳（毫秒） |
| `page` | number | 否 | 页码，从 1 开始，默认 1 |
| `pageSize` | number | 否 | 每页数量，默认 20，最大 200 |

**Response Data**

```typescript
interface ListOperationLogsResp {
  items: OperationLogItem[]
  total: number       // 符合条件的总记录数
  page: number        // 当前页码
  pageSize: number    // 每页数量
  totalPages: number  // 总页数
}

interface OperationLogItem {
  id: string
  uid: string         // 操作管理员 ID
  username: string    // 操作管理员账号
  module: string      // 操作模块
  action: string      // 操作动作
  method: string      // HTTP 请求方法
  uri: string         // 请求路径
  ip: string          // 来源 IP
  request: any        // 请求参数（脱敏，不含密码）
  createdAt: number   // 操作时间，Unix 时间戳（毫秒）
}
```

**示例**

```typescript
const res = await axios.get<ApiResponse<ListOperationLogsResp>>('/api/operationLogs', {
  params: { module: 'Admin', page: 1, pageSize: 20 }
})

// 响应
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "id": "6661234abcdef1234567890a",
        "uid": "6661234abcdef1234567890b",
        "username": "admin",
        "module": "Admin",
        "action": "Create",
        "method": "POST",
        "uri": "/api/admins",
        "ip": "192.168.1.1",
        "request": { "username": "editor01", "nickname": "编辑员" },
        "createdAt": 1719504000
      }
    ],
    "total": 1,
    "page": 1,
    "pageSize": 20,
    "totalPages": 1
  }
}
```

**错误码**

| code | 说明 |
|------|------|
| `3400` | 查询参数格式错误 |
| `3403` | 无权限（非超级管理员） |
| `3500` | 服务内部错误 |
