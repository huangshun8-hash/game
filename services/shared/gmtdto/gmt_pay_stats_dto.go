package gmtdto

// 本文件定义 GMT 运营后台查询玩家充值（付费）统计的跨服务 DTO。
// 统计口径与用户标签匹配（usertag_logic.go 的 MatchTags）使用的付费统计完全一致，
// 保证运营后台看到的数值与标签命中依据同源。

// GmtQueryPayStatsReq GMT 查询玩家充值统计请求
type GmtQueryPayStatsReq struct {
	// PlayerID 玩家唯一标识（必填，>0）
	PlayerID int64 `json:"playerId" form:"playerId"`
}

// GmtPlayerPayStatsResp GMT 玩家充值统计响应（金额单位：分）
// 统计范围：业务状态为 PAID / DELIVERED 的订单
type GmtPlayerPayStatsResp struct {
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// LastPay 最近1笔充值金额
	LastPay int64 `json:"lastPay"`
	// Last4Pay 最近4笔充值金额合计
	Last4Pay int64 `json:"last4Pay"`
	// MonthPay 最近30天充值金额合计
	MonthPay int64 `json:"monthPay"`
	// TotalPay 累积充值金额合计
	TotalPay int64 `json:"totalPay"`
}
