package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// PayOrderRepo 业务订单数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type PayOrderRepo struct {
	mongox.BaseRepo[model.PayOrder]
}

// NewPayOrderRepo 创建业务订单仓库实例
func NewPayOrderRepo(client *mongox.Client) *PayOrderRepo {
	return &PayOrderRepo{BaseRepo: mongox.NewBaseRepo[model.PayOrder](client, collectionPayOrders)}
}

// InsertOrder 创建新业务订单，自动填充时间戳
func (r *PayOrderRepo) InsertOrder(ctx context.Context, order *model.PayOrder) error {
	now := time.Now()
	order.CreatedAt = now
	order.UpdatedAt = now
	id, err := r.Insert(ctx, order)
	if err != nil {
		return err
	}
	order.ID = id
	return nil
}

// FindByOrderID 根据订单号查找业务订单
func (r *PayOrderRepo) FindByOrderID(ctx context.Context, orderID string) (*model.PayOrder, error) {
	return r.FindOne(ctx, bson.D{{Key: "orderId", Value: orderID}})
}

// GameOrderQueryFilter GMT 业务订单查询过滤条件
type GameOrderQueryFilter struct {
	// OrderID 精确匹配订单号（非空时忽略其他条件）
	OrderID string
	// PlayerID 玩家唯一标识（>0 时过滤）
	PlayerID int64
	// Status 业务状态：WAIT_PAY / PAID / DELIVERED / CLOSED（空表示不过滤）
	Status string
	// Channel 支付渠道：apple / google（空表示不过滤）
	Channel string
	// StartTime 创建时间下限（Unix 毫秒，0 表示不过滤）
	StartTime int64
	// EndTime 创建时间上限（Unix 毫秒，0 表示不过滤）
	EndTime int64
}

// PageQueryOrders GMT 运营后台分页查询业务订单。
// 当 filter.OrderID 非空时，精确匹配单条记录并包装为分页结果；
// 否则按 PlayerID / Status / Channel / 时间范围组合过滤并按创建时间倒序分页。
func (r *PayOrderRepo) PageQueryOrders(ctx context.Context, filter GameOrderQueryFilter, page, pageSize int64) (*mongox.PageResult[model.PayOrder], error) {
	return mongox.Paginate[model.PayOrder](ctx, r.Collection(),
		r.buildQueryFilter(filter),
		mongox.PageQuery{
			Page:     page,
			PageSize: pageSize,
			Sort:     bson.D{{Key: "createdAt", Value: -1}},
		},
	)
}

// buildQueryFilter 将结构化过滤条件转换为 bson.D，供 Paginate 使用。
// CreatedAt 在文档中为 time.Time，故将入参的 Unix 毫秒转换为 time.Time 进行范围比较。
func (r *PayOrderRepo) buildQueryFilter(f GameOrderQueryFilter) bson.D {
	// 精确匹配 orderId 时直接返回，忽略其他字段
	if f.OrderID != "" {
		return bson.D{{Key: "orderId", Value: f.OrderID}}
	}

	filter := bson.D{}
	if f.PlayerID > 0 {
		filter = append(filter, bson.E{Key: "playerId", Value: f.PlayerID})
	}
	if f.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: f.Status})
	}
	if f.Channel != "" {
		filter = append(filter, bson.E{Key: "channel", Value: f.Channel})
	}
	if f.StartTime > 0 || f.EndTime > 0 {
		timeFilter := bson.D{}
		if f.StartTime > 0 {
			timeFilter = append(timeFilter, bson.E{Key: "$gte", Value: time.UnixMilli(f.StartTime)})
		}
		if f.EndTime > 0 {
			timeFilter = append(timeFilter, bson.E{Key: "$lte", Value: time.UnixMilli(f.EndTime)})
		}
		filter = append(filter, bson.E{Key: "createdAt", Value: timeFilter})
	}
	return filter
}

