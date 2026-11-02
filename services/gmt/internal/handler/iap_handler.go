package handler

import (
	"errors"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.uber.org/zap"
)

// IAPHandler GMT IAP 订单管理 HTTP 处理器
// 接收来自 Web 管理后台的请求，通过全局 JWT 中间件鉴权后转发至 Platform
type IAPHandler struct {
	iapLogic *logic.IAPLogic
}

// NewIAPHandler 创建 GMT IAP 处理器
func NewIAPHandler(iapLogic *logic.IAPLogic) *IAPHandler {
	return &IAPHandler{iapLogic: iapLogic}
}

// Register 实现 httpx.Router 接口，注册 GMT IAP 相关路由
// 路由通过全局 JWT 中间件鉴权，无需额外签名验证
func (h *IAPHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/api/gmt/iap")
	gmtGroup.Get("/orders", h.QueryOrders)
	gmtGroup.Get("/gameOrders", h.QueryGameOrders)
	gmtGroup.Get("/payStats", h.QueryPayStats)
	gmtGroup.Post("/replenish", h.ReplenishOrder)
}

// QueryOrders 订单分页查询
// GET /gmt/iap/orders
// 支持按 orderId 精确查询或按 playerId / paymentStatus / channel / 时间范围分页查询
func (h *IAPHandler) QueryOrders(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.QueryOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := req.RequestID()
	result, err := h.iapLogic.QueryOrders(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		logx.Error("GMT 查询订单失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// QueryGameOrders 业务订单分页查询（GameLogic 侧）
// GET /api/gmt/iap/gameOrders
// 与 QueryOrders 互补，展示订单的业务履约视角：业务状态、发奖与奖励内容
func (h *IAPHandler) QueryGameOrders(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.QueryGameOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := req.RequestID()
	result, err := h.iapLogic.QueryGameOrders(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		logx.Error("GMT 查询业务订单失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// QueryPayStats 玩家充值统计查询（GameLogic 侧）
// GET /api/gmt/iap/payStats
// 返回与用户标签匹配（MatchTags）一致的付费统计口径：最近1笔、最近4笔合计、最近30天合计、累积充值
func (h *IAPHandler) QueryPayStats(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.QueryPayStatsReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.PlayerID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerId is required")
	}

	requestID := req.RequestID()
	result, err := h.iapLogic.QueryPayStats(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		logx.Error("GMT 查询玩家充值统计失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", req.PlayerID),
			zap.Error(err),
		)
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// ReplenishOrder 手动补单（模拟三方支付回调，强制将指定订单标记为支付成功）
// POST /gmt/iap/replenish
// 适用场景：玩家已实际付款，但因网络/渠道异常导致订单停留在 WAIT 状态
// 执行前运营需在渠道后台确认付款真实发生
func (h *IAPHandler) ReplenishOrder(c httpx.Context) error {
	req, err := httpx.Bind[dto.ReplenishOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 必填字段校验
	if req.OrderID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId is required")
	}
	if req.ThirdTransactionID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "thirdTransactionId is required")
	}
	if req.Reason == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "reason is required")
	}

	// 从 JWT Claims 获取操作人ID，忽略请求体中的 operatorId，防止伪造
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}
	req.OperatorID = operatorID

	requestID := req.RequestID()
	result, err := h.iapLogic.ReplenishOrder(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleError 将 IAP 业务错误映射为 HTTP 错误响应
func (h *IAPHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrOrderNotFound):
		return httpx.NewAppError(model.ErrCodeOrderNotFound, "payment order not found")
	case errors.Is(err, model.ErrOrderStatusInvalid):
		return httpx.NewAppError(model.ErrCodeOrderStatusInvalid, "order status does not allow this operation")
	case errors.Is(err, model.ErrDuplicateTransaction):
		return httpx.NewAppError(model.ErrCodeDuplicateTransaction, "duplicate transaction id")
	case errors.Is(err, model.ErrVersionConflict):
		return httpx.NewAppError(model.ErrCodeVersionConflict, "version conflict, please retry")
	case errors.Is(err, model.ErrPlatformUnavailable):
		logx.Error("Platform 服务不可用",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	case errors.Is(err, model.ErrGameLogicUnavailable):
		logx.Error("GameLogic 服务不可用",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	default:
		logx.Error("GMT IAP 内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
