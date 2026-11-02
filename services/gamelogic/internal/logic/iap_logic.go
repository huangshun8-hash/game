package logic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/signx"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/gmtdto"
	"kemiluo/services/shared/iapdto"
	"kemiluo/services/shared/orderid"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const platformCreateOrderPath = "/internal/iap/paymentOrder"
const orderExpireDuration = 30 * time.Minute

// paySuccessPushTimeout 支付成功推送发布的独立超时上限，隔离于支付回调请求生命周期
const paySuccessPushTimeout = 5 * time.Second

// IAPLogic 内购业务逻辑（GameLogic 侧）
type IAPLogic struct {
	mongoSvc      *mongox.Service
	platformSvc   *httpcli.Service
	redisCli      *redisx.Client
	payStatsCache *cache.PayStatsCache
}

// NewIAPLogic 创建内购业务逻辑
// redisCli: Redis 客户端，支付成功后用于向跨服广播频道发布 WS 推送消息，为 nil 时跳过推送
// payStatsCache: 玩家付费统计缓存；支付成功后用于失效过期统计，为 nil 时跳过失效（TTL 兜底）
func NewIAPLogic(mongoSvc *mongox.Service, platformSvc *httpcli.Service, redisCli *redisx.Client, payStatsCache *cache.PayStatsCache) *IAPLogic {
	return &IAPLogic{mongoSvc: mongoSvc, platformSvc: platformSvc, redisCli: redisCli, payStatsCache: payStatsCache}
}

func (l *IAPLogic) repos() *repo.PayOrderRepo {
	client := l.mongoSvc.Client()
	return repo.NewPayOrderRepo(client)
}

// CreateOrder 创建业务订单
// req: proto 请求消息（业务字段）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *IAPLogic) CreateOrder(ctx context.Context, req *cspb.CreateOrderReq, requestID string, playerID int64) (*cspb.CreateOrderResp, error) {
	payOrderRepo := l.repos()

	// 每次创建请求均生成全新订单号，不再复用历史未支付订单：
	// 避免同玩家同商品的历史订单被反复复用，导致订单号、创建时间与支付渠道被固化，
	// 也使每笔支付意图都有独立订单可追溯。订单号由雪花算法生成，理论上不会碰撞。
	order := &model.PayOrder{
		OrderID:    orderid.New(),
		PlayerID:   playerID,
		ProductID:  req.PayId,
		Channel:    req.Channel,
		AwardTag:   req.AwardTag,
		Award:      req.Award,
		Status:     model.PayOrderStatusWaitPay,
		ExpireTime: time.Now().Add(orderExpireDuration),
	}
	if err := payOrderRepo.InsertOrder(ctx, order); err != nil {
		if mongox.IsDuplicateKeyError(err) {
			// 订单号唯一索引冲突：不复用已有订单，直接要求客户端重试
			logx.Error("业务订单号碰撞",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("orderId", order.OrderID),
				zap.String("payId", req.PayId),
				zap.String("awardTag", req.AwardTag),
				zap.Error(err),
			)
			return nil, fmt.Errorf("order id collision, please retry")
		}
		logx.Error("本地业务订单落库失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", order.OrderID),
			zap.String("payId", req.PayId),
			zap.String("awardTag", req.AwardTag),
			zap.Error(err),
		)
		return nil, fmt.Errorf("save order failed: %w", err)
	}

	// 新建本地订单后调用 Platform 建支付单，保证 Platform 侧支付单与本单一一对应。
	// Platform 的 CreatePaymentOrder 对同 orderId + 同 playerId 幂等，重复调用不会产生重复支付单。
	if _, err := l.callPlatformCreateOrder(ctx, order.OrderID, playerID, order.ProductID, order.Channel, requestID); err != nil {
		logx.Error("调用Platform创建支付订单失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", order.OrderID),
			zap.String("payId", order.ProductID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("create payment order failed: %w", err)
	}

	logx.Info("业务订单创建成功",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("orderId", order.OrderID),
		zap.String("payId", order.ProductID),
		zap.String("awardTag", order.AwardTag),
		zap.String("channel", order.Channel),
	)
	return newCreateOrderResp(order), nil
}

