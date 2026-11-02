package logic

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// MinAdminPasswordLength 管理员密码最小字符数。
const MinAdminPasswordLength = 8

// ValidateAdminPassword 校验管理员密码强度，避免弱口令进入系统。
func ValidateAdminPassword(password string) error {
	if utf8.RuneCountInString(password) < MinAdminPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinAdminPasswordLength)
	}

	var hasUpper bool
	var hasLower bool
	var hasDigit bool
	var hasSpecial bool

	for _, r := range password {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r):
			hasSpecial = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
		return fmt.Errorf("password must include uppercase, lowercase, digit and special character")
	}
	return nil
}
