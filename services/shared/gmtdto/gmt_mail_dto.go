package gmtdto

// GmtReward GMT 邮件奖励条目，描述一条道具奖励
type GmtReward struct {
	// ItemID 道具ID
	ItemID string `json:"itemId"`
	// Count 数量
	Count int64 `json:"count"`
}

// GmtSendMailReq GMT 运营发送邮件请求
// 采用系统邮件模板 + Lazy Create 模式下发：仅写入 mail_templates 模板，
// 玩家拉取邮件列表时按需生成玩家邮件（全服邮件对所有玩家生效，定向邮件仅对目标玩家生效）
type GmtSendMailReq struct {
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
	Rewards []GmtReward `json:"rewards,omitempty"`
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
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtSendMailResp GMT 发送邮件响应
type GmtSendMailResp struct {
	// TemplateID 邮件模板业务ID（字符串格式，避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
	// CreateTime 创建时间戳（Unix毫秒）
	CreateTime int64 `json:"createTime"`
}

// GmtListTemplatesReq GMT 查询邮件模板列表请求
// 所有过滤字段均为可选：零值/哨兵值表示不过滤
type GmtListTemplatesReq struct {
	// TemplateID 按模板ID精确过滤（可选，空字符串或"0"表示不过滤；字符串格式避免前端JS精度丢失）
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
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtMailTemplateItem GMT 邮件模板列表项
type GmtMailTemplateItem struct {
	// TemplateID 邮件模板业务ID（字符串格式，避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
	// Title 邮件标题文本
	Title string `json:"title"`
	// TitleType 邮件标题类型：0=纯文本 1=多语言Key
	TitleType int `json:"titleType"`
	// Content 邮件正文文本
	Content string `json:"content"`
	// ContentType 邮件正文类型：0=纯文本 1=多语言Key
	ContentType int `json:"contentType"`
	// Sender 发送者名称
	Sender string `json:"sender"`
	// Rewards 附件奖励列表
	Rewards []GmtReward `json:"rewards,omitempty"`
	// TargetType 发送目标类型：0=全服 1=指定UID 2=UID列表
	TargetType int `json:"targetType"`
	// TargetUIDs 目标玩家ID列表
	TargetUIDs []int64 `json:"targetUIDs,omitempty"`
	// CreateTime 创建时间戳（Unix毫秒）
	CreateTime int64 `json:"createTime"`
	// StartTime 预发布生效开始时间戳（Unix毫秒），0 表示立即生效
	StartTime int64 `json:"startTime"`
	// EndTime 预发布结束时间戳（Unix毫秒），0 表示不限制
	EndTime int64 `json:"endTime"`
	// ExpireTime 邮件相对有效期（毫秒），0 表示永不过期
	ExpireTime int64 `json:"expireTime"`
	// IsRecalled 模板是否已撤回
	IsRecalled bool `json:"isRecalled"`
	// RecallTime 撤回时间戳（Unix毫秒），0 表示未撤回
	RecallTime int64 `json:"recallTime"`
}

// GmtListTemplatesResp GMT 查询邮件模板列表响应
type GmtListTemplatesResp struct {
	// Items 当前页模板列表
	Items []*GmtMailTemplateItem `json:"items"`
	// Total 符合条件的总记录数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页数量
	PageSize int64 `json:"pageSize"`
	// TotalPages 总页数
	TotalPages int64 `json:"totalPages"`
}

// GmtRecallMailReq GMT 撤回邮件模板请求
type GmtRecallMailReq struct {
	// TemplateID 要撤回的邮件模板业务ID（必填，字符串格式避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
	// OperatorID 操作人ID（必填，由 GMT 系统传入）
	OperatorID string `json:"operatorId"`
}

// GmtRecallMailResp GMT 撤回邮件模板响应
type GmtRecallMailResp struct {
	// TemplateID 邮件模板业务ID（字符串格式，避免前端JS精度丢失）
	TemplateID string `json:"templateId"`
	// IsRecalled 模板当前撤回状态（已撤回或撤回成功均为 true）
	IsRecalled bool `json:"isRecalled"`
	// RecallTime 撤回时间戳（Unix毫秒），已撤回后返回实际撤回时间
	RecallTime int64 `json:"recallTime"`
}
