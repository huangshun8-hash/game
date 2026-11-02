package repo

import (
	"context"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// FriendApplyRepo 好友申请数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type FriendApplyRepo struct {
	mongox.BaseRepo[model.FriendApply]
}

// NewFriendApplyRepo 创建好友申请仓库实例
func NewFriendApplyRepo(client *mongox.Client) *FriendApplyRepo {
	return &FriendApplyRepo{BaseRepo: mongox.NewBaseRepo[model.FriendApply](client, collectionFriendApply)}
}

// InsertPending 新建一条申请记录（applyUid → targetUid）
// 记录存在即表示有一笔待处理申请；(applyUid, targetUid) 唯一索引保证同一方向最多一条记录，
// 并发插入由唯一索引兜底，唯一冲突转换为 ErrFriendApplyAlreadyPending
func (r *FriendApplyRepo) InsertPending(ctx context.Context, applyUID, targetUID, now int64) error {
	_, err := r.Insert(ctx, &model.FriendApply{
		ApplyUID:  applyUID,
		TargetUID: targetUID,
		CreatedAt: now,
	})
	if err != nil {
		if mongox.IsDuplicateKeyError(err) {
			return model.ErrFriendApplyAlreadyPending
		}
		return err
	}
	return nil
}

// ExistsPending 判断指定方向（applyUid → targetUid）是否存在申请（记录存在即待处理）
func (r *FriendApplyRepo) ExistsPending(ctx context.Context, applyUID, targetUID int64) (bool, error) {
	return r.Exists(ctx, bson.D{
		{Key: "applyUid", Value: applyUID},
		{Key: "targetUid", Value: targetUID},
	})
}

// DeleteByPair 删除一条申请记录，返回是否命中（记录不存在返回 false）
// 同意/拒绝/撤销均为删除记录，删除未命中即表示申请不存在或已被处理
func (r *FriendApplyRepo) DeleteByPair(ctx context.Context, applyUID, targetUID int64) (bool, error) {
	deleted, err := r.DeleteOne(ctx, bson.D{
		{Key: "applyUid", Value: applyUID},
		{Key: "targetUid", Value: targetUID},
	})
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

// ListByTargetUid 位置分页查询收到的申请（targetUid, createdAt desc, _id desc）
func (r *FriendApplyRepo) ListByTargetUid(ctx context.Context, targetUID, skip, limit int64) ([]model.FriendApply, bool, error) {
	return r.listPage(ctx, "targetUid", targetUID, skip, limit)
}

// ListByApplyUid 位置分页查询发出的申请（applyUid, createdAt desc, _id desc）
func (r *FriendApplyRepo) ListByApplyUid(ctx context.Context, applyUID, skip, limit int64) ([]model.FriendApply, bool, error) {
	return r.listPage(ctx, "applyUid", applyUID, skip, limit)
}

// listPage 通用位置分页实现（createdAt desc, _id desc）
// createdAt 为申请创建时间（仅插入时设置，处理后记录即删除，天然不可变），作为排序键保证翻页稳定；
// skip 为跳过的记录数（从 0 开始），limit 为单页数量，多取一条判断是否还有更多
func (r *FriendApplyRepo) listPage(ctx context.Context, key string, uid, skip, limit int64) ([]model.FriendApply, bool, error) {
	filter := bson.D{{Key: key, Value: uid}}
	results, err := r.FindMany(ctx, filter,
		options.Find().SetSort(bson.D{
			{Key: "createdAt", Value: -1},
			{Key: "_id", Value: -1},
		}).SetSkip(skip).SetLimit(limit+1))
	if err != nil {
		return nil, false, err
	}
	hasMore := int64(len(results)) > limit
	if hasMore {
		results = results[:limit]
	}
	return results, hasMore, nil
}
