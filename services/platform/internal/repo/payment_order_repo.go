package repo

import (
	"context"
	"errors"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// OrderQueryFilter GMT 订单查询过滤条件
type OrderQueryFilter struct {
	// OrderID 精确匹配订单号（非空时忽略其他条件）；
	// 同时匹配业务订单号与三方订单号（thirdTransactionId），运营无需区分订单号类型
	OrderID string
	// PlayerID 玩家ID（0 表示不过滤）
	PlayerID int64
	// PaymentStatus 支付状态（空表示不过滤）
	PaymentStatus string
	// Channel 支付渠道（空表示不过滤）
	Channel string
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64
}

// PaymentOrderRepo 平台支付主表数据访问层
type PaymentOrderRepo struct {
	mongox.BaseRepo[model.PaymentOrder]
}

// NewPaymentOrderRepo 创建支付订单仓库实例
func NewPaymentOrderRepo(client *mongox.Client) *PaymentOrderRepo {
	return &PaymentOrderRepo{BaseRepo: mongox.NewBaseRepo[model.PaymentOrder](client, collectionPaymentOrders)}
}

// InsertOrder 创建支付订单，自动填充时间
func (r *PaymentOrderRepo) InsertOrder(ctx context.Context, order *model.PaymentOrder) error {
	now := time.Now()
	order.CreatedAt = now
	order.UpdatedAt = now
	if order.CreateTime == 0 {
		order.CreateTime = now.UnixMilli()
	}
	order.Version = 1
	id, err := r.Insert(ctx, order)
	if err != nil {
		return err
	}
	order.ID = id
	return nil
}

// CASMarkPaySuccess 乐观锁标记支付成功（仅 WAIT → SUCCESS）并返回更新后的最新订单。
// 同时记录渠道交易ID、金额、币种、支付时间，以及三方充值回调的归因信息（agent/server/param）。
// 通过 FindOneAndUpdate + ReturnDocument.After 原子完成更新并返回最新文档，
// 调用方直接使用返回订单，无需手工同步内存状态。
// 前置条件：订单必须处于 WAIT 状态，FAILED 等异常状态订单不会被激活。
// 非充值渠道（apple/google）调用时归因参数传空即可。
// 返回：CAS 命中返回最新订单；CAS 未命中（version 不匹配或状态非 WAIT，已被并发/其他路径处理）返回 (nil, nil)；数据库错误返回错误。
func (r *PaymentOrderRepo) CASMarkPaySuccess(ctx context.Context, orderID string, fromVersion int64, thirdTransactionID string, amount int64, currency, agent, server, param string) (*model.PaymentOrder, error) {
	now := time.Now()
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "paymentStatus", Value: model.PaymentStatusSuccess},
		{Key: "thirdTransactionId", Value: thirdTransactionID},
		{Key: "amount", Value: amount},
		{Key: "currency", Value: currency},
		{Key: "agent", Value: agent},
		{Key: "server", Value: server},
		{Key: "param", Value: param},
		{Key: "payTime", Value: now.UnixMilli()},
		{Key: "version", Value: fromVersion + 1},
		{Key: "updatedAt", Value: now},
	}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var updated model.PaymentOrder
	err := r.Collection().FindOneAndUpdate(ctx,
		bson.D{
			{Key: "orderId", Value: orderID},
			{Key: "version", Value: fromVersion},
			// 仅允许 WAIT 状态被标记为支付成功，防止失败态/异常态订单被回调激活
			{Key: "paymentStatus", Value: model.PaymentStatusWait},
		},
		update,
		opts,
	).Decode(&updated)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// CAS 未命中：version 不匹配，订单已被其他请求处理
			return nil, nil
		}
		return nil, err
	}
	return &updated, nil
}

// FindByOrderID 根据业务订单号查找支付订单。
// 订单不存在时返回 model.ErrPaymentOrderNotFound，将"未找到"语义收敛在仓储层，
// 业务层无需感知 mongox 基础设施错误。
func (r *PaymentOrderRepo) FindByOrderID(ctx context.Context, orderID string) (*model.PaymentOrder, error) {
	order, err := r.FindOne(ctx, bson.D{{Key: "orderId", Value: orderID}})
	if err == mongox.ErrNotFound {
		return nil, model.ErrPaymentOrderNotFound
	}
	return order, err
}

