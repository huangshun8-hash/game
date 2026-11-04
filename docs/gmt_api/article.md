# 文章管理模块

> 所有接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

数据存放在 MongoDB `test` 库的 `article` 集合。服务启动建索引时会自动创建该库和集合，不需要单独导入。

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | int64 | 自增主键，从 1 开始 |
| `title` | string | 标题，最多 255 个字符 |
| `content` | string | 正文，最多 65535 个字符，可为空 |
| `createdAt` | int64 | 创建时间，Unix 毫秒 |
| `status` | int | 状态，取值 0-255 |

## 创建文章

`POST /api/article/create`

### Request Body

```json
{
  "title": "hello",
  "content": "body",
  "status": 1
}
```

### Response Data

```typescript
interface ArticleResp {
  id: number
  title: string
  content: string
  createdAt: number
  status: number
}
```

## 更新文章

`POST /api/article/update`

更新标题、正文和状态，不修改 `createdAt`。

### Request Body

```json
{
  "id": 1,
  "title": "hello",
  "content": "updated",
  "status": 0
}
```

响应与创建接口相同。

## 删除文章

`POST /api/article/delete`

### Request Body

```json
{
  "id": 1
}
```

成功时 `data` 为空。

## 查询文章

`GET /api/article/get?id=1`

响应与创建接口相同。

## 文章列表

`GET /api/article/list?page=1&pageSize=20&title=hello`

按 `createdAt` 倒序。`page` 默认 1，`pageSize` 默认 20，最大 200。`title` 可选，按标题子串匹配且不区分大小写；不传则返回全部。

### Response Data

```typescript
interface ListArticleResp {
  items: ArticleResp[]
  total: number
  page: number
  pageSize: number
}
```

## 错误码

| 错误码 | 说明 |
|--------|------|
| `3800` | 请求参数错误 |
| `3804` | 文章不存在 |
| `3850` | 服务内部错误 |
