package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/shared/gmtdto"
)

// BatchBanReq GMT 批量封禁请求 DTO（接收来自 Web 前端的 JSON Body）
type BatchBanReq struct {
	httpx.BaseReq
	// PlayerIDs 需要封禁的玩家ID列表（必填，至少一个，最多100个）
	PlayerIDs []int64 `json:"playerIds"`
	// Reason 封禁原因（必填，用于审计日志）
	Reason string `json:"reason"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtBatchBanReq
// operatorID 由 handler 从 JWT Claims 中获取后传入
func (r *BatchBanReq) ToGmtDTO(operatorID string) *gmtdto.GmtBatchBanReq {
	return &gmtdto.GmtBatchBanReq{
		PlayerIDs:  r.PlayerIDs,
		Reason:     r.Reason,
		OperatorID: operatorID,
	}
}

// BatchUnbanReq GMT 批量解封请求 DTO（接收来自 Web 前端的 JSON Body）
type BatchUnbanReq struct {
	httpx.BaseReq
	// PlayerIDs 需要解封的玩家ID列表（必填，至少一个，最多100个）
	PlayerIDs []int64 `json:"playerIds"`
	// Reason 解封原因（必填，用于审计日志）
	Reason string `json:"reason"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtBatchUnbanReq
// operatorID 由 handler 从 JWT Claims 中获取后传入
func (r *BatchUnbanReq) ToGmtDTO(operatorID string) *gmtdto.GmtBatchUnbanReq {
	return &gmtdto.GmtBatchUnbanReq{
		PlayerIDs:  r.PlayerIDs,
		Reason:     r.Reason,
		OperatorID: operatorID,
	}
}

// QueryBanRecordReq GMT 封禁记录查询请求 DTO（接收来自 Web 前端的 Query 参数）
type QueryBanRecordReq struct {
	httpx.BaseReq
	// PlayerID 按玩家ID过滤（0 表示不过滤）
	PlayerID int64 `query:"playerId"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `query:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `query:"pageSize"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtQueryBanRecordReq
func (r *QueryBanRecordReq) ToGmtDTO() *gmtdto.GmtQueryBanRecordReq {
	return &gmtdto.GmtQueryBanRecordReq{
		PlayerID: r.PlayerID,
		Page:     r.Page,
		PageSize: r.PageSize,
	}
}
