package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/sharedmodel"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// PlayerRepo 玩家数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type PlayerRepo struct {
	mongox.BaseRepo[model.Player]
}

// NewPlayerRepo 创建玩家仓库实例
func NewPlayerRepo(client *mongox.Client) *PlayerRepo {
	return &PlayerRepo{BaseRepo: mongox.NewBaseRepo[model.Player](client, collectionPlayers)}
}

// FindByOpenID 根据平台 OpenID 查找玩家
// openID 为空时直接返回 ErrNotFound，避免空字符串匹配所有游客记录
func (r *PlayerRepo) FindByOpenID(ctx context.Context, openID string) (*model.Player, error) {
	if openID == "" {
		return nil, mongox.ErrNotFound
	}
	return r.FindOne(ctx, bson.D{
		{Key: "openId", Value: openID},
	})
}

// FindRolesByOpenID 根据平台 OpenID 查找玩家角色列表（按 serverId 过滤，0 表示不过滤）
// 用于 LunPlay 平台角色查询：同一 passport（openId）在多个区服可拥有多个角色
func (r *PlayerRepo) FindRolesByOpenID(ctx context.Context, openID string, serverID int64) ([]*model.Player, error) {
	if openID == "" {
		return nil, nil
	}
	filter := bson.D{{Key: "openId", Value: openID}}
	if serverID > 0 {
		filter = append(filter, bson.E{Key: "serverId", Value: serverID})
	}
	results, err := r.FindMany(ctx, filter)
	if err != nil {
		return nil, err
	}
	players := make([]*model.Player, len(results))
	for i := range results {
		players[i] = &results[i]
	}
	return players, nil
}

// FindByPlayerID 根据玩家ID查找玩家
func (r *PlayerRepo) FindByPlayerID(ctx context.Context, playerID int64) (*model.Player, error) {
	return r.FindOne(ctx, bson.D{
		{Key: "playerId", Value: playerID},
	})
}

// InsertPlayer 创建新玩家，自动填充时间戳
func (r *PlayerRepo) InsertPlayer(ctx context.Context, player *model.Player) error {
	now := time.Now()
	player.CreatedAt = now
	player.UpdatedAt = now
	player.LastLoginAt = now
	id, err := r.Insert(ctx, player)
	if err != nil {
		return err
	}
	player.ID = id
	return nil
}

// UpdateLastLogin 更新最后登录时间和客户端信息
func (r *PlayerRepo) UpdateLastLogin(ctx context.Context, playerID int64, clientInfo model.ClientInfo) error {
	_, err := r.UpdateOne(ctx,
		bson.D{{Key: "playerId", Value: playerID}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "lastLoginAt", Value: time.Now()},
			{Key: "clientInfo", Value: clientInfo},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	return err
}

// UpdateLastLoginTime 仅更新最后登录时间，不覆盖客户端信息
// 用于刷新令牌等无新设备信息的活跃行为
func (r *PlayerRepo) UpdateLastLoginTime(ctx context.Context, playerID int64) error {
	_, err := r.UpdateOne(ctx,
		bson.D{{Key: "playerId", Value: playerID}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "lastLoginAt", Value: time.Now()},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	return err
}

// UpgradeFromGuest 将游客账号升级为正式账号，设置平台 openID、渠道账号与登录类型并清除游客标记
// 仅当玩家当前为游客状态（isGuest=true）时才会执行更新
// 使用 MatchedCount 判断是否命中：匹配到 0 条（玩家不存在或已不是游客）返回 ErrNoRowsUpdated。
// 注意不能用 ModifiedCount，幂等重试时若字段值未变化 ModifiedCount 为 0 会造成误判。
func (r *PlayerRepo) UpgradeFromGuest(ctx context.Context, playerID int64, openID, loginType, account string) error {
	res, err := r.Collection().UpdateOne(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "isGuest", Value: true},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "openId", Value: openID},
			{Key: "loginType", Value: loginType},
			{Key: "account", Value: account},
			{Key: "isGuest", Value: false},
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

// DeactivateByPlayerID 注销（作废）玩家的登录身份：将 openId 追加 del_ 前缀释放。
// 设计原则：注销不是删除 Player，而是永久作废其登录身份——原 Player 永久保留（存档/邮件/好友等数据不动），
// openId 被改写为 "del_<unixMilli>_<原openId>" 后即无法再被登录链路命中，从而彻底释放原身份；
// 追加毫秒时间戳前缀是为了避免同一身份被多次注销（注销→重登→再注销）时撞上 openId 唯一索引。
//
// 仅对未注销玩家（openId 不以 del_ 前缀开头）生效，保证并发/重试幂等：
// 命中并成功作废返回作废前的原始 openId；匹配到 0 条（玩家不存在或已注销）返回 ErrNoRowsUpdated。
//
// 参数：
//   - ctx:      请求上下文
//   - playerID: 待注销玩家的唯一标识
//
// 返回：作废前的原始 openId 与错误
func (r *PlayerRepo) DeactivateByPlayerID(ctx context.Context, playerID int64) (string, error) {
	player, err := r.FindByPlayerID(ctx, playerID)
	if err != nil {
		return "", err
	}
	if sharedmodel.IsDeactivatedOpenID(player.OpenID) {
		// 已注销，视为幂等：无需重复作废
		return "", model.ErrNoRowsUpdated
	}

	originalOpenID := player.OpenID
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

// FindByPlayerIDs 根据玩家ID列表批量查找玩家
func (r *PlayerRepo) FindByPlayerIDs(ctx context.Context, playerIDs []int64) ([]*model.Player, error) {
	if len(playerIDs) == 0 {
		return nil, nil
	}
	results, err := r.FindMany(ctx, bson.D{{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}}})
	if err != nil {
		return nil, err
	}
	players := make([]*model.Player, len(results))
	for i := range results {
		players[i] = &results[i]
	}
	return players, nil
}

// FindExistingPlayerIDs 批量查询"存在且未注销"的玩家ID，仅返回 playerId 一个字段。
// 供好友列表/排行榜的幽灵过滤使用：调用方只需要判断玩家是否可见，
// 而 FindByPlayerIDs 会把 openId/saveKey/clientInfo 等无关大字段整文档取出，
// 候选上限可达 200+（好友 100 + 额外好友 100 + 本人），整文档拉取的网络与反序列化开销是纯浪费。
// 投影后单条文档仅含 playerId，已注销玩家（status=StatusDeactivated）与不存在的玩家ID都不在结果中。
func (r *PlayerRepo) FindExistingPlayerIDs(ctx context.Context, playerIDs []int64) ([]int64, error) {
	if len(playerIDs) == 0 {
		return nil, nil
	}
	// status 条件在数据库侧完成过滤（$ne 无索引，playerId 走 ukPlayerId 索引取文档后过滤），
	// 投影只取 playerId，且不需要 _id
	results, err := r.FindMany(ctx, bson.D{
		{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}},
		{Key: "status", Value: bson.D{{Key: "$ne", Value: model.StatusDeactivated}}},
	}, options.Find().SetProjection(bson.D{
		{Key: "playerId", Value: 1},
		{Key: "_id", Value: 0},
	}))
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(results))
	for i := range results {
		ids = append(ids, results[i].PlayerID)
	}
	return ids, nil
}

