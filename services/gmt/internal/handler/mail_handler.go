package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.uber.org/zap"
)

// MailHandler GMT 邮件管理 HTTP 处理器
// 接收来自 Web 管理后台的请求，通过全局 JWT 中间件鉴权后转发至 GameLogic
type MailHandler struct {
	mailLogic *logic.MailLogic
}

// NewMailHandler 创建 GMT 邮件管理处理器
func NewMailHandler(mailLogic *logic.MailLogic) *MailHandler {
	return &MailHandler{mailLogic: mailLogic}
}

// Register 实现 httpx.Router 接口，注册 GMT 邮件管理相关路由
// 路由通过全局 JWT 中间件鉴权，无需额外签名验证
func (h *MailHandler) Register(r httpx.RouterGroup) {
	gmtGroup := r.Group("/api/gmt/mail")
	gmtGroup.Post("/send", h.SendMail)
	gmtGroup.Post("/list", h.ListTemplates)
	gmtGroup.Post("/recall", h.RecallMail)
}

// maxMailExpireMs 邮件相对有效期上限（2 个月，按 60 天计，毫秒），与 GameLogic 侧校验保持一致，
// 防止极端值导致收到时间 + 相对有效期换算时 int64 溢出
const maxMailExpireMs = 60 * 24 * 60 * 60 * 1000

// SendMail 发送邮件
// POST /api/gmt/mail/send
// 支持全服邮件 / 指定单个UID邮件 / 指定UID列表邮件，采用 Lazy Create 模式下发
func (h *MailHandler) SendMail(c httpx.Context) error {
	req, err := httpx.Bind[dto.SendMailReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 参数校验
	if req.Title == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "title is required")
	}
	if req.Content == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "content is required")
	}
	if req.TitleType != 0 && req.TitleType != 1 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid titleType")
	}
	if req.ContentType != 0 && req.ContentType != 1 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid contentType")
	}
	if req.TargetType != 0 && req.TargetType != 1 && req.TargetType != 2 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid targetType")
	}
	if req.TargetType != 0 && len(req.TargetUIDs) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "targetUIDs is required")
	}
	if len(req.TargetUIDs) > 1000 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "targetUIDs cannot exceed 1000")
	}
	for _, r := range req.Rewards {
		if r.ItemID == "" || r.Count <= 0 {
			return httpx.NewAppError(model.ErrCodeBadRequest, "invalid reward item")
		}
	}
	if req.ExpireTime < 0 || req.ExpireTime > maxMailExpireMs {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid expireTime")
	}
	if req.StartTime < 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid startTime")
	}
	if req.EndTime < 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid endTime")
	}
	if req.StartTime > 0 && req.EndTime > 0 && req.StartTime >= req.EndTime {
		return httpx.NewAppError(model.ErrCodeBadRequest, "startTime must be earlier than endTime")
	}

	// 从 JWT Claims 获取操作人ID
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	requestID := req.RequestID()
	logx.Info("GMT 发起发送邮件",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.String("title", req.Title),
		zap.Int("targetType", req.TargetType),
		zap.Int("targetCount", len(req.TargetUIDs)),
	)

	result, err := h.mailLogic.SendMail(c.Context(), req.ToGmtDTO(operatorID), requestID)
	if err != nil {
		logx.Error("GMT 发送邮件失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// ListTemplates 查询邮件模板列表
// POST /api/gmt/mail/list
// 支持按模板ID/目标类型/撤回状态/创建时间范围过滤 + 分页查询
func (h *MailHandler) ListTemplates(c httpx.Context) error {
	req, err := httpx.Bind[dto.ListTemplatesReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	// 从 JWT Claims 获取操作人ID
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	requestID := req.RequestID()
	logx.Info("GMT 发起查询邮件模板列表",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.String("templateId", req.TemplateID),
		zap.Any("targetType", req.TargetType),
		zap.Any("recalled", req.Recalled),
		zap.Int64("page", req.Page),
		zap.Int64("pageSize", req.PageSize),
	)

	result, err := h.mailLogic.ListTemplates(c.Context(), req.ToGmtDTO(operatorID), requestID)
	if err != nil {
		logx.Error("GMT 查询邮件模板列表失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// RecallMail 撤回邮件模板
// POST /api/gmt/mail/recall
// 撤回后未收到邮件的玩家将不再收到该模板邮件（已下发的玩家邮件不受影响）
func (h *MailHandler) RecallMail(c httpx.Context) error {
	req, err := httpx.Bind[dto.RecallMailReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if req.TemplateID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "templateId is required")
	}

	// 从 JWT Claims 获取操作人ID
	operatorID := c.UserID()
	if operatorID == "" {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "unauthorized")
	}

	requestID := req.RequestID()
	logx.Info("GMT 发起撤回邮件模板",
		zap.String("requestId", requestID),
		zap.String("operatorId", operatorID),
		zap.String("templateId", req.TemplateID),
	)

	result, err := h.mailLogic.RecallMail(c.Context(), req.ToGmtDTO(operatorID), requestID)
	if err != nil {
		logx.Error("GMT 撤回邮件模板失败",
			zap.String("requestId", requestID),
			zap.String("templateId", req.TemplateID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}
