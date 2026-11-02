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

// WhitelistHandler GMT 充值白名单管理 HTTP 处理器
// 接收来自 Web 管理后台的请求，经全局 JWT 鉴权后转发至 Platform /gmt/whitelist/*
type WhitelistHandler struct {
	whitelistLogic *logic.WhitelistLogic
}

// NewWhitelistHandler 创建 GMT 白名单处理器
func NewWhitelistHandler(whitelistLogic *logic.WhitelistLogic) *WhitelistHandler {
	return &WhitelistHandler{whitelistLogic: whitelistLogic}
}

// Register 实现 httpx.Router 接口，注册 GMT 白名单管理路由
func (h *WhitelistHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/api/gmt/whitelist")
	gmtGroup.Post("/add", h.Add)
	gmtGroup.Post("/delete", h.Delete)
	gmtGroup.Get("/list", h.Query)
}

// Add 添加白名单账号
// POST /api/gmt/whitelist/add
func (h *WhitelistHandler) Add(c httpx.Context) error {
	req, err := httpx.Bind[dto.WhitelistAddReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.Account == "" && req.PlayerID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "account or playerId is required")
	}

	requestID := req.RequestID()
	operatorID := c.UserID()
	logx.Info("GMT 发起白名单添加",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.Int64("playerId", req.PlayerID),
	)

	result, err := h.whitelistLogic.Add(c.Context(), req.ToGmtDTO(), operatorID, requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// Delete 删除白名单账号（物理删除）
// POST /api/gmt/whitelist/delete
func (h *WhitelistHandler) Delete(c httpx.Context) error {
	req, err := httpx.Bind[dto.WhitelistDeleteReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OpenID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "openId is required")
	}

	requestID := req.RequestID()
	operatorID := c.UserID()
	logx.Info("GMT 发起白名单删除",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.String("openId", req.OpenID),
	)

	if err := h.whitelistLogic.Delete(c.Context(), req.ToGmtDTO(), operatorID, requestID); err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, nil)
}

// Query 分页查询白名单
// GET /api/gmt/whitelist/list
func (h *WhitelistHandler) Query(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.QueryWhitelistReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := req.RequestID()
	result, err := h.whitelistLogic.Query(c.Context(), req.ToGmtDTO(), requestID)
	if err != nil {
		logx.Error("GMT 查询白名单失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleError 将白名单业务错误映射为 HTTP 错误响应
func (h *WhitelistHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrWhitelistAccountNotFound):
		logx.Warn("白名单账号不存在",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "account not found")
	case errors.Is(err, model.ErrWhitelistInvalidIdentity):
		return httpx.NewAppError(model.ErrCodeBadRequest, "account or playerId is required")
	case errors.Is(err, model.ErrPlatformUnavailable):
		logx.Error("Platform 服务不可用",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "platform service unavailable")
	default:
		logx.Error("GMT 白名单操作内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
