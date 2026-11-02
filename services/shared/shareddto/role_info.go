// Package shareddto 定义跨服务共享的请求/响应 DTO。
// 这些类型被多个服务（如 gamelogic、platform）共同引用，
// 统一在此声明，避免重复定义导致字段或 JSON tag 漂移。
package shareddto

// RoleInfo 角色信息（LunPlay 平台角色查询接口的数据载体）
// gamelogic 内部角色查询接口返回此结构体；platform 侧透传给 LunPlay 平台。
// createDate / loginDate 由 gamelogic 按 "yyyyMMdd HH:mm:ss" 格式格式化后下发，
// 与平台接口文档约定一致，platform 侧直接透传无需二次转换。
type RoleInfo struct {
	// RoleID 角色ID（对应玩家 playerId）
	RoleID int64 `json:"roleId"`
	// Passport 平台用户ID（对应玩家 openId，LunPlay 平台称为 passport）
	Passport string `json:"passport"`
	// ServerCode 游戏区服代码（对应玩家 serverId）
	ServerCode int64 `json:"serverCode"`
	// PlayerLevel 角色等级
	PlayerLevel int `json:"playerLevel"`
	// PlayerName 角色名
	PlayerName string `json:"playerName"`
	// CreateDate 创建日期，格式 yyyyMMdd HH:mm:ss
	CreateDate string `json:"createDate"`
	// LoginDate 最后登录时间，格式 yyyyMMdd HH:mm:ss
	LoginDate string `json:"loginDate"`
}

// RoleQueryByPassportReq 按平台用户ID查询角色请求（platform → gamelogic 内部接口）
// serverCode 可选：不传或为 0 时返回该用户在全部区服的角色
type RoleQueryByPassportReq struct {
	// OpenID 平台用户ID（openId，即 LunPlay 平台的 passport）
	OpenID string `json:"openId"`
	// ServerCode 游戏区服代码，为 0 表示不过滤区服
	ServerCode int64 `json:"serverCode"`
}

// RoleQueryByRoleIDReq 按角色ID查询角色请求（platform → gamelogic 内部接口）
type RoleQueryByRoleIDReq struct {
	// RoleID 角色ID（对应玩家 playerId）
	RoleID int64 `json:"roleId"`
}

// RoleQueryResp 角色查询响应
type RoleQueryResp struct {
	// Roles 角色列表（按需求文档，查询结果以数组返回）
	Roles []*RoleInfo `json:"roles"`
}

// ServerInfo 区服信息
type ServerInfo struct {
	// ServerCode 区服ID
	ServerCode int64 `json:"serverCode"`
	// ServerName 区服名
	ServerName string `json:"serverName"`
}

// ServerListResp 区服列表响应
type ServerListResp struct {
	// Servers 区服列表
	Servers []*ServerInfo `json:"servers"`
}
