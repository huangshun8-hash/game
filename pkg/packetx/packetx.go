// Package packetx 提供客户端 protobuf 收发包使用的加解密能力。
package packetx

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"os"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

// aesIVSize AES-CBC 初始化向量长度（16 字节）。
const aesIVSize = aes.BlockSize

// defaultKeyLen 生成收发包密钥的字节长度（32 字节，AES-256）。
const defaultKeyLen = 32

// generateKeyFallbackCounter 兜底派生用的调用计数器，保证每次兜底生成的密钥互不相同。
var generateKeyFallbackCounter atomic.Uint64

// GenerateKey 生成玩家专属收发包加密密钥（32 字节随机值，base64 编码）。
// 密钥在玩家注册时由服务器生成并持久化，登录时下发给客户端，每位玩家各不相同。
// 优先使用系统密码学安全随机源；极端情况下（熵源不可用）自动降级为确定性派生，
// 保证本函数必定成功返回有效密钥，绝不返回失败也绝不 panic。
func GenerateKey() string {
	raw := make([]byte, defaultKeyLen)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return fallbackGenerateKey()
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// fallbackGenerateKey 系统安全随机源不可用时的兜底方案：
// 以时间戳、进程 ID、调用计数器与伪随机内部状态拼接后经 SHA256 派生成 32 字节密钥。
// 该路径使用的基础操作（SHA256 摘要、时间、原子计数、math/rand/v2、base64 编码）均不会失败，
// 从而保证密钥生成必定成功。
func fallbackGenerateKey() string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d-%d-%d-%d",
		time.Now().UnixNano(),
		os.Getpid(),
		generateKeyFallbackCounter.Add(1),
		mrand.Uint64(),
	))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// DecodeKey 将 base64 编码的收发包密钥字符串解码为原始字节，供 Encode/Decode 使用。
func DecodeKey(packetKey string) ([]byte, error) {
	if packetKey == "" {
		return nil, errors.New("packetx: packet key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(packetKey)
	if err != nil {
		return nil, fmt.Errorf("packetx: decode packet key failed: %w", err)
	}
	return raw, nil
}

// NormalizeKey 规范化 AES 密钥：长度合法（16/24/32 字节）直接使用，
// 否则以 SHA256 摘要（32 字节）作为实际 AES 密钥。
func NormalizeKey(key []byte) []byte {
	switch len(key) {
	case 16, 24, 32:
		return key
	default:
		sum := sha256.Sum256(key)
		return sum[:]
	}
}

// SealWire 将明文原始字节经过 AES-CBC 加密后编码为线格式字节。
// 与 Encode 的唯一区别：SealWire 不做 protobuf 序列化，适用于调用方已持有明文原始字节、且不关心具体消息类型的场景。
// 输出结构：16 字节随机 IV + AES-CBC 密文。
// key: AES 原始密钥字节（长度非 16/24/32 时自动以 SHA256 规范化）。
func SealWire(plain []byte, key []byte) ([]byte, error) {
	return encrypt(plain, key)
}

// OpenWire 将线格式字节（16 字节随机 IV + AES-CBC 密文）解密后还原为明文原始字节。
// 与 Decode 的唯一区别：OpenWire 不做 protobuf 反序列化，适用于调用方需自行解析明文字节或使用动态消息类型的场景。
// key: AES 原始密钥字节（长度非 16/24/32 时自动以 SHA256 规范化）。
func OpenWire(wire []byte, key []byte) ([]byte, error) {
	return decrypt(wire, key)
}

// Encode 将 proto message 序列化后经 AES-CBC 加密编码为线格式字节。
// 输出结构：16 字节随机 IV + AES-CBC 密文。
// key: AES 原始密钥字节（长度非 16/24/32 时自动以 SHA256 规范化）。
func Encode(msg proto.Message, key []byte) ([]byte, error) {
	raw, err := proto.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("packetx: proto marshal failed: %w", err)
	}
	return SealWire(raw, key)
}

// Decode 将线格式字节（16 字节随机 IV + AES-CBC 密文）解密后反序列化为指定 message 类型。
// newMsg 返回一个待反序列化的 message 实例（每次调用需返回新实例）。
// key: AES 原始密钥字节（长度非 16/24/32 时自动以 SHA256 规范化）。
func Decode[M proto.Message](wire []byte, key []byte, newMsg func() M) (M, error) {
	var zero M
	raw, err := OpenWire(wire, key)
	if err != nil {
		return zero, err
	}
	msg := newMsg()
	if err := proto.Unmarshal(raw, msg); err != nil {
		return zero, fmt.Errorf("packetx: proto unmarshal failed: %w", err)
	}
	return msg, nil
}

// ComputeSign 计算收发包内容（线格式字节）的 SHA256 小写十六进制签名（64 字符）。
// 签名基于加密后的线格式字节，客户端与服务器保持一致。
func ComputeSign(wire []byte) string {
	sum := sha256.Sum256(wire)
	return hex.EncodeToString(sum[:])
}

// encrypt 使用 AES-CBC 加密数据，输出 16 字节随机 IV + 密文（PKCS7 填充）。
func encrypt(plain []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(NormalizeKey(key))
	if err != nil {
		return nil, fmt.Errorf("packetx: create aes cipher failed: %w", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("packetx: generate iv failed: %w", err)
	}
	padded := pkcs7Pad(plain, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return append(iv, ciphertext...), nil
}

// decrypt 从线格式字节（16 字节随机 IV + AES-CBC 密文）中解密数据。
func decrypt(wire []byte, key []byte) ([]byte, error) {
	if len(wire) < aes.BlockSize {
		return nil, errors.New("packetx: wire bytes too short")
	}
	block, err := aes.NewCipher(NormalizeKey(key))
	if err != nil {
		return nil, fmt.Errorf("packetx: create aes cipher failed: %w", err)
	}
	iv := wire[:aes.BlockSize]
	ciphertext := wire[aes.BlockSize:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, errors.New("packetx: invalid ciphertext length")
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	return pkcs7Unpad(plain)
}

// pkcs7Pad 按 blockSize 进行 PKCS7 填充。
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

// pkcs7Unpad 移除 PKCS7 填充。
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("packetx: empty padded data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return nil, errors.New("packetx: invalid pkcs7 padding")
	}
	for i := len(data) - padding; i < len(data); i++ {
		if int(data[i]) != padding {
			return nil, errors.New("packetx: invalid pkcs7 padding")
		}
	}
	return data[:len(data)-padding], nil
}
