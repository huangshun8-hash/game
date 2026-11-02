package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 发布任务状态常量
const (
	// ReleaseStatusDraft 已创建：任务刚创建，尚未上传任何配置表
	ReleaseStatusDraft = "draft"
	// ReleaseStatusUploading 配置上传中：至少有一张表已上传
	ReleaseStatusUploading = "uploading"
	// ReleaseStatusPublished 已发布：所有已上传的表均成功写入生产集合，终态
	ReleaseStatusPublished = "published"
	// ReleaseStatusClosed 已关闭：任务被手动关闭，暂存数据已清除，终态
	ReleaseStatusClosed = "closed"
)

// 发布任务事件类型常量
const (
	// ReleaseEventCreate 任务创建
	ReleaseEventCreate = "create"
	// ReleaseEventUpload 配置表上传成功
	ReleaseEventUpload = "upload"
	// ReleaseEventPublished 发布成功完成
	ReleaseEventPublished = "published"
	// ReleaseEventClosed 任务被手动关闭
	ReleaseEventClosed = "closed"
)

// ReleaseTableDetail 发布任务中单张配置表的上传状态
type ReleaseTableDetail struct {
	// Table 配置表名
	Table string `bson:"table" json:"table"`
	// IsUploaded 是否已上传
	IsUploaded bool `bson:"isUploaded" json:"isUploaded"`
	// SchemaHash 本次上传的 Schema Hash
	SchemaHash string `bson:"schemaHash" json:"schemaHash"`
	// DataHash 本次上传的 Data Hash
	DataHash string `bson:"dataHash" json:"dataHash"`
	// Schema 本次上传的字段结构定义快照（发布时写入正式 config_schema，供代码生成使用）
	Schema []FieldDef `bson:"schema" json:"schema"`
	// IsSchemaChanged 是否触发了 Schema 变更
	IsSchemaChanged bool `bson:"isSchemaChanged" json:"isSchemaChanged"`
	// UploadedAt 上传完成时间（未上传时为零值）
	UploadedAt *time.Time `bson:"uploadedAt" json:"uploadedAt,omitempty"`
	// UploadedBy 上传操作者
	UploadedBy string `bson:"uploadedBy" json:"uploadedBy"`
}

// ReleaseEvent 发布任务操作流水记录，仅追加不修改
type ReleaseEvent struct {
	// EventType 事件类型：create / upload / review / completeReview / skipReview / published
	EventType string `bson:"eventType" json:"eventType"`
	// Operator 操作者账号名
	Operator string `bson:"operator" json:"operator"`
	// Message 附加说明（如上传的表名、发布结果描述、错误信息）
	Message string `bson:"message" json:"message"`
	// OccurredAt 事件发生时间
	OccurredAt time.Time `bson:"occurredAt" json:"occurredAt"`
}

// ConfigRelease 配置发布任务文档
// 作为聚合根，将一次完整的配置发布流程（创建→上传→验收→发布）封装为一个可追踪的任务单元。
// 存储于 config_releases 集合，_id 使用 MongoDB 自动生成的 ObjectID。
type ConfigRelease struct {
	// ID MongoDB 自动生成的唯一任务ID（ObjectID）
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// Description 任务说明，由创建者填写，用于在列表中快速识别本次发布的目的
	Description string `bson:"description" json:"description"`
	// Tables 本次任务已上传的配置表名列表（上传时动态追加，去重）
	Tables []string `bson:"tables" json:"tables"`
	// TableDetails 每张表的上传状态详情，key 为表名
	TableDetails map[string]ReleaseTableDetail `bson:"tableDetails" json:"tableDetails"`
	// Status 任务当前状态：draft / uploading / reviewing / reviewed / published
	Status string `bson:"status" json:"status"`
	// ActiveFlag 活动标记：true 表示任务处于进行中（非终态）。
	// 通过 config_releases 集合上的部分唯一索引保证同一时刻最多只有一个进行中的发布任务。
	ActiveFlag bool `bson:"activeFlag" json:"activeFlag"`
	// Events 操作流水（按时间顺序追加，不可修改）
	Events []ReleaseEvent `bson:"events" json:"events"`

	// --- 创建信息 ---

	// CreatedBy 任务创建者账号名
	CreatedBy string `bson:"createdBy" json:"createdBy"`
	// CreatedAt 任务创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 文档最后更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`

	// --- 发布信息 ---

	// PublishedBy 发布操作者账号名（未发布时为空）
	PublishedBy string `bson:"publishedBy" json:"publishedBy"`
	// PublishedAt 发布完成时间（未发布时为 nil）
	PublishedAt *time.Time `bson:"publishedAt" json:"publishedAt,omitempty"`
}

// HasUploaded 检查任务是否至少有一张表已上传（用于发布前校验）
func (r *ConfigRelease) HasUploaded() bool {
	for _, d := range r.TableDetails {
		if d.IsUploaded {
			return true
		}
	}
	return false
}

// HasSchemaChange 检查本次发布任务是否包含任何 Schema 变更
func (r *ConfigRelease) HasSchemaChange() bool {
	for _, d := range r.TableDetails {
		if d.IsSchemaChanged {
			return true
		}
	}
	return false
}

// IsTerminated 检查任务是否已处于终态（published 或 closed），终态任务不可再操作
func (r *ConfigRelease) IsTerminated() bool {
	return r.Status == ReleaseStatusPublished || r.Status == ReleaseStatusClosed
}

// CanPublish 检查任务是否处于可发布状态（已上传至少一张配置表且未终止）。
// 新流程移除了验收环节，上传完成后即可直接发布。
func (r *ConfigRelease) CanPublish() bool {
	return r.HasUploaded() && !r.IsTerminated()
}