// FindByThirdTransactionID 根据渠道交易ID查找（防重放，幂等）
func (r *PaymentOrderRepo) FindByThirdTransactionID(ctx context.Context, channel, thirdTransactionID string) (*model.PaymentOrder, error) {
	return r.FindOne(ctx, bson.D{
		{Key: "channel", Value: channel},
		{Key: "thirdTransactionId", Value: thirdTransactionID},
	})
}

// UpdateNotifyStatus 更新通知状态，持久化 notifyID 与通知来源，并原子递增通知次数（发出通知后调用）
// 使用 $inc 原子递增 notifyCount，避免并发通知时计数丢失；
// notifyID 使用 $set 固化，便于补通知复用同一值实现跨服务日志追溯；
// notifySource 使用 $set 固化本次通知来源，补通知沿用同一来源，保证 GameLogic 侧来源判定一致；
// notifySource 为空时不写入，避免覆盖既有来源。
// 条件更新保护：仅当当前 notifyStatus 非 SUCCESS 时才允许写入，确保"成功状态一旦写入不被失败覆盖"，
// 避免并发下 FAILED 覆盖 SUCCESS 导致通知状态回退、触发重复通知。
func (r *PaymentOrderRepo) UpdateNotifyStatus(ctx context.Context, orderID, notifyID, notifySource, notifyStatus string) error {
	now := time.Now()
	set := bson.D{
		{Key: "notifyStatus", Value: notifyStatus},
		{Key: "notifyId", Value: notifyID},
		{Key: "lastNotifyTime", Value: now.UnixMilli()},
		{Key: "updatedAt", Value: now},
	}
	if notifySource != "" {
		set = append(set, bson.E{Key: "notifySource", Value: notifySource})
	}
	_, err := r.UpdateOne(ctx,
		bson.D{
			{Key: "orderId", Value: orderID},
			{Key: "notifyStatus", Value: bson.D{{Key: "$ne", Value: model.NotifyStatusSuccess}}},
		},
		bson.D{
			{Key: "$set", Value: set},
			{Key: "$inc", Value: bson.D{
				{Key: "notifyCount", Value: 1},
			}},
		},
	)
	return err
}

