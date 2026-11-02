package model

import "errors"

// GMT IAP 接口透传错误码（与 Platform 侧对齐，便于前端统一处理）
// GMT 充当透传层，将 Platform 返回的业务错误码原样转发给前端
const (
	// ErrCodeOrderNotFound 订单不存在（对应文档 1420）
	ErrCodeOrderNotFound = 1420
	// ErrCodeOrderStatusInvalid 订单状态不允许此操作（对应文档 1422）
	ErrCodeOrderStatusInvalid = 1422
	// ErrCodeVersionConflict 乐观锁冲突，稍后重试（对应文档 1426）
	ErrCodeVersionConflict = 1426
	// ErrCodeDuplicateTransaction 渠道交易ID已被其他订单使用（对应文档 1427）
	ErrCodeDuplicateTransaction = 1427
)

var (
	// ErrOrderNotFound 订单不存在
	ErrOrderNotFound = errors.New("payment order not found")
	// ErrOrderStatusInvalid 订单状态不允许此操作
	ErrOrderStatusInvalid = errors.New("order status does not allow this operation")
	// ErrVersionConflict 乐观锁版本冲突
	ErrVersionConflict = errors.New("version conflict, please retry")
	// ErrDuplicateTransaction 渠道交易ID重复
	ErrDuplicateTransaction = errors.New("duplicate transaction id")
	// ErrWhitelistAccountNotFound 白名单操作中按 playerId 解析不到现存账号（不存在或已注销）
	ErrWhitelistAccountNotFound = errors.New("whitelist account not found")
	// ErrWhitelistInvalidIdentity 白名单操作未提供 playerId 或 account
	ErrWhitelistInvalidIdentity = errors.New("whitelist requires playerId or account")
	// ErrPlatformUnavailable Platform 服务不可用
	ErrPlatformUnavailable = errors.New("platform service unavailable")
	// ErrGameLogicUnavailable GameLogic 服务不可用
	ErrGameLogicUnavailable = errors.New("gamelogic service unavailable")
)
