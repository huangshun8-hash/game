package dto

import "kemiluo/pkg/httpx"

// UploadProtoReq 上传Proto文件请求DTO
// Content-Type: multipart/form-data，文件字段名 "file"，表单字段 "protoName"
type UploadProtoReq struct {
	httpx.BaseReq
	// ProtoName Proto模板唯一业务标识（表单字段）
	ProtoName string `form:"protoName"`
}

// ProtoExistsResp 查询Proto模板是否存在响应DTO
type ProtoExistsResp struct {
	// Exists 是否存在全局Proto模板
	Exists bool `json:"exists"`
	// ProtoName Proto模板名称（不存在时为空）
	ProtoName string `json:"protoName,omitempty"`
	// MessageList Player直接message字段名组成的存档key列表（不存在时为空）
	MessageList []string `json:"messageList,omitempty"`
	// MD5 Proto源码MD5校验值（不存在时为空）
	MD5 string `json:"md5,omitempty"`
	// UpdateTime 最后更新时间，毫秒时间戳（不存在时为0）
	UpdateTime int64 `json:"updateTime,omitempty"`
}

// UploadProtoResp 上传Proto文件响应DTO
type UploadProtoResp struct {
	// ProtoName Proto模板名称
	ProtoName string `json:"protoName"`
	// MessageList Player直接message字段名组成的存档key列表
	MessageList []string `json:"messageList"`
	// MD5 Proto源码MD5校验值
	MD5 string `json:"md5"`
	// UpdateTime 更新时间（毫秒时间戳）
	UpdateTime int64 `json:"updateTime"`
}

// GetPlayerDataReq 查询玩家分片数据请求DTO
// MessageName 可选：传入Player直接字段名时返回单个分片数据，为空时返回该玩家所有分片数据
type GetPlayerDataReq struct {
	httpx.BaseReq
	// PlayerID 玩家唯一ID
	PlayerID int64 `query:"playerId"`
	// MessageName Player直接字段名，即存档key（可选，为空时返回所有分片）
	MessageName string `query:"messageName"`
}

// PlayerDataItem 单个分片数据项
type PlayerDataItem struct {
	// MessageName Player直接字段名，即存档key
	MessageName string `json:"messageName"`
	// Data JSON格式化的分片数据
	Data any `json:"data"`
	// UpdateTime 分片数据最后修改时间（毫秒时间戳）
	UpdateTime int64 `json:"updateTime"`
}

// GetPlayerDataResp 查询玩家分片数据响应DTO
type GetPlayerDataResp struct {
	// PlayerID 玩家唯一ID
	PlayerID int64 `json:"playerId"`
	// Items 分片数据列表（传入messageName时仅一项，否则为全部分片）
	Items []*PlayerDataItem `json:"items"`
}

// SavePlayerDataItem 单个待保存的分片数据项
type SavePlayerDataItem struct {
	// MessageName Player直接字段名，即存档key
	MessageName string `json:"messageName"`
	// Data JSON格式的分片数据，将被转换为字段对应的Protobuf二进制存储
	Data any `json:"data"`
}

// SavePlayerDataReq 批量保存玩家分片数据请求DTO
type SavePlayerDataReq struct {
	httpx.BaseReq
	// PlayerID 玩家唯一ID
	PlayerID int64 `json:"playerId"`
	// Items 待保存的分片数据列表，支持一次提交多个Player字段数据
	Items []*SavePlayerDataItem `json:"items"`
}

// SavePlayerDataResp 批量保存玩家分片数据响应DTO
type SavePlayerDataResp struct {
	// UpdateTime 本次批量操作完成时间（毫秒时间戳）
	UpdateTime int64 `json:"updateTime"`
}
