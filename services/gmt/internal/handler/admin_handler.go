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

// AdminHandler 管理员账号管理相关的HTTP处理器
type AdminHandler struct {
	adminLogic *logic.AdminLogic
}

// NewAdminHandler 创建管理员账号管理处理器
func NewAdminHandler(adminLogic *logic.AdminLogic) *AdminHandler {
	return &AdminHandler{adminLogic: adminLogic}
}

// Register 实现httpx.Router接口，注册管理员账号管理路由
// 说明：除列表查询外，账号管理（创建、编辑、禁用、重置密码）均仅限超级管理员
func (h *AdminHandler) Register(r httpx.RouterGroup) {
	adminsGroup := r.Group("/api/admins")
	adminsGroup.Get("", h.ListAdmins)
	adminsGroup.Post("", h.CreateAdmin)
	adminsGroup.Put("/:id", h.UpdateAdmin)
	// 系统不支持物理删除账号，DELETE 语义为禁用账号
	adminsGroup.Delete("/:id", h.DisableAdmin)
	// 超级管理员重置其他账号密码
	adminsGroup.Put("/:id/password", h.ResetPassword)
}

// isOperatorSuper 从请求上下文解析当前操作者是否为超级管理员
func isOperatorSuper(c httpx.Context) bool {
	claims := c.Claims()
	return claims != nil && claims.Role == "super"
}

// ListAdmins 获取管理员列表
// GET /api/admins
func (h *AdminHandler) ListAdmins(c httpx.Context) error {
	result, err := h.adminLogic.ListAdmins(c.Context())
	if err != nil {
		logx.Error("获取管理员列表失败",
			zap.String("requestId", c.RequestID()),
			zap.String("userId", c.UserID()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "failed to list admins")
	}
	return httpx.OK(c, result)
}

// CreateAdmin 新增管理员账号
// POST /api/admins
func (h *AdminHandler) CreateAdmin(c httpx.Context) error {
	req, err := httpx.Bind[dto.CreateAdminReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if req.Username == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "username is required")
	}
	if req.Password == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "password is required")
	}
	if err := logic.ValidateAdminPassword(req.Password); err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
	}

	operator := c.UserID()
	result, err := h.adminLogic.CreateAdmin(c.Context(), req, operator, isOperatorSuper(c), c.IP())
	if err != nil {
		return h.handleError(err, req.RequestID(), operator)
	}
	return httpx.OK(c, result)
}

// UpdateAdmin 编辑管理员账号信息
// PUT /api/admins/:id
func (h *AdminHandler) UpdateAdmin(c httpx.Context) error {
	// 先绑定路由参数中的id
	params, err := httpx.BindParams[dto.AdminIDReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid path parameter")
	}

	// 再绑定请求体
	body, err := httpx.Bind[dto.UpdateAdminReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	// 将路由参数中的id填入body
	body.ID = params.ID

	if body.Nickname == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "nickname is required")
	}
	if body.Status != model.AdminStatusNormal && body.Status != model.AdminStatusDisabled {
		return httpx.NewAppError(model.ErrCodeBadRequest, "status must be 0 or 1")
	}

	operator := c.UserID()
	if err := h.adminLogic.UpdateAdmin(c.Context(), body, operator, isOperatorSuper(c), c.IP()); err != nil {
		return h.handleError(err, body.RequestID(), operator)
	}
	return httpx.OK(c, nil)
}

// DisableAdmin 禁用管理员账号（系统不支持物理删除）
// DELETE /api/admins/:id
func (h *AdminHandler) DisableAdmin(c httpx.Context) error {
	req, err := httpx.BindParams[dto.AdminIDReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid path parameter")
	}

	if req.ID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "id is required")
	}

	operator := c.UserID()
	if err := h.adminLogic.DisableAdmin(c.Context(), req, operator, isOperatorSuper(c), c.IP()); err != nil {
		return h.handleError(err, req.RequestID(), operator)
	}
	return httpx.OK(c, nil)
}

// ResetPassword 超级管理员重置其他管理员账号密码
// PUT /api/admins/:id/password
func (h *AdminHandler) ResetPassword(c httpx.Context) error {
	// 先绑定路由参数中的id
	params, err := httpx.BindParams[dto.ResetPasswordReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid path parameter")
	}

	// 再绑定请求体
	body, err := httpx.Bind[dto.ResetPasswordReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	body.ID = params.ID

	if body.NewPassword == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "newPassword is required")
	}
	if err := logic.ValidateAdminPassword(body.NewPassword); err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
	}

	operator := c.UserID()
	if err := h.adminLogic.ResetPassword(c.Context(), body, operator, isOperatorSuper(c), c.IP()); err != nil {
		return h.handleError(err, body.RequestID(), operator)
	}
	return httpx.OK(c, nil)
}

// handleError 将管理员管理相关业务错误转换为 AppError
// requestID: 当前请求追踪ID；operatorID: 执行操作的管理员ID
func (h *AdminHandler) handleError(err error, requestID, operatorID string) error {
	switch {
	case errors.Is(err, model.ErrAdminNotFound):
		return httpx.NewAppError(model.ErrCodeAdminNotFound, "admin not found")
	case errors.Is(err, model.ErrAdminExists):
		return httpx.NewAppError(model.ErrCodeAdminExists, "username already exists")
	case errors.Is(err, model.ErrForbidden):
		return httpx.NewAppError(model.ErrCodeForbidden, "operation not allowed")
	default:
		logx.Error("管理员账号管理操作错误",
			zap.String("requestId", requestID),
			zap.String("userId", operatorID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "admin service error")
	}
}
