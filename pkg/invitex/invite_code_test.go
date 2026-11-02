package invitex

import "testing"

// TestScrambleInverse 校验扰乱函数在大量样本上可逆且单射
func TestScrambleInverse(t *testing.T) {
	seen := make(map[int64]bool, 2000000)
	for uid := int64(0); uid < 2000000; uid++ {
		s := scramble(uid)
		if seen[s] {
			t.Fatalf("scramble 产生碰撞: uid=%d 扰乱值 %d 已存在", uid, s)
		}
		seen[s] = true
		if got := unscramble(s); got != uid {
			t.Fatalf("unscramble(scramble(%d)) = %d, 期望 %d", uid, got, uid)
		}
	}
}

// TestInviteCodeNoCollision 校验连续玩家ID编码后邀请码唯一（无碰撞）
func TestInviteCodeNoCollision(t *testing.T) {
	seen := make(map[string]bool, 200000)
	for uid := int64(0); uid < 2000000; uid++ {
		code, err := UID2InviteCode(uid)
		if err != nil {
			t.Fatalf("UID2InviteCode(%d) 出错: %v", uid, err)
		}
		if seen[code] {
			t.Fatalf("邀请码碰撞: uid=%d code=%q 已存在", uid, code)
		}
		seen[code] = true
	}
}

// TestInviteCodeRoundTrip 校验有效玩家ID的编解码互逆且长度恒为6位
func TestInviteCodeRoundTrip(t *testing.T) {
	for _, uid := range []int64{0, 1, 2, 31, 32, 511, 512, 12345, 1000000, MaxUID - 1, MaxUID} {
		code, err := UID2InviteCode(uid)
		if err != nil {
			t.Fatalf("UID2InviteCode(%d) 出错: %v", uid, err)
		}
		if len(code) != codeLength {
			t.Fatalf("UID2InviteCode(%d) 长度 = %d, 期望 %d", uid, len(code), codeLength)
		}
		got, err := InviteCode2UID(code)
		if err != nil {
			t.Fatalf("InviteCode2UID(%q) 出错: %v", code, err)
		}
		if got != uid {
			t.Errorf("编解码往返 uid=%d code=%q got=%d", uid, code, got)
		}
	}
}

// TestUID2InviteCode_Overflow 校验超出容量上限或负数玩家ID返回错误
func TestUID2InviteCode_Overflow(t *testing.T) {
	if _, err := UID2InviteCode(MaxUID + 1); err == nil {
		t.Error("超出容量的玩家ID应当返回错误")
	}
	if _, err := UID2InviteCode(-1); err == nil {
		t.Error("负数玩家ID应当返回错误")
	}
}

// TestInviteCode2UID_Invalid 校验长度或字符不合法的邀请码返回错误
func TestInviteCode2UID_Invalid(t *testing.T) {
	if _, err := InviteCode2UID("22222"); err == nil {
		t.Error("长度不足应当返回错误")
	}
	if _, err := InviteCode2UID("2222222"); err == nil {
		t.Error("长度过长应当返回错误")
	}
	if _, err := InviteCode2UID("22O222"); err == nil {
		t.Error("包含易混淆字符 O 应当返回错误")
	}
	if _, err := InviteCode2UID("221222"); err == nil {
		t.Error("包含易混淆字符 1 应当返回错误")
	}
	if _, err := InviteCode2UID("22a222"); err == nil {
		t.Error("包含小写字符 a 应当返回错误")
	}
}
