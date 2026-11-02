package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.uber.org/zap"
)

// OperationLogHandler 操作日志查询相关的HTTP处理器
type OperationLogHandler struct {
	logLogic *logic.OperationLogLogic
}

// NewOperationLogHandler 创建操作日志查询处理器
func NewOperationLogHandler(logLogic *logic.OperationLogLogic) *OperationLogHandler {
	return &OperationLogHandler{logLogic: logLogic}
}

// Register 实现httpx.Router接口，注册操作日志路由
// 说明：日志查询所有已登录管理员均可访问
func (h *OperationLogHandler) Register(r httpx.RouterGroup) {
	r.Get("/api/operationLogs", h.ListLogs)
}

// ListLogs 分页查询操作日志
// GET /api/operationLogs?uid=&module=&action=&startTime=&endTime=&page=&pageSize=
func (h *OperationLogHandler) ListLogs(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.ListOperationLogsReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid query parameters")
	}

	result, err := h.logLogic.ListLogs(c.Context(), req)
	if err != nil {
		logx.Error("查询操作日志失败",
			zap.String("requestId", c.RequestID()),
			zap.String("userId", c.UserID()),
			zap.Any("query", req),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "failed to list operation logs")
	}

	return httpx.OK(c, result)
}