// FindByPlayerAndStatus 查找指定玩家指定状态的订单列表（分页）
func (r *PayOrderRepo) FindByPlayerAndStatus(ctx context.Context, playerID int64, status string, limit int64) ([]*model.PayOrder, error) {
	filter := bson.D{{Key: "playerId", Value: playerID}}
	if status != "" {
		filter = append(filter, bson.E{Key: "status", Value: status})
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "createdAt", Value: -1}}).
		SetLimit(limit)
	cursor, err := r.Collection().Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var orders []*model.PayOrder
	if err = cursor.All(ctx, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// CASResult 原子状态流转结果
type CASResult int8

const (
	// CASNotFound 订单不存在（MatchedCount == 0 且无文档匹配）
	CASNotFound CASResult = iota
	// CASSkipped 订单存在但状态不匹配（已被其他请求流转，幂等场景）
	CASSkipped
	// CASSucceeded 本次流转成功（唯一赢家）
	CASSucceeded
)

// casTransition 基于状态条件的原子状态机流转。
// 仅当订单当前 status==fromStatus 时，才原子地翻转为 toStatus。
// 该操作在单文档上由 MongoDB 保证原子性，是并发安全的核心：
// 无论多少并发请求，只有一个能成功将订单从 fromStatus 翻转为 toStatus，其余返回 CASSkipped。
// 通过先以无状态条件查询 matchedCount，区分"订单不存在"与"状态已流转"两种情况，
// 从而在单次网络往返内同时完成原子写入和存在性校验。
//
// 参数：
//   - orderID:     订单号
//   - fromStatus:  期望的当前状态（前置条件）
//   - toStatus:    目标状态
//   - extraFields: 随状态一同写入的附加字段，可为 nil
//
// 返回：CASResult（CASSucceeded / CASSkipped / CASNotFound）以及数据库错误
func (r *PayOrderRepo) casTransition(ctx context.Context, orderID, fromStatus, toStatus string, extraFields bson.D) (CASResult, error) {
	setFields := bson.D{
		{Key: "status", Value: toStatus},
		{Key: "updatedAt", Value: time.Now()},
	}
	if len(extraFields) > 0 {
		setFields = append(setFields, extraFields...)
	}
	filter := bson.D{
		{Key: "orderId", Value: orderID},
		{Key: "status", Value: fromStatus},
	}
	result, err := r.Collection().UpdateOne(ctx,
		filter,
		bson.D{
			{Key: "$set", Value: setFields},
		},
	)
	if err != nil {
		return CASNotFound, err
	}
	if result.ModifiedCount > 0 {
		return CASSucceeded, nil
	}
	// ModifiedCount==0：有两种情况——订单不存在 or 状态已流转。
	// 追加一次无状态条件的 CountDocuments 区分，代价极低（命中索引 orderId）。
	count, err := r.Collection().CountDocuments(ctx, bson.D{{Key: "orderId", Value: orderID}})
	if err != nil {
		return CASNotFound, err
	}
	if count == 0 {
		return CASNotFound, nil
	}
	return CASSkipped, nil
}

// MarkPaid 原子标记订单为已支付（WAIT_PAY → PAID），记录通知ID、支付时间和支付金额。
// 并发的多次支付通知中只有一个会成功，天然幂等。
// 金额随支付成功回调一并落库，作为用户标签付费统计的数据源。
// 返回 CASResult：CASSucceeded 本次成功；CASSkipped 已被并发推进（幂等ACK）；CASNotFound 订单不存在。
func (r *PayOrderRepo) MarkPaid(ctx context.Context, orderID, notifyID string, amount int64) (CASResult, error) {
	return r.casTransition(ctx, orderID, model.PayOrderStatusWaitPay, model.PayOrderStatusPaid, bson.D{
		{Key: "notifyId", Value: notifyID},
		{Key: "paidAt", Value: time.Now()},
		{Key: "amount", Value: amount},
	})
}

// MarkDelivered 原子标记订单为已发奖（PAID → DELIVERED），写入通知ID。
// 基于 status==PAID 的条件更新，保证并发下只有一个请求能闭环，天然幂等（单文档原子更新）。
// 返回 CASResult：CASSucceeded 本次成功；CASSkipped 已被并发推进；CASNotFound 订单不存在。
func (r *PayOrderRepo) MarkDelivered(ctx context.Context, orderID, notifyID string) (CASResult, error) {
	return r.casTransition(ctx, orderID, model.PayOrderStatusPaid, model.PayOrderStatusDelivered, bson.D{
		{Key: "notifyId", Value: notifyID},
		{Key: "deliveredAt", Value: time.Now()},
	})
}

// CloseExpired 原子关闭过期未支付订单（WAIT_PAY → CLOSED）。
// 基于状态条件，避免误关已支付订单，并发安全。
// 返回 CASResult：CASSucceeded 成功关闭；CASSkipped 已被并发关闭或已支付；CASNotFound 订单不存在。
func (r *PayOrderRepo) CloseExpired(ctx context.Context, orderID string) (CASResult, error) {
	return r.casTransition(ctx, orderID, model.PayOrderStatusWaitPay, model.PayOrderStatusClosed, nil)
}

// ReopenClosed 重新激活已关闭订单（CLOSED → PAID），记录通知ID、支付时间和支付金额。
// 适用场景：订单超时关闭后，GMT 手动补单或渠道延迟入账触发的支付通知仍未完成；
// 此时订单在 Platform 侧已确认收款，本方法将订单重新激活为 PAID，供客户端正常领奖。
// 基于 status==CLOSED 的条件更新，防止与正常支付流程冲突，并发安全。
// 返回 CASResult：CASSucceeded 重新激活成功；CASSkipped 已被并发推进（幂等场景）；CASNotFound 订单不存在。
func (r *PayOrderRepo) ReopenClosed(ctx context.Context, orderID, notifyID string, amount int64) (CASResult, error) {
	return r.casTransition(ctx, orderID, model.PayOrderStatusClosed, model.PayOrderStatusPaid, bson.D{
		{Key: "notifyId", Value: notifyID},
		{Key: "paidAt", Value: time.Now()},
		{Key: "amount", Value: amount},
	})
}

// CloseAllExpired 批量关闭所有过期未支付订单（定时任务专用）。
// 分三步：
//  1. 投影查询：仅拉取过期 WAIT_PAY 订单的 orderId 和 playerId（不加载完整文档）
//  2. UpdateMany：按 orderId $in + status==WAIT_PAY 条件原子批量关闭
//  3. 回查：取本批中确实已变为 CLOSED 的订单作为精确返回值
//
// 步骤2的状态条件保证并发安全：若某订单在步骤间恰好被支付成功，UpdateMany 不会命中它。
// 步骤3确保返回值仅包含真正被关闭的订单，避免调用方为已支付订单误写“超时关闭”事件流水。
//
// 参数：
//   - limit: 单次最多处理的订单数，防止单批次过大
func (r *PayOrderRepo) CloseAllExpired(ctx context.Context, limit int64) ([]*model.PayOrder, error) {
	// 第一步：投影查询，只取 orderId + playerId，节省内存带宽
	opts := options.Find().
		SetSort(bson.D{{Key: "expireTime", Value: 1}}).
		SetLimit(limit).
		SetProjection(bson.D{
			{Key: "orderId", Value: 1},
			{Key: "playerId", Value: 1},
		})
	cursor, err := r.Collection().Find(ctx,
		bson.D{
			{Key: "status", Value: model.PayOrderStatusWaitPay},
			{Key: "expireTime", Value: bson.D{{Key: "$lte", Value: time.Now()}}},
		},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var candidates []*model.PayOrder
	if err = cursor.All(ctx, &candidates); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	// 收集 orderId 列表
	orderIDs := make([]string, 0, len(candidates))
	for _, o := range candidates {
		orderIDs = append(orderIDs, o.OrderID)
	}

	// 第二步：UpdateMany，带 status==WAIT_PAY 条件，并发安全
	result, err := r.Collection().UpdateMany(ctx,
		bson.D{
			{Key: "orderId", Value: bson.D{{Key: "$in", Value: orderIDs}}},
			{Key: "status", Value: model.PayOrderStatusWaitPay},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: model.PayOrderStatusClosed},
			{Key: "updatedAt", Value: time.Now()},
		}}},
	)
	if err != nil {
		return nil, err
	}
	if result.ModifiedCount == 0 {
		return nil, nil
	}

	// 第三步：回查本批中确实已变为 CLOSED 的订单，返回精确的实际关闭列表。
	// 必要性：候选订单可能在第一、二步之间被支付成功（WAIT_PAY→PAID），UpdateMany 的
	// status==WAIT_PAY 条件不会命中它，但它仍在 candidates 中。若直接返回 candidates，
	// 调用方会为这笔已支付订单错误地写入“超时关闭”事件流水，污染审计。
	// 回查仅取投影字段，命中 orderId 索引，开销可控。
	closedOpts := options.Find().SetProjection(bson.D{
		{Key: "orderId", Value: 1},
		{Key: "playerId", Value: 1},
	})
	closedCursor, err := r.Collection().Find(ctx,
		bson.D{
			{Key: "orderId", Value: bson.D{{Key: "$in", Value: orderIDs}}},
			{Key: "status", Value: model.PayOrderStatusClosed},
		},
		closedOpts,
	)
	if err != nil {
		return nil, err
	}
	defer closedCursor.Close(ctx)

	var closed []*model.PayOrder
	if err = closedCursor.All(ctx, &closed); err != nil {
		return nil, err
	}
	return closed, nil
}

