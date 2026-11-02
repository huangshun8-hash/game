package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 配置发布状态常量
const (
	// ConfigStatusPublished 已发布，线上生效
	ConfigStatusPublished = "published"
)

// FieldDef 配置表字段定义
type FieldDef struct {
	// Name 字段名（原始名，来自Excel第1行）
	Name string `bson:"name" json:"name"`
	// Type 字段类型（来自Excel第2行，如 int、string、[]int 等）
	Type string `bson:"type" json:"type"`
	// Comment 字段说明（来自Excel第4行）
	Comment string `bson:"comment" json:"comment"`
}

// ConfigSchema 配置表结构定义文档
// 存储于 config_schema 集合，_id 为表名
type ConfigSchema struct {
	// ID 表名（如 Activity、Item），作为 MongoDB _id
	ID string `bson:"_id" json:"id"`
	// Version 最近一次发布该表的发布任务 ObjectID
	Version bson.ObjectID `bson:"version" json:"version"`
	// SchemaHash 结构 Hash（字段名+类型排序后SHA256）
	SchemaHash string `bson:"schemaHash" json:"schemaHash"`
	// Fields 服务端导出字段列表
	Fields []FieldDef `bson:"fields" json:"fields"`
	// UpdatedAt 最后更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// ConfigManifest 配置清单文档，记录每张配置表当前线上（已发布）的版本快照
// 存储于 config_manifest 集合，_id 为表名，供 Uploader 工具做增量对比
//
// 状态流转：
//
//	上传 → stagingVersion 更新，status=pending
//	发布 → publishedVersion=stagingVersion，status=published
type ConfigManifest struct {
	// ID 表名，作为 MongoDB _id
	ID string `bson:"_id" json:"id"`

	// --- 暂存区（上传后立即更新）---

	// StagingVersion 暂存版本号，对应上传时关联的发布任务 ObjectID
	StagingVersion bson.ObjectID `bson:"stagingVersion" json:"stagingVersion"`
	// StagingSchemaHash 暂存 Schema Hash
	StagingSchemaHash string `bson:"stagingSchemaHash" json:"stagingSchemaHash"`
	// StagingDataHash 暂存 Data Hash
	StagingDataHash string `bson:"stagingDataHash" json:"stagingDataHash"`
	// StagingAuthor 最近一次上传操作者
	StagingAuthor string `bson:"stagingAuthor" json:"stagingAuthor"`
	// StagingAt 最近一次上传时间
	StagingAt time.Time `bson:"stagingAt" json:"stagingAt"`

	// --- 发布区（发布后更新）---

	// PublishedVersion 已发布版本号，对应发布时关联的发布任务 ObjectID（零值表示从未发布）
	PublishedVersion bson.ObjectID `bson:"publishedVersion" json:"publishedVersion"`
	// PublishedSchemaHash 已发布 Schema Hash
	PublishedSchemaHash string `bson:"publishedSchemaHash" json:"publishedSchemaHash"`
	// PublishedDataHash 已发布 Data Hash
	PublishedDataHash string `bson:"publishedDataHash" json:"publishedDataHash"`
	// PublishedAuthor 最近一次发布操作者
	PublishedAuthor string `bson:"publishedAuthor" json:"publishedAuthor"`
	// PublishedAt 最近一次发布时间
	PublishedAt *time.Time `bson:"publishedAt" json:"publishedAt,omitempty"`
	// ReleaseID 关联的发布任务 ObjectID
	ReleaseID bson.ObjectID `bson:"releaseId" json:"releaseId"`

	// --- 状态 ---

	// Status 当前状态：pending=有待发布版本，published=暂存与发布一致
	Status string `bson:"status" json:"status"`
	// UpdatedAt 文档最后更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}
