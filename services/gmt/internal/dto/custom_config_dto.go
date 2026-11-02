package dto

import "kemiluo/pkg/httpx"

// SaveCustomConfigReq 保存自定义配置请求DTO
type SaveCustomConfigReq struct {
	httpx.BaseReq
	// Data 原始JSON数据，原样存储，必填
	Data any `json:"data"`
}

// SaveCustomConfigResp 保存自定义配置响应DTO
type SaveCustomConfigResp struct {
	// UpdatedAt 更新时间（毫秒时间戳）
	UpdatedAt int64 `json:"updatedAt"`
}

// GetCustomConfigReq 查询自定义配置请求DTO（无业务参数）
type GetCustomConfigReq struct {
	httpx.BaseReq
}

// GetCustomConfigResp 查询自定义配置响应DTO
type GetCustomConfigResp struct {
	// Data 原始JSON数据
	Data any `json:"data"`
	// UpdatedAt 更新时间（毫秒时间戳）
	UpdatedAt int64 `json:"updatedAt"`
}
