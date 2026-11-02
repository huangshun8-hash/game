package handler

import (
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
)

// TimeHandler 服务器时间相关的HTTP处理器
type TimeHandler struct{}

// NewTimeHandler 创建服务器时间处理器
func NewTimeHandler() *TimeHandler {
	return &TimeHandler{}
}

// Register 实现 httpx.Router 接口，注册服务器时间相关路由
func (h *TimeHandler) Register(r httpx.RouterGroup) {
	r.Get("/api/serverTime", h.GetServerTime)
}

// GetServerTime 获取服务器当前时间
// GET /api/serverTime
func (h *TimeHandler) GetServerTime(c httpx.Context) error {
	return httpx.OK(c, &cspb.ServerTimeResp{
		Data: &cspb.ServerTimeData{
			ServerTime: time.Now().UnixMilli(),
		},
	})
}
