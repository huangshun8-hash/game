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

// SaveHeadRepo 存档头数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力。
type SaveHeadRepo struct {
	mongox.BaseRepo[model.SaveHead]
}

// NewSaveHeadRepo 创建存档头仓库实例。
func NewSaveHeadRepo(client *mongox.Client) *SaveHeadRepo {
	return &SaveHeadRepo{BaseRepo: mongox.NewBaseRepo[model.SaveHead](client, collectionSaveHeads)}
}

// FindByKey 根据玩家ID和 key 查找存档头。
func (r *SaveHeadRepo) FindByKey(ctx context.Context, playerID int64, key string) (*model.SaveHead, error) {
	return r.FindOne(ctx, bson.D{{Key: "playerId", Value: playerID}, {Key: "key", Value: key}})
}

// FindOrNil 查找存档头，不存在时返回 (nil, nil)。
func (r *SaveHeadRepo) FindOrNil(ctx context.Context, playerID int64, key string) (*model.SaveHead, error) {
	head, err := r.FindByKey(ctx, playerID, key)
	if errors.Is(err, mongox.ErrNotFound) {
		return nil, nil
	}
	return head, err
}

// FindManyByPlayerIDs 批量查询多个玩家同一 key 的存档头。
func (r *SaveHeadRepo) FindManyByPlayerIDs(ctx context.Context, playerIDs []int64, key string) ([]model.SaveHead, error) {
	filter := bson.D{{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}}, {Key: "key", Value: key}}
	return r.FindMany(ctx, filter)
}

// FindByKeys 批量查询指定玩家的多个 key 对应的存档头。
func (r *SaveHeadRepo) FindByKeys(ctx context.Context, playerID int64, keys []string) ([]model.SaveHead, error) {
	filter := bson.D{{Key: "playerId", Value: playerID}, {Key: "key", Value: bson.D{{Key: "$in", Value: keys}}}}
	return r.FindMany(ctx, filter)
}

// FindAllByPlayer 查询指定玩家的所有存档头。
func (r *SaveHeadRepo) FindAllByPlayer(ctx context.Context, playerID int64) ([]model.SaveHead, error) {
	return r.FindMany(ctx, bson.D{{Key: "playerId", Value: playerID}})
}

// UpsertHead 首次上传时原子创建存档头，防止并发重复创建。
func (r *SaveHeadRepo) UpsertHead(ctx context.Context, head *model.SaveHead) (isCreated bool, err error) {
	now := time.Now()
	filter := bson.D{{Key: "playerId", Value: head.PlayerID}, {Key: "key", Value: head.Key}}
	update := bson.D{{Key: "$setOnInsert", Value: bson.D{
		{Key: "playerId", Value: head.PlayerID},
		{Key: "key", Value: head.Key},
		{Key: "currentVersion", Value: head.CurrentVersion},
		{Key: "prevVersion", Value: int64(0)},
		{Key: "latestSnapshotId", Value: bson.ObjectID{}},
		{Key: "snapshotSeq", Value: int64(1)},
		{Key: "updatedAt", Value: now},
		{Key: "createdAt", Value: now},
	}}}
	res, err := r.Collection().UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return false, err
	}
	if res.UpsertedID == nil {
		return false, nil
	}
	if id, ok := res.UpsertedID.(bson.ObjectID); ok {
		head.ID = id
	}
	head.SnapshotSeq = 1
	head.CreatedAt = now
	head.UpdatedAt = now
	return true, nil
}

// FixSnapshotID 在快照插入后回填快照引用、版本与时间。
func (r *SaveHeadRepo) FixSnapshotID(ctx context.Context, playerID int64, key string, newVersion int64, snapshotID bson.ObjectID, newSeq int64, prevVersion int64) error {
	filter := bson.D{{Key: "playerId", Value: playerID}, {Key: "key", Value: key}, {Key: "snapshotSeq", Value: newSeq}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "currentVersion", Value: newVersion},
		{Key: "prevVersion", Value: prevVersion},
		{Key: "latestSnapshotId", Value: snapshotID},
		{Key: "updatedAt", Value: time.Now()},
	}}}
	res, err := r.Collection().UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// CASUpdateSeqOnly 使用乐观锁原子递增 snapshotSeq。
func (r *SaveHeadRepo) CASUpdateSeqOnly(ctx context.Context, playerID int64, key string, baseVersion int64) (updated int64, newSeq int64, err error) {
	filter := bson.D{{Key: "playerId", Value: playerID}, {Key: "key", Value: key}, {Key: "currentVersion", Value: baseVersion}}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "snapshotSeq", Value: int64(1)}}}, {Key: "$set", Value: bson.D{{Key: "updatedAt", Value: time.Now()}}}}
	var result model.SaveHead
	findErr := r.Collection().FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result)
	if findErr != nil {
		if isNoDocuments(findErr) {
			return 0, 0, nil
		}
		return 0, 0, findErr
	}
	return 1, result.SnapshotSeq, nil
}

// isNoDocuments 判断查询未命中错误。
func isNoDocuments(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments)
}

// ForceUpdateSeqOnly 强制原子递增 snapshotSeq，不校验当前版本。
func (r *SaveHeadRepo) ForceUpdateSeqOnly(ctx context.Context, playerID int64, key string) (newSeq int64, prevVersion int64, err error) {
	filter := bson.D{{Key: "playerId", Value: playerID}, {Key: "key", Value: key}}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "snapshotSeq", Value: int64(1)}}}, {Key: "$set", Value: bson.D{{Key: "updatedAt", Value: time.Now()}}}}
	var result model.SaveHead
	if findErr := r.Collection().FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&result); findErr != nil {
		if isNoDocuments(findErr) {
			return 0, 0, mongox.ErrNotFound
		}
		return 0, 0, findErr
	}
	return result.SnapshotSeq, result.CurrentVersion, nil
}
