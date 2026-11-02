package model

import "go.mongodb.org/mongo-driver/v2/bson"

// FriendRelation 好友关系文档（user_friend 集合）
// 好友关系采用双向冗余存储：A 与 B 互为好友时同时保存 A→B 与 B→A 两条记录，
// 换取好友列表与好友关系判断的查询性能
type FriendRelation struct {
	// ID MongoDB文档主键，业务透明，不对外暴露
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// UID 当前用户玩家ID
	UID int64 `bson:"uid" json:"uid"`
	// FriendUID 好友玩家ID
	FriendUID int64 `bson:"friendUid" json:"friendUid"`
	// CreatedAt 建立好友关系时间（Unix毫秒）
	CreatedAt int64 `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 好友关系更新时间（Unix毫秒）
	UpdatedAt int64 `bson:"updatedAt" json:"updatedAt"`
}

// FriendApply 好友申请记录（friend_apply 集合），代表单向申请 applyUid → targetUid
// 记录存在即表示有一笔待处理申请，同意/拒绝/撤销后立即删除记录，不保存历史状态
type FriendApply struct {
	// ID MongoDB文档主键，业务透明，不对外暴露
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// ApplyUID 申请人玩家ID
	ApplyUID int64 `bson:"applyUid" json:"applyUid"`
	// TargetUID 被申请人玩家ID
	TargetUID int64 `bson:"targetUid" json:"targetUid"`
	// CreatedAt 申请创建时间（Unix毫秒）
	CreatedAt int64 `bson:"createdAt" json:"createdAt"`
}
