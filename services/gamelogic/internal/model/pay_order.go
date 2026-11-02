package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 业务订单状态常量
const (
	// PayOrderStatusWaitPay 等待支付
	PayOrderStatusWaitPay = "WAIT_PAY"
	// PayOrderStatusPaid 支付成功（渠道已扣款，等待玩家主动领奖）
	PayOrderStatusPaid = "PAID"
	// PayOrderStatusDelivered 发奖成功（业务闭环）
	// 发奖由单文档 CAS 原子流转保证并发幂等（status==PAID → DELIVERED 条件更新），
	// 不存在“已发奖未确认”的中间态，故无需 DELIVERING / FAILED 等过渡状态。
	PayOrderStatusDelivered = "DELIVERED"
	// PayOrderStatusClosed 已关闭（超时未支付或取消）
	PayOrderStatusClosed = "CLOSED"
)

// 支付渠道常量
const (
	// PayChannelApple 苹果 App Store
	PayChannelApple = "apple"
	// PayChannelGoogle Google Play
	PayChannelGoogle = "google"
)

// IsValidPayChannel 判断是否为受支持的支付渠道，用于 handler 层入参校验。
// 新增渠道时在此处扩充，无需修改各 handler。
func IsValidPayChannel(channel string) bool {
	switch channel {
	case PayChannelApple, PayChannelGoogle:
		return true
	default:
		return false
	}
}

// PayOrder 游戏逻辑服业务订单主表
// GameLogic 负责订单创建、发奖和业务状态管理
type PayOrder struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// OrderID 全局唯一订单号（雪花算法或时间+序列）
	OrderID string `bson:"orderId" json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// ProductID 商品ID
	ProductID string `bson:"productId" json:"productId"`
	// Channel 支付渠道：apple / google
	Channel string `bson:"channel" json:"channel"`
	// Status 订单业务状态：WAIT_PAY / PAID / DELIVERED / FAILED / CLOSED
	Status string `bson:"status" json:"status"`
	// AwardTag 奖项标识，客户端创建订单时传入，说明本笔订单领取的是哪笔奖项，原样持久化
	AwardTag string `bson:"awardTag" json:"awardTag"`
	// Award 奖励内容，客户端创建订单时传入，原样持久化且不参与重复订单判定
	Award string `bson:"award" json:"award"`
	// NotifyID 最后一次收到的Platform通知唯一ID，用于追溯通知批次
	NotifyID string `bson:"notifyId" json:"notifyId"`
	// Amount 支付金额（分），由Platform支付成功回调写入，作为付费统计的数据源
	Amount int64 `bson:"amount" json:"amount"`
	// ExpireTime 订单过期时间（用于定时关闭超时订单）
	ExpireTime time.Time `bson:"expireTime" json:"expireTime"`
	// PaidAt 支付成功时间
	PaidAt *time.Time `bson:"paidAt,omitempty" json:"paidAt,omitempty"`
	// DeliveredAt 发奖成功时间
	DeliveredAt *time.Time `bson:"deliveredAt,omitempty" json:"deliveredAt,omitempty"`
	// CreatedAt 创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}
