package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// SaveHead 存档头信息，每个玩家每个 key 各一条记录。
type SaveHead struct {
	ID               bson.ObjectID `bson:"_id,omitempty" json:"id"`
	PlayerID         int64         `bson:"playerId" json:"playerId"`
	Key              string        `bson:"key" json:"key"`
	CurrentVersion   int64         `bson:"currentVersion" json:"currentVersion"`
	PrevVersion      int64         `bson:"prevVersion" json:"prevVersion"`
	LatestSnapshotID bson.ObjectID `bson:"latestSnapshotId" json:"latestSnapshotId"`
	SnapshotSeq      int64         `bson:"snapshotSeq" json:"snapshotSeq"`
	UpdatedAt        time.Time     `bson:"updatedAt" json:"updatedAt"`
	CreatedAt        time.Time     `bson:"createdAt" json:"createdAt"`
}

// SaveSnapshot 存档快照，保存 gzip 压缩的 protobuf 存档内容。
type SaveSnapshot struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	PlayerID    int64         `bson:"playerId" json:"playerId"`
	Key         string        `bson:"key" json:"key"`
	Version     int64         `bson:"version" json:"version"`
	PrevVersion int64         `bson:"prevVersion" json:"prevVersion"`
	SnapshotSeq int64         `bson:"snapshotSeq" json:"snapshotSeq"`
	Sign        string        `bson:"sign" json:"sign"`
	Content     string        `bson:"content" json:"content"`
	CreatedAt   time.Time     `bson:"createdAt" json:"createdAt"`
}