// newCreateOrderResp 根据业务订单构造创建订单响应。
func newCreateOrderResp(order *model.PayOrder) *cspb.CreateOrderResp {
	return &cspb.CreateOrderResp{
		Data: &cspb.CreateOrderData{
			OrderId:  order.OrderID,
			PayId:    order.ProductID,
			Channel:  order.Channel,
			AwardTag: order.AwardTag,
			Award:    order.Award,
		},
	}
}

// HandlePayNotify 处理 Platform 支付成功通知（内部接口）
// 通知来源仅为真实支付成功或 GMT 手动补单（Platform 侧确认收款后才发送）：
//   - WAIT_PAY → PAID：常规首次入账；
//   - CLOSED → PAID：GMT 手动补单不受限制；渠道来源携带有效三方交易号时同样自动重新激活
//     超时关闭订单（处理与 GMT 补单一致，不做时间窗口限制），否则拒绝并需运营介入；
//   - PAID / DELIVERED：幂等 ACK，不重复流转。
//
// 订单成功流转为 PAID（首次入账或重开）后，经 Redis Pub/Sub 跨服广播 WS 推送，
// 通知玩家订单已入账可领取（见 publishPaySuccess）；幂等 ACK 路径不重复推送。
func (l *IAPLogic) HandlePayNotify(ctx context.Context, req *iapdto.PayNotifyReq, requestID string) (*iapdto.PayNotifyResp, error) {
	payOrderRepo := l.repos()

	// 入参校验：通知必须携带有效玩家ID，防止异常通知绕过订单归属约束
	if req.PlayerID <= 0 {
		logx.Warn("支付通知缺少有效玩家ID",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("notifyId", req.NotifyID),
			zap.Int64("playerId", req.PlayerID),
		)
		return nil, fmt.Errorf("invalid player id in pay notify")
	}

	order, err := payOrderRepo.FindByOrderID(ctx, req.OrderID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return nil, model.ErrOrderNotFound
		}
		return nil, fmt.Errorf("query order failed: %w", err)
	}
	// 玩家归属校验：订单必须归属通知声称的玩家，防止跨玩家串单或误激活
	if order.PlayerID != req.PlayerID {
		logx.Error("支付通知玩家ID与订单归属不一致，拒绝处理",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("notifyId", req.NotifyID),
			zap.Int64("userId", req.PlayerID),
			zap.Int64("orderPlayerId", order.PlayerID),
		)
		return nil, model.ErrOrderNotOwned
	}

	switch order.Status {
	case model.PayOrderStatusPaid, model.PayOrderStatusDelivered:
		// 已支付（含已发奖）订单：仅幂等 ACK，不重复流转
		logx.Info("订单已处理，幂等ACK",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("notifyId", req.NotifyID),
			zap.String("status", order.Status),
		)
		return &iapdto.PayNotifyResp{OrderID: req.OrderID, IsPaid: true}, nil
	case model.PayOrderStatusClosed:
		// 超时关闭订单的重开资格判定（处理与 GMT 手动补单一致，不做时间窗口限制）：
		//  - GMT 来源（后台人工补单）：直接重开；
		//  - 渠道来源：携带有效三方交易号（Platform 已确认收款）即自动重开，
		//    覆盖玩家支付流程长/回调延迟导致的"订单先关闭、后到账"。
		canReopen := req.Source == iapdto.NotifySourceGMT ||
			(req.Source == iapdto.NotifySourceChannel && req.ThirdTransactionID != "")
		if !canReopen {
			logx.Warn("已关闭订单的通知来源不满足自动重开条件，拒绝重开，需GMT后台手动补单",
				zap.String("requestId", requestID),
				zap.String("orderId", req.OrderID),
				zap.String("notifyId", req.NotifyID),
				zap.String("source", req.Source),
				zap.String("thirdTransactionID", req.ThirdTransactionID),
				zap.Int64("userId", order.PlayerID),
			)
			return nil, model.ErrOrderStatusInvalid
		}
		// 可自动重开（GMT 补单或携带有效三方交易号的渠道补单）：CAS 重新激活 CLOSED → PAID，激活后客户端正常领奖
		cas, reopenErr := payOrderRepo.ReopenClosed(ctx, req.OrderID, req.NotifyID, req.Amount)
		if reopenErr != nil {
			return nil, fmt.Errorf("reopen closed order failed: %w", reopenErr)
		}
		switch cas {
		case repo.CASNotFound:
			return nil, model.ErrOrderNotFound
		case repo.CASSkipped:
			// 并发下已被其他通知/补单推进：回查最新状态，PAID/DELIVERED 视为幂等成功
			if l.isPaidFinalState(ctx, payOrderRepo, req.OrderID, requestID) {
				return &iapdto.PayNotifyResp{OrderID: req.OrderID, IsPaid: true}, nil
			}
			return nil, model.ErrOrderStatusInvalid
		}
	case model.PayOrderStatusWaitPay:
		// 常规入账：WAIT_PAY → PAID
		cas, markErr := payOrderRepo.MarkPaid(ctx, req.OrderID, req.NotifyID, req.Amount)
		if markErr != nil {
			return nil, fmt.Errorf("mark paid failed: %w", markErr)
		}
		switch cas {
		case repo.CASNotFound:
			return nil, model.ErrOrderNotFound
		case repo.CASSkipped:
			// 并发下已由其他通知推进：回查最新状态，PAID/DELIVERED 幂等 ACK
			if l.isPaidFinalState(ctx, payOrderRepo, req.OrderID, requestID) {
				return &iapdto.PayNotifyResp{OrderID: req.OrderID, IsPaid: true}, nil
			}
			return nil, model.ErrOrderStatusInvalid
		}
	default:
		logx.Warn("收到未知订单状态的通知",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("notifyId", req.NotifyID),
			zap.String("status", order.Status),
		)
		return nil, model.ErrOrderStatusInvalid
	}

	logx.Info("订单已标记为PAID，等待客户端主动领奖",
		zap.String("requestId", requestID),
		zap.String("orderId", req.OrderID),
		zap.String("notifyId", req.NotifyID),
	)
	// 支付成功后付费统计已变化，主动失效缓存，保证下一次标签匹配即时反映最新付费；
	// 失效失败仅记录日志，TTL 兜底保证数据最终一致。
	if l.payStatsCache != nil {
		if err := l.payStatsCache.Del(ctx, req.PlayerID); err != nil {
			logx.Warn("失效付费统计缓存失败",
				zap.String("requestId", requestID),
				zap.String("orderId", req.OrderID),
				zap.Int64("userId", req.PlayerID),
				zap.Error(err),
			)
		}
	}
	// 支付成功：跨服广播 WS 推送通知，提示玩家订单已入账可领取。
	// 仅真实入账（CAS 流转成功走到这里）才通知，幂等 ACK 路径不推送，避免客户端收到重复通知。
	l.publishPaySuccess(req.PlayerID, order, requestID)
	return &iapdto.PayNotifyResp{OrderID: req.OrderID, IsPaid: true}, nil
}

