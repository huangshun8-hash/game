package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// BanRecordRepo 封禁记录数据访问层
type BanRecordRepo struct {
	mongox.BaseRepo[model.BanRecord]
}

// NewBanRecordRepo 创建封禁记录仓库实例
func NewBanRecordRepo(client *mongox.Client) *BanRecordRepo {
	return &BanRecordRepo{BaseRepo: mongox.NewBaseRepo[model.BanRecord](client, collectionBanRecords)}
}

// InsertRecord 插入一条封禁记录
func (r *BanRecordRepo) InsertRecord(ctx context.Context, record *model.BanRecord) error {
	record.CreatedAt = time.Now()
	id, err := r.Insert(ctx, record)
	if err != nil {
		return err
	}
	record.ID = id
	return nil
}

// BatchInsert 批量插入封禁记录（忽略重复键错误，保证幂等）
func (r *BanRecordRepo) BatchInsert(ctx context.Context, records []*model.BanRecord) error {
	if len(records) == 0 {
		return nil
	}
	now := time.Now()
	docs := make([]any, 0, len(records))
	for _, rec := range records {
		rec.CreatedAt = now
		docs = append(docs, rec)
	}
	_, err := r.Collection().InsertMany(ctx, docs)
	if err != nil && !mongox.IsDuplicateKeyError(err) {
		return err
	}
	return nil
}

// DeleteByPlayerIDs 根据玩家ID列表批量删除封禁记录（解封时调用）
func (r *BanRecordRepo) DeleteByPlayerIDs(ctx context.Context, playerIDs []int64) error {
	if len(playerIDs) == 0 {
		return nil
	}
	_, err := r.DeleteMany(ctx, bson.D{{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}}})
	return err
}

// FindExistingPlayerIDs 查询给定玩家ID中已存在封禁记录的玩家ID集合。
// 用于封禁事务内过滤出真正需要插入的记录，避免事务内命中唯一索引重复键导致事务中止。
func (r *BanRecordRepo) FindExistingPlayerIDs(ctx context.Context, playerIDs []int64) (map[int64]struct{}, error) {
	existing := make(map[int64]struct{}, len(playerIDs))
	if len(playerIDs) == 0 {
		return existing, nil
	}
	opts := options.Find().SetProjection(bson.D{{Key: "playerId", Value: 1}})
	cursor, err := r.Collection().Find(ctx,
		bson.D{{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}}},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc struct {
			PlayerID int64 `bson:"playerId"`
		}
		if err := cursor.Decode(&doc); err == nil {
			existing[doc.PlayerID] = struct{}{}
		}
	}
	return existing, cursor.Err()
}

// DeleteByPlayerID 根据玩家ID删除封禁记录（解封时调用）
func (r *BanRecordRepo) DeleteByPlayerID(ctx context.Context, playerID int64) error {
	_, err := r.DeleteMany(ctx, bson.D{{Key: "playerId", Value: playerID}})
	return err
}

// QueryRecords 分页查询封禁记录
// filter: 查询条件（由调用方构建）
func (r *BanRecordRepo) QueryRecords(ctx context.Context, filter bson.D, page, pageSize int64) (*mongox.PageResult[model.BanRecord], error) {
	return r.Paginate(ctx, filter, mongox.PageQuery{
		Page:     page,
		PageSize: pageSize,
		Sort:     bson.D{{Key: "createdAt", Value: -1}},
	})
}
