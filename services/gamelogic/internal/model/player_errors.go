package model

import "errors"

// gamelogic 玩家查询业务错误码（2450-2469）
const (
	// ErrCodePlayerQueryInvalidParam 查询参数不合法（playerId 与 inviteCode 至少提供一个）
	ErrCodePlayerQueryInvalidParam = 2450
	// ErrCodePlayerQueryInvalidCode 邀请码不合法
	ErrCodePlayerQueryInvalidCode = 2451
	// ErrCodePlayerQueryFailed 玩家查询失败（系统错误兜底）
	ErrCodePlayerQueryFailed = 2452
)

var (
	// ErrPlayerQueryParamMissing 查询参数缺失（playerId 与 inviteCode 至少提供一个）
	ErrPlayerQueryParamMissing = errors.New("player query parameter missing")
	// ErrPlayerQueryInvalidCode 邀请码不合法
	ErrPlayerQueryInvalidCode = errors.New("player query invalid invite code")
	// ErrPlayerQueryFailed 玩家查询失败（系统错误兜底）
	ErrPlayerQueryFailed = errors.New("player query failed")
)
