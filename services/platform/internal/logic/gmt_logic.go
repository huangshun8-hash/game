package logic

import (
	"context"
	"errors"
	"fmt"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/platform/internal/repo"
	"kemiluo/services/shared/gmtdto"
	"kemiluo/services/shared/iapdto"

	"go.uber.org/zap"
)

// GmtLogic 运营管理后台（GMT）业务逻辑
// 提供订单查询和补单能力，仅限内部运营系统调用
type GmtLogic struct {
	iapLogic *IAPLogic
}

// NewGmtLogic 创建 GMT 业务逻辑实例
// iapLogic: 复用 IAP 逻辑层的数据访问和通知能力
func NewGmtLogic(iapLogic *IAPLogic) *GmtLogic {
	return &GmtLogic{iapLogic: iapLogic}
}

// QueryOrders GMT 订单分页查询
// 支持按 orderId 精确查询或按 playerId / 状态 / 渠道 / 时间范围分页查询
//
// 参数：
//   - req: 查询条件，orderId 非空时精确查询，否则组合过滤
//   - requestID: 请求追踪ID
func (l *GmtLogic) QueryOrders(ctx context.Context, req *gmtdto.GmtQueryOrderReq, requestID string) (*gmtdto.GmtQueryOrderResp, error) {
	paymentOrderRepo := l.iapLogic.repos()

	// 分页参数归一化
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

	filter := repo.OrderQueryFilter{
		OrderID:       req.OrderID,
		PlayerID:      req.PlayerID,
		PaymentStatus: req.PaymentStatus,
		Channel:       req.Channel,
		StartTime:     req.StartTime,
		EndTime:       req.EndTime,
	}

	result, err := paymentOrderRepo.PageQueryOrders(ctx, filter, page, pageSize)
	if err != nil {
		logx.Error("GMT查询订单失败",
			zap.String("requestId", requestID),
			zap.Any("filter", filter),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询订单失败: %w", err)
	}

	// 将 model.PaymentOrder 转换为 GmtOrderItem，仅暴露运营关心的字段
	items := make([]*gmtdto.GmtOrderItem, len(result.Items))
	for i, o := range result.Items {
		items[i] = toGmtOrderItem(&o)
	}

	logx.Info("GMT查询订单成功",
		zap.String("requestId", requestID),
		zap.Int64("total", result.Total),
		zap.Int64("page", result.Page),
	)

	return &gmtdto.GmtQueryOrderResp{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

// ReplenishOrder GMT 补单（模拟三方支付回调）
// 仅允许对 WAIT 状态订单执行，强制将订单标记为支付成功并触发 GameLogic 通知。
// 该接口绕过渠道收据验证，必须由有权限的运营人员凭渠道后台凭证操作。
//
// 参数：
//   - req: 补单请求，包含 orderId、thirdTransactionId、reason 和 operatorId
//   - requestID: 请求追踪ID
func (l *GmtLogic) ReplenishOrder(ctx context.Context, req *gmtdto.GmtReplenishOrderReq, requestID string) (*gmtdto.GmtReplenishOrderResp, error) {
	paymentOrderRepo := l.iapLogic.repos()

	// 查询原始订单
	order, err := paymentOrderRepo.FindByOrderID(ctx, req.OrderID)
	if err != nil {
		if errors.Is(err, model.ErrPaymentOrderNotFound) {
			return nil, err
		}
		logx.Error("GMT补单查询订单失败",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("operatorId", req.OperatorID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询订单失败: %w", err)
	}

	// 已支付：幂等返回成功，并尝试补推通知（防止通知丢失场景）
	if order.PaymentStatus == model.PaymentStatusSuccess {
		logx.Info("GMT补单：订单已支付，触发补偿通知",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("operatorId", req.OperatorID),
		)
		_ = l.iapLogic.notifyGamelogic(ctx, order, iapdto.NotifySourceGMT, requestID)
		return &gmtdto.GmtReplenishOrderResp{
			OrderID:   req.OrderID,
			IsSuccess: true,
			Message:   "order already paid, notification resent",
		}, nil
	}

	// 仅允许对 WAIT 状态补单，FAILED 状态需运营确认后再决策
	if order.PaymentStatus != model.PaymentStatusWait {
		logx.Warn("GMT补单：订单状态不允许补单",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("paymentStatus", order.PaymentStatus),
			zap.String("operatorId", req.OperatorID),
		)
		return nil, model.ErrOrderStatusInvalid
	}

	// 渠道幂等检查：确保 thirdTransactionId 未被其他订单使用
	existing, err := paymentOrderRepo.FindByThirdTransactionID(ctx, order.Channel, req.ThirdTransactionID)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("渠道幂等查询失败: %w", err)
	}
	if existing != nil && existing.OrderID != req.OrderID {
		logx.Warn("GMT补单：渠道交易ID已被其他订单使用",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("thirdTransactionId", req.ThirdTransactionID),
			zap.String("existingOrderId", existing.OrderID),
			zap.String("operatorId", req.OperatorID),
		)
		return nil, model.ErrDuplicateTransaction
	}

	// 乐观锁标记支付成功并返回最新订单（使用订单原始金额，补单不改变金额；非充值渠道无归因信息）
	updated, err := paymentOrderRepo.CASMarkPaySuccess(
		ctx,
		req.OrderID,
		order.Version,
		req.ThirdTransactionID,
		order.Amount,
		order.Currency,
		"", "", "",
	)
	if err != nil {
		// 唯一索引兜底：thirdTransactionId 在并发下被其他订单抢占
		if mongox.IsDuplicateKeyError(err) {
			logx.Warn("GMT补单：渠道交易ID重复（唯一索引拦截）",
				zap.String("requestId", requestID),
				zap.String("orderId", req.OrderID),
				zap.String("thirdTransactionId", req.ThirdTransactionID),
			)
			return nil, model.ErrDuplicateTransaction
		}
		logx.Error("GMT补单：更新支付状态失败",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("operatorId", req.OperatorID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("更新支付状态失败: %w", err)
	}
	if updated == nil {
		// 乐观锁未命中：并发补单已被其他请求处理，重查最新状态
		latest, queryErr := paymentOrderRepo.FindByOrderID(ctx, req.OrderID)
		if queryErr == nil && latest.PaymentStatus == model.PaymentStatusSuccess {
			logx.Info("GMT补单：并发请求已完成，幂等返回成功",
				zap.String("requestId", requestID),
				zap.String("orderId", req.OrderID),
			)
			_ = l.iapLogic.notifyGamelogic(ctx, latest, iapdto.NotifySourceGMT, requestID)
			return &gmtdto.GmtReplenishOrderResp{
				OrderID:   req.OrderID,
				IsSuccess: true,
				Message:   "replenished successfully",
			}, nil
		}
		logx.Warn("GMT补单：乐观锁冲突，请重试",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("operatorId", req.OperatorID),
		)
		return nil, model.ErrVersionConflict
	}
	order = updated

	// 同步通知 GameLogic（失败不回滚，依赖定时任务补通知）
	if notifyErr := l.iapLogic.notifyGamelogic(ctx, order, iapdto.NotifySourceGMT, requestID); notifyErr != nil {
		logx.Error("GMT补单：通知GameLogic失败，定时任务将重试",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.String("operatorId", req.OperatorID),
			zap.Error(notifyErr),
		)
	}

	logx.Info("GMT补单成功",
		zap.String("requestId", requestID),
		zap.String("orderId", req.OrderID),
		zap.Int64("playerId", order.PlayerID),
		zap.String("thirdTransactionId", req.ThirdTransactionID),
		zap.String("operatorId", req.OperatorID),
		zap.String("reason", req.Reason),
	)

	return &gmtdto.GmtReplenishOrderResp{
		OrderID:   req.OrderID,
		IsSuccess: true,
		Message:   "replenished successfully",
	}, nil
}

// toGmtOrderItem 将 model.PaymentOrder 转换为 GMT 视角的订单数据
// 仅暴露运营关心的字段，屏蔽收据原文等敏感/冗余信息
func toGmtOrderItem(o *model.PaymentOrder) *gmtdto.GmtOrderItem {
	return &gmtdto.GmtOrderItem{
		OrderID:            o.OrderID,
		PlayerID:           o.PlayerID,
		ProductID:          o.ProductID,
		Channel:            o.Channel,
		ThirdTransactionID: o.ThirdTransactionID,
		PaymentStatus:      o.PaymentStatus,
		NotifyStatus:       o.NotifyStatus,
		NotifyCount:        o.NotifyCount,
		Amount:             o.Amount,
		Currency:           o.Currency,
		CreateTime:         o.CreateTime,
		PayTime:            o.PayTime,
	}
}
