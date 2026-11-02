package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// notExpiredFilter 构造未过期条件（expireTime=0 永不过期，否则必须 > nowMs）
// 复用此辅助函数避免各查询方法重复拼写相同的 $or 条件
func notExpiredFilter(nowMs int64) bson.E {
	return bson.E{Key: "$or", Value: bson.A{
		bson.D{{Key: "expireTime", Value: 0}},
		bson.D{{Key: "expireTime", Value: bson.D{{Key: "$gt", Value: nowMs}}}},
	}}
}

// PlayerMailRepo 玩家邮件数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type PlayerMailRepo struct {
	mongox.BaseRepo[model.PlayerMail]
}

// NewPlayerMailRepo 创建玩家邮件仓库实例
func NewPlayerMailRepo(client *mongox.Client) *PlayerMailRepo {
	return &PlayerMailRepo{BaseRepo: mongox.NewBaseRepo[model.PlayerMail](client, collectionPlayerMails)}
}

// InsertMail 插入玩家邮件，自动填充创建时间
func (r *PlayerMailRepo) InsertMail(ctx context.Context, mail *model.PlayerMail) error {
	if mail.CreateTime == 0 {
		mail.CreateTime = time.Now().UnixMilli()
	}
	id, err := r.Insert(ctx, mail)
	if err != nil {
		return err
	}
	mail.ID = id
	return nil
}

// InsertManyMails 批量插入玩家邮件（用于 Lazy Create 批量下发）
// 使用 ordered=false，允许部分文档插入失败（如重复键），不中断其余文档的写入
// 返回实际成功插入的邮件数量
func (r *PlayerMailRepo) InsertManyMails(ctx context.Context, mails []*model.PlayerMail) (int64, error) {
	now := time.Now().UnixMilli()
	docs := make([]any, len(mails))
	for i, m := range mails {
		if m.CreateTime == 0 {
			m.CreateTime = now
		}
		docs[i] = m
	}
	// ordered=false：单条重复键不阻断批次中其余文档的写入
	result, err := r.Collection().InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	if err != nil {
		// 纯重复键错误视为并发幂等（另一请求已插入相同模板邮件），正常情况可吞掉；
		// 混合错误（含校验失败、写关注错误等）必须上报，避免部分邮件静默丢失
		if !mongox.IsDuplicateKeyOnlyError(err) {
			return 0, fmt.Errorf("mongox: insert many failed: %w", err)
		}
	}

	// 失败文档的下标集合（ordered=false 下 WriteError.Index 为原始批内下标）
	// 注意：驱动在 marshal 阶段就为所有文档生成了本地 _id，InsertedIDs 中失败位置并非 nil，
	// 必须借助 WriteErrors 的下标才能区分真实写入成功的文档，否则计数与 ID 回填都会失真
	failedIndexes := make(map[int]struct{})
	if err != nil {
		var bwe mongo.BulkWriteException
		if errors.As(err, &bwe) {
			for _, we := range bwe.WriteErrors {
				failedIndexes[we.Index] = struct{}{}
			}
		}
	}

	// 回填实际写入成功文档的 ObjectID，并统计真实插入数量
	var inserted int64
	if result != nil {
		for i, id := range result.InsertedIDs {
			if _, failed := failedIndexes[i]; failed || i >= len(mails) {
				continue
			}
			if oid, ok := id.(bson.ObjectID); ok {
				mails[i].ID = oid
				inserted++
			}
		}
	}
	return inserted, nil
}

// FindByMailID 根据邮件业务ID和玩家ID查找邮件
// 同时在数据库层过滤已过期的邮件，查不到时统一返回 mongox.ErrNotFound
// nowMs: 当前时间戳（Unix毫秒），用于过期判断
func (r *PlayerMailRepo) FindByMailID(ctx context.Context, playerID, mailID int64, nowMs int64) (*model.PlayerMail, error) {
	return r.FindOne(ctx, bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "mailId", Value: mailID},
		// 未过期
		notExpiredFilter(nowMs),
	})
}

// FindMailList 查询玩家邮件列表（排除已过期，offset/limit 分页）
// 排序规则：status 升序（未读=0 最前，已读次之，已读且已领取最后）→ 同 status 内创建时间倒序
// nowMs: 当前时间戳（Unix毫秒），用于过滤已过期邮件
// offset: 起始偏移量（从 0 开始）；limit: 返回条数上限（最大 50，默认 20）
func (r *PlayerMailRepo) FindMailList(ctx context.Context, playerID int64, nowMs int64, offset, limit int64) ([]model.PlayerMail, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		notExpiredFilter(nowMs),
	}
	opts := options.Find().
		SetSort(bson.D{
			{Key: "status", Value: 1},
			{Key: "createTime", Value: -1},
		}).
		SetSkip(offset).
		SetLimit(limit)
	return r.FindMany(ctx, filter, opts)
}

