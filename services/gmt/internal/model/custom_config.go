package model

import "time"

// CustomConfigID 自定义配置的固定文档ID（全局仅一条）
const CustomConfigID = "global"

// CustomConfig 自定义配置文档，GMT后台配置的JSON数据原样存储
// 存储于 custom_configs 集合，全局仅一条记录，_id 固定为 "global"
type CustomConfig struct {
	// ID 固定为 "global"（作为 MongoDB _id）
	ID string `bson:"_id" json:"id"`
	// Data 原始 JSON 数据，原样存储、原样返回客户端
	Data any `bson:"data" json:"data"`
	// Operator 最近一次操作者账号名
	Operator string `bson:"operator" json:"operator"`
	// UpdatedAt 最后更新时间（毫秒时间戳）
	UpdatedAt int64 `bson:"updatedAt" json:"updatedAt"`
	// CreatedAt 创建时间（毫秒时间戳）
	CreatedAt int64 `bson:"createdAt" json:"createdAt"`
}

// NewCustomConfig 创建自定义配置文档
func NewCustomConfig(data any, operator string) *CustomConfig {
	now := time.Now().UnixMilli()
	return &CustomConfig{
		ID:        CustomConfigID,
		Data:      data,
		Operator:  operator,
		UpdatedAt: now,
		CreatedAt: now,
	}
}
