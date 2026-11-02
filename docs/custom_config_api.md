# 自定义配置接口文档

> 客户端：Unity (C#)
> 协议：HTTP/1.1，JSON 编码
> Base URL：由部署环境决定

---

## 接口说明

### `GET /api/customConfig`

获取客户端可见的自定义配置。该接口**无需登录**。

### 请求头

| Header | 必填 | 说明 |
|--------|------|------|
| `Content-Type` | 否 | `application/json` |

### 成功响应

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "switchA": true,
    "welcomeText": "hello",
    "list": [1, 2, 3]
  }
}
```

### 说明

- `data` 为原始 JSON 对象，字段结构由运营配置决定
- 当没有可用配置或读取失败时，接口返回空对象：

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

### 错误响应

该接口采用降级策略，通常不会向客户端返回业务错误；读取失败时直接返回空对象。
