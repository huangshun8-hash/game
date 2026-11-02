# 自定义配置模块

> 所有接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 保存自定义配置

`POST /api/customConfig/save`

用于保存一份全局自定义配置，`data` 支持任意 JSON 结构。

### Request Body

```json
{
  "data": {
    "switchA": true,
    "welcomeText": "hello",
    "list": [1, 2, 3]
  }
}
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `data` | object / array / string / number / bool | 是 | 原始 JSON 数据，原样保存 |

### Response Data

```typescript
interface SaveCustomConfigResp {
  updatedAt: number
}
```

| 字段 | 说明 |
|------|------|
| `updatedAt` | 更新时间，Unix 毫秒时间戳 |

### 错误码

| code | 说明 |
|------|------|
| `3600` | 请求参数错误 |
| `3650` | 服务内部错误 |

---

## 查询自定义配置

`GET /api/customConfig/get`

### Response Data

```typescript
interface GetCustomConfigResp {
  data: any
  updatedAt: number
}
```

### 说明

- `data` 为保存时写入的原始 JSON 数据
- 若当前没有可用配置，接口会返回空对象

### 示例

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "data": {
      "switchA": true,
      "welcomeText": "hello"
    },
    "updatedAt": 1689321600000
  }
}
```

---

## 接口汇总

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/customConfig/save` | 保存自定义配置 |
| GET | `/api/customConfig/get` | 查询自定义配置 |
