package model

import "time"

// ProtoSchema Proto唯一模板文档
// 存储全局唯一的Proto源码，每个protoName仅保留一条最新记录
// 作为所有玩家存档分片数据解析的唯一Schema依据
type ProtoSchema struct {
	// ProtoName Proto模板唯一业务标识，全局唯一（作为 MongoDB _id）
	ProtoName string `bson:"_id" json:"protoName"`
	// Content 最新完整Proto源码文本
	Content string `bson:"content" json:"content"`
	// MessageList 上传时从Player直接message字段派生的存档key数组
	MessageList []string `bson:"messageList" json:"messageList"`
	// MD5 Proto源码MD5校验值，用于去重、完整性校验
	MD5 string `bson:"md5" json:"md5"`
	// CreateTime 首次上传创建时间（毫秒时间戳）
	CreateTime int64 `bson:"createTime" json:"createTime"`
	// UpdateTime 最后一次上传覆盖更新时间（毫秒时间戳）
	UpdateTime int64 `bson:"updateTime" json:"updateTime"`
}

// NewProtoSchema 创建Proto模板文档
func NewProtoSchema(protoName, content, md5 string, messageList []string) *ProtoSchema {
	now := time.Now().UnixMilli()
	return &ProtoSchema{
		ProtoName:   protoName,
		Content:     content,
		MessageList: messageList,
		MD5:         md5,
		CreateTime:  now,
		UpdateTime:  now,
	}
}
