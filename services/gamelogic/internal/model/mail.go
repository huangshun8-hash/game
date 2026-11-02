package model

import "go.mongodb.org/mongo-driver/v2/bson"

// 邮件状态位标记常量（int32，与 PlayerMail.Status 类型一致，避免调用处手动转换）
const (
	// MailRead 已读状态位
	MailRead int32 = 1 << 0
	// MailRewarded 附件已领取状态位
	MailRewarded int32 = 1 << 1
)

// 邮件发送目标类型
const (
	// MailTargetAll 全服邮件
	MailTargetAll = 0
	// MailTargetUID 指定单个UID
	MailTargetUID = 1
	// MailTargetUIDs 指定UID列表
	MailTargetUIDs = 2
)

// 文本内容类型
const (
	// TextTypeNone 纯文本：直接展示原文本
	TextTypeNone int32 = 0
	// TextTypeLc 多语言Key：由客户端根据当前语言环境自行解析展示
	TextTypeLc int32 = 1
)

// 每位玩家最多保存的邮件数量上限
const MailBoxMaxSize = 100

// MaxMailExpireMs 邮件相对有效期上限（2 个月，按 60 天计，毫秒）：
// 防止极端值导致创建时间 + 相对有效期换算时 int64 溢出
const MaxMailExpireMs int64 = 60 * 24 * 60 * 60 * 1000

// TextContent 支持多语言的文本内容，用于邮件标题/正文等可本地化字段
type TextContent struct {
	// Type 文本内容类型：0=纯文本 1=多语言Key
	Type int32 `bson:"type" json:"type"`
	// Text 纯文本内容（None类型）或多语言Key（Lc类型）
	Text string `bson:"text" json:"text"`
}

// Reward 统一奖励结构，描述一条道具奖励
type Reward struct {
	// ItemID 道具ID
	ItemID string `bson:"itemId" json:"itemId"`
	// Count 数量
	Count int64 `bson:"count" json:"count"`
}

// MailTemplate 系统邮件模板
// GM 创建后保存一条模板，采用 Lazy Create 模式按需生成玩家邮件
type MailTemplate struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// TemplateID 模板业务ID（自增或雪花），用于与 PlayerMail 关联
	TemplateID int64 `bson:"templateId" json:"templateId"`
	// Title 邮件标题（含类型）
	Title TextContent `bson:"title" json:"title"`
	// Content 邮件正文（含类型）
	Content TextContent `bson:"content" json:"content"`
	// Sender 发送者名称
	Sender string `bson:"sender" json:"sender"`
	// Rewards 附件奖励列表
	Rewards []Reward `bson:"rewards" json:"rewards"`
	// TargetType 发送目标类型：0=全服 1=指定UID 2=UID列表
	TargetType int `bson:"targetType" json:"targetType"`
	// TargetUIDs 目标玩家ID列表（TargetType=1 时存单个UID，TargetType=2 时存多个；
	// 下发判定按 playerId 是否在该列表内进行匹配）
	TargetUIDs []int64 `bson:"targetUIDs,omitempty" json:"targetUIDs,omitempty"`
	// CreateTime 创建时间戳（Unix毫秒）
	CreateTime int64 `bson:"createTime" json:"createTime"`
	// StartTime 预发布生效开始时间戳（Unix毫秒），0 表示立即生效；服务器时间未到则不下发
	StartTime int64 `bson:"startTime" json:"startTime"`
	// EndTime 预发布结束时间戳（Unix毫秒），0 表示不限制；服务器时间到达后不再下发
	EndTime int64 `bson:"endTime" json:"endTime"`
	// ExpireTime 邮件相对有效期（毫秒），从邮件下发（玩家收到）时刻开始计算，经过该时长过期；0 表示永不过期
	ExpireTime int64 `bson:"expireTime" json:"expireTime"`
	// IsRecalled 模板是否已撤回：撤回后不再向未收到邮件的玩家下发（已下发的玩家邮件不受影响）
	IsRecalled bool `bson:"isRecalled" json:"isRecalled"`
	// RecallTime 撤回时间戳（Unix毫秒），0 表示未撤回
	RecallTime int64 `bson:"recallTime" json:"recallTime"`
}

// IsExpired 判断模板预发布窗口是否已结束（endTime=0 表示不限制，永不结束）
// 与模板下发过滤保持一致：now 到达 endTime 时刻（now >= endTime）即视为窗口结束
func (t *MailTemplate) IsExpired(nowMs int64) bool {
	return t.EndTime > 0 && t.EndTime <= nowMs
}

// PlayerMail 玩家邮件文档（每玩家每封邮件一条记录）
// TemplateID != 0 时表示来源于系统邮件模板，TemplateID == 0 表示独立个人邮件
type PlayerMail struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// MailID 邮件业务ID（自增或雪花），客户端唯一标识
	MailID int64 `bson:"mailId" json:"mailId"`
	// PlayerID 所属玩家ID
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// TemplateID 来源模板ID，为 0 表示独立个人邮件
	TemplateID int64 `bson:"templateId" json:"templateId"`
	// Title 邮件标题（含类型）
	Title TextContent `bson:"title" json:"title"`
	// Content 邮件正文（含类型）
	Content TextContent `bson:"content" json:"content"`
	// Sender 发送者名称
	Sender string `bson:"sender" json:"sender"`
	// Rewards 附件奖励列表，为空表示无附件
	Rewards []Reward `bson:"rewards,omitempty" json:"rewards,omitempty"`
	// Status 状态位标记：bit0=已读 bit1=附件已领取
	Status int32 `bson:"status" json:"status"`
	// CreateTime 创建时间戳（Unix毫秒）
	CreateTime int64 `bson:"createTime" json:"createTime"`
	// ExpireTime 过期时间戳（Unix毫秒），0 表示永不过期；
	// 系统模板邮件必有过期时间：由模板相对有效期换算而来（= 收到邮件时刻 + 相对有效期），
	// 模板未配置相对有效期（0）时默认按 7 天计算
	ExpireTime int64 `bson:"expireTime" json:"expireTime"`
}

// IsRead 判断邮件是否已读
func (m *PlayerMail) IsRead() bool {
	return m.Status&MailRead != 0
}

// IsRewarded 判断附件是否已领取
func (m *PlayerMail) IsRewarded() bool {
	return m.Status&MailRewarded != 0
}

// IsExpired 判断邮件是否已过期
func (m *PlayerMail) IsExpired(nowMs int64) bool {
	return m.ExpireTime > 0 && m.ExpireTime < nowMs
}

// HasReward 判断邮件是否带有附件
func (m *PlayerMail) HasReward() bool {
	return len(m.Rewards) > 0
}

// MarkRead 标记已读
func (m *PlayerMail) MarkRead() {
	m.Status |= MailRead
}

// MarkRewarded 标记附件已领取，同时标记已读
func (m *PlayerMail) MarkRewarded() {
	m.Status |= MailRewarded | MailRead
}
