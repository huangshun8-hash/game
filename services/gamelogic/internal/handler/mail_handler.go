package handler

import (
	"errors"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/gmtdto"

	"go.uber.org/zap"
)

// MailHandler 邮件相关的HTTP处理器
type MailHandler struct {
	mailLogic *logic.MailLogic
	deps      *Deps
}

// NewMailHandler 创建邮件处理器
func NewMailHandler(mailLogic *logic.MailLogic, deps *Deps) *MailHandler {
	return &MailHandler{mailLogic: mailLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册邮件相关路由
// /api/mail 为客户端接口；/gmt/mail 为 GMT 内部接口（需验证服务间签名）
func (h *MailHandler) Register(r httpx.RouterGroup) {
	mailGroup := r.Group("/api/mail")
	mailGroup.Get("/list", h.GetMailList)
	mailGroup.Post("/read", h.ReadMail)
	mailGroup.Post("/receive", h.ReceiveMail)
	mailGroup.Post("/receiveAll", h.ReceiveAllMails)
	mailGroup.Post("/delete", h.DeleteReadMails)

	gmtGroup := r.Group("/gmt/mail")
	gmtGroup.Post("/send", h.GmtSendMail)
	gmtGroup.Post("/list", h.GmtListTemplates)
	gmtGroup.Post("/recall", h.GmtRecallMail)
}

// GetMailList 获取邮件列表
// GET /api/mail/list?start=0&end=19
func (h *MailHandler) GetMailList(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.MailListReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.mailLogic.GetMailList(c.Context(), req, requestID, playerID)
	if err != nil {
		logx.Error("获取邮件列表失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// ReadMail 查看邮件详情，标记已读
// POST /api/mail/read
func (h *MailHandler) ReadMail(c httpx.Context) error {
	req, err := httpx.Bind[cspb.MailReadReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.MailId <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "mailId is required")
	}

	result, err := h.mailLogic.ReadMail(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleMailError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// ReceiveMail 领取单封邮件附件
// POST /api/mail/receive
func (h *MailHandler) ReceiveMail(c httpx.Context) error {
	req, err := httpx.Bind[cspb.MailReceiveReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.MailId <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "mailId is required")
	}

	result, err := h.mailLogic.ReceiveMail(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleMailError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// ReceiveAllMails 一键领取所有可领取的邮件附件
// POST /api/mail/receiveAll
func (h *MailHandler) ReceiveAllMails(c httpx.Context) error {
	req, err := httpx.Bind[cspb.MailReceiveAllReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.mailLogic.ReceiveAllMails(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleMailError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// DeleteReadMails 清理所有已读且可删除的邮件
// POST /api/mail/delete
func (h *MailHandler) DeleteReadMails(c httpx.Context) error {
	req, err := httpx.Bind[cspb.MailDeleteReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.mailLogic.DeleteReadMails(c.Context(), req, requestID, playerID)
	if err != nil {
		logx.Error("清理已读邮件失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtSendMail GMT 发送邮件
// POST /gmt/mail/send
// 内部接口，需验证服务间签名；仅创建系统邮件模板，由 Lazy Create 模式按需下发
func (h *MailHandler) GmtSendMail(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtSendMailReq](c)
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
	if int32(req.TitleType) != model.TextTypeNone && int32(req.TitleType) != model.TextTypeLc {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid titleType")
	}
	if int32(req.ContentType) != model.TextTypeNone && int32(req.ContentType) != model.TextTypeLc {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid contentType")
	}
	if req.TargetType != model.MailTargetAll &&
		req.TargetType != model.MailTargetUID &&
		req.TargetType != model.MailTargetUIDs {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid targetType")
	}
	if req.TargetType != model.MailTargetAll && len(req.TargetUIDs) == 0 {
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
	if req.ExpireTime < 0 || req.ExpireTime > model.MaxMailExpireMs {
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

	requestID := c.RequestID()
	result, err := h.mailLogic.SendMail(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT 发送邮件执行异常",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtListTemplates GMT 查询邮件模板列表
// POST /gmt/mail/list
// 内部接口，需验证服务间签名；支持按模板ID/目标类型/撤回状态/创建时间范围过滤 + 分页
func (h *MailHandler) GmtListTemplates(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtListTemplatesReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := c.RequestID()
	result, err := h.mailLogic.ListTemplates(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT 查询邮件模板列表执行异常",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtRecallMail GMT 撤回邮件模板
// POST /gmt/mail/recall
// 内部接口，需验证服务间签名；撤回后未收到邮件的玩家将不再收到该模板邮件（已下发的玩家邮件不受影响）
func (h *MailHandler) GmtRecallMail(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtRecallMailReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.TemplateID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "templateId is required")
	}

	requestID := c.RequestID()
	result, err := h.mailLogic.RecallTemplate(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT 撤回邮件模板执行异常",
			zap.String("requestId", requestID),
			zap.String("templateId", req.TemplateID),
			zap.Error(err),
		)
		if errors.Is(err, model.ErrMailTemplateNotFound) {
			return httpx.NewAppError(model.ErrCodeMailTemplateNotFound, "mail template not found")
		}
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// verifyInternalSignature 校验来自 GMT 的内部服务签名
func (h *MailHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}

// handleMailError 将邮件业务错误映射为对应的 AppError 响应
func (h *MailHandler) handleMailError(err error, requestID string, playerID int64) error {
	switch {
	case errors.Is(err, model.ErrMailNotFound):
		return httpx.NewAppError(model.ErrCodeMailNotFound, "mail not found")
	case errors.Is(err, model.ErrMailNoReward):
		return httpx.NewAppError(model.ErrCodeMailNoReward, "mail has no reward")
	case errors.Is(err, model.ErrMailAlreadyRewarded):
		return httpx.NewAppError(model.ErrCodeMailAlreadyRewarded, "reward already received")
	default:
		logx.Error("邮件操作未知错误",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
