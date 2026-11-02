package packetx

import (
	"encoding/hex"
	"testing"

	"kemiluo/pb/cspb"

	"google.golang.org/protobuf/proto"
)

// newTestBase 返回一个测试用 Base 实例（实现 proto.Message）
func newTestBase() *cspb.Base { return &cspb.Base{} }

// TestGenerateKey 验证生成的秘钥为 base64 编码的 32 字节随机值且互不相同
func TestGenerateKey(t *testing.T) {
	key1 := GenerateKey()
	key2 := GenerateKey()
	raw, err := DecodeKey(key1)
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("秘钥解码后长度 = %d，期望 32", len(raw))
	}
	if key1 == key2 {
		t.Errorf("两次生成的秘钥相同，缺少随机性")
	}
}

// TestFallbackGenerateKey 验证熵源不可用时的兜底派生秘钥同样为 base64 编码的 32 字节且互不相同
func TestFallbackGenerateKey(t *testing.T) {
	key1 := fallbackGenerateKey()
	key2 := fallbackGenerateKey()
	raw, err := DecodeKey(key1)
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("兜底秘钥解码后长度 = %d，期望 32", len(raw))
	}
	if key1 == key2 {
		t.Errorf("两次兜底生成的秘钥相同，缺少随机性")
	}
}

// TestNormalizeKey 验证秘钥规范化：合法长度原样返回，其他长度以 SHA256 归一化
func TestNormalizeKey(t *testing.T) {
	for _, size := range []int{16, 24, 32} {
		key := make([]byte, size)
		if got := NormalizeKey(key); len(got) != size {
			t.Errorf("长度 %d 的合法秘钥被改写为 %d 字节", size, len(got))
		}
	}
	short := []byte("short-key")
	got := NormalizeKey(short)
	if len(got) != 32 {
		t.Errorf("非常规长度秘钥规范化后长度 = %d，期望 32", len(got))
	}
	if string(got) == string(short) {
		t.Errorf("规范化结果不应等于原始输入")
	}
	if string(NormalizeKey(short)) != string(got) {
		t.Errorf("规范化结果不稳定，两次结果不一致")
	}
}

// TestEncodeDecodeRoundTrip 验证编解码往返一致：proto -> AES-CBC -> 解码还原
func TestEncodeDecodeRoundTrip(t *testing.T) {
	saveKey := GenerateKey()
	key, err := DecodeKey(saveKey)
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}

	msg := &cspb.Base{Lv: 99, Nickname: "测试玩家", Avatar: "avatar", AvatarFrame: "frame", Exp: 1000}
	wire, err := Encode(msg, key)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	if len(wire) <= aesIVSize {
		t.Fatalf("线格式字节长度 %d 应包含 16 字节 IV", len(wire))
	}

	decoded, err := Decode(wire, key, newTestBase)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if decoded.GetLv() != msg.GetLv() || decoded.GetNickname() != msg.GetNickname() ||
		decoded.GetAvatar() != msg.GetAvatar() || decoded.GetAvatarFrame() != msg.GetAvatarFrame() ||
		decoded.GetExp() != msg.GetExp() {
		t.Errorf("解码结果与原消息不一致：got %v，want %v", decoded, msg)
	}
}

// TestDecodeWithNormalizedKey 验证非常规长度的秘钥经规范化后仍可正常编解码
func TestDecodeWithNormalizedKey(t *testing.T) {
	key := []byte("arbitrary-key-string")
	msg := &cspb.Base{Lv: 7}
	wire, err := Encode(msg, key)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	decoded, err := Decode(wire, key, newTestBase)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if decoded.GetLv() != 7 {
		t.Errorf("解码等级 = %d，期望 7", decoded.GetLv())
	}
}

// TestDecodeWrongKey 验证使用错误秘钥解密会失败
func TestDecodeWrongKey(t *testing.T) {
	keyA, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	keyB, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	wire, err := Encode(&cspb.Base{Lv: 1}, keyA)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	if _, err := Decode(wire, keyB, newTestBase); err == nil {
		t.Fatal("使用错误秘钥解密应失败")
	}
}