// FindUnreceivedMails 查询玩家所有有附件、未领取且未过期的邮件（用于一键领取）
// nowMs: 当前时间戳（Unix毫秒），用于过滤已过期邮件
func (r *PlayerMailRepo) FindUnreceivedMails(ctx context.Context, playerID int64, nowMs int64) ([]model.PlayerMail, error) {
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		// 有附件（rewards 字段存在且非空）
		{Key: "rewards", Value: bson.D{{Key: "$exists", Value: true}, {Key: "$ne", Value: bson.A{}}}},
		// 附件未领取
		{Key: "status", Value: bson.D{{Key: "$bitsAllClear", Value: model.MailRewarded}}},
		// 未过期
		notExpiredFilter(nowMs),
	}
	return r.FindMany(ctx, filter)
}

// CountByPlayerID 统计玩家邮件总数
func (r *PlayerMailRepo) CountByPlayerID(ctx context.Context, playerID int64) (int64, error) {
	return r.Count(ctx, bson.D{
		{Key: "playerId", Value: playerID},
	})
}

// ExistsByTemplateID 判断玩家是否已存在来自指定模板的邮件（Lazy Create 去重）
func (r *PlayerMailRepo) ExistsByTemplateID(ctx context.Context, playerID, templateID int64) (bool, error) {
	return r.Exists(ctx, bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "templateId", Value: templateID},
	})
}

// FindExistingTemplateIDs 批量查询玩家已存在的模板ID集合（用于 Lazy Create 批量去重）
func (r *PlayerMailRepo) FindExistingTemplateIDs(ctx context.Context, playerID int64, templateIDs []int64) (map[int64]struct{}, error) {
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "templateId", Value: bson.D{{Key: "$in", Value: templateIDs}}},
	}
	opts := options.Find().SetProjection(bson.D{{Key: "templateId", Value: 1}})
	cursor, err := r.Collection().Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	exists := make(map[int64]struct{}, len(templateIDs))
	for cursor.Next(ctx) {
		var doc struct {
			TemplateID int64 `bson:"templateId"`
		}
		if err := cursor.Decode(&doc); err == nil {
			exists[doc.TemplateID] = struct{}{}
		}
	}
	return exists, cursor.Err()
}

// UpdateStatus 更新单封邮件的状态（$bit $or 位操作置位）
// 仅更新匹配 playerId + mailId 的文档，防止越权
func (r *PlayerMailRepo) UpdateStatus(ctx context.Context, playerID, mailID int64, orMask int32) (int64, error) {
	return r.UpdateOne(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "mailId", Value: mailID},
		},
		bson.D{{Key: "$bit", Value: bson.D{
			{Key: "status", Value: bson.D{{Key: "or", Value: orMask}}},
		}}},
	)
}

// ClaimReward 原子领取邮件附件：仅当附件尚未被领取时才执行置位，防止并发重复领取
// 通过在 filter 中加入 $bitsAllClear: MailRewarded 前置条件，确保操作幂等
// 返回 modified 数量：0 表示附件已被领取（并发冲突），>0 表示领取成功
func (r *PlayerMailRepo) ClaimReward(ctx context.Context, playerID, mailID int64) (int64, error) {
	return r.UpdateOne(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "mailId", Value: mailID},
			// 前置条件：附件尚未领取，保证原子幂等
			{Key: "status", Value: bson.D{{Key: "$bitsAllClear", Value: model.MailRewarded}}},
		},
		bson.D{{Key: "$bit", Value: bson.D{
			{Key: "status", Value: bson.D{{Key: "or", Value: model.MailRewarded | model.MailRead}}},
		}}},
	)
}

// MarkNoRewardMailsAsRead 将玩家所有未读且无附件的邮件批量标记为已读
// 用于一键领取时同步消化纯通知邮件
func (r *PlayerMailRepo) MarkNoRewardMailsAsRead(ctx context.Context, playerID int64) (int64, error) {
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		// 未读
		{Key: "status", Value: bson.D{{Key: "$bitsAllClear", Value: model.MailRead}}},
		// 无附件
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "rewards", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "rewards", Value: bson.A{}}},
		}},
	}
	return mongox.UpdateMany(ctx, r.Collection(), filter,
		bson.D{{Key: "$bit", Value: bson.D{
			{Key: "status", Value: bson.D{{Key: "or", Value: model.MailRead}}},
		}}},
	)
}

