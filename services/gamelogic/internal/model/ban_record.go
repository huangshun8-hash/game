package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// BanRecord 封禁记录（仅记录当前被封禁的账号，解封时从表中删除）
type BanRecord struct {
	// ID MongoDB 文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// PlayerID 玩家唯一标识
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// Reason 封禁原因
	Reason string `bson:"reason" json:"reason"`
	// CreatedAt 封禁时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}
