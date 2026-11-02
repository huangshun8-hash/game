package model

import "errors"

// platform 服务业务错误码（1000-1999）
const (
	// ErrCodeBadRequest 请求参数错误
	ErrCodeBadRequest = 1400
	// ErrCodeUnauthorized 签名验证失败或未提供签名
	ErrCodeUnauthorized = 1401
	// ErrCodeAccountBanned 账号已被封禁
	ErrCodeAccountBanned = 1403
	// ErrCodeAccountNotFound 账号不存在
	ErrCodeAccountNotFound = 1404
	// ErrCodeInternal 内部服务错误
	ErrCodeInternal = 1500
)

var (
	// ErrAccountNotFound 账号不存在
	ErrAccountNotFound = errors.New("account not found")
	// ErrAccountBanned 账号已被封禁
	ErrAccountBanned = errors.New("account is banned")
	// ErrNoRowsUpdated 更新操作未匹配到任何记录（记录不存在或条件不满足）
	ErrNoRowsUpdated = errors.New("no rows updated")
	// ErrWhitelistAccountNotFound 按 playerId 添加/查询白名单时账号不存在或已注销（只校验现存账号）
	ErrWhitelistAccountNotFound = errors.New("whitelist account not found")
	// ErrWhitelistInvalidIdentity 白名单操作未提供 playerId 或 account，无法定位账号
	ErrWhitelistInvalidIdentity = errors.New("whitelist requires playerId or account")
)
