// Package gmtdto 定义供运营管理后台（GMT）使用的跨服务共享 DTO。
// Platform 服务实现接口，GMT 服务直接引用此包进行请求构造和响应解析，
// 避免在两侧各自维护一份定义导致字段漂移。
package gmtdto

// GmtQueryOrderReq GMT 订单查询请求
// 支持按 orderId 精确查询或按 playerId / 支付状态 / 时间范围分页查询
type GmtQueryOrderReq struct {
	// OrderID 业务订单号（精确匹配，非空时忽略其他筛选条件）；
	// 同时兼容三方订单号（渠道侧交易ID），即一处查询覆盖两种订单号，无需区分
	OrderID string `json:"orderId" form:"orderId"`
	// PlayerID 玩家唯一标识（0 表示不过滤）
	PlayerID int64 `json:"playerId" form:"playerId"`
	// PaymentStatus 支付状态过滤：WAIT / SUCCESS / FAILED（空表示不过滤）
	PaymentStatus string `json:"paymentStatus" form:"paymentStatus"`
	// Channel 支付渠道过滤：apple / google（空表示不过滤）
	Channel string `json:"channel" form:"channel"`
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64 `json:"startTime" form:"startTime"`
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64 `json:"endTime" form:"endTime"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `json:"page" form:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `json:"pageSize" form:"pageSize"`
}

// GmtOrderItem GMT 视角的订单数据（供运营后台展示，不含 bson 标签）
type GmtOrderItem struct {
	// OrderID 业务订单号
	OrderID string `json:"orderId"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// ProductID 商品ID
	ProductID string `json:"productId"`
	// Channel 支付渠道：apple / google
	Channel string `json:"channel"`
	// ThirdTransactionID 渠道侧交易ID
	ThirdTransactionID string `json:"thirdTransactionId"`
	// PaymentStatus 渠道支付状态：WAIT / SUCCESS / FAILED
	PaymentStatus string `json:"paymentStatus"`
	// NotifyStatus 对 GameLogic 的通知状态：WAIT / SUCCESS / FAILED
	NotifyStatus string `json:"notifyStatus"`
	// NotifyCount 累计通知次数
	NotifyCount int `json:"notifyCount"`
	// Amount 金额（分）
	Amount int64 `json:"amount"`
	// Currency 币种
	Currency string `json:"currency"`
	// CreateTime 创建时间（Unix 毫秒）
	CreateTime int64 `json:"createTime"`
	// PayTime 支付成功时间（Unix 毫秒），0 表示未支付
	PayTime int64 `json:"payTime"`
}

// GmtQueryOrderResp GMT 订单查询响应
type GmtQueryOrderResp struct {
	// Items 订单列表
	Items []*GmtOrderItem `json:"items"`
	// Total 满足条件的订单总数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页条数
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}

// GmtReplenishOrderReq GMT 补单请求（模拟三方支付回调）
// 仅允许对 WAIT 状态订单执行，用于运营人工处理异常单
type GmtReplenishOrderReq struct {
	// OrderID 业务订单号（必填）
	OrderID string `json:"orderId"`
	// ThirdTransactionID 渠道交易ID（由运营从渠道后台获取，必填）
	ThirdTransactionID string `json:"thirdTransactionId"`
	// Reason 补单原因备注（必填，用于审计）
	Reason string `json:"reason"`
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtReplenishOrderResp GMT 补单响应
type GmtReplenishOrderResp struct {
	// OrderID 订单号
	OrderID string `json:"orderId"`
	// IsSuccess 补单是否成功
	IsSuccess bool `json:"isSuccess"`
	// Message 操作结果描述
	Message string `json:"message"`
}