// FindPaidOrdersByPlayerID 查询玩家所有已支付待领取（PAID 状态）的订单
// 弱联网场景下客户端主动拉取，用于展示待领取奖励列表
func (r *PayOrderRepo) FindPaidOrdersByPlayerID(ctx context.Context, playerID int64) ([]*model.PayOrder, error) {
	opts := options.Find().
		SetSort(bson.D{{Key: "paidAt", Value: 1}}) // 按支付时间升序，先支付先领
	cursor, err := r.Collection().Find(ctx,
		bson.D{
			{Key: "playerId", Value: playerID},
			{Key: "status", Value: model.PayOrderStatusPaid},
		},
		opts,
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var orders []*model.PayOrder
	if err = cursor.All(ctx, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

// PayStats 玩家付费统计结果（分为单位）
type PayStats struct {
	// LastPay 最近1笔付费金额
	LastPay int64
	// Last4Pay 最近4笔付费合计
	Last4Pay int64
	// MonthPay 最近30天付费合计
	MonthPay int64
	// TotalPay 累积付费合计
	TotalPay int64
}

// FindPaidStatsByPlayerID 聚合查询玩家付费统计。
// 统计口径：仅计入业务状态为 PAID/DELIVERED 的订单。
// 实现：单次 $match + $group，用 $topN 在 DB 端取最近4笔金额，与 $sum 汇总在同一次扫描内完成——
//   - totalPay：全生命周期累积付费（$sum）；
//   - monthPay：最近30天付费（$sum + paidAt 时间条件约束）；
//   - recent4：最近4笔金额（$topN，sortBy paidAt 倒序），在应用层展开为最近1笔与最近4笔合计。
//
// 相比 $facet 双子管道：仅一次索引扫描、内存只保留 top4（无全量 sort 缓冲）、回传仅3个聚合值。
// 注意：$topN 需 MongoDB 5.2+，服务端版本过低时聚合报错（调用方已降级，付费条件按不满足处理）。
// 性能说明：每次标签匹配请求仅调用一次，结果复用于所有配置行。
func (r *PayOrderRepo) FindPaidStatsByPlayerID(ctx context.Context, playerID int64) (PayStats, error) {
	var stats PayStats
	monthStart := time.Now().Add(-30 * 24 * time.Hour)
	match := bson.D{
		{Key: "playerId", Value: playerID},
		{Key: "status", Value: bson.D{{Key: "$in", Value: []string{model.PayOrderStatusPaid, model.PayOrderStatusDelivered}}}},
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: nil},
			{Key: "totalPay", Value: bson.D{{Key: "$sum", Value: "$amount"}}},
			{Key: "monthPay", Value: bson.D{{Key: "$sum", Value: bson.D{
				{Key: "$cond", Value: bson.A{
					bson.D{{Key: "$gte", Value: bson.A{"$paidAt", monthStart}}},
					"$amount",
					0,
				}},
			}}}},
			{Key: "recent4", Value: bson.D{{Key: "$topN", Value: bson.D{
				{Key: "n", Value: 4},
				{Key: "sortBy", Value: bson.D{{Key: "paidAt", Value: -1}}},
				// 存量订单可能缺失 amount 字段，用 $ifNull 归 0（与 $sum 对缺失字段按 0 计的口径一致），
				// 避免返回 BSON null 导致应用层解码 []int64 失败
				{Key: "output", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$amount", 0}}}},
			}}}},
		}}},
	}

	var result struct {
		TotalPay int64   `bson:"totalPay"`
		MonthPay int64   `bson:"monthPay"`
		Recent4  []int64 `bson:"recent4"`
	}
	cursor, err := r.Collection().Aggregate(ctx, pipeline)
	if err != nil {
		return stats, err
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		if err = cursor.Decode(&result); err != nil {
			return stats, err
		}
	}
	if err = cursor.Err(); err != nil {
		return stats, err
	}
	stats.TotalPay = result.TotalPay
	stats.MonthPay = result.MonthPay
	// $topN 已按 paidAt 倒序返回，第一项即最近1笔
	for i, amount := range result.Recent4 {
		if i == 0 {
			stats.LastPay = amount
		}
		stats.Last4Pay += amount
	}
	return stats, nil
}
