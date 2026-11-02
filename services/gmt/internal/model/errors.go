package model

import "errors"

// GMT管理后台业务错误码（3000-3999）
const (
	// ErrCodeBadRequest 请求参数错误
	ErrCodeBadRequest = 3400
	// ErrCodeUnauthorized 未认证或令牌无效
	ErrCodeUnauthorized = 3401
	// ErrCodeForbidden 无权限操作（如普通管理员尝试操作超级管理员账号）
	ErrCodeForbidden = 3403
	// ErrCodeAdminNotFound 管理员不存在
	ErrCodeAdminNotFound = 3404
	// ErrCodeAdminExists 管理员账号已存在
	ErrCodeAdminExists = 3409
	// ErrCodePasswordWrong 密码错误
	ErrCodePasswordWrong = 3410
	// ErrCodeAccountDisabled 账号已禁用
	ErrCodeAccountDisabled = 3411
	// ErrCodeLoginLocked 登录失败次数过多，账号暂时锁定
	ErrCodeLoginLocked = 3412
	// ErrCodeInternal 内部服务错误
	ErrCodeInternal = 3500
)

var (
	// ErrAdminNotFound 管理员账号不存在
	ErrAdminNotFound = errors.New("admin not found")
	// ErrAdminExists 管理员账号已存在（用户名重复）
	ErrAdminExists = errors.New("admin already exists")
	// ErrPasswordWrong 密码错误
	ErrPasswordWrong = errors.New("wrong password")
	// ErrAccountDisabled 账号已被禁用
	ErrAccountDisabled = errors.New("account is disabled")
	// ErrLoginLocked 登录失败次数过多，账号暂时锁定
	ErrLoginLocked = errors.New("account temporarily locked due to too many failed attempts")
	// ErrForbidden 无权限执行此操作
	ErrForbidden = errors.New("operation not allowed")
	// ErrNoRowsUpdated 更新操作未匹配到任何记录
	ErrNoRowsUpdated = errors.New("no rows updated")
)
