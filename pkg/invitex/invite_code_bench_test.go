package invitex

import "testing"

// sink 防止编译器对无副作用调用做死代码消除，保证测量结果真实
var sink int64

// BenchmarkScramble 测量扰乱函数性能
func BenchmarkScramble(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sink = scramble(int64(i))
	}
}

// BenchmarkUnscramble 测量逆扰乱函数性能
func BenchmarkUnscramble(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sink = unscramble(int64(i))
	}
}

// BenchmarkUID2InviteCode 测量玩家ID编码为邀请码的性能
func BenchmarkUID2InviteCode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = UID2InviteCode(int64(i))
	}
}

// BenchmarkInviteCode2UID 测量邀请码反解为玩家ID的性能
func BenchmarkInviteCode2UID(b *testing.B) {
	code, _ := UID2InviteCode(1000000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = InviteCode2UID(code)
	}
}
