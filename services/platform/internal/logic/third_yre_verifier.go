package logic

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"time"

	"kemiluo/services/shared/shareddto"
)

// thirdYreDefaultTimeTolerance 三方数据签名默认容忍窗口（毫秒级时间戳，防重放）
const thirdYreDefaultTimeTolerance = 5 * time.Minute

// ThirdYreVerifier 三方数据签名校验抽象接口。
// 通过接口隔离具体签名算法与时间窗策略，便于后续按渠道扩展/替换不同的校验实现，
// 亦方便在单元测试中注入桩实现。
type ThirdYreVerifier interface {
	// Verify 校验三方数据签名。
	// 入参 yre 为待校验的三方数据（调用方需保证非 nil）。
	// 返回 nil 表示校验通过；校验失败返回具体原因错误。
	Verify(yre *shareddto.ThirdYre) error
}

// thirdYreVerifierConfig 三方数据校验器配置，抽象出密钥与时间窗等可配置项。
type thirdYreVerifierConfig struct {
	// secret 三方平台共用签名密钥（key），为空时一律拒绝
	secret string
	// timeTolerance 毫秒级时间戳容忍窗口
	timeTolerance time.Duration
}

// md5ThirdYreVerifier 基于 md5(siteCode + t + key + uid) 的默认校验实现。
type md5ThirdYreVerifier struct {
	cfg thirdYreVerifierConfig
}

// ThirdYreVerifierOption 校验器可选配置项，便于按需覆盖默认行为（如自定义时间窗）。
type ThirdYreVerifierOption func(*thirdYreVerifierConfig)

// WithThirdYreTimeTolerance 自定义时间戳容忍窗口
func WithThirdYreTimeTolerance(d time.Duration) ThirdYreVerifierOption {
	return func(c *thirdYreVerifierConfig) {
		if d > 0 {
			c.timeTolerance = d
		}
	}
}

// NewThirdYreVerifier 创建默认的三方数据签名校验器（md5 算法）。
// secret: 三方平台共用签名密钥（key）；为空时对所有携带 thirdYre 的请求拒绝。
// opts: 可选配置项，未指定时间窗时使用默认 5 分钟。
func NewThirdYreVerifier(secret string, opts ...ThirdYreVerifierOption) ThirdYreVerifier {
	cfg := thirdYreVerifierConfig{
		secret:        secret,
		timeTolerance: thirdYreDefaultTimeTolerance,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &md5ThirdYreVerifier{cfg: cfg}
}

// Verify 校验规则：
//  1. 密钥（secret）已配置；未配置时一律拒绝，避免签名原文可被攻击者自行计算而伪造。
//  2. 毫秒级时间戳 t 落在容忍窗口内（防重放）。
//  3. ck == md5(siteCode + t + key + uid) 的小写十六进制（常量时间比较防时序侧信道）。
func (v *md5ThirdYreVerifier) Verify(yre *shareddto.ThirdYre) error {
	if v.cfg.secret == "" {
		return errThirdYreSecretMissing
	}
	if !v.withinTimeWindow(yre.T) {
		return errThirdYreTimeWindow
	}
	// ck = md5(siteCode + t + key + uid)
	raw := yre.SiteCode + strconv.FormatInt(yre.T, 10) + v.cfg.secret + yre.Uid
	sum := md5.Sum([]byte(raw))
	expected := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(yre.Ck)) != 1 {
		return errThirdYreSignMismatch
	}
	return nil
}

// withinTimeWindow 判断毫秒级时间戳是否落在容忍窗口内
func (v *md5ThirdYreVerifier) withinTimeWindow(t int64) bool {
	now := time.Now().UnixMilli()
	diff := now - t
	if diff < 0 {
		diff = -diff
	}
	return diff <= v.cfg.timeTolerance.Milliseconds()
}
