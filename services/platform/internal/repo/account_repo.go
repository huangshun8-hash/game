package repo

import (
	"context"
	"fmt"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/shared/sharedmodel"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// AccountRepo 账号数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type AccountRepo struct {
	mongox.BaseRepo[model.Account]
}

// NewAccountRepo 创建账号仓库实例
func NewAccountRepo(client *mongox.Client) *AccountRepo {
	return &AccountRepo{BaseRepo: mongox.NewBaseRepo[model.Account](client, collectionAccounts)}
}

// FindByOpenID 根据 OpenID 查找账号
// openID 为空时直接返回 ErrNotFound，避免空值查询导致意外匹配
func (r *AccountRepo) FindByOpenID(ctx context.Context, openID string) (*model.Account, error) {
	if openID == "" {
		return nil, mongox.ErrNotFound
	}
	return r.FindOne(ctx, bson.D{
		{Key: "openId", Value: openID},
	})
}

// InsertAccount 创建新账号，自动填充时间戳
func (r *AccountRepo) InsertAccount(ctx context.Context, account *model.Account) error {
	now := time.Now()
	account.CreatedAt = now
	account.UpdatedAt = now
	account.LastLoginAt = now
	_, err := r.Insert(ctx, account)
	return err
}

// playerIDSeqBase 玩家 ID 序号基准值，首个注册玩家的 playerId 为 15000001（基准值 + 1）
// 基准值以下的低位序号段预留（不使用），便于内部账号、历史迁移数据与真实玩家 ID 区分
const playerIDSeqBase int64 = 15000000

// NextPlayerID 生成下一个玩家自增 ID
// 使用 MongoDB counters 集合实现原子自增，并发安全。
// 自增以聚合管道方式完成，保证序号「从不低于基准值且严格单调递增」：
//   - 计数器首次创建（seq 不存在）时以 playerIDSeqBase 为基准，故首个玩家 ID 为 15000001；
//   - 计数器已高于基准（如历史存量数据）时在既有值上顺延，绝不回退，避免与已发放的 ID 重合。
func (r *AccountRepo) NextPlayerID(ctx context.Context) (int64, error) {
	coll := r.Collection().Database().Collection(collectionCounters)
	filter := bson.D{{Key: "_id", Value: "playerId"}}
	// 管道更新：当前序号（计数器不存在时视为 0）先抬升到基准值，再自增 1
	update := bson.A{
		bson.D{{Key: "$set", Value: bson.D{{
			Key: "seq",
			Value: bson.D{{Key: "$add", Value: bson.A{
				bson.D{{Key: "$max", Value: bson.A{
					bson.D{{Key: "$ifNull", Value: bson.A{"$seq", int64(0)}}},
					playerIDSeqBase,
				}}},
				int64(1),
			}}},
		}}}},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var result struct {
		Seq int64 `bson:"seq"`
	}
	err := coll.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		return 0, fmt.Errorf("生成玩家自增ID失败: %w", err)
	}
	return result.Seq, nil
}

// UpdateLastLogin 更新最后登录时间
func (r *AccountRepo) UpdateLastLogin(ctx context.Context, openID string) error {
	_, err := r.UpdateOne(ctx,
		bson.D{{Key: "openId", Value: openID}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "lastLoginAt", Value: time.Now()},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	return err
}

// FindByPlayerID 根据玩家ID查找账号
func (r *AccountRepo) FindByPlayerID(ctx context.Context, playerID int64) (*model.Account, error) {
	return r.FindOne(ctx, bson.D{
		{Key: "playerId", Value: playerID},
	})
}

// DeactivateByPlayerID 注销（作废）账号的登录身份：将 openId 追加 del_ 前缀释放。
// 设计原则：注销不是删除 Account，而是永久作废其登录身份——原 Account 永久保留（供审计/对账），
// 但 openId 被改写为 "del_<unixMilli>_<原openId>" 后即无法再被登录链路命中，从而彻底释放原身份；
// 追加毫秒时间戳前缀是为了避免同一身份被多次注销（注销→重登→再注销）时撞上 openId 唯一索引。
//
// 仅对未注销账号（openId 不以 del_ 前缀开头）生效，保证并发/重试幂等：
// 命中并成功作废返回作废前的原始 openId；匹配到 0 条（账号不存在或已注销）返回 ErrNoRowsUpdated。
//
// 参数：
//   - ctx:      请求上下文
//   - playerID: 待注销账号的玩家唯一标识
//
// 返回：作废前的原始 openId 与错误
func (r *AccountRepo) DeactivateByPlayerID(ctx context.Context, playerID int64) (string, error) {
	account, err := r.FindByPlayerID(ctx, playerID)
	if err != nil {
		return "", err
	}
	if sharedmodel.IsDeactivatedOpenID(account.OpenID) {
		// 已注销，视为幂等：无需重复作废
		return "", model.ErrNoRowsUpdated
	}

	originalOpenID := account.OpenID
	deactivatedOpenID := sharedmodel.BuildDeactivatedOpenID(originalOpenID)
	res, err := r.Collection().UpdateOne(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "openId", Value: originalOpenID},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "openId", Value: deactivatedOpenID},
			{Key: "status", Value: model.StatusDeactivated},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	if err != nil {
		return "", err
	}
	if res.MatchedCount == 0 {
		// 并发下已被其他请求作废，视为幂等
		return "", model.ErrNoRowsUpdated
	}
	return originalOpenID, nil
}

// UpgradeFromGuest 将游客账号升级为正式账号，更新 openId 和 loginType
// 仅当账号当前 loginType 为 guest 时才会执行更新，并发安全
// 使用 MatchedCount 判断是否命中：匹配到 0 条（账号不存在或已不是游客）返回 ErrNoRowsUpdated。
// 注意不能用 ModifiedCount，幂等重试时若字段值未变化 ModifiedCount 为 0 会造成误判。
func (r *AccountRepo) UpgradeFromGuest(ctx context.Context, playerID int64, newOpenID, loginType string) error {
	res, err := r.Collection().UpdateOne(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "loginType", Value: model.LoginTypeGuest},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "openId", Value: newOpenID},
			{Key: "loginType", Value: loginType},
			{Key: "lastLoginAt", Value: time.Now()},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return model.ErrNoRowsUpdated
	}
	return nil
}
