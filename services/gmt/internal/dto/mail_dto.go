package dto

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/shared/gmtdto"
)

// SendMailReq GMT 发送邮件请求 DTO（接收来自 Web 前端的 JSON Body）
type SendMailReq struct {
	httpx.BaseReq
	// Title 邮件标题（必填）
	Title string `json:"title"`
	// TitleType 邮件标题类型：0=纯文本 1=多语言Key（必填）
	TitleType int `json:"titleType"`
	// Content 邮件正文（必填）
	Content string `json:"content"`
	// ContentType 邮件正文类型：0=纯文本 1=多语言Key（必填）
	ContentType int `json:"contentType"`
	// Sender 发送者名称（选填，空表示默认发送者）
	Sender string `json:"sender"`
	// Rewards 附件奖励列表（选填，为空表示纯通知邮件）
	Rewards []gmtdto.GmtReward `json:"rewards,omitempty"`
	// TargetType 发送目标类型：0=全服 1=指定单个UID 2=指定UID列表（必填）
	TargetType int `json:"targetType"`
	// TargetUIDs 目标玩家ID列表（TargetType=1 时存单个UID，TargetType=2 时存多个，必填）
	TargetUIDs []int64 `json:"targetUIDs,omitempty"`
	// StartTime 预发布生效开始时间戳（Unix毫秒），0 表示立即生效；服务器时间未到则不下发（选填）
	StartTime int64 `json:"startTime"`
	// EndTime 预发布结束时间戳（Unix毫秒），0 表示不限制；服务器时间到达后不再下发（选填）
	EndTime int64 `json:"endTime"`
	// ExpireTime 邮件相对有效期（毫秒），从邮件下发（玩家收到）时刻开始计算，经过该时长过期；0 表示永不过期（选填）
	ExpireTime int64 `json:"expireTime"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtSendMailReq
// operatorID 由 handler 从 JWT Claims 中获取后传入
func (r *SendMailReq) ToGmtDTO(operatorID string) *gmtdto.GmtSendMailReq {
	return &gmtdto.GmtSendMailReq{
		Title:       r.Title,
		TitleType:   r.TitleType,
		Content:     r.Content,
		ContentType: r.ContentType,
		Sender:      r.Sender,
		Rewards:     r.Rewards,
		TargetType:  r.TargetType,
		TargetUIDs:  r.TargetUIDs,
		StartTime:   r.StartTime,
		EndTime:     r.EndTime,
		ExpireTime:  r.ExpireTime,
		OperatorID:  operatorID,
	}
}

// ListTemplatesReq GMT 查询邮件模板列表请求 DTO（接收来自 Web 前端的 JSON Body）
// 所有过滤字段均为可选：零值/哨兵值表示不过滤
type ListTemplatesReq struct {
	httpx.BaseReq
	// TemplateID 按模板ID精确过滤（可选，空字符串表示不过滤；字符串格式避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
	// TargetType 按发送目标类型过滤（可选，nil/省略 表示不过滤；0=全服 1=指定UID 2=UID列表）
	TargetType *int `json:"targetType,omitempty"`
	// Recalled 按撤回状态过滤（可选，nil/省略 表示不过滤；false=未撤回 true=已撤回）
	Recalled *bool `json:"recalled,omitempty"`
	// StartTime 创建时间范围起点，Unix毫秒（可选，0 表示不过滤）
	StartTime int64 `json:"startTime"`
	// EndTime 创建时间范围终点，Unix毫秒（可选，0 表示不过滤）
	EndTime int64 `json:"endTime"`
	// Page 页码，从1开始，默认1
	Page int64 `json:"page"`
	// PageSize 每页数量，默认20，最大200
	PageSize int64 `json:"pageSize"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtListTemplatesReq
// operatorID 由 handler 从 JWT Claims 中获取后传入
func (r *ListTemplatesReq) ToGmtDTO(operatorID string) *gmtdto.GmtListTemplatesReq {
	return &gmtdto.GmtListTemplatesReq{
		TemplateID: r.TemplateID,
		TargetType: r.TargetType,
		Recalled:   r.Recalled,
		StartTime:  r.StartTime,
		EndTime:    r.EndTime,
		Page:       r.Page,
		PageSize:   r.PageSize,
		OperatorID: operatorID,
	}
}

// RecallMailReq GMT 撤回邮件模板请求 DTO（接收来自 Web 前端的 JSON Body）
type RecallMailReq struct {
	httpx.BaseReq
	// TemplateID 要撤回的邮件模板业务ID（必填，字符串格式避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
}

// ToGmtDTO 将请求 DTO 转换为转发给 GameLogic 的 gmtdto.GmtRecallMailReq
// operatorID 由 handler 从 JWT Claims 中获取后传入
func (r *RecallMailReq) ToGmtDTO(operatorID string) *gmtdto.GmtRecallMailReq {
	return &gmtdto.GmtRecallMailReq{
		TemplateID: r.TemplateID,
		OperatorID: operatorID,
	}
}
