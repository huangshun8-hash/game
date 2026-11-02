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

// BanHandler GMT 封禁/解封管理 HTTP 处理器
// 接收来自 Web 管理后台的请求，通过全局 JWT 中间件鉴权后转发至 GameLogic
type BanHandler struct {
	banLogic *logic.BanLogic
}

// NewBanHandler 创建 GMT 封禁处理器
func NewBanHandler(banLogic *logic.BanLogic) *BanHandler {
	return &BanHandler{banLogic: banLogic}
}

// Register 实现 httpx.Router 接口，注册 GMT 封禁管理相关路由
// 路由通过全局 JWT 中间件鉴权，无需额外签名验证
func (h *BanHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/api/gmt/ban")
	gmtGroup.Post("/batchBan", h.BatchBan)
	gmtGroup.Post("/batchUnban", h.BatchUnban)
	gmtGroup.Get("/records", h.QueryBanRecords)
}

// BatchBan 批量封禁玩家
// POST /api/gmt/ban/batchBan
func (h *BanHandler) BatchBan(c httpx.Context) error {
	req, err := httpx.Bind[dto.BatchBanReq](c)
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

	// 从 JWT Claims 获取操作人ID
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	requestID := req.RequestID()
	logx.Info("GMT 发起批量封禁",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.Int("count", len(req.PlayerIDs)),
	)

	result, err := h.banLogic.BatchBan(c.Context(), req.ToGmtDTO(operatorID), requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// BatchUnban 批量解封玩家
// POST /api/gmt/ban/batchUnban
func (h *BanHandler) BatchUnban(c httpx.Context) error {
	req, err := httpx.Bind[dto.BatchUnbanReq](c)
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

	// 从 JWT Claims 获取操作人ID
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	requestID := req.RequestID()
	logx.Info("GMT 发起批量解封",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.Int("count", len(req.PlayerIDs)),
	)

	result, err := h.banLogic.BatchUnban(c.Context(), req.ToGmtDTO(operatorID), requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// QueryBanRecords 查询封禁记录
// GET /api/gmt/ban/records
// 支持按 playerId / action / operatorId / 时间范围分页查询
func (h *BanHandler) QueryBanRecords(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.QueryBanRecordReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := req.RequestID()
	result, err := h.banLogic.QueryBanRecords(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		logx.Error("GMT 查询封禁记录失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleError 将业务错误映射为 HTTP 错误响应
func (h *BanHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrGameLogicUnavailable):
		logx.Error("GameLogic 服务不可用",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "gamelogic service unavailable")
	default:
		logx.Error("GMT 封禁操作内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
