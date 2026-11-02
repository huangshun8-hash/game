// Package gmtdto 定义供运营管理后台（GMT）使用的跨服务共享 DTO。
package gmtdto

// GmtBatchBanReq GMT 批量封禁请求
type GmtBatchBanReq struct {
	// PlayerIDs 需要封禁的玩家ID列表（必填，至少一个）
	PlayerIDs []int64 `json:"playerIds"`
	// Reason 封禁原因（必填，用于审计日志）
	Reason string `json:"reason"`
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtBatchUnbanReq GMT 批量解封请求
type GmtBatchUnbanReq struct {
	// PlayerIDs 需要解封的玩家ID列表（必填，至少一个）
	PlayerIDs []int64 `json:"playerIds"`
	// Reason 解封原因（必填，用于审计日志）
	Reason string `json:"reason"`
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtBatchBanResp GMT 批量封禁/解封响应
type GmtBatchBanResp struct {
	// SuccessIDs 操作成功的玩家ID列表
	SuccessIDs []int64 `json:"successIds"`
	// FailedItems 操作失败的详情列表
	FailedItems []*BanFailedItem `json:"failedItems,omitempty"`
}

// BanFailedItem 封禁/解封失败的单项详情
type BanFailedItem struct {
	// PlayerID 操作失败的玩家ID
	PlayerID int64 `json:"playerId"`
	// Message 失败原因
	Message string `json:"message"`
}

// GmtQueryBanRecordReq GMT 查询封禁记录请求
type GmtQueryBanRecordReq struct {
	// PlayerID 按玩家ID过滤（0 表示不过滤）
	PlayerID int64 `json:"playerId" form:"playerId"`
	// Page 页码，从 1 开始（默认 1）
	Page int64 `json:"page" form:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `json:"pageSize" form:"pageSize"`
}

// GmtBanRecordItem 封禁记录列表项
type GmtBanRecordItem struct {
	// ID 记录唯一标识
	ID string `json:"id"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// Reason 封禁原因
	Reason string `json:"reason"`
	// CreatedAt 封禁时间（Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
}

// GmtQueryBanRecordResp GMT 封禁记录查询响应
type GmtQueryBanRecordResp struct {
	// Items 记录列表
	Items []*GmtBanRecordItem `json:"items"`
	// Total 满足条件的总记录数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页条数
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}
