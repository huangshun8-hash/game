package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/shared/gmtdto"
)

// QueryOrderReq GMT 订单查询请求 DTO（接收来自 Web 前端的 Query 参数）
// 字段含义与 gmtdto.GmtQueryOrderReq 一致，此处额外嵌入 BaseReq 以获取 RequestID
type QueryOrderReq struct {
	httpx.BaseReq
	// OrderID 业务订单号，精确匹配；非空时忽略其他所有条件；同时兼容三方订单号
	OrderID string `query:"orderId"`
	// PlayerID 玩家唯一标识，0 或不传表示不过滤
	PlayerID int64 `query:"playerId"`
	// PaymentStatus 支付状态过滤：WAIT / SUCCESS / FAILED（空表示不过滤）
	PaymentStatus string `query:"paymentStatus"`
	// Channel 支付渠道过滤：apple / google（空表示不过滤）
	Channel string `query:"channel"`
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64 `query:"startTime"`
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64 `query:"endTime"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `query:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `query:"pageSize"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 Platform 的 gmtdto.GmtQueryOrderReq
func (r *QueryOrderReq) ToGmtDTO() *gmtdto.GmtQueryOrderReq {
	return &gmtdto.GmtQueryOrderReq{
		OrderID:       r.OrderID,
		PlayerID:      r.PlayerID,
		PaymentStatus: r.PaymentStatus,
		Channel:       r.Channel,
		StartTime:     r.StartTime,
		EndTime:       r.EndTime,
		Page:          r.Page,
		PageSize:      r.PageSize,
	}
}

// QueryGameOrderReq GMT 业务订单查询请求 DTO（接收来自 Web 前端的 Query 参数）
// 对应 GameLogic 侧业务履约视角订单，字段含义与 gmtdto.GmtQueryGameOrderReq 一致
type QueryGameOrderReq struct {
	httpx.BaseReq
	// OrderID 业务订单号，精确匹配；非空时忽略其他所有条件
	OrderID string `query:"orderId"`
	// PlayerID 玩家唯一标识，0 或不传表示不过滤
	PlayerID int64 `query:"playerId"`
	// Status 业务状态过滤：WAIT_PAY / PAID / DELIVERED / CLOSED（空表示不过滤）
	Status string `query:"status"`
	// Channel 支付渠道过滤：apple / google（空表示不过滤）
	Channel string `query:"channel"`
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64 `query:"startTime"`
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64 `query:"endTime"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `query:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `query:"pageSize"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtQueryGameOrderReq
func (r *QueryGameOrderReq) ToGmtDTO() *gmtdto.GmtQueryGameOrderReq {
	return &gmtdto.GmtQueryGameOrderReq{
		OrderID:   r.OrderID,
		PlayerID:  r.PlayerID,
		Status:    r.Status,
		Channel:   r.Channel,
		StartTime: r.StartTime,
		EndTime:   r.EndTime,
		Page:      r.Page,
		PageSize:  r.PageSize,
	}
}

// QueryPayStatsReq GMT 玩家充值统计查询请求 DTO（接收来自 Web 前端的 Query 参数）
// 统计口径与用户标签匹配（MatchTags）使用的付费统计一致，金额单位为分
type QueryPayStatsReq struct {
	httpx.BaseReq
	// PlayerID 玩家唯一标识（必填，>0）
	PlayerID int64 `query:"playerId"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtQueryPayStatsReq
func (r *QueryPayStatsReq) ToGmtDTO() *gmtdto.GmtQueryPayStatsReq {
	return &gmtdto.GmtQueryPayStatsReq{PlayerID: r.PlayerID}
}

// ReplenishOrderReq GMT 补单请求 DTO（接收来自 Web 前端的 JSON Body）
type ReplenishOrderReq struct {
	httpx.BaseReq
	// OrderID 业务订单号（必填）
	OrderID string `json:"orderId"`
	// ThirdTransactionID 渠道交易ID（必填，由运营从渠道后台获取）
	ThirdTransactionID string `json:"thirdTransactionId"`
	// Reason 补单原因备注（必填，用于审计）
	Reason string `json:"reason"`
	// OperatorID 操作人ID（由 GMT 系统从登录态注入，忽略请求体传入值）
	OperatorID string `json:"operatorId"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 Platform 的 gmtdto.GmtReplenishOrderReq
func (r *ReplenishOrderReq) ToGmtDTO() *gmtdto.GmtReplenishOrderReq {
	return &gmtdto.GmtReplenishOrderReq{
		OrderID:            r.OrderID,
		ThirdTransactionID: r.ThirdTransactionID,
		Reason:             r.Reason,
		OperatorID:         r.OperatorID,
	}
}
