package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 管理员账号状态常量
const (
	// AdminStatusNormal 账号正常
	AdminStatusNormal = 1
	// AdminStatusDisabled 账号已禁用
	AdminStatusDisabled = 0
)

// Admin 管理员账号实体
type Admin struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// Username 登录账号（唯一）
	Username string `bson:"username" json:"username"`
	// Nickname 用户昵称
	Nickname string `bson:"nickname" json:"nickname"`
	// Password bcrypt加密密码（不返回给客户端）
	Password string `bson:"password" json:"-"`
	// Status 账号状态：1=正常 0=禁用
	Status int `bson:"status" json:"status"`
	// IsSuper 是否超级管理员
	IsSuper bool `bson:"isSuper" json:"isSuper"`
	// LastLoginIP 最后登录IP
	LastLoginIP string `bson:"lastLoginIp" json:"lastLoginIp"`
	// LastLoginAt 最后登录时间
	LastLoginAt *time.Time `bson:"lastLoginAt" json:"lastLoginAt,omitempty"`
	// CreatedAt 账号创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 账号更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// IsActive 返回账号是否处于正常可用状态
func (a *Admin) IsActive() bool {
	return a.Status == AdminStatusNormal
}
