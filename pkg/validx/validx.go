package validx

import (
	"errors"
	"regexp"
)

// uuidMaxLen UUID最大长度
const uuidMaxLen = 64

// identifierRegex 通用标识符合法字符：字母、数字、下划线、连字符
var identifierRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// ValidateUUID 校验UUID格式
// 规则：非空、长度≤64、仅允许 [a-zA-Z0-9_-]
func ValidateUUID(uuid string) error {
	if uuid == "" {
		return errors.New("uuid is required")
	}
	if len(uuid) > uuidMaxLen {
		return errors.New("uuid exceeds maximum length")
	}
	if !identifierRegex.MatchString(uuid) {
		return errors.New("invalid uuid format, only letters, numbers, underscores and hyphens are allowed")
	}
	return nil
}

// ValidateLoginType 校验业务渠道标签
// 规则：非空，仅允许 [a-zA-Z0-9_-]（具体渠道由运营配置，不在此硬编码）
func ValidateLoginType(loginType string) error {
	if loginType == "" {
		return errors.New("loginType is required")
	}
	if !identifierRegex.MatchString(loginType) {
		return errors.New("invalid loginType format, only letters, numbers, underscores and hyphens are allowed")
	}
	return nil
}
