package model

import "errors"

// IAP 相关错误码（platform 服务 1000-1999 段内扩展）
const (
	// ErrCodeOrderNotFound 订单不存在
	ErrCodeOrderNotFound = 1420
	// ErrCodeOrderDuplicate 订单号重复
	ErrCodeOrderDuplicate = 1421
	// ErrCodeOrderStatusInvalid 订单状态不允许此操作
	ErrCodeOrderStatusInvalid = 1422
	// ErrCodeProductNotFound 商品不存在
	ErrCodeProductNotFound = 1423
	// ErrCodeChannelError 渠道接口错误
	ErrCodeChannelError = 1425
	// ErrCodeVersionConflict 乐观锁冲突
	ErrCodeVersionConflict = 1426
	// ErrCodeDuplicateTransaction 渠道交易ID重复（防重放）
	ErrCodeDuplicateTransaction = 1427
)

var (
	// ErrPaymentOrderNotFound 支付订单不存在
	ErrPaymentOrderNotFound = errors.New("payment order not found")
	// ErrOrderDuplicate 订单号已存在
	ErrOrderDuplicate = errors.New("order already exists")
	// ErrOrderStatusInvalid 订单状态不允许此操作
	ErrOrderStatusInvalid = errors.New("order status not allowed for this operation")
	// ErrProductNotFound 商品配置不存在
	ErrProductNotFound = errors.New("product not found")
	// ErrChannelError 渠道接口异常
	ErrChannelError = errors.New("channel api error")
	// ErrVersionConflict 乐观锁版本冲突
	ErrVersionConflict = errors.New("version conflict, please retry")
	// ErrDuplicateTransaction 渠道交易ID重复，防止重放攻击
	ErrDuplicateTransaction = errors.New("duplicate transaction id")
)

// 三方充值接口错误码（与接口文档 code 语义保持一致，负数为接口约定取值）
const (
	// RechargeCodeSuccess 充值成功（已经完成）
	RechargeCodeSuccess = 0
	// RechargeCodeInvalidAccount 无效的帐号名
	RechargeCodeInvalidAccount = -1
	// RechargeCodeInvalidRole 无效的角色名
	RechargeCodeInvalidRole = -2
	// RechargeCodeInvalidTarget 无效的充值目标标识
	RechargeCodeInvalidTarget = -3
	// RechargeCodeDuplicateOrder 重复的订单号
	RechargeCodeDuplicateOrder = -4
	// RechargeCodeInvalidInfo 无效的充值信息
	RechargeCodeInvalidInfo = -5
	// RechargeCodeInvalidExtra 无效的附加充值信息
	RechargeCodeInvalidExtra = -6
	// RechargeCodeServerCommError 与游戏服通讯异常
	RechargeCodeServerCommError = -7
	// RechargeCodeServerProcessError 游戏服处理异常
	RechargeCodeServerProcessError = -8
	// RechargeCodeUnknown 未知错误
	RechargeCodeUnknown = -255
)

var (
	// ErrRechargeInvalidAccount 充值账号不存在（无效的帐号名）
	ErrRechargeInvalidAccount = errors.New("recharge: invalid account")
	// ErrRechargeInvalidRole 充值角色无效（无效的角色名）
	ErrRechargeInvalidRole = errors.New("recharge: invalid role")
	// ErrRechargeInvalidTarget 充值目标标识无效
	ErrRechargeInvalidTarget = errors.New("recharge: invalid target")
	// ErrRechargeDuplicateOrder 充值订单号重复
	ErrRechargeDuplicateOrder = errors.New("recharge: duplicate order")
	// ErrRechargeOrderNotFound 回调的充值订单不存在（拒绝回调，不自动创建订单）
	ErrRechargeOrderNotFound = errors.New("recharge: order not found")
	// ErrRechargeInvalidInfo 充值信息无效（金额/参数不合法）
	ErrRechargeInvalidInfo = errors.New("recharge: invalid info")
	// ErrRechargeInvalidExtra 附加充值信息无效
	ErrRechargeInvalidExtra = errors.New("recharge: invalid extra info")
	// ErrRechargeServerCommError 与游戏服通讯异常
	ErrRechargeServerCommError = errors.New("recharge: game server communication error")
	// ErrRechargeServerProcessError 游戏服处理异常
	ErrRechargeServerProcessError = errors.New("recharge: game server process error")
)
