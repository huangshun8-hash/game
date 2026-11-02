package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/gmtdto"

	"go.uber.org/zap"
)

// BanHandler 封禁/解封 GMT 内部接口处理器
// 由 GMT 服务通过内部签名调用，不对外暴露
type BanHandler struct {
	banLogic *logic.BanLogic
	deps     *Deps
}

// NewBanHandler 创建封禁处理器
func NewBanHandler(banLogic *logic.BanLogic, deps *Deps) *BanHandler {
	return &BanHandler{banLogic: banLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册封禁相关 GMT 内部路由
func (h *BanHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/gmt/ban")
	gmtGroup.Post("/batchBan", h.BatchBan)
	gmtGroup.Post("/batchUnban", h.BatchUnban)
	gmtGroup.Get("/records", h.QueryBanRecords)
}

// BatchBan 批量封禁玩家
// POST /gmt/ban/batchBan
// 内部接口，需验证服务间签名
func (h *BanHandler) BatchBan(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtBatchBanReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 参数校验
	if len(req.PlayerIDs) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerIds is required")
	}
	if len(req.PlayerIDs) > 100 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerIds cannot exceed 100")
	}
	if req.Reason == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "reason is required")
	}
	if req.OperatorID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "operatorId is required")
	}

	requestID := c.RequestID()
	result, err := h.banLogic.BatchBan(c.Context(), req, requestID)
	if err != nil {
		logx.Error("批量封禁执行异常",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// BatchUnban 批量解封玩家
// POST /gmt/ban/batchUnban
// 内部接口，需验证服务间签名
func (h *BanHandler) BatchUnban(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtBatchUnbanReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 参数校验
	if len(req.PlayerIDs) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerIds is required")
	}
	if len(req.PlayerIDs) > 100 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerIds cannot exceed 100")
	}
	if req.Reason == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "reason is required")
	}
	if req.OperatorID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "operatorId is required")
	}

	requestID := c.RequestID()
	result, err := h.banLogic.BatchUnban(c.Context(), req, requestID)
	if err != nil {
		logx.Error("批量解封执行异常",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// QueryBanRecords 查询封禁记录
// GET /gmt/ban/records
// 内部接口，需验证服务间签名
func (h *BanHandler) QueryBanRecords(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.BindQuery[gmtdto.GmtQueryBanRecordReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := c.RequestID()
	result, err := h.banLogic.QueryBanRecords(c.Context(), req, requestID)
	if err != nil {
		logx.Error("查询封禁记录失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// verifyInternalSignature 校验来自 GMT 的内部服务签名
func (h *BanHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}
