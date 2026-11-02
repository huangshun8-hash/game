package handler

import (
	"errors"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/gmtdto"
	"kemiluo/services/shared/iapdto"

	"go.uber.org/zap"
)

// IAPHandler 内购相关 HTTP 处理器
type IAPHandler struct {
	iapLogic     *logic.IAPLogic
	userTagLogic *logic.UserTagLogic
	deps         *Deps
}

// NewIAPHandler 创建内购处理器
func NewIAPHandler(iapLogic *logic.IAPLogic, userTagLogic *logic.UserTagLogic, deps *Deps) *IAPHandler {
	return &IAPHandler{iapLogic: iapLogic, userTagLogic: userTagLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册内购相关路由
func (h *IAPHandler) Register(r httpx.RouterGroup) {
	iapGroup := r.Group("/api/iap")
	iapGroup.Post("/order", h.CreateOrder)
	iapGroup.Post("/claim", h.ClaimReward)
	iapGroup.Get("/orders/paid", h.ListPaidOrders)
	iapGroup.Get("/order/status", h.GetOrderStatus)
	iapGroup.Post("/userTags", h.UserTags)

	internalGroup := r.Group("/internal/iap")
	internalGroup.Post("/notify", h.HandlePayNotify)

	gmtGroup := r.Group("/gmt/iap")
	gmtGroup.Get("/orders", h.GmtQueryOrders)
	gmtGroup.Get("/payStats", h.GmtQueryPayStats)
}

// CreateOrder 创建内购订单
// POST /api/iap/order
func (h *IAPHandler) CreateOrder(c httpx.Context) error {
	req, err := httpx.Bind[cspb.CreateOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.PayId == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "payId is required")
	}
	if !model.IsValidPayChannel(req.Channel) {
		return httpx.NewAppError(model.ErrCodeBadRequest, "channel must be apple or google")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.iapLogic.CreateOrder(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// ClaimReward 客户端主动领取奖励
// POST /api/iap/claim
func (h *IAPHandler) ClaimReward(c httpx.Context) error {
	req, err := httpx.Bind[cspb.ClaimRewardReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OrderId == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId is required")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.iapLogic.ClaimReward(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// ListPaidOrders 查询当前玩家所有已支付待领取的订单
// GET /api/iap/orders/paid
func (h *IAPHandler) ListPaidOrders(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.ListPaidOrdersReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.iapLogic.ListPaidOrders(c.Context(), req, requestID, playerID)
	if err != nil {
		logx.Error("查询待领取订单失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GetOrderStatus 查询单个订单状态
// GET /api/iap/order/status?orderId=xxx
func (h *IAPHandler) GetOrderStatus(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.GetOrderStatusReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OrderId == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId is required")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.iapLogic.GetOrderStatus(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// GmtQueryOrders GMT 业务订单查询接口
// GET /gmt/iap/orders
func (h *IAPHandler) GmtQueryOrders(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}
	req, err := httpx.BindQuery[gmtdto.GmtQueryGameOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	result, err := h.iapLogic.GmtQueryOrders(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT查询业务订单失败", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtQueryPayStats GMT 玩家充值统计查询接口
// GET /gmt/iap/payStats
// 统计口径与用户标签匹配（MatchTags）使用的付费统计一致，金额单位为分
func (h *IAPHandler) GmtQueryPayStats(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}
	req, err := httpx.BindQuery[gmtdto.GmtQueryPayStatsReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.PlayerID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerId is required")
	}
	requestID := c.RequestID()
	result, err := h.iapLogic.GmtQueryPayStats(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT查询玩家充值统计失败", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// HandlePayNotify 处理 Platform 的支付成功回调（内部接口）
// POST /internal/iap/notify
func (h *IAPHandler) HandlePayNotify(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}
	req, err := httpx.Bind[iapdto.PayNotifyReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OrderID == "" || req.NotifyID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId and notifyId are required")
	}
	requestID := c.RequestID()
	result, err := h.iapLogic.HandlePayNotify(c.Context(), req, requestID)
	if err != nil {
		return h.handleNotifyError(err, requestID)
	}
	return httpx.OK(c, result)
}

// verifyInternalSignature 校验来自 Platform 的内部回调签名
func (h *IAPHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}

// handleError 将业务错误映射为 HTTP 错误响应
func (h *IAPHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrOrderNotFound):
		return httpx.NewAppError(model.ErrCodeOrderNotFound, "order not found")
	case errors.Is(err, model.ErrOrderDuplicate):
		return httpx.NewAppError(model.ErrCodeOrderDuplicate, "order already exists")
	case errors.Is(err, model.ErrOrderStatusInvalid):
		return httpx.NewAppError(model.ErrCodeOrderStatusInvalid, "order is not ready for this operation")
	case errors.Is(err, model.ErrOrderAlreadyClaimed):
		return httpx.NewAppError(model.ErrCodeOrderAlreadyClaimed, "order reward already claimed")
	case errors.Is(err, model.ErrProductNotFound):
		return httpx.NewAppError(model.ErrCodeProductNotFound, "product not found")
	case errors.Is(err, model.ErrDeliveryFailed):
		return httpx.NewAppError(model.ErrCodeDeliveryFailed, "delivery failed, please try again later")
	default:
		logx.Error("内购服务内部错误", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}

// handleNotifyError 处理 Platform 回调时的错误
func (h *IAPHandler) handleNotifyError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrOrderNotFound):
		return httpx.NewAppError(model.ErrCodeOrderNotFound, "order not found")
	case errors.Is(err, model.ErrOrderStatusInvalid):
		return httpx.NewAppError(model.ErrCodeOrderStatusInvalid, "invalid order status")
	case errors.Is(err, model.ErrOrderNotOwned):
		return httpx.NewAppError(model.ErrCodeOrderNotOwned, "order player mismatch")
	default:
		logx.Error("处理支付通知内部错误", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}

// UserTags 用户标签匹配接口
// POST /api/iap/userTags
func (h *IAPHandler) UserTags(c httpx.Context) error {
	req, err := httpx.Bind[cspb.UserTagsReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.userTagLogic.MatchTags(c.Context(), req, requestID, playerID)
	if err != nil {
		logx.Error("用户标签匹配失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}
