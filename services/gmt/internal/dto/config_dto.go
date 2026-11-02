package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/gmt/internal/model"
)

// ===================== Uploader 工具专用 =====================

// GetManifestReq 获取配置清单请求DTO（无业务参数，仅用于提取 requestId）
type GetManifestReq struct {
	httpx.BaseReq
}

// ManifestTableInfo 清单中单张配置表的版本信息（供 Uploader 增量对比）
// 暂存区字段在每次上传后立即更新，发布前即可用于增量对比，无需等到发布完成。
type ManifestTableInfo struct {
	// StagingVersion 最近一次上传时关联的发布任务 ID（十六进制 ObjectID 字符串）
	StagingVersion string `json:"stagingVersion"`
	// StagingSchemaHash 暂存区 Schema Hash（Uploader 增量对比基准）
	StagingSchemaHash string `json:"stagingSchemaHash"`
	// StagingDataHash 暂存区 Data Hash（Uploader 增量对比基准）
	StagingDataHash string `json:"stagingDataHash"`
	// PublishedVersion 已发布版本号（空字符串表示从未发布）
	PublishedVersion string `json:"publishedVersion"`
}

// GetManifestResp 获取配置清单响应DTO
type GetManifestResp struct {
	// Tables 所有配置表的版本信息，key 为表名
	Tables map[string]ManifestTableInfo `json:"tables"`
}

// ===================== 发布任务管理 =====================

// CreateReleaseReq 创建发布任务请求DTO
type CreateReleaseReq struct {
	httpx.BaseReq
	// Description 任务说明，用于在列表中识别本次发布目的，必填
	Description string `json:"description"`
}

// CreateReleaseResp 创建发布任务响应DTO
type CreateReleaseResp struct {
	// ID 发布任务ID（MongoDB ObjectID，十六进制字符串）
	ID string `json:"id"`
	// Description 任务说明
	Description string `json:"description"`
	// Status 任务初始状态（draft）
	Status string `json:"status"`
	// CreatedAt 创建时间（毫秒时间戳，Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
}

// UploadToReleaseReq 向指定发布任务上传单张配置表请求DTO
type UploadToReleaseReq struct {
	httpx.BaseReq
	// ReleaseID 发布任务ID（路由参数，由 Bind 填充）
	ReleaseID string `uri:"id"`
	// Table 配置表名
	Table string `json:"table"`
	// SchemaHash 本次上传的 Schema Hash（SHA256）
	SchemaHash string `json:"schemaHash"`
	// DataHash 本次上传的 Data Hash（SHA256）
	DataHash string `json:"dataHash"`
	// Schema 字段结构定义列表
	Schema []model.FieldDef `json:"schema"`
	// Data 配置数据列表（每行包含 id 字段）
	Data []map[string]any `json:"data"`
}

// UploadToReleaseResp 上传响应DTO
type UploadToReleaseResp struct {
	// ReleaseID 发布任务ID
	ReleaseID string `json:"releaseId"`
	// Table 配置表名
	Table string `json:"table"`
	// IsSchemaChanged 是否触发了 Schema 变更
	IsSchemaChanged bool `json:"isSchemaChanged"`
	// ReleaseStatus 上传后任务的最新状态
	ReleaseStatus string `json:"releaseStatus"`
}

// CloseReleaseReq 关闭发布任务请求DTO
type CloseReleaseReq struct {
	httpx.BaseReq
	// ReleaseID 发布任务ID（路由参数，由 Bind 填充）
	ReleaseID string `uri:"id"`
}

// CloseReleaseResp 关闭发布任务响应DTO
type CloseReleaseResp struct {
	// ReleaseID 发布任务ID
	ReleaseID string `json:"releaseId"`
	// Status 关闭后的任务状态（closed）
	Status string `json:"status"`
}

// PublishReleaseReq 发布任务请求DTO
type PublishReleaseReq struct {
	httpx.BaseReq
	// ReleaseID 发布任务ID（路由参数，由 Bind 填充）
	ReleaseID string `uri:"id"`
}

// PublishTableResult 单张配置表的发布结果
type PublishTableResult struct {
	// Table 配置表名
	Table string `json:"table"`
	// IsSchemaChanged 是否包含 Schema 变更（需重新编译部署）
	IsSchemaChanged bool `json:"isSchemaChanged"`
}

// PublishReleaseResp 发布任务响应DTO
type PublishReleaseResp struct {
	// ReleaseID 发布任务ID
	ReleaseID string `json:"releaseId"`
	// PublishedCount 实际发布的表数量
	PublishedCount int `json:"publishedCount"`
	// Results 每张表的发布结果
	Results []PublishTableResult `json:"results"`
	// HasSchemaChange 本次发布是否包含任何 Schema 变更
	HasSchemaChange bool `json:"hasSchemaChange"`
}

