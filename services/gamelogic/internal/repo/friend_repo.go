package repo

import (
	"context"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// FriendRepo 好友关系数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type FriendRepo struct {
	mongox.BaseRepo[model.FriendRelation]
}

// NewFriendRepo 创建好友关系仓库实例
func NewFriendRepo(client *mongox.Client) *FriendRepo {
	return &FriendRepo{BaseRepo: mongox.NewBaseRepo[model.FriendRelation](client, collectionUserFriend)}
}

// IsFriend 判断 uidA 与 uidB 是否为好友
// 好友关系双向冗余存储，查询单向记录即可，命中 (uid, friendUid) 唯一索引
func (r *FriendRepo) IsFriend(ctx context.Context, uidA, uidB int64) (bool, error) {
	return r.Exists(ctx, bson.D{
		{Key: "uid", Value: uidA},
		{Key: "friendUid", Value: uidB},
	})
}

// CountByUid 统计某玩家的好友数量
// 好友关系双向冗余存储，好友数 = 以该玩家为 uid 的记录数，命中 uid 索引
func (r *FriendRepo) CountByUid(ctx context.Context, uid int64) (int64, error) {
	return r.Count(ctx, bson.D{{Key: "uid", Value: uid}})
}

// UpsertRelation 幂等写入一条好友关系记录
// 不存在则插入，存在则仅更新时间戳；返回是否新建了记录
// 用于同意申请时自动补齐缺失一侧的历史单向脏数据
func (r *FriendRepo) UpsertRelation(ctx context.Context, uid, friendUID, now int64) (bool, error) {
	result, err := mongox.UpsertOne(ctx, r.Collection(), bson.D{
		{Key: "uid", Value: uid},
		{Key: "friendUid", Value: friendUID},
	}, bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "updatedAt", Value: now},
		}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "uid", Value: uid},
			{Key: "friendUid", Value: friendUID},
			{Key: "createdAt", Value: now},
		}},
	})
	if err != nil {
		return false, err
	}
	return result.UpsertedCount > 0, nil
}

// DeleteRelation 删除一条好友关系记录，返回是否删除成功
func (r *FriendRepo) DeleteRelation(ctx context.Context, uid, friendUID int64) (bool, error) {
	deleted, err := r.DeleteOne(ctx, bson.D{
		{Key: "uid", Value: uid},
		{Key: "friendUid", Value: friendUID},
	})
	if err != nil {
		return false, err
	}
	return deleted > 0, nil
}

// ListPage 位置分页查询好友列表（createdAt desc, _id desc）
// skip 为跳过的记录数（从 0 开始），limit 为单页数量；
// 多取一条判断是否还有更多
func (r *FriendRepo) ListPage(ctx context.Context, uid, skip, limit int64) ([]model.FriendRelation, bool, error) {
	filter := bson.D{{Key: "uid", Value: uid}}
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

// ListFriendIDs 查询某玩家的全部好友ID，仅返回 friendUid 一个字段。
// 好友数量较少（社交规模受限），排行榜等场景需要全量数据以计算排名，故不做分页。
// 好友排行榜只需要好友ID列表（用于拼装排行候选并回填 Redis 缓存），
// 关系文档的 createdAt/updatedAt 等字段无用；同时省去排序，
// 因为 (uid, friendUid) 唯一索引已能直接覆盖过滤条件，无需额外排序开销。
func (r *FriendRepo) ListFriendIDs(ctx context.Context, uid int64) ([]int64, error) {
	relations, err := r.FindMany(ctx, bson.D{{Key: "uid", Value: uid}},
		options.Find().SetProjection(bson.D{
			{Key: "friendUid", Value: 1},
			{Key: "_id", Value: 0},
		}))
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(relations))
	for i := range relations {
		ids = append(ids, relations[i].FriendUID)
	}
	return ids, nil
}