// FindExhaustedNotifyOrders 查找已超出补通知限制的订单（需人工介入处理）
// 超限条件（任一满足）：
//   - notifyCount >= NotifyMaxRetryCount：累计通知次数已达上限
//   - payTime < now-NotifyMaxRetryDuration：支付成功时间超出有效窗口
func (r *PaymentOrderRepo) FindExhaustedNotifyOrders(ctx context.Context, limit int64) ([]*model.PaymentOrder, error) {
	earliestPayTime := time.Now().Add(-model.NotifyMaxRetryDuration).UnixMilli()
	opts := options.Find().
		SetSort(bson.D{{Key: "payTime", Value: 1}}).
		SetLimit(limit)
	cursor, err := r.Collection().Find(ctx,
		bson.D{
			{Key: "paymentStatus", Value: model.PaymentStatusSuccess},
			{Key: "notifyStatus", Value: bson.D{{Key: "$ne", Value: model.NotifyStatusSuccess}}},
			{Key: "$or", Value: bson.A{
				bson.D{{Key: "notifyCount", Value: bson.D{{Key: "$gte", Value: model.NotifyMaxRetryCount}}}},
				bson.D{{Key: "payTime", Value: bson.D{{Key: "$lt", Value: earliestPayTime}}}},
			}},
		},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var orders []*model.PaymentOrder
	if err = cursor.All(ctx, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// PageQueryOrders GMT 分页查询订单。
// 当 filter.OrderID 非空时，按订单号精确匹配（同时匹配业务订单号与三方订单号）并包装为分页结果；
// 否则按 PlayerID / PaymentStatus / Channel / 时间范围组合过滤并分页。
func (r *PaymentOrderRepo) PageQueryOrders(ctx context.Context, filter OrderQueryFilter, page, pageSize int64) (*mongox.PageResult[model.PaymentOrder], error) {
	return mongox.Paginate[model.PaymentOrder](ctx, r.Collection(),
		r.buildQueryFilter(filter),
		mongox.PageQuery{
			Page:     page,
			PageSize: pageSize,
			Sort:     bson.D{{Key: "createTime", Value: -1}},
		},
	)
}

// buildQueryFilter 将结构化过滤条件转换为 bson.D，供 Paginate 使用
func (r *PaymentOrderRepo) buildQueryFilter(f OrderQueryFilter) bson.D {
	// 按订单号精确匹配时直接返回，忽略其他字段。
	// 订单号同时覆盖业务订单号（orderId）与三方订单号（thirdTransactionId），
	// 运营输入任一种订单号均可命中，无需在接口层区分订单号类型。
	if f.OrderID != "" {
		return bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "orderId", Value: f.OrderID}},
			bson.D{{Key: "thirdTransactionId", Value: f.OrderID}},
		}}}
	}

	filter := bson.D{}
	if f.PlayerID > 0 {
		filter = append(filter, bson.E{Key: "playerId", Value: f.PlayerID})
	}
	if f.PaymentStatus != "" {
		filter = append(filter, bson.E{Key: "paymentStatus", Value: f.PaymentStatus})
	}
	if f.Channel != "" {
		filter = append(filter, bson.E{Key: "channel", Value: f.Channel})
	}
	if f.StartTime > 0 || f.EndTime > 0 {
		timeFilter := bson.D{}
		if f.StartTime > 0 {
			timeFilter = append(timeFilter, bson.E{Key: "$gte", Value: f.StartTime})
		}
		if f.EndTime > 0 {
			timeFilter = append(timeFilter, bson.E{Key: "$lte", Value: f.EndTime})
		}
		filter = append(filter, bson.E{Key: "createTime", Value: timeFilter})
	}
	return filter
}

// FindWaitOrders 查找待支付订单（paymentStatus == WAIT），供模拟补单定时任务使用。
// 仅限 local/dev 环境调用；按创建时间升序捞取，避免积压订单被无限推迟。
func (r *PaymentOrderRepo) FindWaitOrders(ctx context.Context, limit int64) ([]*model.PaymentOrder, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "createTime", Value: 1}}).
		SetLimit(limit)
	cursor, err := r.Collection().Find(ctx,
		bson.D{{Key: "paymentStatus", Value: model.PaymentStatusWait}},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var orders []*model.PaymentOrder
	if err = cursor.All(ctx, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// FindPendingNotifyOrders 查找待补通知的订单（定时任务使用）。
// 筛选条件：支付成功、通知未成功、累计次数未超限、支付时间在有效窗口内。
// 同时施加两个上限，满足任一则不再捞取，需人工介入：
//   - notifyCount < NotifyMaxRetryCount：累计通知次数未超限
//   - payTime >= now-NotifyMaxRetryDuration：支付成功时间在有效窗口内
func (r *PaymentOrderRepo) FindPendingNotifyOrders(ctx context.Context, limit int64) ([]*model.PaymentOrder, error) {
	earliestPayTime := time.Now().Add(-model.NotifyMaxRetryDuration).UnixMilli()
	opts := options.Find().
		SetSort(bson.D{{Key: "lastNotifyTime", Value: 1}}).
		SetLimit(limit)
	cursor, err := r.Collection().Find(ctx,
		bson.D{
			{Key: "paymentStatus", Value: model.PaymentStatusSuccess},
			{Key: "notifyStatus", Value: bson.D{{Key: "$ne", Value: model.NotifyStatusSuccess}}},
			{Key: "notifyCount", Value: bson.D{{Key: "$lt", Value: model.NotifyMaxRetryCount}}},
			{Key: "payTime", Value: bson.D{{Key: "$gte", Value: earliestPayTime}}},
		},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var orders []*model.PaymentOrder
	if err = cursor.All(ctx, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}
