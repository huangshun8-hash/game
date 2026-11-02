package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/shared/gmtdto"
)

// WhitelistAddReq GMT 白名单添加请求 DTO（接收来自 Web 前端的 JSON Body）
type WhitelistAddReq struct {
	httpx.BaseReq
	// Account 白名单账号（openId）；与 PlayerID 至少提供一个
	Account string `json:"account"`
	// PlayerID 玩家唯一标识（0 表示未提供）；与 Account 至少提供一个
	PlayerID int64 `json:"playerId"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 Platform 的 gmtdto.GmtWhitelistAddReq
func (r *WhitelistAddReq) ToGmtDTO() *gmtdto.GmtWhitelistAddReq {
	return &gmtdto.GmtWhitelistAddReq{
		Account:  r.Account,
		PlayerID: r.PlayerID,
	}
}

// WhitelistDeleteReq GMT 白名单删除请求 DTO（接收来自 Web 前端的 JSON Body）
type WhitelistDeleteReq struct {
	httpx.BaseReq
	// OpenID 白名单账号（openId），必填
	OpenID string `json:"openId"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 Platform 的 gmtdto.GmtWhitelistDeleteReq
func (r *WhitelistDeleteReq) ToGmtDTO() *gmtdto.GmtWhitelistDeleteReq {
	return &gmtdto.GmtWhitelistDeleteReq{
		OpenID: r.OpenID,
	}
}

// QueryWhitelistReq GMT 白名单查询请求 DTO（接收来自 Web 前端的 Query 参数，无条件仅分页）
type QueryWhitelistReq struct {
	httpx.BaseReq
	// Page 页码，从 1 开始（默认 1）
	Page int64 `query:"page"`
	// PageSize 每页条数（默认 20，最大 100）
	PageSize int64 `query:"pageSize"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 Platform 的 gmtdto.GmtWhitelistQueryReq
func (r *QueryWhitelistReq) ToGmtDTO() *gmtdto.GmtWhitelistQueryReq {
	return &gmtdto.GmtWhitelistQueryReq{
		Page:     r.Page,
		PageSize: r.PageSize,
	}
}