// publishPaySuccess 支付成功后跨服广播 WS 推送通知。
// 多服务器部署下玩家 WS 连接所在实例与支付回调处理实例可能不同（每实例 Hub 仅管理本地连接），
// 故将推送消息发布到共享 Redis 频道，各 GameLogic 实例订阅后按 PlayerID 路由到各自 Hub 下发。
// 发布属于尽力而为的补充通知，不依赖支付回调请求生命周期：在独立带超时的后台上下文上执行，
// 避免 Fiber 请求被取消或超时时通知随请求丢失；redisCli 为空（Redis 未就绪）时仅记录告警后跳过，不影响支付回调主流程。
func (l *IAPLogic) publishPaySuccess(playerID int64, order *model.PayOrder, requestID string) {
	if l.redisCli == nil {
		logx.Warn("Redis 客户端未就绪，跳过支付成功WS通知",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", order.OrderID),
		)
		return
	}

	msg := iapdto.PaySuccessPushMessage{
		PlayerID: playerID,
		Data: iapdto.PaySuccessPushData{
			OrderID:   order.OrderID,
			ProductID: order.ProductID,
			Channel:   order.Channel,
			AwardTag:  order.AwardTag,
			Award:     order.Award,
			PaidAt:    time.Now().UnixMilli(),
		},
	}
	body, err := sonic.Marshal(msg)
	if err != nil {
		logx.Warn("序列化支付成功推送消息失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", order.OrderID),
			zap.Error(err),
		)
		return
	}

	// PUBLISH 返回收到消息的订阅者数量（0 表示当前无任何实例订阅或仅发布实例自身在订阅，属正常情况）
	// 使用独立后台上下文并设置超时，隔离于支付回调请求生命周期，超时后放弃本次通知（可依赖客户端轮询兜底）
	publishCtx, cancel := context.WithTimeout(context.Background(), paySuccessPushTimeout)
	defer cancel()
	subscribers, err := l.redisCli.Raw().Publish(publishCtx, iapdto.PaySuccessPushChannel, body).Result()
	if err != nil {
		logx.Warn("发布支付成功推送消息失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", order.OrderID),
			zap.Error(err),
		)
		return
	}
	logx.Info("支付成功WS通知已跨服广播",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("orderId", order.OrderID),
		zap.Int64("subscribers", subscribers),
	)
}

