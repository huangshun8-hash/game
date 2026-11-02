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

// AuthHandler 认证相关的HTTP处理器
type AuthHandler struct {
	authLogic *logic.AuthLogic
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authLogic *logic.AuthLogic) *AuthHandler {
	return &AuthHandler{authLogic: authLogic}
}

// Register 实现httpx.Router接口，注册认证相关路由
func (h *AuthHandler) Register(r httpx.RouterGroup) {
	adminGroup := r.Group("/api/admin")
	// 登录接口跳过JWT鉴权（在service.go中配置skipPaths）
	adminGroup.Post("/login", h.Login)
}

// Login 管理员登录
// POST /api/admin/login
func (h *AuthHandler) Login(c httpx.Context) error {
	req, err := httpx.Bind[dto.LoginReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if req.Username == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "username is required")
	}
	if req.Password == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "password is required")
	}

	result, err := h.authLogic.Login(c.Context(), req, c.IP())
	if err != nil {
		return h.handleError(err, req.RequestID())
	}
	return httpx.OK(c, result)
}

// handleError 将业务错误转换为对应的 AppError 响应
// requestID: 当前请求的追踪ID，用于日志关联
func (h *AuthHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrAdminNotFound):
		// 不暴露账号是否存在，统一返回凭据错误
		return httpx.NewAppError(model.ErrCodeUnauthorized, "invalid username or password")
	case errors.Is(err, model.ErrPasswordWrong):
		return httpx.NewAppError(model.ErrCodeUnauthorized, "invalid username or password")
	case errors.Is(err, model.ErrLoginLocked):
		return httpx.NewAppError(model.ErrCodeLoginLocked, "too many failed attempts, please try again later")
	case errors.Is(err, model.ErrAccountDisabled):
		return httpx.NewAppError(model.ErrCodeAccountDisabled, "account is disabled")
	default:
		logx.Error("管理员登录未知错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "login service error")
	}
}
