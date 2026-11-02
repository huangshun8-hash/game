package handler

import (
	"errors"
	"strings"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/validx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// AuthHandler 认证相关的HTTP处理器（面向客户端）
type AuthHandler struct {
	authLogic *logic.AuthLogic
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authLogic *logic.AuthLogic) *AuthHandler {
	return &AuthHandler{authLogic: authLogic}
}

// Register 实现httpx.Router接口，注册认证相关路由
func (h *AuthHandler) Register(r httpx.RouterGroup) {
	authGroup := r.Group("/auth")
	authGroup.Post("/login", h.Login)
	// 账号注销（需 JWT）：作废登录身份，玩家ID一律取自认证上下文，禁止客户端伪造
	authGroup.Post("/deactivate", h.Deactivate)
}

// Deactivate 账号注销入口（需 JWT）
// 注销当前登录玩家的登录身份（作废 openId），原玩家数据永久保留但不可再以原身份登录。
func (h *AuthHandler) Deactivate(c httpx.Context) error {
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	if err := h.authLogic.Deactivate(c.Context(), playerID, requestID); err != nil {
		return h.handleDeactivateError(err, requestID, playerID)
	}
	return httpx.OK(c, &cspb.DeactivateResp{})
}

// handleDeactivateError 将注销业务错误转换为客户端响应错误。
func (h *AuthHandler) handleDeactivateError(err error, requestID string, playerID int64) error {
	switch {
	case errors.Is(err, model.ErrAccountDeactivateFailed):
		logx.Warn("账号注销失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeAccountDeactivateFailed, "account deactivate failed")
	default:
		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			if fiberErr.Code >= 500 {
				logx.Error("注销服务内部错误", zap.String("requestId", requestID), zap.Int64("userId", playerID), zap.Error(err))
				return httpx.NewAppError(model.ErrCodeInternal, "auth service error")
			}
			logx.Warn("注销请求被拒绝", zap.String("requestId", requestID), zap.Int64("userId", playerID), zap.Error(err))
			return httpx.NewAppError(model.ErrCodeBadRequest, "account deactivate failed")
		}
		logx.Error("注销服务未知错误", zap.String("requestId", requestID), zap.Int64("userId", playerID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "auth service error")
	}
}

// Login 统一登录入口，支持 guest / 三方登录
func (h *AuthHandler) Login(c httpx.Context) error {
	req, err := httpx.Bind[cspb.LoginReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if err := validx.ValidateLoginType(req.LoginType); err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
	}
	if req.LoginType == model.LoginTypeGuest {
		if err := validx.ValidateUUID(req.Uuid); err != nil {
			return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
		}
	}
	if req.Platform == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "platform is required")
	}
	// deviceId 是严格单端（顶号）判定的唯一依据：缺失时无法区分「同一设备重登」与「换端登录」，
	// 静默放过会导致单端策略被绕过，静默踢人又会误伤正常用户，故在入口直接拒绝
	if strings.TrimSpace(req.ClientInfo.GetDeviceId()) == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "deviceId is required")
	}

	requestID := c.RequestID()
	// 客户端 IP 由 handler 显式传入：顶号通知中需要告知旧端「新登录来自哪个 IP」
	result, err := h.authLogic.Login(c.Context(), req, requestID, c.IP())
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleError 将业务错误转换为对应的AppError响应
func (h *AuthHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrPlayerBanned):
		logx.Warn("封禁账号尝试登录", zap.String("requestId", requestID))
		return httpx.NewAppError(model.ErrCodePlayerBanned, "account is banned")
	case errors.Is(err, model.ErrPlayerNotFound):
		return httpx.NewAppError(model.ErrCodePlayerNotFound, "player not found")
	default:
		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			if fiberErr.Code >= 500 {
				logx.Error("认证服务内部错误", zap.String("requestId", requestID), zap.Error(err))
				return httpx.NewAppError(model.ErrCodeInternal, "auth service error")
			}
			logx.Warn("认证请求被平台拒绝", zap.String("requestId", requestID), zap.Error(err))
			return httpx.NewAppError(model.ErrCodeBadRequest, "authentication failed")
		}
		logx.Error("认证服务未知错误", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "auth service error")
	}
}
