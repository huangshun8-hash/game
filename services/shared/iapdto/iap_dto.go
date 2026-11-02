// Package iapdto 定义 GameLogic 与 Platform 之间内购内部接口的共享 DTO。
// 统一在此声明，避免两侧各自定义导致字段或 JSON tag 漂移（编译期无法发现、只能线上静默丢字段）。
package iapdto

// 通知来源常量
const (
	// NotifySourceChannel 通知来源：渠道真实回调（或本地模拟渠道支付）
	NotifySourceChannel = "channel"
	// NotifySourceGMT 通知来源：GMT 后台手动补单
	NotifySourceGMT = "gmt"
)

// PayNotifyReq Platform 向 GameLogic 发送的支付成功通知请求（内部接口）
type PayNotifyReq struct {
	// NotifyID 通知唯一ID（UUID），用于精准追溯通知批次
	NotifyID string `json:"notifyId"`
	// Source 通知来源：gmt（GMT后台手动补单）/ channel（渠道回调或模拟）
	// GameLogic 据此判定已关闭（CLOSED）订单是否允许重开，仅 gmt 来源可激活
	Source string `json:"source"`
	// OrderID 业务订单号
	OrderID string `json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// Channel 支付渠道
	Channel string `json:"channel"`
	// ThirdTransactionID 渠道交易ID
	ThirdTransactionID string `json:"thirdTransactionId"`
	// Amount 金额（分）
	Amount int64 `json:"amount"`
	// Currency 币种
	Currency string `json:"currency"`
	// Agent 运营商标识（三方充值接口透传，供游戏服按运营商区分处理）
	Agent string `json:"agent"`
	// Server 服务器代码（三方充值接口透传）
	Server string `json:"server"`
	// Param 透传参数（三方充值接口原样透传）
	Param string `json:"param"`
}

// PayNotifyResp GameLogic 对支付通知的 ACK 响应
type PayNotifyResp struct {
	// OrderID 订单号
	OrderID string `json:"orderId"`
	// IsPaid 订单是否已标记为 PAID（收到通知并落库）
	IsPaid bool `json:"isPaid"`
}

// PaySuccessPushChannel 内购支付成功跨服广播的 Redis Pub/Sub 频道名。
// 多服务器部署下，处理支付回调的 GameLogic 实例与玩家 WS 连接所在实例可能不同
// （每个实例的 wsx.Hub 仅管理本进程内的连接），故支付成功实例将推送消息发布到此频道，
// 各 GameLogic 实例订阅后按 PlayerID 判断目标玩家是否连接在本实例上，在线则下发 WS 推送。
const PaySuccessPushChannel = "iap:paySuccess"

// PaySuccessPushMessage 内购支付成功跨服广播消息（频道：iap:paySuccess）。
// 承载订阅方路由所需的 PlayerID 与业务负载 Data；订阅方收到后把 Data 转换为
// 客户端契约 cspb.PaySuccessPush（proto）并经 wsx.Hub 下发 WS 推送，
// 保证各实例推送内容一致。
type PaySuccessPushMessage struct {
	// PlayerID 目标玩家ID，订阅方据此路由到本实例 Hub 上的玩家连接
	PlayerID int64 `json:"playerId"`
	// Data 支付成功业务负载（字段与 cspb.PaySuccessPush 一一对应）
	Data PaySuccessPushData `json:"data"`
}

// PaySuccessPushData 支付成功业务负载（Redis Pub/Sub 内部传输契约，JSON 编码）。
// 订阅方收到后转换为客户端契约 cspb.PaySuccessPush（proto）下发。
type PaySuccessPushData struct {
	// OrderID 业务订单号（同时作为推送帧 bizId，供客户端去重）
	OrderID string `json:"orderId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// Channel 支付渠道：apple / google
	Channel string `json:"channel"`
	// AwardTag 奖项标识（创建订单时客户端传入，原样回传）
	AwardTag string `json:"awardTag"`
	// Award 奖励内容（创建订单时客户端传入，原样回传）
	Award string `json:"award"`
	// PaidAt 支付成功时间（Unix 毫秒）
	PaidAt int64 `json:"paidAt"`
}

// CreatePaymentOrderReq GameLogic → Platform 创建支付订单请求（内部接口）
// 精简字段：仅传 orderId、playerId、productId，价格/SKU 由 Platform 自主查询配置中心
type CreatePaymentOrderReq struct {
	// OrderID 业务订单号（由 GameLogic 生成，全局唯一）
	OrderID string `json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// Channel 支付渠道：apple / google
	Channel string `json:"channel"`
}

// CreatePaymentOrderResp 创建支付订单响应
type CreatePaymentOrderResp struct {
	// OrderID 订单号
	OrderID string `json:"orderId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// SKU 渠道商品SKU
	SKU string `json:"sku"`
	// Amount 标准价格（分）
	Amount int64 `json:"amount"`
	// Currency 币种
	Currency string `json:"currency"`
}
