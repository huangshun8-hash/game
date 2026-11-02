package handler

import (
	"errors"
	"strconv"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/validx"
	"kemiluo/services/platform/internal/dto"
	"kemiluo/services/platform/internal/logic"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/shared/shareddto"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// AuthHandler 认证相关的HTTP处理器
type AuthHandler struct {
	authLogic    *logic.AuthLogic
	lunPlayLogic *logic.LunPlayLogic
	deps         *Deps
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authLogic *logic.AuthLogic, lunPlayLogic *logic.LunPlayLogic, deps *Deps) *AuthHandler {
	return &AuthHandler{
		authLogic:    authLogic,
		lunPlayLogic: lunPlayLogic,
		deps:         deps,
	}
}

// Register 实现httpx.Router接口，注册认证相关路由
func (h *AuthHandler) Register(r httpx.RouterGroup) {
	// 内部账号验证接口（仅供内部服务调用，不挂载 JWT/限流中间件）
	// 统一挂在 /internal 前缀下，与未来对外暴露的接口路径隔离，避免冲突
	internalGroup := r.Group("/internal/account")
	internalGroup.Post("/auth", h.Auth)
	internalGroup.Post("/deactivate", h.Deactivate)

	// LunPlay 平台角色查询接口（平台侧调用，MD5 验签，不挂 JWT）
	lunplayGroup := r.Group("/lunplay/role")
	lunplayGroup.Get("/query", h.QueryRoleSingle)
	lunplayGroup.Get("/list", h.QueryRoleList)
	lunplayGroup.Get("/byRoleId", h.QueryRoleByRoleID)
	lunplayGroup.Get("/serverList", h.QueryServerList)
}

// Auth 统一账号验证接口（内部服务调用）
// 根据 loginType 路由到对应的验证逻辑，通过 MD5 签名验证调用方身份
func (h *AuthHandler) Auth(c httpx.Context) error {
	// 验签 + nonce 防重放（HMAC-SHA256，复用公共实现）
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	if err := httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli); err != nil {
		return err
	}

	// 解析请求参数
	req, err := httpx.Bind[dto.AuthReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 校验渠道标签
	if err := validx.ValidateLoginType(req.LoginType); err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, err.Error())
	}

	result, err := h.authLogic.Auth(c.Context(), req)
	if err != nil {
		return h.handleError(err, req.RequestID())
	}
	return httpx.OK(c, result)
}

// Deactivate 账号注销接口（内部服务调用）
// 作废账号登录身份（openId 释放），由 gamelogic 携带内部签名调用。
func (h *AuthHandler) Deactivate(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	if err := httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli); err != nil {
		return err
	}

	req, err := httpx.Bind[dto.DeactivateReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	result, err := h.authLogic.Deactivate(c.Context(), req)
	if err != nil {
		return h.handleError(err, req.RequestID())
	}
	return httpx.OK(c, result)
}

// handleError 将业务错误转换为对应的AppError响应
// requestID: 当前请求的追踪ID，用于日志关联
func (h *AuthHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrAccountBanned):
		logx.Warn("封禁账号尝试认证",
			zap.String("requestId", requestID),
		)
		return httpx.NewAppError(model.ErrCodeAccountBanned, "account is banned")
	case errors.Is(err, model.ErrAccountNotFound):
		return httpx.NewAppError(model.ErrCodeAccountNotFound, "account not found")
	}

	// 认证逻辑返回的 *fiber.Error 携带渠道/参数错误语义，按状态码映射业务错误码，
	// 避免参数错误、渠道不一致等被吞成内部错误（1500）
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		switch fiberErr.Code {
		case fiber.StatusBadRequest:
			return httpx.NewAppError(model.ErrCodeBadRequest, fiberErr.Message)
		case fiber.StatusUnauthorized:
			return httpx.NewAppError(model.ErrCodeUnauthorized, fiberErr.Message)
		}
	}

	logx.Error("认证服务内部错误",
		zap.String("requestId", requestID),
		zap.Error(err),
	)
	return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
}