// DeleteReadMails 删除玩家所有已读且可清理的邮件：无附件 或 附件已领取
// 含有附件但尚未领取的邮件不会被删除，保护玩家利益
// 物理删除，返回实际删除的数量
func (r *PlayerMailRepo) DeleteReadMails(ctx context.Context, playerID int64) (int64, error) {
	filter := bson.D{
		{Key: "playerId", Value: playerID},
		// 已读
		{Key: "status", Value: bson.D{
			{Key: "$bitsAllSet", Value: model.MailRead},
		}},
		// 无附件（rewards 不存在或为空列表）或 附件已领取
		// 含有附件且未领取的邮件不在此范围内，防止误删玩家未领取的奖励
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "rewards", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "rewards", Value: bson.A{}}},
			bson.D{{Key: "status", Value: bson.D{{Key: "$bitsAllSet", Value: model.MailRewarded}}}},
		}},
	}
	return r.DeleteMany(ctx, filter)
}

// CleanupOverflowMails 邮箱超出上限时按优先级清理：
// 1. 已过期 → 2. 已领取附件（最旧优先）→ 3. 最旧邮件（兜底）
// deleteCount: 需要腾出的数量；返回实际删除数量
func (r *PlayerMailRepo) CleanupOverflowMails(ctx context.Context, playerID int64, deleteCount int64) (int64, error) {
	if deleteCount <= 0 {
		return 0, nil
	}
	nowMs := time.Now().UnixMilli()

	// 第一轮：删除已过期邮件
	deleted, err := r.DeleteMany(ctx, bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "expireTime", Value: bson.D{{Key: "$gt", Value: int64(0)}, {Key: "$lt", Value: nowMs}}},
	})
	if err != nil {
		return deleted, err
	}
	if deleted >= deleteCount {
		return deleted, nil
	}

	// 第二轮：删除最旧的已领取附件邮件
	n, err := r.deleteOldestByFilter(ctx, playerID, bson.D{
		{Key: "status", Value: bson.D{{Key: "$bitsAllSet", Value: model.MailRewarded}}},
	}, deleteCount-deleted)
	deleted += n
	if err != nil || deleted >= deleteCount {
		return deleted, err
	}

	// 第三轮：兜底删除最旧邮件
	n, err = r.deleteOldestByFilter(ctx, playerID, bson.D{}, deleteCount-deleted)
	return deleted + n, err
}

// DeleteExpiredMails 删除过期超过指定时点的邮件（定时任务专用）
// 先投影查询最旧的 limit 条再批量删除，控制单次操作规模，避免大范围删除阻塞服务
// beforeMs: 清理阈值时间戳（Unix毫秒），仅删除 expireTime > 0 且 expireTime < beforeMs 的邮件（永不过期邮件不受影响）
// limit: 单批处理上限；返回实际删除数量
func (r *PlayerMailRepo) DeleteExpiredMails(ctx context.Context, beforeMs int64, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "expireTime", Value: 1}}).
		SetLimit(limit).
		SetProjection(bson.D{{Key: "mailId", Value: 1}})

	cursor, err := r.Collection().Find(ctx, bson.D{
		{Key: "expireTime", Value: bson.D{{Key: "$gt", Value: int64(0)}, {Key: "$lt", Value: beforeMs}}},
	}, opts)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var ids []int64
	for cursor.Next(ctx) {
		var doc struct {
			MailID int64 `bson:"mailId"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, doc.MailID)
		}
	}
	if err := cursor.Err(); err != nil || len(ids) == 0 {
		return 0, err
	}

	return r.DeleteMany(ctx, bson.D{
		{Key: "mailId", Value: bson.D{{Key: "$in", Value: ids}}},
	})
}

// deleteOldestByFilter 按创建时间升序删除最旧的 limit 条满足 extraFilter 的邮件
func (r *PlayerMailRepo) deleteOldestByFilter(ctx context.Context, playerID int64, extraFilter bson.D, limit int64) (int64, error) {
	filter := append(bson.D{{Key: "playerId", Value: playerID}}, extraFilter...)

	opts := options.Find().
		SetSort(bson.D{{Key: "createTime", Value: 1}}).
		SetLimit(limit).
		SetProjection(bson.D{{Key: "mailId", Value: 1}})

	cursor, err := r.Collection().Find(ctx, filter, opts)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var ids []int64
	for cursor.Next(ctx) {
		var doc struct {
			MailID int64 `bson:"mailId"`
		}
		if err := cursor.Decode(&doc); err == nil {
			ids = append(ids, doc.MailID)
		}
	}
	if err := cursor.Err(); err != nil || len(ids) == 0 {
		return 0, err
	}

	return r.DeleteMany(ctx, bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "mailId", Value: bson.D{{Key: "$in", Value: ids}}},
	})
}
