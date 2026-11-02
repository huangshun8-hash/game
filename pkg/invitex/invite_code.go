// Package invitex 提供玩家ID与6位邀请码互转能力。
// 邀请码采用 32 进制编码（大写字母与数字，剔除易混淆字符），固定 6 位长度。
// 为破坏「小玩家ID高位连续为0导致邀请码前几位相同、可被顺序枚举」的问题，
// 编码前先对玩家ID做一次 30 位域内的可逆扰乱（双射），对外展示的邀请码仍可反解回原玩家ID。
package invitex

import (
	"fmt"
	"slices"
	"strings"
)

// chars 邀请码字符集：8 数字 + 24 大写字母，剔除易混淆的 0/O、1/I，共 32 个字符。
const chars = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

const (
	// codeLength 邀请码固定长度（位）
	codeLength = 6
	// base 编码进制，等于字符集大小
	base = 32
	// MaxUID 6位邀请码可表示的最大玩家ID（32^6 - 1 = 2^30 - 1 = 1073741823）
	MaxUID int64 = 1<<30 - 1
	// scrambleMask 扰乱运算的位宽掩码（30 位），与 MaxUID 一致
	scrambleMask = MaxUID
	// scrambleXor 扰乱用 XOR 常数，上线后不可修改
	scrambleXor int64 = 0x27d4eb2d
)

// scramble 在 [0, 2^30) 域内对玩家ID做可逆扰乱（双射）。
// 通过 30 位循环左移与 XOR 常数破坏小玩家ID高位连续为0的规律，
// 每个玩家ID映射到唯一的扰乱值，解码时可用 unscramble 完全还原。
func scramble(uid int64) int64 {
	v := uid & scrambleMask
	// 30 位循环左移 13 位
	v = ((v << 13) | (v >> 17)) & scrambleMask
	v ^= scrambleXor & scrambleMask
	// 30 位循环左移 8 位
	v = ((v << 8) | (v >> 22)) & scrambleMask
	return v
}

// unscramble 是 scramble 的逆变换，将扰乱值还原为原始玩家ID。
func unscramble(v int64) int64 {
	// 逆最后一步：30 位循环右移 8 位
	v = ((v >> 8) | (v << 22)) & scrambleMask
	v ^= scrambleXor & scrambleMask
	// 逆第一步：30 位循环右移 13 位
	v = ((v >> 13) | (v << 17)) & scrambleMask
	return v
}

// UID2InviteCode 将玩家ID编码为固定6位邀请码。
// uid: 玩家ID，有效范围 [0, MaxUID]。
// 返回值：6位邀请码字符串；uid 超出容量上限时返回错误。
func UID2InviteCode(uid int64) (string, error) {
	if uid < 0 || uid > MaxUID {
		return "", fmt.Errorf("invitex: 玩家ID %d 超出6位邀请码容量上限 [0, %d]", uid, MaxUID)
	}
	// 扰乱后低位在前逐位取模映射为字符，不足 6 位时高位自动补字符集首字符
	buf := make([]byte, 0, codeLength)
	n := scramble(uid)
	for range codeLength {
		buf = append(buf, chars[n%base])
		n /= base
	}
	// 反转得到高位在前的最终字符串
	slices.Reverse(buf)
	return string(buf), nil
}

// InviteCode2UID 将邀请码反解为玩家ID。
// code: 邀请码字符串。
// 返回值：对应的玩家ID；邀请码长度不合法或包含字符集外字符时返回错误。
func InviteCode2UID(code string) (int64, error) {
	if len(code) != codeLength {
		return 0, fmt.Errorf("invitex: 邀请码长度必须为 %d，实际为 %d", codeLength, len(code))
	}
	var scrambled int64
	for _, c := range code {
		idx := strings.IndexRune(chars, c)
		if idx < 0 {
			return 0, fmt.Errorf("invitex: 邀请码包含非法字符 %q", c)
		}
		scrambled = scrambled*base + int64(idx)
	}
	return unscramble(scrambled), nil
}