// TestDecodeTamperedData 验证篡改密文或长度非法时解码失败
func TestDecodeTamperedData(t *testing.T) {
	key, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	wire, err := Encode(&cspb.Base{Lv: 1}, key)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}

	tampered := make([]byte, len(wire))
	copy(tampered, wire)
	tampered[len(tampered)-1] ^= 0xFF
	if _, err := Decode(tampered, key, newTestBase); err == nil {
		t.Fatal("篡改后的密文应解码失败")
	}
	if _, err := Decode([]byte("tiny"), key, newTestBase); err == nil {
		t.Fatal("长度不足 IV 的线格式字节应解码失败")
	}
}

// TestSealWireOpenWireRoundTrip 验证字节级原语 SealWire/OpenWire 往返一致（明文 -> AES-CBC -> 解密还原）
func TestSealWireOpenWireRoundTrip(t *testing.T) {
	key, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	plain := []byte("plain-wire-bytes-for-gmt")
	wire, err := SealWire(plain, key)
	if err != nil {
		t.Fatalf("SealWire 失败: %v", err)
	}
	if len(wire) <= aesIVSize {
		t.Fatalf("线格式字节长度 %d 应包含 16 字节 IV", len(wire))
	}
	opened, err := OpenWire(wire, key)
	if err != nil {
		t.Fatalf("OpenWire 失败: %v", err)
	}
	if string(opened) != string(plain) {
		t.Errorf("OpenWire 解密结果与原明文不一致：got %q，want %q", opened, plain)
	}
}

// TestSealWireInteropWithEncode 验证 SealWire/OpenWire 与 Encode/Decode 使用同一线格式互通
// 即以 Encode 生成的内容可由 OpenWire 还原为 proto marshaled 明文，反之亦然
func TestSealWireInteropWithEncode(t *testing.T) {
	key, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	msg := &cspb.Base{Lv: 42, Nickname: "互通测试"}

	// Encode 生成的线格式可由 OpenWire 还原为 proto 明文字节
	wire, err := Encode(msg, key)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	plain, err := OpenWire(wire, key)
	if err != nil {
		t.Fatalf("OpenWire 解密 Encode 产物失败: %v", err)
	}
	decoded := &cspb.Base{}
	if err := proto.Unmarshal(plain, decoded); err != nil {
		t.Fatalf("proto Unmarshal OpenWire 明文失败: %v", err)
	}
	if decoded.GetLv() != 42 || decoded.GetNickname() != "互通测试" {
		t.Errorf("还原结果与原消息不一致：got %v", decoded)
	}

	// SealWire 生成的加密线格式可由 Decode 反序列化为原消息
	plainBytes, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto Marshal 失败: %v", err)
	}
	sealed, err := SealWire(plainBytes, key)
	if err != nil {
		t.Fatalf("SealWire 失败: %v", err)
	}
	roundTrip, err := Decode(sealed, key, newTestBase)
	if err != nil {
		t.Fatalf("Decode SealWire 产物失败: %v", err)
	}
	if roundTrip.GetLv() != 42 {
		t.Errorf("解码等级 = %d，期望 42", roundTrip.GetLv())
	}
}

// TestOpenWireWrongKey 验证使用错误秘钥解密线格式字节会失败
func TestOpenWireWrongKey(t *testing.T) {
	keyA, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	keyB, err := DecodeKey(GenerateKey())
	if err != nil {
		t.Fatalf("DecodeKey 失败: %v", err)
	}
	wire, err := SealWire([]byte("secret"), keyA)
	if err != nil {
		t.Fatalf("SealWire 失败: %v", err)
	}
	if _, err := OpenWire(wire, keyB); err == nil {
		t.Fatal("使用错误秘钥解密应失败")
	}
}

// TestComputeSign 验证签名为 64 位 SHA256 小写十六进制且内容相关
func TestComputeSign(t *testing.T) {
	wire := []byte("hello-wire")
	sign := ComputeSign(wire)
	if len(sign) != 64 {
		t.Errorf("签名长度 = %d，期望 64", len(sign))
	}
	if _, err := hex.DecodeString(sign); err != nil {
		t.Errorf("签名不是合法十六进制: %v", err)
	}
	if sign != ComputeSign(wire) {
		t.Errorf("同一内容的签名不稳定")
	}
	if ComputeSign([]byte("other-content")) == sign {
		t.Errorf("不同内容的签名不应相同")
	}
}
