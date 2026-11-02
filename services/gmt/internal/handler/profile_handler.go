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

// ProfileHandler 个人资料相关的HTTP处理器
type ProfileHandler struct {
	profileLogic *logic.ProfileLogic
}

// NewProfileHandler 创建个人资料处理器
func NewProfileHandler(profileLogic *logic.ProfileLogic) *ProfileHandler {
	return &ProfileHandler{profileLogic: profileLogic}
}

// Register 实现httpx.Router接口，注册个人资料相关路由
func (h *ProfileHandler) Register(r httpx.RouterGroup) {
	adminGroup := r.Group("/api/admin")
	adminGroup.Get("/profile", h.GetProfile)
	adminGroup.Put("/profile", h.UpdateProfile)
	adminGroup.Post("/changePassword", h.ChangePassword)
}

// GetProfile 获取当前登录管理员信息
// GET /api/admin/profile
func (h *ProfileHandler) GetProfile(c httpx.Context) error {
	adminID := c.UserID()
	if adminID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	result, err := h.profileLogic.GetProfile(c.Context(), adminID)
	if err != nil {
		return h.handleError(err, c.RequestID(), adminID)
	}
	return httpx.OK(c, result)
}

// UpdateProfile 修改当前登录管理员的昵称
// PUT /api/admin/profile
func (h *ProfileHandler) UpdateProfile(c httpx.Context) error {
	req, err := httpx.Bind[dto.UpdateProfileReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	adminID := c.UserID()
	if req.Nickname == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "nickname is required")
	}

	if err := h.profileLogic.UpdateProfile(c.Context(), req, adminID, c.IP()); err != nil {
		return h.handleError(err, req.RequestID(), adminID)
	}
	return httpx.OK(c, nil)
}

// ChangePassword 修改当前登录管理员的密码
// POST /api/admin/changePassword
func (h *ProfileHandler) ChangePassword(c httpx.Context) error {
	req, err := httpx.Bind[dto.ChangePasswordReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	adminID := c.UserID()
	if req.OldPassword == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "oldPassword is required")
	}
	if req.NewPassword == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "newPassword is required")
	}
	if err := logic.ValidateAdminPassword(req.NewPassword); err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
	}

	if err := h.profileLogic.ChangePassword(c.Context(), req, adminID, c.IP()); err != nil {
		return h.handleProfilePasswordError(err, req.RequestID(), adminID)
	}
	return httpx.OK(c, nil)
}

// handleError 处理资料相关业务错误
func (h *ProfileHandler) handleError(err error, requestID, adminID string) error {
	switch {
	case errors.Is(err, model.ErrAdminNotFound):
		return httpx.NewAppError(model.ErrCodeAdminNotFound, "admin not found")
	default:
		logx.Error("个人资料操作错误",
			zap.String("requestId", requestID),
			zap.String("userId", adminID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "profile service error")
	}
}

// handleProfilePasswordError 处理密码修改相关业务错误
func (h *ProfileHandler) handleProfilePasswordError(err error, requestID, adminID string) error {
	switch {
	case errors.Is(err, model.ErrAdminNotFound):
		return httpx.NewAppError(model.ErrCodeAdminNotFound, "admin not found")
	case errors.Is(err, model.ErrPasswordWrong):
		return httpx.NewAppError(model.ErrCodePasswordWrong, "old password is incorrect")
	default:
		logx.Error("修改密码操作错误",
			zap.String("requestId", requestID),
			zap.String("userId", adminID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "change password failed")
	}
}