// isPaidFinalState 回查订单最新状态，判断是否已处于 PAID / DELIVERED 终态（幂等 ACK 判定）。
// 仅用于 CAS 流转被并发推进后的回查路径；订单不存在或查询失败时返回 false，由调用方按错误处理。
func (l *IAPLogic) isPaidFinalState(ctx context.Context, payOrderRepo *repo.PayOrderRepo, orderID, requestID string) bool {
	latest, queryErr := payOrderRepo.FindByOrderID(ctx, orderID)
	if queryErr != nil {
		logx.Warn("支付通知并发回查订单状态失败",
			zap.String("requestId", requestID),
			zap.String("orderId", orderID),
			zap.Error(queryErr),
		)
		return false
	}
	if latest.Status == model.PayOrderStatusPaid || latest.Status == model.PayOrderStatusDelivered {
		logx.Info("订单已由并发流程处理，幂等ACK",
			zap.String("requestId", requestID),
			zap.String("orderId", orderID),
			zap.String("status", latest.Status),
		)
		return true
	}
	return false
}

// ListPaidOrders 查询玩家所有已支付待领取订单
// req: proto 请求消息（无业务字段）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *IAPLogic) ListPaidOrders(ctx context.Context, req *cspb.ListPaidOrdersReq, requestID string, playerID int64) (*cspb.ListPaidOrdersResp, error) {
	payOrderRepo := l.repos()

	orders, err := payOrderRepo.FindPaidOrdersByPlayerID(ctx, playerID)
	if err != nil {
		logx.Error("查询待领取订单失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("query paid orders failed: %w", err)
	}

	items := make([]*cspb.PaidOrderItem, 0, len(orders))
	for _, o := range orders {
		var paidAt int64
		if o.PaidAt != nil {
			paidAt = o.PaidAt.UnixMilli()
		}
		items = append(items, &cspb.PaidOrderItem{
			OrderId:  o.OrderID,
			PayId:    o.ProductID,
			Channel:  o.Channel,
			AwardTag: o.AwardTag,
			Award:    o.Award,
			PaidAt:   paidAt,
		})
	}

	return &cspb.ListPaidOrdersResp{
		Data: &cspb.ListPaidOrdersData{Orders: items},
	}, nil
}

// ClaimReward 客户端主动领取奖励
// req: proto 请求消息（orderId、reward）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *IAPLogic) ClaimReward(ctx context.Context, req *cspb.ClaimRewardReq, requestID string, playerID int64) (*cspb.ClaimRewardResp, error) {
	payOrderRepo := l.repos()

	order, err := payOrderRepo.FindByOrderID(ctx, req.OrderId)
	if err != nil {
		if err == mongox.ErrNotFound {
			return nil, model.ErrOrderNotFound
		}
		return nil, fmt.Errorf("query order failed: %w", err)
	}
	if order.PlayerID != playerID {
		return nil, model.ErrOrderNotFound
	}
	if order.Status == model.PayOrderStatusDelivered {
		logx.Warn("订单已领取，拒绝重复领奖",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", req.OrderId),
		)
		return nil, model.ErrOrderAlreadyClaimed
	}
	if order.Status != model.PayOrderStatusPaid {
		logx.Warn("订单状态不允许领取",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", req.OrderId),
			zap.String("status", order.Status),
		)
		return nil, model.ErrOrderStatusInvalid
	}

	reward := req.Reward
	cas, markErr := payOrderRepo.MarkDelivered(ctx, order.OrderID, order.NotifyID)
	if markErr != nil {
		logx.Error("标记订单已发奖失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", req.OrderId),
			zap.Error(markErr),
		)
		return nil, fmt.Errorf("mark order delivered failed: %w", markErr)
	}
	switch cas {
	case repo.CASNotFound:
		logx.Error("领奖时订单不存在",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", req.OrderId),
		)
		return nil, model.ErrOrderStatusInvalid
	case repo.CASSkipped:
		// 并发下已由其他请求完成：回查最新状态，DELIVERED 视为幂等成功
		if latest, queryErr := payOrderRepo.FindByOrderID(ctx, req.OrderId); queryErr == nil && latest.Status == model.PayOrderStatusDelivered {
			logx.Info("并发领奖已由其他请求完成，幂等返回",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("orderId", req.OrderId),
			)
			return &cspb.ClaimRewardResp{
				Data: &cspb.ClaimRewardData{OrderId: req.OrderId, Status: latest.Status},
			}, nil
		}
		logx.Warn("领奖期间订单状态异常，拒绝领奖",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("orderId", req.OrderId),
		)
		return nil, model.ErrOrderStatusInvalid
	}

	logx.Info("玩家领奖成功",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("orderId", req.OrderId),
		zap.Any("reward", reward),
	)
	return &cspb.ClaimRewardResp{
		Data: &cspb.ClaimRewardData{OrderId: req.OrderId, Status: model.PayOrderStatusDelivered},
	}, nil
}

