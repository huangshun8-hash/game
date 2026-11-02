package dto

import "kemiluo/pkg/httpx"

// LoginReq 管理员登录请求DTO
type LoginReq struct {
	httpx.BaseReq
	// Username 登录账号
	Username string `json:"username"`
	// Password 登录密码（明文，传输层依赖HTTPS加密）
	Password string `json:"password"`
}

// LoginResp 管理员登录响应DTO
type LoginResp struct {
	// Token JWT访问令牌
	Token string `json:"token"`
	// Expire 令牌有效期（秒）
	Expire int64 `json:"expire"`
}

// ProfileResp 当前管理员信息响应DTO（不含密码）
type ProfileResp struct {
	// ID 管理员唯一标识
	ID string `json:"id"`
	// Username 登录账号
	Username string `json:"username"`
	// Nickname 用户昵称
	Nickname string `json:"nickname"`
	// IsSuper 是否超级管理员
	IsSuper bool `json:"isSuper"`
}

// UpdateProfileReq 修改个人资料请求DTO
type UpdateProfileReq struct {
	httpx.BaseReq
	// Nickname 新昵称
	Nickname string `json:"nickname"`
}

// ChangePasswordReq 修改密码请求DTO
type ChangePasswordReq struct {
	httpx.BaseReq
	// OldPassword 原密码（明文）
	OldPassword string `json:"oldPassword"`
	// NewPassword 新密码（明文）
	NewPassword string `json:"newPassword"`
}

// CreateAdminReq 新增管理员请求DTO
type CreateAdminReq struct {
	httpx.BaseReq
	// Username 登录账号
	Username string `json:"username"`
	// Nickname 用户昵称
	Nickname string `json:"nickname"`
	// Password 初始密码（明文）
	Password string `json:"password"`
}

// UpdateAdminReq 编辑管理员信息请求DTO
type UpdateAdminReq struct {
	httpx.BaseReq
	// ID 管理员ID（路由参数，由Bind填充）
	ID string `uri:"id"`
	// Nickname 新昵称
	Nickname string `json:"nickname"`
	// Status 账号状态：1=正常 0=禁用
	Status int `json:"status"`
}

// AdminIDReq 仅含路由参数id的通用请求DTO（用于禁用、按id定位等场景）
type AdminIDReq struct {
	httpx.BaseReq
	// ID 管理员ID（路由参数）
	ID string `uri:"id"`
}

// ResetPasswordReq 超级管理员重置其他账号密码请求DTO
type ResetPasswordReq struct {
	httpx.BaseReq
	// ID 目标管理员ID（路由参数，由Bind填充）
	ID string `uri:"id"`
	// NewPassword 新密码（明文）
	NewPassword string `json:"newPassword"`
}

// AdminItem 管理员列表项DTO（用于列表响应，不含密码）
type AdminItem struct {
	// ID 管理员唯一标识
	ID string `json:"id"`
	// Username 登录账号
	Username string `json:"username"`
	// Nickname 用户昵称
	Nickname string `json:"nickname"`
	// Status 账号状态
	Status int `json:"status"`
	// IsSuper 是否超级管理员
	IsSuper bool `json:"isSuper"`
	// LastLoginIP 最后登录IP
	LastLoginIP string `json:"lastLoginIp"`
	// LastLoginAt 最后登录时间（毫秒时间戳，Unix 毫秒，可为nil）
	LastLoginAt *int64 `json:"lastLoginAt,omitempty"`
	// CreatedAt 创建时间（毫秒时间戳，Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
}

// ListOperationLogsReq 操作日志分页查询请求DTO
// 所有过滤字段均为可选，零值表示不过滤
type ListOperationLogsReq struct {
	httpx.BaseReq
	// UID 按操作管理员ID过滤（可选）
	UID string `query:"uid"`
	// Module 按操作模块过滤，如 Admin、Auth、Profile（可选）
	Module string `query:"module"`
	// Action 按操作动作过滤，如 Login、Create、Update、Delete（可选）
	Action string `query:"action"`
	// StartTime 时间范围起点，Unix时间戳（毫秒，可选）
	StartTime int64 `query:"startTime"`
	// EndTime 时间范围终点，Unix时间戳（毫秒，可选）
	EndTime int64 `query:"endTime"`
	// Page 页码，从1开始，默认1
	Page int64 `query:"page"`
	// PageSize 每页数量，默认20，最大200
	PageSize int64 `query:"pageSize"`
}

// OperationLogItem 操作日志列表项DTO
type OperationLogItem struct {
	// ID 日志唯一标识
	ID string `json:"id"`
	// UID 操作管理员ID
	UID string `json:"uid"`
	// Username 操作管理员账号
	Username string `json:"username"`
	// Module 操作模块
	Module string `json:"module"`
	// Action 操作动作
	Action string `json:"action"`
	// Method HTTP请求方法
	Method string `json:"method"`
	// URI 请求路径
	URI string `json:"uri"`
	// IP 请求来源IP
	IP string `json:"ip"`
	// Request 请求参数（脱敏）
	Request any `json:"request"`
	// CreatedAt 操作时间（毫秒时间戳，Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
}

// ListOperationLogsResp 操作日志分页查询响应DTO
type ListOperationLogsResp struct {
	// Items 当前页日志列表
	Items []*OperationLogItem `json:"items"`
	// Total 符合条件的总记录数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页数量
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}
