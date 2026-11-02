// gmt_whitelist_dto.go 定义充值白名单的跨服务共享 DTO。
// Platform 服务实现接口，GMT 服务直接引用此包进行请求构造和响应解析，避免字段漂移。
package gmtdto

// GmtWhitelistAddReq 白名单添加请求
// playerId 与 account 至少提供一个：playerId 时按"现存账号"解析其 openId 落库，account 时直接作为 openId 落库
type GmtWhitelistAddReq struct {
	// Account 白名单账号（openId），提供后直接作为白名单标识，忽略 playerId 解析
	Account string `json:"account"`
	// PlayerID 玩家唯一标识（0 或未提供表示不使用）；仅当 Account 为空时按此解析现存账号的 openId
	PlayerID int64 `json:"playerId"`
}

// GmtWhitelistAddResp 白名单添加响应
type GmtWhitelistAddResp struct {
	// OpenID 入库/已存在的白名单账号（原始身份）
	OpenID string `json:"openId"`
	// AlreadyExists 是否已存在于白名单（true 表示本次为幂等命中，未重复插入）
	AlreadyExists bool `json:"alreadyExists"`
	// CreatedAt 创建时间（Unix 毫秒）；AlreadyExists 为 true 时表示既有记录的创建时间
	CreatedAt int64 `json:"createdAt"`
}

// GmtWhitelistDeleteReq 白名单删除请求
// 仅支持按账号（openId）删除
type GmtWhitelistDeleteReq struct {
	// OpenID 白名单账号（openId），必填
	OpenID string `json:"openId"`
}

// GmtWhitelistQueryReq 白名单分页查询请求（无条件，仅分页）
type GmtWhitelistQueryReq struct {
	// Page 页码，从 1 开始（默认 1）
	Page int64 `json:"page" form:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `json:"pageSize" form:"pageSize"`
}

// GmtWhitelistItem 白名单记录（仅暴露 openId 与创建时间）
type GmtWhitelistItem struct {
	// OpenID 白名单账号
	OpenID string `json:"openId"`
	// CreatedAt 创建时间（Unix 毫秒）
	CreatedAt int64 `json:"createdAt"`
}

// GmtWhitelistQueryResp 白名单分页查询响应
type GmtWhitelistQueryResp struct {
	// Items 当前页白名单记录
	Items []*GmtWhitelistItem `json:"items"`
	// Total 满足条件的总记录数
	Total int64 `json:"total"`
	// Page 当前页码（从 1 开始）
	Page int64 `json:"page"`
	// PageSize 每页条数
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}
