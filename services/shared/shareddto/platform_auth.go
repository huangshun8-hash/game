// Package shareddto 定义跨服务共享的请求/响应 DTO。
// 这些类型被多个服务（如 gamelogic、platform）共同引用，
// 统一在此声明，避免重复定义导致字段或 JSON tag 漂移。
package shareddto

import "kemiluo/services/shared/sharedmodel"

// PlatformAuthReq 平台认证请求的业务字段
// gamelogic 调用 platform 内部接口时发送此结构体（通过 JSON 序列化传输）；
// platform 侧通过 dto.AuthReq 嵌入此类型进行绑定，字段和 JSON tag 必须完全一致。
type PlatformAuthReq struct {
	// LoginType 业务渠道标签（产品概念）：guest / channel1 / channel2 ...
	// platform 侧根据此字段内部映射到对应的 token 验证逻辑
	LoginType string `json:"loginType"`
	// Platform 客户端平台：ios / android
	Platform string `json:"platform"`
	// OpenID 平台开放 ID（游客登录时为客户端生成的 UUID；三方登录时为游客 UUID，可为空）
	// 注意：游客场景下此字段对应 gamelogic 侧 LoginReq.UUID 字段
	OpenID string `json:"openId,omitempty"`
	// AuthToken 三方登录凭证（可选），部分渠道无需服务端验证
	AuthToken string `json:"authToken,omitempty"`
	// Account 第三方账号（可选），免验证渠道用于身份标识，写入玩家档案留存
	Account string `json:"account,omitempty"`
	// ClientInfo 客户端设备信息（首次注册时传入，用于账号维度的注册信息留存）
	ClientInfo *sharedmodel.ClientInfo `json:"clientInfo,omitempty"`
	// ThirdYre 三方数据（可选），三方渠道登录时透传，platform 侧据此进行签名校验
	ThirdYre *ThirdYre `json:"thirdYre,omitempty"`
}

// ThirdYre 三方数据，随三方渠道登录透传，platform 侧用于签名校验
// 校验规则：md5(siteCode + t + key + uid) == ck，key 为三方平台共用签名密钥
type ThirdYre struct {
	// T 时间（Unix 毫秒时间戳）
	T int64 `json:"t,omitempty"`
	// SiteCode 站点编码
	SiteCode string `json:"siteCode,omitempty"`
	// Ck 校验码：md5(siteCode + t + key + uid)
	Ck string `json:"ck,omitempty"`
	// Uid 三方用户 ID
	Uid string `json:"uid,omitempty"`
}

// PlatformAuthResp 平台认证响应
// platform 内部接口返回此结构体；gamelogic 解析后用于玩家身份绑定和创建。
type PlatformAuthResp struct {
	// PlayerID 玩家唯一标识（由平台服务自增生成）
	PlayerID int64 `json:"playerId"`
	// OpenID 平台开放 ID
	OpenID string `json:"openId"`
	// LoginType 登录类型
	LoginType string `json:"loginType"`
}

// PlatformErrCodeInternal 平台服务内部错误的业务错误码（对应 platform/internal/model.ErrCodeInternal）。
// 平台所有响应（含 /internal 内部接口）都由 httpx.DefaultErrorHandler 统一以 HTTP 200 返回，
// 业务结果只体现在响应体的 code 字段，因此调用方无法靠 HTTP 状态码识别平台内部错误，
// 必须比对该业务码。此处以常量形式固化跨服务契约，避免调用方散落魔法数字。
const PlatformErrCodeInternal = 1500

// PlatformDeactivateReq 平台账号注销（作废登录身份）请求。
// gamelogic 收到客户端注销请求后，携带内部签名调用 platform 内部接口 /internal/account/deactivate，
// 由 platform 释放该账号的登录身份（openId 追加 del_ 前缀作废），原 Account 永久保留但不可再登录。
type PlatformDeactivateReq struct {
	// PlayerID 待注销账号的玩家唯一标识（取自 gamelogic 认证上下文，禁止客户端伪造）
	PlayerID int64 `json:"playerId"`
}

// PlatformDeactivateResp 平台账号注销响应。
type PlatformDeactivateResp struct {
	// PlayerID 已注销账号的玩家唯一标识
	PlayerID int64 `json:"playerId"`
	// DeactivatedOpenID 注销前的原始 openId（作废前的登录身份），供 gamelogic 记录审计
	DeactivatedOpenID string `json:"deactivatedOpenId"`
}
