package handler

import (
	"errors"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/platform/internal/logic"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/shared/gmtdto"

	"go.uber.org/zap"
)

// WhitelistHandler 充值白名单管理 HTTP 处理器（Platform 侧，仅供内部 GMT 调用，内部签名验证）
type WhitelistHandler struct {
	whitelistLogic *logic.WhitelistLogic
	deps           *Deps
}

// NewWhitelistHandler 创建充值白名单处理器
func NewWhitelistHandler(whitelistLogic *logic.WhitelistLogic, deps *Deps) *WhitelistHandler {
	return &WhitelistHandler{whitelistLogic: whitelistLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册 GMT-facing 白名单管理路由
func (h *WhitelistHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/gmt/whitelist")
	gmtGroup.Post("/add", h.Add)
	gmtGroup.Post("/delete", h.Delete)
	gmtGroup.Get("/list", h.Query)
}

// Add 添加白名单账号
// POST /gmt/whitelist/add
func (h *WhitelistHandler) Add(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtWhitelistAddReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.Account == "" && req.PlayerID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "account or playerId is required")
	}

	requestID := c.RequestID()
	result, err := h.whitelistLogic.Add(c.Context(), req, requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// Delete 删除白名单账号（物理删除）
// POST /gmt/whitelist/delete
func (h *WhitelistHandler) Delete(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtWhitelistDeleteReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OpenID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "openId is required")
	}

	requestID := c.RequestID()
	if err := h.whitelistLogic.Delete(c.Context(), req, requestID); err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, nil)
}

// Query 分页查询白名单
// GET /gmt/whitelist/list
func (h *WhitelistHandler) Query(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.BindQuery[gmtdto.GmtWhitelistQueryReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := c.RequestID()
	result, err := h.whitelistLogic.Query(c.Context(), req, requestID)
	if err != nil {
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
		return httpx.NewAppError(model.ErrCodeAccountNotFound, "account not found")
	case errors.Is(err, model.ErrWhitelistInvalidIdentity):
		return httpx.NewAppError(model.ErrCodeBadRequest, "account or playerId is required")
	default:
		logx.Error("白名单操作内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}

// verifyInternalSignature 验证内部服务间签名（HMAC-SHA256 + nonce 防重放）
// Redis 客户端用于 nonce 去重，未就绪时降级为不去重（失败放行）
func (h *WhitelistHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}