// GetReleaseReq 查询单个发布任务请求DTO
type GetReleaseReq struct {
	httpx.BaseReq
	// ReleaseID 发布任务ID（路由参数）
	ReleaseID string `uri:"id"`
}

// ReleaseEventItem 任务事件流水项
type ReleaseEventItem struct {
	// EventType 事件类型
	EventType string `json:"eventType"`
	// Operator 操作者
	Operator string `json:"operator"`
	// Message 附加说明
	Message string `json:"message"`
	// OccurredAt 事件发生时间（毫秒时间戳，Unix 毫秒）
	OccurredAt int64 `json:"occurredAt"`
}

// ReleaseTableDetailItem 任务中单张表的状态项
type ReleaseTableDetailItem struct {
	// Table 配置表名
	Table string `json:"table"`
	// IsUploaded 是否已上传
	IsUploaded bool `json:"isUploaded"`
	// IsSchemaChanged 是否触发 Schema 变更
	IsSchemaChanged bool `json:"isSchemaChanged"`
	// SchemaHash Schema Hash
	SchemaHash string `json:"schemaHash"`
	// DataHash Data Hash
	DataHash string `json:"dataHash"`
	// UploadedAt 上传完成时间（毫秒时间戳，Unix 毫秒，未上传时为0）
	UploadedAt int64 `json:"uploadedAt,omitempty"`
	// UploadedBy 上传者
	UploadedBy string `json:"uploadedBy"`
}

// ReleaseDetailResp 发布任务详情响应DTO
type ReleaseDetailResp struct {
	// ID 任务ID（MongoDB ObjectID，十六进制字符串）
	ID string `json:"id"`
	// Description 任务说明
	Description string `json:"description"`
	// Status 任务状态：draft / uploading / published / closed
	Status string `json:"status"`
	// Tables 已上传的配置表名列表
	Tables []string `json:"tables"`
	// TableDetails 每张表的上传状态详情
	TableDetails []ReleaseTableDetailItem `json:"tableDetails"`
	// Events 操作流水
	Events []ReleaseEventItem `json:"events"`
	// CreatedBy 创建者
	CreatedBy string `json:"createdBy"`
	// PublishedBy 发布者（未发布时为空）
	PublishedBy string `json:"publishedBy"`
	// HasSchemaChange 是否包含 Schema 变更
	HasSchemaChange bool `json:"hasSchemaChange"`
	// CreatedAt 创建时间（毫秒时间戳，Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
	// UpdatedAt 最后更新时间（毫秒时间戳，Unix 毫秒）
	UpdatedAt int64 `json:"updatedAt"`
	// PublishedAt 发布时间（毫秒时间戳，Unix 毫秒，未发布时为0）
	PublishedAt int64 `json:"publishedAt,omitempty"`
}

// ListReleasesReq 分页查询发布任务列表请求DTO
type ListReleasesReq struct {
	httpx.BaseReq
	// Page 页码，从1开始，默认1
	Page int64 `query:"page"`
	// PageSize 每页数量，默认20，最大100
	PageSize int64 `query:"pageSize"`
}

// ReleaseListItem 发布任务列表项（不含详细流水和数据，适合列表展示）
type ReleaseListItem struct {
	// ID 任务ID（MongoDB ObjectID，十六进制字符串）
	ID string `json:"id"`
	// Description 任务说明
	Description string `json:"description"`
	// Status 任务状态
	Status string `json:"status"`
	// Tables 涵盖的配置表名列表
	Tables []string `json:"tables"`
	// UploadedCount 已上传的表数量
	UploadedCount int `json:"uploadedCount"`
	// TotalCount 声明的总表数量
	TotalCount int `json:"totalCount"`
	// CreatedBy 创建者
	CreatedBy string `json:"createdBy"`
	// PublishedBy 发布者
	PublishedBy string `json:"publishedBy"`
	// HasSchemaChange 是否包含 Schema 变更
	HasSchemaChange bool `json:"hasSchemaChange"`
	// CreatedAt 创建时间（毫秒时间戳，Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
	// PublishedAt 发布时间（毫秒时间戳，Unix 毫秒，未发布时为0）
	PublishedAt int64 `json:"publishedAt,omitempty"`
}

// ListReleasesResp 分页查询发布任务列表响应DTO
type ListReleasesResp struct {
	// Items 当前页任务列表
	Items []ReleaseListItem `json:"items"`
	// Total 总记录数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页数量
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}
