package repo

import (
	"context"
	"errors"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	// maxSnapshotsPerPlayer 每个玩家保留的最大快照数量
	maxSnapshotsPerPlayer = 5
)

// SaveSnapshotRepo 存档快照数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type SaveSnapshotRepo struct {
	mongox.BaseRepo[model.SaveSnapshot]
}

// NewSaveSnapshotRepo 创建存档快照仓库实例
func NewSaveSnapshotRepo(client *mongox.Client) *SaveSnapshotRepo {
	return &SaveSnapshotRepo{BaseRepo: mongox.NewBaseRepo[model.SaveSnapshot](client, collectionSaveSnapshots)}
}

// InsertSnapshot 插入新的存档快照，自动填充创建时间
func (r *SaveSnapshotRepo) InsertSnapshot(ctx context.Context, snap *model.SaveSnapshot) error {
	snap.CreatedAt = time.Now()
	id, err := r.Insert(ctx, snap)
	if err != nil {
		return err
	}
	snap.ID = id
	return nil
}

// FindBySnapshotID 根据快照ID查找快照（用于下载存档）
func (r *SaveSnapshotRepo) FindBySnapshotID(ctx context.Context, snapshotID bson.ObjectID) (*model.SaveSnapshot, error) {
	return r.FindByID(ctx, snapshotID)
}

// FindBySnapshotIDs 批量根据快照ID列表查找快照
// 返回实际存在的记录（顺序不保证），调用方需自行按 ID 建立映射
func (r *SaveSnapshotRepo) FindBySnapshotIDs(ctx context.Context, snapshotIDs []bson.ObjectID) ([]model.SaveSnapshot, error) {
	filter := bson.D{
		{Key: "_id", Value: bson.D{{Key: "$in", Value: snapshotIDs}}},
	}
	return r.FindMany(ctx, filter)
}

// FindLatestByKey 根据玩家ID和 key 查找最新快照（snapshotSeq 最大）
// 不存在时返回 mongox.ErrNotFound
func (r *SaveSnapshotRepo) FindLatestByKey(ctx context.Context, playerID int64, key string) (*model.SaveSnapshot, error) {
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "key", Value: key},
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "snapshotSeq", Value: -1}})
	var snap model.SaveSnapshot
	err := r.Collection().FindOne(ctx, filter, opts).Decode(&snap)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, mongox.ErrNotFound
		}
		return nil, err
	}
	return &snap, nil
}

// PruneOldSnapshots 删除超出保留上限的旧快照
// 利用 SaveHead.SnapshotSeq 精准定位需要删除的范围：
// 删除条件：snapshotSeq < currentSeq - maxSnapshotsPerPlayer + 1
// 即保留序号最大的 maxSnapshotsPerPlayer 条，直接走索引无需排序或加载文档
// 返回删除的条数
func (r *SaveSnapshotRepo) PruneOldSnapshots(ctx context.Context, playerID int64, key string, currentSeq int64) (int64, error) {
	// 计算保留下限序号，低于此序号的快照均可删除
	keepFrom := currentSeq - maxSnapshotsPerPlayer + 1
	if keepFrom <= 0 {
		// 序号尚未超出保留上限，无需清理
		return 0, nil
	}
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "key", Value: key},
		{Key: "snapshotSeq", Value: bson.D{{Key: "$lt", Value: keepFrom}}},
	}
	return r.DeleteMany(ctx, filter)
}
