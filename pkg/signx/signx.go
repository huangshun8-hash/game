package signx

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"time"
)

// DefaultTolerance 默认签名容忍窗口（5分钟）
const DefaultTolerance = 5 * time.Minute

// Sign 对 timestamp + nonce + body 使用 HMAC-SHA256 签名（密钥为 secret），返回 hex 字符串。
// 签名内容：HMAC-SHA256(secret, timestamp + "." + nonce + "." + body)。
// 引入 nonce 是为了配合服务端去重，抵御容忍窗口内的请求重放。
//
// 参数：
//   - secret:    共享密钥（HMAC 密钥）
//   - timestamp: Unix 时间戳（毫秒）字符串
//   - nonce:     一次性随机串（建议 UUID），由调用方生成
//   - body:      原始请求体
//
// 返回：hex 编码的签名字符串
func Sign(secret []byte, timestamp, nonce string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(nonce))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify 校验签名是否有效，并检查 timestamp 是否在容忍窗口内（默认 5 分钟）。
// 注意：本函数只做密码学校验与时效校验，nonce 的重放去重需由调用方借助 Redis 等存储完成。
//
// 参数：
//   - secret:    共享密钥
//   - timestamp: 请求头中的 Unix 时间戳（毫秒）
//   - nonce:     请求头中的一次性随机串
//   - signature: 请求头中的签名值（hex）
//   - body:      原始请求体
//   - tolerance: 时间容忍窗口，为 0 则使用 DefaultTolerance
//
// 返回：签名有效且在时效窗口内返回 true，否则 false
func Verify(secret []byte, timestamp, nonce, signature string, body []byte, tolerance time.Duration) bool {
	if tolerance == 0 {
		tolerance = DefaultTolerance
	}

	// 解析时间戳
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	// 检查时间戳是否在容忍窗口内
	reqTime := time.UnixMilli(ts)
	diff := time.Since(reqTime)
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		return false
	}

	// 计算期望签名并做常量时间比较，防止时序侧信道
	expected := Sign(secret, timestamp, nonce, body)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}
