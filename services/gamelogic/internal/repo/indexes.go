package repo

import (
	"context"

	"kemiluo/pkg/mongox"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EnsureIndexes 确保逻辑服所需的数据库索引存在
func EnsureIndexes(ctx context.Context, client *mongox.Client) error {
	return client.EnsureIndexes(ctx, []mongox.IndexDef{
		// players 集合：playerId 唯一索引
		{
			Collection: collectionPlayers,
			Keys:       bson.D{{Key: "playerId", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerId",
		},
		// players 集合：openId 部分唯一索引（仅对非空值强制唯一）
		// 游客玩家 openId 存储为客户端 UUID（非空），三方玩家为平台返回的 openID，
		// 两类玩家统一由该索引保证唯一性，无需再单独维护 uuid 索引
		{
			Collection:              collectionPlayers,
			Keys:                    bson.D{{Key: "openId", Value: 1}},
			Unique:                  true,
			Name:                    "ukOpenId",
			PartialFilterExpression: bson.D{{Key: "openId", Value: bson.D{{Key: "$gt", Value: ""}}}},
		},
		// players 集合：account 部分索引（仅对非空值建索引）
		// 用于好友排行榜按 account 批量解析额外好友；账号非全局唯一（同一账号在多个区服可有多个角色），故不设唯一约束。
		// 游客玩家 account 为空，partial filter 过滤空值避免为空字符串建立海量索引项；
		// 注意：部分索引只在查询条件可判定为过滤表达式子集时才会被使用，
		// 因此 FindByAccounts 的查询必须显式带上 account > "" 条件（与 $in 组合），否则该索引不会被命中
		{
			Collection:              collectionPlayers,
			Keys:                    bson.D{{Key: "account", Value: 1}},
			Name:                    "idxAccount",
			PartialFilterExpression: bson.D{{Key: "account", Value: bson.D{{Key: "$gt", Value: ""}}}},
		},
		// save_heads 集合：(playerId, key) 复合唯一索引（每玩家每个 key 仅一条存档头）
		{
			Collection: collectionSaveHeads,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "key", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerKey",
		},
		// save_snapshots 集合：(playerId, key, snapshotSeq) 唯一复合索引
		// snapshotSeq 由对应的 SaveHead（同 playerId+key）原子递增产生，同一玩家同一 key 不应出现重复序号；
		// 唯一约束兼作防御性保障，一旦逻辑错误可及早在写入时暴露；
		// 同时支持按序号范围精准清理旧快照（PruneOldSnapshots 的 $lt keepFrom 条件直接命中此索引）
		{
			Collection: collectionSaveSnapshots,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "key", Value: 1}, {Key: "snapshotSeq", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerKeySnapshotSeq",
		},
		// save_snapshots 集合：(playerId, key, snapshotSeq desc) 复合索引
		// 用于 FindLatestByKey 按 snapshotSeq 降序取第一条
		{
			Collection: collectionSaveSnapshots,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "key", Value: 1}, {Key: "snapshotSeq", Value: -1}},
			Name:       "idxPlayerKeySnapshotSeq",
		},

		// mail_templates 集合：templateId 唯一索引
		{
			Collection: collectionMailTemplates,
			Keys:       bson.D{{Key: "templateId", Value: 1}},
			Unique:     true,
			Name:       "ukTemplateId",
		},
		// mail_templates 集合：(targetType, createTime) 复合索引，加速全服邮件按时间排序拉取
		{
			Collection: collectionMailTemplates,
			Keys:       bson.D{{Key: "targetType", Value: 1}, {Key: "createTime", Value: 1}},
			Name:       "idxTargetTypeCreateTime",
		},
		// mail_templates 集合：(targetUIDs, createTime) 复合索引，加速定向邮件查询
		{
			Collection: collectionMailTemplates,
			Keys:       bson.D{{Key: "targetUIDs", Value: 1}, {Key: "createTime", Value: 1}},
			Name:       "idxTargetUIDsCreateTime",
		},

		// player_mails 集合：(playerId, mailId) 唯一索引，防止重复插入
		{
			Collection: collectionPlayerMails,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "mailId", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerMailId",
		},
		// player_mails 集合：(playerId, status, createTime) 复合索引
		// FindMailList 排序规则为 status asc + createTime desc，需与此索引对应才能避免内存排序
		{
			Collection: collectionPlayerMails,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "status", Value: 1}, {Key: "createTime", Value: -1}},
			Name:       "idxPlayerStatusCreateTime",
		},
		// player_mails 集合：(playerId, createTime) 复合索引
		// 用于 CleanupOverflowMails 按创建时间升序定位最旧邮件（反向扫描该降序索引）
		{
			Collection: collectionPlayerMails,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "createTime", Value: -1}},
			Name:       "idxPlayerCreateTime",
		},
		// player_mails 集合：(playerId, templateId) 部分唯一索引，用于 Lazy Create 去重
		// 仅对 templateId > 0（系统模板邮件）强制唯一，防止并发 Lazy Create 重复下发同一模板；
		// 个人邮件 templateId == 0 不受唯一约束，允许一个玩家拥有多封个人邮件
		{
			Collection:              collectionPlayerMails,
			Keys:                    bson.D{{Key: "playerId", Value: 1}, {Key: "templateId", Value: 1}},
			Unique:                  true,
			Name:                    "ukPlayerTemplateId",
			PartialFilterExpression: bson.D{{Key: "templateId", Value: bson.D{{Key: "$gt", Value: 0}}}},
		},
		// player_mails 集合：expireTime 部分索引，加速定时清理过期邮件的定位查询
		// 仅对 expireTime > 0（有明确过期时间）的邮件建索引，永不过期邮件（expireTime=0）不占索引空间
		{
			Collection:              collectionPlayerMails,
			Keys:                    bson.D{{Key: "expireTime", Value: 1}},
			Name:                    "idxExpireTime",
			PartialFilterExpression: bson.D{{Key: "expireTime", Value: bson.D{{Key: "$gt", Value: int64(0)}}}},
		},
		// pay_orders 集合：orderId 唯一索引（核心幂等键）
		{
			Collection: collectionPayOrders,
			Keys:       bson.D{{Key: "orderId", Value: 1}},
			Unique:     true,
			Name:       "ukOrderId",
		},
		// pay_orders 集合：playerId + status 复合索引（查询玩家订单列表）
		{
			Collection: collectionPayOrders,
			Keys:       bson.D{{Key: "playerId", Value: 1}, {Key: "status", Value: 1}},
			Name:       "idxPlayerStatus",
		},
		// pay_orders 集合：status + updatedAt 复合索引（定时任务补发/关闭扫描）
		{
			Collection: collectionPayOrders,
			Keys:       bson.D{{Key: "status", Value: 1}, {Key: "updatedAt", Value: 1}},
			Name:       "idxStatusUpdatedAt",
		},
		// pay_orders 集合：status + expireTime 复合索引（定时关闭过期订单）
		{
			Collection: collectionPayOrders,
			Keys:       bson.D{{Key: "status", Value: 1}, {Key: "expireTime", Value: 1}},
			Name:       "idxStatusExpireTime",
		},

		// ban_records 集合：playerId 唯一索引（每个玩家至多一条封禁记录）
		{
			Collection: collectionBanRecords,
			Keys:       bson.D{{Key: "playerId", Value: 1}},
			Unique:     true,
			Name:       "ukPlayerId",
		},
		// ban_records 集合：createdAt 索引（分页查询按时间倒序）
		{
			Collection: collectionBanRecords,
			Keys:       bson.D{{Key: "createdAt", Value: -1}},
			Name:       "idxCreatedAt",
		},

		// user_friend 集合：(uid, friendUid) 唯一索引
		// 防止同一方向产生重复好友关系，同时支持好友关系判断（is-friend）
		{
			Collection: collectionUserFriend,
			Keys:       bson.D{{Key: "uid", Value: 1}, {Key: "friendUid", Value: 1}},
			Unique:     true,
			Name:       "ukUidFriendUid",
		},
		// user_friend 集合：(uid, createdAt desc, _id desc) 复合索引
		// 好友列表偏移分页；_id 作为第二排序键保证相同 createdAt 下仍具有稳定且唯一的分页顺序
		{
			Collection: collectionUserFriend,
			Keys:       bson.D{{Key: "uid", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}},
			Name:       "idxUidCreatedAt",
		},

		// friend_apply 集合：(applyUid, targetUid) 唯一索引
		// 记录存在即表示待处理，保证同一方向最多一条申请；并发插入冲突由该索引兜底
		{
			Collection: collectionFriendApply,
			Keys:       bson.D{{Key: "applyUid", Value: 1}, {Key: "targetUid", Value: 1}},
			Unique:     true,
			Name:       "ukApplyTarget",
		},
		// friend_apply 集合：(targetUid, createdAt desc, _id desc) 复合索引
		// 收到的申请列表偏移分页；createdAt 为不可变排序键（仅插入时设置），保证翻页稳定
		{
			Collection: collectionFriendApply,
			Keys:       bson.D{{Key: "targetUid", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}},
			Name:       "idxTargetUidCreatedAt",
		},
		// friend_apply 集合：(applyUid, createdAt desc, _id desc) 复合索引
		// 发出的申请列表偏移分页
		{
			Collection: collectionFriendApply,
			Keys:       bson.D{{Key: "applyUid", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}},
			Name:       "idxApplyUidCreatedAt",
		},
	})
}
