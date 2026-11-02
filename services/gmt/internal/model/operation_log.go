package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// OperationLog 操作日志实体
// 记录所有管理员的敏感写操作行为（登录、增删改等）
type OperationLog struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// UID 操作管理员ID（使用ObjectID字符串形式）
	UID string `bson:"uid" json:"uid"`
	// Username 操作管理员账号
	Username string `bson:"username" json:"username"`
	// Module 操作模块（如：Admin、Auth、Profile）
	Module string `bson:"module" json:"module"`
	// Action 操作动作（如：Login、Create、Update、Delete）
	Action string `bson:"action" json:"action"`
	// Method HTTP请求方法
	Method string `bson:"method" json:"method"`
	// URI 请求路径
	URI string `bson:"uri" json:"uri"`
	// IP 请求来源IP
	IP string `bson:"ip" json:"ip"`
	// Request 请求参数（脱敏后存储，不含密码等敏感字段）
	Request any `bson:"request" json:"request"`
	// Response 响应内容摘要
	Response any `bson:"response" json:"response"`
	// CreatedAt 记录创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}
