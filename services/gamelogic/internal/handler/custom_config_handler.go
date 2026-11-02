package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gamelogic/internal/logic"

	"go.uber.org/zap"
)

// CustomConfigHandler 自定义配置客户端读取接口处理器（无需JWT鉴权）
type CustomConfigHandler struct {
	customConfigLogic *logic.CustomConfigLogic
}

// NewCustomConfigHandler 创建自定义配置处理器
func NewCustomConfigHandler(customConfigLogic *logic.CustomConfigLogic) *CustomConfigHandler {
	return &CustomConfigHandler{customConfigLogic: customConfigLogic}
}

// Register 实现 httpx.Router 接口，注册自定义配置相关路由
func (h *CustomConfigHandler) Register(r httpx.RouterGroup) {
	r.Get("/api/customConfig", h.Get)
}

// Get 获取自定义配置（客户端接口，无需JWT）
// GET /api/customConfig
func (h *CustomConfigHandler) Get(c httpx.Context) error {
	requestID := c.RequestID()

	data, err := h.customConfigLogic.Get(c.Context(), requestID)
	if err != nil {
		// 降级处理：读取失败时返回空JSON，不报错
		logx.Warn("获取自定义配置失败，降级返回空数据",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.OK(c, map[string]any{})
	}

	return httpx.OK(c, data)
}
