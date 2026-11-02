// 收发包加解密测试辅助：服务端对 protobuf 请求/响应按玩家专属秘钥做 AES-CBC 加解密，
// 测试客户端需与之保持一致——发送前加密请求体、收到后解密响应体。
// 登录接口不加密（登录时客户端尚未持有秘钥），因此其请求/响应仍走明文，不经过本文件的辅助函数。
package tests

import (
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"kemiluo/pkg/packetx"
)

// tokenPacketKeys 记录 token 到玩家收发包秘钥（原始字节）的映射。
// 登录成功后由 rememberPacketKey 写入，认证请求的收发包辅助函数据此加解密。
// 测试并发登录时可能并发写入，故加读写锁保护。
var (
	tokenPacketKeysMu sync.RWMutex
	tokenPacketKeys   = make(map[string][]byte)
)

// rememberPacketKey 登录成功后记录 token 对应的收发包秘钥（原始字节，登录响应直接下发原始 32 字节）。
// key 为空表示该 token 无收发包秘钥（如管理端），对应请求/响应按明文处理。
func rememberPacketKey(token string, key []byte) {
	if token == "" {
		return
	}
	tokenPacketKeysMu.Lock()
	tokenPacketKeys[token] = key
	tokenPacketKeysMu.Unlock()
}

// packetKeyForToken 返回 token 对应的收发包秘钥（原始字节）；无记录或秘钥为空时返回 nil（按明文处理）。
func packetKeyForToken(token string) []byte {
	tokenPacketKeysMu.RLock()
	key := tokenPacketKeys[token]
	tokenPacketKeysMu.RUnlock()
	if len(key) == 0 {
		return nil
	}
	return key
}

// marshalProtoReq 将 protobuf 请求消息序列化为发送用的线格式字节。
// token 持有收发包秘钥时经 AES-CBC 加密（与服务端算法一致）；否则输出裸 protobuf 明文。
func marshalProtoReq(t *testing.T, token string, msg proto.Message) []byte {
	t.Helper()
	if key := packetKeyForToken(token); key != nil {
		wire, err := packetx.Encode(msg, key)
		if err != nil {
			t.Fatalf("收发包加密请求失败: %v", err)
		}
		return wire
	}
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto 序列化请求失败: %v", err)
	}
	return raw
}

// unmarshalProtoResp 将收到的响应线格式字节反序列化为指定 protobuf 消息。
// token 持有收发包秘钥时先解密再反序列化；否则按裸 protobuf 明文反序列化。
func unmarshalProtoResp(t *testing.T, token string, wire []byte, dst proto.Message) {
	t.Helper()
	plain := wire
	if key := packetKeyForToken(token); key != nil {
		opened, err := packetx.OpenWire(wire, key)
		if err != nil {
			t.Fatalf("收发包解密响应失败: %v", err)
		}
		plain = opened
	}
	if err := proto.Unmarshal(plain, dst); err != nil {
		t.Fatalf("proto 反序列化响应失败: %v", err)
	}
}
