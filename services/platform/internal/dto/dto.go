package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/shared/shareddto"
)

// AuthReq 账号验证请求 DTO（从 gamelogic 转发）
// 嵌入 shareddto.PlatformAuthReq 保证与 gamelogic 侧发送的字段及 JSON tag 严格一致，
// 同时通过 httpx.BaseReq 提供请求追踪等框架能力。
type AuthReq struct {
	httpx.BaseReq
	shareddto.PlatformAuthReq
}

// AuthResp 账号验证响应 DTO（返回给 gamelogic）
// type alias 确保 JSON tag 与 shareddto.PlatformAuthResp 始终保持一致。
type AuthResp = shareddto.PlatformAuthResp

// DeactivateReq 账号注销请求 DTO（从 gamelogic 转发）。
// 嵌入 shareddto.PlatformDeactivateReq 保证与 gamelogic 侧发送的字段及 JSON tag 严格一致，
// 同时通过 httpx.BaseReq 提供请求追踪等框架能力。
type DeactivateReq struct {
	httpx.BaseReq
	shareddto.PlatformDeactivateReq
}

// DeactivateResp 账号注销响应 DTO（返回给 gamelogic）
// type alias 确保 JSON tag 与 shareddto.PlatformDeactivateResp 始终保持一致。
type DeactivateResp = shareddto.PlatformDeactivateResp

// RechargeReq 三方充值回调请求 DTO（游戏方调用平台充值回调接口）
// 请求方式支持 GET（query）与 POST（application/x-www-form-urlencoded），字段与接口文档一一对应。
// 注意：请求中的 money 参数单位为元（可能带小数，如 1.99），handler 层已按四舍五入转为分后填充 Money 字段；
// 因此本结构体的 Money 单位为分，与订单金额（分）直接比较；签名校验在 handler 层使用原始字符串完成。
type RechargeReq struct {
	// Agent 运营商标识
	Agent string
	// Server 所属服务器代码
	Server string
	// Passport 玩家唯一账号
	Passport string
	// Money 充值金额（分，由请求中的元金额四舍五入转换而来）
	Money int64
	// Order 充值订单号（唯一）
	Order string
	// Time 平台服务器 Linux 时间戳（毫秒）
	Time int64
	// Param 透传参数（UTF-8 编码；手机端 SDK 充值原样携带平台订单ID；网页端平台储值时为 0）
	Param string
	// GoodID 渠道商品ID（goodid，字符串 SKU，必填）；与订单固化的渠道内购ID 严格校验
	GoodID string
}
