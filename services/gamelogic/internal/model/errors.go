package model

import "errors"

// gamelogic 服务业务错误码（2000-2999）
const (
	// ErrCodeBadRequest 请求参数错误
	ErrCodeBadRequest = 2400
	// ErrCodeUnauthorized 未认证或令牌无效（客户端收到此码应重新登录）
	ErrCodeUnauthorized = 2401
	// ErrCodePlayerBanned 玩家已被封禁
	ErrCodePlayerBanned = 2403
	// ErrCodePlayerNotFound 玩家不存在
	ErrCodePlayerNotFound = 2404
	// ErrCodeSaveNotFound 存档不存在
	ErrCodeSaveNotFound = 2405
	// ErrCodeAccountDeactivateFailed 账号注销失败（作废登录身份时出错）
	ErrCodeAccountDeactivateFailed = 2406
	// ErrCodeSaveConflict 存档版本冲突
	ErrCodeSaveConflict = 2409
	// ErrCodeMailNotFound 邮件不存在
	ErrCodeMailNotFound = 2420
	// ErrCodeMailNoReward 邮件无附件，不可领取
	ErrCodeMailNoReward = 2421
	// ErrCodeMailAlreadyRewarded 附件已领取
	ErrCodeMailAlreadyRewarded = 2422
	// ErrCodeMailTemplateNotFound 邮件模板不存在
	ErrCodeMailTemplateNotFound = 2423
	// ErrCodeInternal 内部服务错误
	ErrCodeInternal = 2500
)

var (
	// ErrPlayerNotFound 玩家不存在
	ErrPlayerNotFound = errors.New("player not found")
	// ErrPlayerBanned 玩家已被封禁
	ErrPlayerBanned = errors.New("player is banned")
	// ErrNoRowsUpdated 更新操作未匹配到任何记录（记录不存在或条件不满足）
	ErrNoRowsUpdated = errors.New("no rows updated")
	// ErrAccountDeactivateFailed 账号注销失败（作废登录身份时出错）
	ErrAccountDeactivateFailed = errors.New("account deactivate failed")
	// ErrSaveNotFound 存档不存在
	ErrSaveNotFound = errors.New("save not found")

	// ErrMailNotFound 邮件不存在
	ErrMailNotFound = errors.New("mail not found")
	// ErrMailNoReward 邮件无附件，不可领取
	ErrMailNoReward = errors.New("mail has no reward")
	// ErrMailAlreadyRewarded 附件已领取，不可重复领取
	ErrMailAlreadyRewarded = errors.New("mail already rewarded")
	// ErrMailTemplateNotFound 邮件模板不存在
	ErrMailTemplateNotFound = errors.New("mail template not found")
)