// GmtQueryOrders GMT 运营后台分页查询（内部接口，保持 JSON）
func (l *IAPLogic) GmtQueryOrders(ctx context.Context, req *gmtdto.GmtQueryGameOrderReq, requestID string) (*gmtdto.GmtQueryGameOrderResp, error) {
	payOrderRepo := l.repos()

	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	filter := repo.GameOrderQueryFilter{
		OrderID:   req.OrderID,
		PlayerID:  req.PlayerID,
		Status:    req.Status,
		Channel:   req.Channel,
		StartTime: req.StartTime,
		EndTime:   req.EndTime,
	}

	result, err := payOrderRepo.PageQueryOrders(ctx, filter, page, pageSize)
	if err != nil {
		logx.Error("GMT查询业务订单失败",
			zap.String("requestId", requestID),
			zap.Any("filter", filter),
			zap.Error(err),
		)
		return nil, fmt.Errorf("query game orders failed: %w", err)
	}

	items := make([]*gmtdto.GmtGameOrderItem, len(result.Items))
	for i := range result.Items {
		items[i] = toGmtGameOrderItem(&result.Items[i])
	}
	logx.Info("GMT查询业务订单成功",
		zap.String("requestId", requestID),
		zap.Int64("total", result.Total),
		zap.Int64("page", result.Page),
	)
	return &gmtdto.GmtQueryGameOrderResp{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

// GmtQueryPayStats GMT 运营后台查询玩家充值统计（内部接口，保持 JSON）。
// 统计口径与用户标签匹配（usertag_logic.go 的 MatchTags）使用的付费统计完全一致：
// 仅统计 PAID / DELIVERED 订单，单位分，包含最近1笔、最近4笔合计、最近30天合计与累积充值。
// 后台查询直接回源 DB 聚合，不读取付费统计缓存，避免运营看到滞后的数值。
func (l *IAPLogic) GmtQueryPayStats(ctx context.Context, req *gmtdto.GmtQueryPayStatsReq, requestID string) (*gmtdto.GmtPlayerPayStatsResp, error) {
	stats, err := l.repos().FindPaidStatsByPlayerID(ctx, req.PlayerID)
	if err != nil {
		logx.Error("GMT查询玩家充值统计失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", req.PlayerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("query pay stats failed: %w", err)
	}
	logx.Info("GMT查询玩家充值统计成功",
		zap.String("requestId", requestID),
		zap.Int64("userId", req.PlayerID),
	)
	return &gmtdto.GmtPlayerPayStatsResp{
		PlayerID: req.PlayerID,
		LastPay:  stats.LastPay,
		Last4Pay: stats.Last4Pay,
		MonthPay: stats.MonthPay,
		TotalPay: stats.TotalPay,
	}, nil
}

func toGmtGameOrderItem(o *model.PayOrder) *gmtdto.GmtGameOrderItem {
	var paidAt, deliveredAt int64
	if o.PaidAt != nil {
		paidAt = o.PaidAt.UnixMilli()
	}
	if o.DeliveredAt != nil {
		deliveredAt = o.DeliveredAt.UnixMilli()
	}
	return &gmtdto.GmtGameOrderItem{
		OrderID:     o.OrderID,
		PlayerID:    o.PlayerID,
		ProductID:   o.ProductID,
		Channel:     o.Channel,
		Status:      o.Status,
		AwardTag:    o.AwardTag,
		Award:       o.Award,
		NotifyID:    o.NotifyID,
		Amount:      o.Amount,
		PaidAt:      paidAt,
		DeliveredAt: deliveredAt,
		ExpireTime:  o.ExpireTime.UnixMilli(),
		CreateTime:  o.CreatedAt.UnixMilli(),
		UpdateTime:  o.UpdatedAt.UnixMilli(),
	}
}

// GetOrderStatus 查询单个订单状态
// req: proto 请求消息（orderId）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
func (l *IAPLogic) GetOrderStatus(ctx context.Context, req *cspb.GetOrderStatusReq, requestID string, playerID int64) (*cspb.GetOrderStatusResp, error) {
	payOrderRepo := l.repos()
	order, err := payOrderRepo.FindByOrderID(ctx, req.OrderId)
	if err != nil {
		if err == mongox.ErrNotFound {
			return nil, model.ErrOrderNotFound
		}
		return nil, fmt.Errorf("query order failed: %w", err)
	}
	if order.PlayerID != playerID {
		return nil, model.ErrOrderNotFound
	}
	return &cspb.GetOrderStatusResp{
		Data: &cspb.GetOrderStatusData{
			OrderId: order.OrderID,
			PayId:   order.ProductID,
			Channel: order.Channel,
			Status:  order.Status,
		},
	}, nil
}

// CloseExpiredOrders 关闭过期未支付订单（定时任务）
func (l *IAPLogic) CloseExpiredOrders(ctx context.Context) error {
	payOrderRepo := l.repos()
	_, err := payOrderRepo.CloseAllExpired(ctx, 50)
	if err != nil {
		logx.Error("批量关闭过期订单失败", zap.Error(err))
		return err
	}
	return nil
}

func (l *IAPLogic) callPlatformCreateOrder(ctx context.Context, orderID string, playerID int64, productID, channel, requestID string) (*iapdto.CreatePaymentOrderResp, error) {
	return callPlatformInternal[iapdto.CreatePaymentOrderResp](ctx, l.platformSvc, platformCreateOrderPath, iapdto.CreatePaymentOrderReq{
		OrderID:   orderID,
		PlayerID:  playerID,
		ProductID: productID,
		Channel:   channel,
	}, requestID)
}

func callPlatformInternal[T any](ctx context.Context, platformSvc *httpcli.Service, path string, reqBody any, requestID string) (*T, error) {
	bodyBytes, err := sonic.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	signature := signx.Sign([]byte(platformSvc.Secret()), timestamp, nonce, bodyBytes)

	client := platformSvc.Client()
	if client == nil {
		return nil, fmt.Errorf("platform service client not ready")
	}
	resp, err := client.Post(ctx, path, bodyBytes,
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
		httpcli.WithHeader("X-Signature", signature),
		httpcli.WithHeader("X-Timestamp", timestamp),
		httpcli.WithHeader("X-Nonce", nonce),
	)
	if err != nil {
		logx.Error("调用Platform接口失败",
			zap.String("requestId", requestID),
			zap.String("url", path),
			zap.Error(err),
		)
		return nil, fmt.Errorf("call platform %s failed: %w", path, err)
	}
	defer resp.Close()

	if resp.StatusCode() != 200 {
		logx.Error("Platform接口返回非200",
			zap.String("requestId", requestID),
			zap.String("url", path),
			zap.Int("status", resp.StatusCode()),
			zap.String("body", resp.String()),
		)
		return nil, fmt.Errorf("platform %s returned status %d", path, resp.StatusCode())
	}

	var wrapper struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	if err = resp.JSON(&wrapper); err != nil {
		return nil, fmt.Errorf("parse platform response failed: %w", err)
	}
	if wrapper.Code != 0 {
		return nil, fmt.Errorf("platform error: %s", wrapper.Message)
	}

	dataBytes, err := sonic.Marshal(wrapper.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data field failed: %w", err)
	}
	var result T
	if err = sonic.Unmarshal(dataBytes, &result); err != nil {
		return nil, fmt.Errorf("unmarshal data field failed: %w", err)
	}
	return &result, nil
}