// FindByAccounts 根据第三方账号（account）列表批量查找玩家，仅返回 account 与 playerId 两个字段
// 空账号会被过滤：游客玩家 account 为空，若不过滤则空字符串会匹配到大量无关记录；
// 已注销玩家（status=StatusDeactivated）同样被排除：注销仅作废登录身份、保留 account 原值，
// 不过滤会让同一账号在注销重登后同时解析出新旧两个角色（旧角色带着过期等级/头像混入榜单）。
// 同一账号在多个区服可能存在多个有效角色，故按账号查询仍可能返回多个玩家，由调用方按 playerId 去重。
//
// 该方法的用途是"账号 → 玩家ID"解析（好友排行榜的额外好友），调用方只需要 account/playerId 做映射，
// openId/saveKey/clientInfo 等大字段全部无用，故投影掉以减少网络传输与反序列化开销；
// 如需完整玩家文档请改用 FindByPlayerIDs。注意：返回的 Player 仅 account 与 playerId 有效。
func (r *PlayerRepo) FindByAccounts(ctx context.Context, accounts []string) ([]*model.Player, error) {
	if len(accounts) == 0 {
		return nil, nil
	}
	valid := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account != "" {
			valid = append(valid, account)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	// $gt: "" 与 partialFilterExpression 完全一致，使查询能命中 idxAccount 部分索引
	// （部分索引仅在查询可判定为过滤条件子集时才会被使用，仅靠 $in 无法被规划器证明）；
	// $ne: StatusDeactivated 排除已注销角色
	results, err := r.FindMany(ctx, bson.D{
		{Key: "account", Value: bson.D{{Key: "$in", Value: valid}, {Key: "$gt", Value: ""}}},
		{Key: "status", Value: bson.D{{Key: "$ne", Value: model.StatusDeactivated}}},
	}, options.Find().SetProjection(bson.D{
		{Key: "account", Value: 1},
		{Key: "playerId", Value: 1},
		{Key: "_id", Value: 0},
	}))
	if err != nil {
		return nil, err
	}
	players := make([]*model.Player, len(results))
	for i := range results {
		players[i] = &results[i]
	}
	return players, nil
}

// BatchUpdateStatus 批量更新玩家状态
// 仅更新 playerId 在列表中且当前状态不等于目标状态的记录（幂等）
// 返回实际被修改的数量
func (r *PlayerRepo) BatchUpdateStatus(ctx context.Context, playerIDs []int64, fromStatus, toStatus int) (int64, error) {
	if len(playerIDs) == 0 {
		return 0, nil
	}
	res, err := r.Collection().UpdateMany(ctx,
		bson.D{
			{Key: "playerId", Value: bson.D{{Key: "$in", Value: playerIDs}}},
			{Key: "status", Value: fromStatus},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: toStatus},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}