// QueryRoleSingle 按区服+passport 查询角色（单服务器单角色）
// GET /lunplay/role/query
// 参数：serverCode（int）、passport、t（秒）、ck=md5(passport+t+key+serverCode)
// 返回：{"code":0,"playerLevel":30,"playerName":"...","loginDate":"...","createDate":"...","roleid":"..."}
func (h *AuthHandler) QueryRoleSingle(c httpx.Context) error {
	requestID := c.RequestID()

	// 解析请求参数（与充值接口一致：同时兼容 GET query）
	serverCode := c.Query("serverCode")
	passport := c.Query("passport")
	t := c.Query("t")
	ck := c.Query("ck")
	if serverCode == "" || passport == "" || t == "" || ck == "" {
		logx.Warn("LunPlay角色查询必填参数缺失",
			zap.String("requestId", requestID),
			zap.String("path", "/lunplay/role/query"),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	// 验签
	if !h.lunPlayLogic.VerifySignByPassport(passport, t, serverCode, ck) {
		logx.Warn("LunPlay角色查询签名验证失败",
			zap.String("requestId", requestID),
			zap.String("passport", passport),
			zap.String("serverCode", serverCode),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	// 跨服查询角色
	serverID, err := strconv.ParseInt(serverCode, 10, 64)
	if err != nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}
	req := &shareddto.RoleQueryByPassportReq{OpenID: passport, ServerCode: serverID}
	result, err := h.lunPlayLogic.QueryByPassport(c.Context(), req, requestID)
	if err != nil {
		logx.Error("LunPlay角色查询失败",
			zap.String("requestId", requestID),
			zap.String("passport", passport),
			zap.String("serverCode", serverCode),
			zap.Error(err),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeServerError)
	}

	// 单角色接口：未查询到角色或角色数据异常视为查询失败
	if len(result.Roles) == 0 || result.Roles[0] == nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeQueryFailed)
	}

	// 输出文档约定的扁平结构（code 与其他字段同级）
	role := dto.ToLunPlayRole(result.Roles[0])
	return c.JSON(fiber.Map{
		"code":        dto.LunPlayCodeSuccess,
		"playerLevel": role.PlayerLevel,
		"playerName":  role.PlayerName,
		"loginDate":   role.LoginDate,
		"createDate":  role.CreateDate,
		"roleid":      role.RoleID,
	})
}

// QueryRoleList 按区服+passport 查询角色（单服务器多角色）
// GET /lunplay/role/list
// 参数：serverCode（int）、passport、t（秒）、ck=md5(passport+t+key+serverCode)
// 返回：{"code":0,"data":[{...}]}
func (h *AuthHandler) QueryRoleList(c httpx.Context) error {
	requestID := c.RequestID()

	serverCode := c.Query("serverCode")
	passport := c.Query("passport")
	t := c.Query("t")
	ck := c.Query("ck")
	if serverCode == "" || passport == "" || t == "" || ck == "" {
		logx.Warn("LunPlay角色列表必填参数缺失",
			zap.String("requestId", requestID),
			zap.String("path", "/lunplay/role/list"),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	if !h.lunPlayLogic.VerifySignByPassport(passport, t, serverCode, ck) {
		logx.Warn("LunPlay角色列表签名验证失败",
			zap.String("requestId", requestID),
			zap.String("passport", passport),
			zap.String("serverCode", serverCode),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	serverID, err := strconv.ParseInt(serverCode, 10, 64)
	if err != nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}
	req := &shareddto.RoleQueryByPassportReq{OpenID: passport, ServerCode: serverID}
	result, err := h.lunPlayLogic.QueryByPassport(c.Context(), req, requestID)
	if err != nil {
		logx.Error("LunPlay角色列表查询失败",
			zap.String("requestId", requestID),
			zap.String("passport", passport),
			zap.String("serverCode", serverCode),
			zap.Error(err),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeServerError)
	}

	roles := make([]*dto.LunPlayRole, 0, len(result.Roles))
	for _, r := range result.Roles {
		if r == nil {
			continue
		}
		roles = append(roles, dto.ToLunPlayRole(r))
	}
	return c.JSON(fiber.Map{
		"code": dto.LunPlayCodeSuccess,
		"data": roles,
	})
}

// QueryRoleByRoleID 按 roleid 查询角色
// GET /lunplay/role/byRoleId
// 参数：roleid、t（秒）、ck=md5(roleid+t+key)
// 返回：{"code":0,"data":[{...,"passport":"1111","serverCode":"1"}]}
func (h *AuthHandler) QueryRoleByRoleID(c httpx.Context) error {
	requestID := c.RequestID()

	roleID := c.Query("roleid")
	t := c.Query("t")
	ck := c.Query("ck")
	if roleID == "" || t == "" || ck == "" {
		logx.Warn("LunPlay按角色ID查询必填参数缺失",
			zap.String("requestId", requestID),
			zap.String("path", "/lunplay/role/byRoleId"),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	if !h.lunPlayLogic.VerifySignByRoleID(roleID, t, ck) {
		logx.Warn("LunPlay按角色ID查询签名验证失败",
			zap.String("requestId", requestID),
			zap.String("roleId", roleID),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	roleIDNum, err := strconv.ParseInt(roleID, 10, 64)
	if err != nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}
	result, err := h.lunPlayLogic.QueryByRoleID(c.Context(), &shareddto.RoleQueryByRoleIDReq{RoleID: roleIDNum}, requestID)
	if err != nil {
		logx.Error("LunPlay按角色ID查询失败",
			zap.String("requestId", requestID),
			zap.String("roleId", roleID),
			zap.Error(err),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeServerError)
	}

	roles := make([]*dto.LunPlayRole, 0, len(result.Roles))
	for _, r := range result.Roles {
		if r == nil {
			continue
		}
		roles = append(roles, dto.ToLunPlayRole(r))
	}
	return c.JSON(fiber.Map{
		"code": dto.LunPlayCodeSuccess,
		"data": roles,
	})
}

// QueryServerList 按角色ID查询区服列表
// GET /lunplay/role/serverList
// 参数：roleid、t（秒）、ck=md5(roleid+t+key)
// 返回：{"code":0,"serverCode":30,"serverName":"..."}
func (h *AuthHandler) QueryServerList(c httpx.Context) error {
	requestID := c.RequestID()

	roleID := c.Query("roleid")
	t := c.Query("t")
	ck := c.Query("ck")
	if roleID == "" || t == "" || ck == "" {
		logx.Warn("LunPlay区服列表必填参数缺失",
			zap.String("requestId", requestID),
			zap.String("path", "/lunplay/role/serverList"),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	if !h.lunPlayLogic.VerifySignByRoleID(roleID, t, ck) {
		logx.Warn("LunPlay区服列表签名验证失败",
			zap.String("requestId", requestID),
			zap.String("roleId", roleID),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}

	roleIDNum, err := strconv.ParseInt(roleID, 10, 64)
	if err != nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeSignInvalid)
	}
	result, err := h.lunPlayLogic.ServerList(c.Context(), &shareddto.RoleQueryByRoleIDReq{RoleID: roleIDNum}, requestID)
	if err != nil {
		logx.Error("LunPlay区服列表查询失败",
			zap.String("requestId", requestID),
			zap.Int64("roleId", roleIDNum),
			zap.Error(err),
		)
		return h.lunPlayResponse(c, dto.LunPlayCodeServerError)
	}

	// 按文档返回单个区服（首个），角色不存在或无区服时返回查询失败
	if len(result.Servers) == 0 {
		return h.lunPlayResponse(c, dto.LunPlayCodeQueryFailed)
	}
	server := result.Servers[0]
	if server == nil {
		return h.lunPlayResponse(c, dto.LunPlayCodeQueryFailed)
	}
	return c.JSON(fiber.Map{
		"code":       dto.LunPlayCodeSuccess,
		"serverCode": server.ServerCode,
		"serverName": dto.URLEncode(server.ServerName),
	})
}

// lunPlayResponse 输出 LunPlay 平台约定的错误响应（HTTP 状态恒为 200）
func (h *AuthHandler) lunPlayResponse(c httpx.Context, code int) error {
	return c.JSON(fiber.Map{"code": code})
}
