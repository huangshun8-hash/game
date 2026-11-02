package model

import "errors"

// IAP 相关错误码（gamelogic 服务 2000-2999 段内扩展）
const (
	// ErrCodeOrderNotFound 订单不存在
	ErrCodeOrderNotFound = 2420
	// ErrCodeOrderDuplicate 订单号重复
	ErrCodeOrderDuplicate = 2421
	// ErrCodeOrderStatusInvalid 订单状态不允许此操作
	ErrCodeOrderStatusInvalid = 2422
	// ErrCodeProductNotFound 商品不存在
	ErrCodeProductNotFound = 2423
	// ErrCodeDeliveryFailed 发奖失败
	ErrCodeDeliveryFailed = 2424
	// ErrCodeOrderAlreadyClaimed 订单已领取，不可重复领奖
	ErrCodeOrderAlreadyClaimed = 2426
	// ErrCodeOrderNotOwned 支付通知玩家ID与订单归属不一致
	ErrCodeOrderNotOwned = 2428
)

var (
	// ErrOrderNotFound 订单不存在
	ErrOrderNotFound = errors.New("order not found")
	// ErrOrderDuplicate 订单号已存在（幂等重复创建）
	ErrOrderDuplicate = errors.New("order already exists")
	// ErrOrderStatusInvalid 当前订单状态不允许此操作
	ErrOrderStatusInvalid = errors.New("invalid order status for this operation")
	// ErrProductNotFound 商品配置不存在
	ErrProductNotFound = errors.New("product not found")
	// ErrDeliveryFailed 发奖执行失败
	ErrDeliveryFailed = errors.New("delivery failed")
	// ErrOrderAlreadyClaimed 订单已领取，不可重复领奖
	ErrOrderAlreadyClaimed = errors.New("order reward already claimed")
	// ErrOrderNotOwned 订单归属与支付通知中的玩家ID不一致
	ErrOrderNotOwned = errors.New("order player mismatch")
)
