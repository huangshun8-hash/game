package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/shareddto"

	"go.uber.org/zap"
)

// RoleHandler 角色信息查询内部接口处理器（供平台侧跨服调用）
type RoleHandler struct {
	roleLogic *logic.RoleLogic
	deps      *Deps
}

// NewRoleHandler 创建角色信息查询处理器
func NewRoleHandler(roleLogic *logic.RoleLogic, deps *Deps) *RoleHandler {
	return &RoleHandler{roleLogic: roleLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册角色查询内部路由
func (h *RoleHandler) Register(r httpx.RouterGroup) {
	internalGroup := r.Group("/internal/role")
	internalGroup.Post("/queryByPassport", h.QueryByPassport)
	internalGroup.Post("/queryByRoleId", h.QueryByRoleID)
	internalGroup.Post("/serverList", h.ServerList)
}

// QueryByPassport 按平台用户ID（openId）查询角色列表（内部接口）
// POST /internal/role/queryByPassport
func (h *RoleHandler) QueryByPassport(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[shareddto.RoleQueryByPassportReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.OpenID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "openId is required")
	}

	requestID := c.RequestID()
	result, err := h.roleLogic.QueryByPassport(c.Context(), req, requestID)
	if err != nil {
		logx.Error("按平台用户ID查询角色失败",
			zap.String("requestId", requestID),
			zap.String("openId", req.OpenID),
			zap.Int64("serverCode", req.ServerCode),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// QueryByRoleID 按角色ID（playerId）查询角色信息（内部接口）
// POST /internal/role/queryByRoleId
func (h *RoleHandler) QueryByRoleID(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[shareddto.RoleQueryByRoleIDReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.RoleID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "roleId is required")
	}

	requestID := c.RequestID()
	result, err := h.roleLogic.QueryByRoleID(c.Context(), req, requestID)
	if err != nil {
		logx.Error("按角色ID查询角色失败",
			zap.String("requestId", requestID),
			zap.Int64("roleId", req.RoleID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// ServerList 按角色ID查询区服列表（内部接口）
// POST /internal/role/serverList
func (h *RoleHandler) ServerList(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[shareddto.RoleQueryByRoleIDReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.RoleID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "roleId is required")
	}

	requestID := c.RequestID()
	result, err := h.roleLogic.ServerListByRoleID(c.Context(), req, requestID)
	if err != nil {
		logx.Error("按角色ID查询区服失败",
			zap.String("requestId", requestID),
			zap.Int64("roleId", req.RoleID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// verifyInternalSignature 校验来自 Platform 的内部服务签名
func (h *RoleHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}
