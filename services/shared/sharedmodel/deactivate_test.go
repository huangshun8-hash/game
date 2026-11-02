package sharedmodel

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestIsDeactivatedOpenID 校验作废 openId 的识别：
// 仅以 del_ 前缀开头者视为已注销，其余（含空串、含 del 但非前缀）均为未注销。
func TestIsDeactivatedOpenID(t *testing.T) {
	cases := []struct {
		name   string // 用例名称
		openID string // 待判定的 openId
		want   bool   // 期望是否已注销
	}{
		{name: "已作废的标准前缀", openID: "del_1700000000000_abc", want: true},
		{name: "仅前缀无后缀", openID: "del_", want: true},
		{name: "普通游客身份", openID: "guest_uuid_123", want: false},
		{name: "三方账号身份", openID: "yre_998877", want: false},
		{name: "空字符串", openID: "", want: false},
		{name: "del出现在中间不算前缀", openID: "xdel_123", want: false},
		{name: "大写前缀不匹配", openID: "DEL_123", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsDeactivatedOpenID(c.openID); got != c.want {
				t.Fatalf("IsDeactivatedOpenID(%q) = %v, 期望 %v", c.openID, got, c.want)
			}
		})
	}
}

// TestBuildDeactivatedOpenID 校验作废 openId 的构造：
// 结果须为 "del_<unixMilli>_<原openId>"，且能被 IsDeactivatedOpenID 识别为已注销，
// 同时保留原始 openId 供审计溯源。
func TestBuildDeactivatedOpenID(t *testing.T) {
	const original = "guest_uuid_123"

	before := time.Now().UnixMilli()
	built := BuildDeactivatedOpenID(original)
	after := time.Now().UnixMilli()

	// 前缀必须存在且被识别为已注销
	if !IsDeactivatedOpenID(built) {
		t.Fatalf("构造结果 %q 未被识别为已注销", built)
	}
	if !strings.HasPrefix(built, DeactivatedOpenIDPrefix) {
		t.Fatalf("构造结果 %q 缺少作废前缀 %q", built, DeactivatedOpenIDPrefix)
	}

	// 结构须为 del_<unixMilli>_<原openId>，且原 openId 完整保留在尾部
	rest := strings.TrimPrefix(built, DeactivatedOpenIDPrefix)
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 {
		t.Fatalf("构造结果 %q 结构非法，无法拆出时间戳与原openId", built)
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("构造结果 %q 的时间戳段 %q 非法: %v", built, parts[0], err)
	}
	// 时间戳须落在构造前后的时间窗口内（毫秒级）
	if ts < before || ts > after {
		t.Fatalf("时间戳 %d 不在窗口 [%d, %d] 内", ts, before, after)
	}
	if parts[1] != original {
		t.Fatalf("原openId未完整保留：got %q, 期望 %q", parts[1], original)
	}
}

// TestBuildDeactivatedOpenID_TwiceNotEqual 校验同一身份多次注销（注销→重登→再注销）
// 生成的作废 openId 不相同，避免撞上 openId 唯一索引导致二次注销失败。
func TestBuildDeactivatedOpenID_TwiceNotEqual(t *testing.T) {
	const original = "guest_uuid_123"

	first := BuildDeactivatedOpenID(original)
	// 毫秒时间戳是区分手段，间隔 1ms 以上以确保不同
	time.Sleep(2 * time.Millisecond)
	second := BuildDeactivatedOpenID(original)

	if first == second {
		t.Fatalf("两次作废结果相同 %q，无法避免唯一索引冲突", first)
	}
}

// TestBuildDeactivatedOpenID_EmptyOriginal 校验原 openId 为空时仍生成合法作废串，
// 且不会被误判为未注销（防御边界输入）。
func TestBuildDeactivatedOpenID_EmptyOriginal(t *testing.T) {
	built := BuildDeactivatedOpenID("")
	if !IsDeactivatedOpenID(built) {
		t.Fatalf("空原openId的作废结果 %q 未被识别为已注销", built)
	}
}
