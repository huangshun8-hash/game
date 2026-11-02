# 游戏配置管理模块

> 所有接口均需 JWT 认证。
> 通用约定（统一响应格式 / 认证方式 / 错误码 / TypeScript 类型定义）见 [README.md](README.md)。

## 发布流程

```
① POST /api/config/releases
        ↓
② POST /api/config/releases/:id/upload
        ↓（首次上传后状态变为 uploading）
③ POST /api/config/releases/:id/publish
```

如需放弃任务，可调用：

```
POST /api/config/releases/:id/close
```

- 任务 ID（`releaseId`）为十六进制字符串。

- 无需预声明配置表，上传哪些表，发布时就发布哪些表。

- 同一时刻只允许一个未完成任务存在。

- 上传完成后即可直接发布，当前流程**没有验收接口**。

- 所有时间字段均为 **Unix 毫秒时间戳**。

ConfigUploader 工具在上传前，可先调用 `GET /api/config/manifest` 获取当前版本清单做增量对比。

***

## 任务状态说明

| 状态          | 说明                     |
| ----------- | ---------------------- |
| `draft`     | 已创建，尚未上传任何配置表          |
| `uploading` | 已上传至少一张配置表，可继续上传、发布或关闭 |
| `published` | 已发布，终态                 |
| `closed`    | 已关闭，终态                 |

***

## 获取配置清单

`GET /api/config/manifest`

**Response Data**

```typescript
interface GetManifestResp {
  tables: Record<string, ManifestTableInfo>
}

interface ManifestTableInfo {
  stagingVersion: string
  stagingSchemaHash: string
  stagingDataHash: string
  publishedVersion: string
}
```

| 字段                  | 说明                    |
| ------------------- | --------------------- |
| `stagingVersion`    | 当前暂存版本号；无暂存数据时通常为空字符串 |
| `stagingSchemaHash` | 当前暂存 Schema Hash      |
| `stagingDataHash`   | 当前暂存 Data Hash        |
| `publishedVersion`  | 已发布版本号；从未发布时为空字符串     |

***

## 创建发布任务

`POST /api/config/releases`

**Request Body**

```typescript
{
  description: string
}
```

**Response Data**

```typescript
interface CreateReleaseResp {
  id: string
  description: string
  status: 'draft'
  createdAt: number
}
```

**错误码**

| code   | 说明          |
| ------ | ----------- |
| `3600` | 请求参数错误      |
| `3606` | 已存在进行中的发布任务 |

***

## 上传配置表到任务

`POST /api/config/releases/:id/upload`

每次上传一张配置表。可重复调用，发布时会以任务内最后一次上传的内容为准。

客户端可设置 `Content-Encoding: gzip/deflate/br/zstd` 压缩请求体。

**Path Parameters**

| 参数   | 说明      |
| ---- | ------- |
| `id` | 发布任务 ID |

**Request Body**

```typescript
{
  table: string
  schemaHash: string
  dataHash: string
  schema: FieldDef[]
  data: Record<string, any>[]
}

interface FieldDef {
  name: string
  type: string
  comment: string
}
```

**Response Data**

```typescript
interface UploadToReleaseResp {
  releaseId: string
  table: string
  isSchemaChanged: boolean
  releaseStatus: 'uploading'
}
```

**错误码**

| code   | 说明           |
| ------ | ------------ |
| `3600` | 请求参数错误       |
| `3601` | Schema 校验失败  |
| `3602` | 数据校验失败       |
| `3604` | 任务不存在        |
| `3605` | 任务已终止，不能继续上传 |
| `3650` | 服务内部错误       |

***

## 关闭发布任务

`POST /api/config/releases/:id/close`

关闭指定任务。关闭后任务进入终态，不能再上传或发布。

**Path Parameters**

| 参数   | 说明      |
| ---- | ------- |
| `id` | 发布任务 ID |

**Request Body**

```json
{}
```

**Response Data**

