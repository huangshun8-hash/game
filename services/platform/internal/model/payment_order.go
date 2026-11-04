package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 支付状态常量
const (
	// PaymentStatusWait 等待支付
	PaymentStatusWait = "WAIT"
	// PaymentStatusSuccess 支付成功
	PaymentStatusSuccess = "SUCCESS"
	// PaymentStatusFailed 支付失败
	PaymentStatusFailed = "FAILED"
)

// 通知状态常量
const (
	// NotifyStatusWait 等待通知
	NotifyStatusWait = "WAIT"
	// NotifyStatusSuccess 通知成功（GameLogic 已 ACK）
	NotifyStatusSuccess = "SUCCESS"
	// NotifyStatusFailed 通知失败（重试超限）
	NotifyStatusFailed = "FAILED"
)

// 支付渠道常量
const (
	// ChannelApple 苹果 App Store
	ChannelApple = "apple"
	// ChannelGoogle Google Play
	ChannelGoogle = "google"
)

// 补通知重试限制
const (
	// NotifyMaxRetryCount 定时补通知的最大累计次数上限
	// 超过此次数后订单不再被定时任务捞取，需人工介入处理
	NotifyMaxRetryCount = 20
	// NotifyMaxRetryDuration 定时补通知的最大时间窗口（从支付成功起算）
	// 超过此时长后订单不再被定时任务捞取，需人工介入处理
	NotifyMaxRetryDuration = 24 * time.Hour
)

// PaymentOrder Platform 支付主表
// 存储平台侧全量支付状态、通知状态、渠道原始凭证
// 平台宕机重启可自愈，不依赖游戏服数据
type PaymentOrder struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// OrderID 业务订单号（跨服务唯一标识）
	OrderID string `bson:"orderId" json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// ProductID 商品ID
	ProductID string `bson:"productId" json:"productId"`
	// GoodID 渠道商品ID（goodId），下单时按 payId 从 IAPProductGlobal 配置读取并固化：
	// iOS（apple）取 corre1、Google 取 corre2；用于充值回调时校验回调 goodid 是否与本单商品一致
	GoodID string `bson:"goodId" json:"goodId"`
	// Channel 支付渠道：apple / google
	Channel string `bson:"channel" json:"channel"`
	// ThirdTransactionID 渠道侧交易唯一ID（Apple transactionId / Google purchaseToken）
	ThirdTransactionID string `bson:"thirdTransactionId" json:"thirdTransactionId"`
	// PaymentStatus 渠道支付状态：WAIT / SUCCESS / FAILED
	PaymentStatus string `bson:"paymentStatus" json:"paymentStatus"`
	// NotifyStatus 对 GameLogic 的通知状态：WAIT / SUCCESS / FAILED
	NotifyStatus string `bson:"notifyStatus" json:"notifyStatus"`
	// NotifyID 通知唯一ID（UUID），支付成功时生成并固化，补通知复用同一值以保证跨服务可追溯
	NotifyID string `bson:"notifyId" json:"notifyId"`
	// NotifySource 通知来源：gmt（GMT后台手动补单）/ channel（渠道回调或模拟）
	// 首次通知时固化并随通知下发，补通知沿用同一来源；GMT 补单可覆盖为 gmt 以激活已关闭订单
	NotifySource string `bson:"notifySource" json:"notifySource"`
	// NotifyCount 累计通知次数
	NotifyCount int `bson:"notifyCount" json:"notifyCount"`
	// LastNotifyTime 最后一次通知时间（Unix 毫秒），0 表示从未通知
	LastNotifyTime int64 `bson:"lastNotifyTime" json:"lastNotifyTime"`
	// Amount 金额（分）
	Amount int64 `bson:"amount" json:"amount"`
	// Currency 币种（如 CNY / USD）
	Currency string `bson:"currency" json:"currency"`
	// Agent 运营商标识（三方充值接口专用，记录充值来源运营商）
	Agent string `bson:"agent" json:"agent"`
	// Server 目标服务器代码（三方充值接口专用）
	Server string `bson:"server" json:"server"`
	// Param 透传参数（三方充值接口的 param，原样透传给游戏服）
	Param string `bson:"param" json:"param"`
	// Version 乐观锁版本号，防并发脏写
	Version int64 `bson:"version" json:"version"`
	// CreateTime 创建时间（Unix 毫秒）
	CreateTime int64 `bson:"createTime" json:"createTime"`
	// PayTime 支付成功时间（Unix 毫秒），0 表示未支付
	PayTime int64 `bson:"payTime" json:"payTime"`
	// CreatedAt 记录创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 记录更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
	//sdkid
	SdkId string `bson:"sdkId" json:"sdkId"`
}
