package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.uber.org/zap"
)

// CustomConfigHandler 自定义配置管理HTTP处理器
type CustomConfigHandler struct {
	customConfigLogic *logic.CustomConfigLogic
}

// NewCustomConfigHandler 创建自定义配置处理器
func NewCustomConfigHandler(customConfigLogic *logic.CustomConfigLogic) *CustomConfigHandler {
	return &CustomConfigHandler{customConfigLogic: customConfigLogic}
}

// Register 实现 httpx.Router 接口，注册自定义配置相关路由
func (h *CustomConfigHandler) Register(r httpx.RouterGroup) {
	g := r.Group("/api/customConfig")
	g.Post("/save", h.Save)
	g.Get("/get", h.Get)
}

// Save 保存自定义配置
// POST /api/customConfig/save
func (h *CustomConfigHandler) Save(c httpx.Context) error {
	req, err := httpx.Bind[dto.SaveCustomConfigReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	if req.Data == nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "data is required")
	}

	operator := h.operator(c)
	result, err := h.customConfigLogic.Save(c.Context(), req, operator)
	if err != nil {
		logx.Error("保存自定义配置失败",
			zap.String("requestId", req.RequestID()),
			zap.String("operator", operator),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "failed to save custom config")
	}
	return httpx.OK(c, result)
}

// Get 查询自定义配置
// GET /api/customConfig/get
func (h *CustomConfigHandler) Get(c httpx.Context) error {
	result, err := h.customConfigLogic.Get(c.Context())
	if err != nil {
		logx.Warn("查询自定义配置失败，降级返回空数据",
			zap.String("requestId", c.RequestID()),
			zap.Error(err),
		)
		return httpx.OK(c, &dto.GetCustomConfigResp{
			Data: map[string]any{},
		})
	}
	return httpx.OK(c, result)
}

// operator 从 JWT Claims 中读取当前操作者账号名
func (h *CustomConfigHandler) operator(c httpx.Context) string {
	if claims := c.Claims(); claims != nil {
		return claims.UserID
	}
	return ""
}