```typescript
interface CloseReleaseResp {
  releaseId: string
  status: 'closed'
}
```

**错误码**

| code   | 说明           |
| ------ | ------------ |
| `3604` | 任务不存在        |
| `3605` | 任务已终止，不能重复关闭 |
| `3650` | 服务内部错误       |

***

## 发布任务

`POST /api/config/releases/:id/publish`

将当前任务中已上传的配置表一次性发布。

**Path Parameters**

| 参数   | 说明      |
| ---- | ------- |
| `id` | 发布任务 ID |

**Request Body**

```json
{}
```

**Response Data**

```typescript
interface PublishReleaseResp {
  releaseId: string
  publishedCount: number
  hasSchemaChange: boolean
  results: PublishTableResult[]
}

interface PublishTableResult {
  table: string
  isSchemaChanged: boolean
}
```

**错误码**

| code   | 说明           |
| ------ | ------------ |
| `3603` | 任务内没有已上传的配置表 |
| `3604` | 任务不存在        |
| `3605` | 任务状态不允许发布    |
| `3650` | 服务内部错误       |

***

## 查询发布任务列表

`GET /api/config/releases?page=1&pageSize=20`

**Query Parameters**

| 参数         | 类型     | 说明                |
| ---------- | ------ | ----------------- |
| `page`     | number | 页码，默认 1           |
| `pageSize` | number | 每页数量，默认 20，最大 100 |

**Response Data**

```typescript
interface ListReleasesResp {
  items: ReleaseListItem[]
  total: number
  page: number
  pageSize: number
  totalPages: number
}

interface ReleaseListItem {
  id: string
  description: string
  status: 'draft' | 'uploading' | 'published' | 'closed'
  tables: string[]
  uploadedCount: number
  totalCount: number
  createdBy: string
  publishedBy: string
  hasSchemaChange: boolean
  createdAt: number
  publishedAt?: number
}
```

***

## 查询发布任务详情

`GET /api/config/releases/:id`

**Response Data**

```typescript
interface ReleaseDetailResp {
  id: string
  description: string
  status: 'draft' | 'uploading' | 'published' | 'closed'
  tables: string[]
  tableDetails: ReleaseTableDetailItem[]
  events: ReleaseEventItem[]
  createdBy: string
  publishedBy: string
  hasSchemaChange: boolean
  createdAt: number
  updatedAt: number
  publishedAt?: number
}

interface ReleaseTableDetailItem {
  table: string
  isUploaded: boolean
  isSchemaChanged: boolean
  schemaHash: string
  dataHash: string
  uploadedAt?: number
  uploadedBy: string
}

interface ReleaseEventItem {
  eventType: string
  operator: string
  message: string
  occurredAt: number
}
```

常见 `eventType`：

| 值           | 说明    |
| ----------- | ----- |
| `create`    | 创建任务  |
| `upload`    | 上传配置表 |
| `published` | 发布成功  |
| `closed`    | 关闭任务  |

**错误码**

| code   | 说明         |
| ------ | ---------- |
| `3600` | 发布任务 ID 非法 |
| `3604` | 任务不存在      |
| `3650` | 服务内部错误     |

***

## 接口汇总

| 方法   | 路径                                 | 调用方            | 说明       |
| ---- | ---------------------------------- | -------------- | -------- |
| GET  | `/api/config/manifest`             | ConfigUploader | 获取配置清单   |
| POST | `/api/config/releases`             | GMT 前端         | 创建发布任务   |
| GET  | `/api/config/releases`             | GMT 前端         | 分页查询任务列表 |
| GET  | `/api/config/releases/:id`         | GMT 前端         | 查询任务详情   |
| POST | `/api/config/releases/:id/upload`  | ConfigUploader | 上传配置表到任务 |
| POST | `/api/config/releases/:id/close`   | GMT 前端         | 关闭发布任务   |
| POST | `/api/config/releases/:id/publish` | GMT 前端         | 发布任务     |

