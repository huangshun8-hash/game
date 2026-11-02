package dto

import (
	"net/url"
	"strconv"
	"strings"

	"kemiluo/services/shared/shareddto"
)

// URLEncode 对字符串做标准百分号编码（RFC 3986 口径，空格编码为 %20）。
// url.QueryEscape 将空格编码为 "+"，此处统一替换为 "%20"，
// 与 LunPlay 平台接口文档约定的 URL 编码口径保持一致。
func URLEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// LunPlay 平台角色查询接口响应码（与平台接口文档约定一致）
const (
	// LunPlayCodeSuccess 查询成功
	LunPlayCodeSuccess = 0
	// LunPlayCodeSignInvalid 验证错误
	LunPlayCodeSignInvalid = 1
	// LunPlayCodeQueryFailed 查询角色信息失败
	LunPlayCodeQueryFailed = 2
	// LunPlayCodeServerError 服务器错误
	LunPlayCodeServerError = 3
)

// LunPlayRole 角色信息响应结构（LunPlay 平台可见字段）
// 字段命名与类型严格遵循平台接口文档：roleid/playerName 为字符串，
// playerName 需 URL 编码，createDate/loginDate 格式 yyyyMMdd HH:mm:ss
type LunPlayRole struct {
	// PlayerLevel 角色等级
	PlayerLevel int `json:"playerLevel"`
	// PlayerName 角色名（URL 编码）
	PlayerName string `json:"playerName"`
	// CreateDate 创建日期，格式 yyyyMMdd HH:mm:ss
	CreateDate string `json:"createDate"`
	// LoginDate 最后登录时间，格式 yyyyMMdd HH:mm:ss
	LoginDate string `json:"loginDate"`
	// RoleID 角色ID
	RoleID string `json:"roleid"`
	// Passport 平台用户ID（接口三返回）
	Passport string `json:"passport,omitempty"`
	// ServerCode 游戏区服ID（接口三返回）
	ServerCode string `json:"serverCode,omitempty"`
}

// ToLunPlayRole 将跨服角色信息转换为平台响应结构
// playerName 按文档约定 URL 编码（标准百分号编码），roleid/serverCode 转为字符串
func ToLunPlayRole(r *shareddto.RoleInfo) *LunPlayRole {
	if r == nil {
		return nil
	}
	return &LunPlayRole{
		PlayerLevel: r.PlayerLevel,
		PlayerName:  URLEncode(r.PlayerName),
		CreateDate:  r.CreateDate,
		LoginDate:   r.LoginDate,
		RoleID:      strconv.FormatInt(r.RoleID, 10),
		Passport:    r.Passport,
		ServerCode:  strconv.FormatInt(r.ServerCode, 10),
	}
}
