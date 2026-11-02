package model

import "errors"

// gamelogic 好友业务错误码（2430-2449）
// 其中 2433、2436、2439 为预留错误码，暂未使用
const (
	// ErrCodeFriendSelfAdd 不能添加自己为好友
	ErrCodeFriendSelfAdd = 2430
	// ErrCodeFriendAlreadyExists 已经是好友
	ErrCodeFriendAlreadyExists = 2431
	// ErrCodeFriendTargetNotFound 目标玩家不存在
	ErrCodeFriendTargetNotFound = 2432
	// ErrCodeFriendApplyAlreadyPending 已存在同方向待处理申请（记录存在即待处理）
	ErrCodeFriendApplyAlreadyPending = 2434
	// ErrCodeFriendApplyNotFound 申请记录不存在
	ErrCodeFriendApplyNotFound = 2435
	// ErrCodeFriendApplyReceived 对方已向你发送申请，无需反向申请
	ErrCodeFriendApplyReceived = 2437
	// ErrCodeFriendCountLimitExceeded 好友数量已达上限（最多 100 个）
	ErrCodeFriendCountLimitExceeded = 2438
	// ErrCodeFriendOperationFailed 好友操作失败（系统错误兜底，含事务与普通读写失败）
	ErrCodeFriendOperationFailed = 2440
)

var (
	// ErrFriendSelfAdd 不能添加自己为好友
	ErrFriendSelfAdd = errors.New("friend self add")
	// ErrFriendAlreadyExists 已经是好友
	ErrFriendAlreadyExists = errors.New("friend already exists")
	// ErrFriendTargetNotFound 目标玩家不存在
	ErrFriendTargetNotFound = errors.New("friend target not found")
	// ErrFriendApplyAlreadyPending 已存在同方向待处理申请
	ErrFriendApplyAlreadyPending = errors.New("friend apply already pending")
	// ErrFriendApplyNotFound 申请记录不存在
	ErrFriendApplyNotFound = errors.New("friend apply not found")
	// ErrFriendApplyReceived 对方已向你发送申请，请在收到的申请列表中处理
	ErrFriendApplyReceived = errors.New("friend apply received from target")
	// ErrFriendCountLimitExceeded 好友数量已达上限，无法再建立好友关系
	ErrFriendCountLimitExceeded = errors.New("friend count limit exceeded")
	// ErrFriendOperationFailed 好友操作失败（系统错误兜底）
	ErrFriendOperationFailed = errors.New("friend operation failed")
	// ErrRankTypeUnsupported 排行榜类型暂未支持
	ErrRankTypeUnsupported = errors.New("rank type not supported")
)

// IsFriendBusinessError 判断错误是否为好友业务可识别错误（用于事务失败时区分业务错误与系统错误）
func IsFriendBusinessError(err error) bool {
	switch {
	case errors.Is(err, ErrFriendSelfAdd),
		errors.Is(err, ErrFriendAlreadyExists),
		errors.Is(err, ErrFriendTargetNotFound),
		errors.Is(err, ErrFriendApplyAlreadyPending),
		errors.Is(err, ErrFriendApplyNotFound),
		errors.Is(err, ErrFriendApplyReceived),
		errors.Is(err, ErrFriendCountLimitExceeded):
		return true
	default:
		return false
	}
}
