package logic

import (
	"context"
	"fmt"

	"kemiluo/pkg/async"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

// MailLogic GMT 邮件管理业务逻辑层，负责将 Web 前端请求转发到 GameLogic 服务
type MailLogic struct {
	gamelogicSvc *httpcli.Service
	mongoSvc     *mongox.Service
	asyncSvc     *async.Service
}

// NewMailLogic 创建 GMT 邮件管理逻辑实例
// gamelogicSvc: 调用 GameLogic GMT 内部接口的客户端
// mongoSvc: MongoDB服务（用于操作日志写入）
// asyncSvc: 异步任务服务（用于异步写入操作日志）
func NewMailLogic(gamelogicSvc *httpcli.Service, mongoSvc *mongox.Service, asyncSvc *async.Service) *MailLogic {
	return &MailLogic{
		gamelogicSvc: gamelogicSvc,
		mongoSvc:     mongoSvc,
		asyncSvc:     asyncSvc,
	}
}

// SendMail 发送邮件，转发到 GameLogic POST /gmt/mail/send
//
// 参数：
//   - ctx:       请求上下文
//   - req:       发送邮件请求（标题/正文/附件/目标/过期时间/操作人）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：邮件模板ID与创建时间，或业务错误
func (l *MailLogic) SendMail(ctx context.Context, req *gmtdto.GmtSendMailReq, requestID string) (*gmtdto.GmtSendMailResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化发送邮件请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal send mail request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	logx.Info("转发发送邮件请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/mail/send"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/mail/send", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发发送邮件请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/send"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtSendMailResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 发送邮件响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/send"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic send mail error %d: %s", result.Code, result.Message)
	}

	// 异步记录操作日志
	l.logOperationAsync(req.OperatorID, "Mail", "SendMail", "POST",
		"/api/gmt/mail/send", requestID, map[string]any{
			"title":       req.Title,
			"targetType":  req.TargetType,
			"targetUIDs":  req.TargetUIDs,
			"rewardCount": len(req.Rewards),
		})

	return result.Data, nil
}

// ListTemplates 查询邮件模板列表，转发到 GameLogic POST /gmt/mail/list
//
// 参数：
//   - ctx:       请求上下文
//   - req:       列表查询请求（过滤条件 + 分页参数）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：分页模板列表，或业务错误
func (l *MailLogic) ListTemplates(ctx context.Context, req *gmtdto.GmtListTemplatesReq, requestID string) (*gmtdto.GmtListTemplatesResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化查询邮件模板列表请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal list templates request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	logx.Info("转发查询邮件模板列表请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/mail/list"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/mail/list", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发查询邮件模板列表请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/list"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtListTemplatesResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 查询邮件模板列表响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/list"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic list templates error %d: %s", result.Code, result.Message)
	}

	return result.Data, nil
}

// RecallMail 撤回邮件模板，转发到 GameLogic POST /gmt/mail/recall
// 撤回后未收到邮件的玩家将不再收到该模板邮件；已下发到玩家邮箱的邮件不受影响
//
// 参数：
//   - ctx:       请求上下文
//   - req:       撤回请求（模板业务ID）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：模板ID、当前撤回状态与撤回时间，或业务错误
func (l *MailLogic) RecallMail(ctx context.Context, req *gmtdto.GmtRecallMailReq, requestID string) (*gmtdto.GmtRecallMailResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化撤回邮件模板请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal recall mail request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	logx.Info("转发撤回邮件模板请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/mail/recall"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/mail/recall", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发撤回邮件模板请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/recall"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtRecallMailResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 撤回邮件模板响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/mail/recall"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic recall mail error %d: %s", result.Code, result.Message)
	}

	// 异步记录操作日志
	l.logOperationAsync(req.OperatorID, "Mail", "RecallMail", "POST",
		"/api/gmt/mail/recall", requestID, map[string]any{
			"templateId": req.TemplateID,
		})

	return result.Data, nil
}

// logOperationAsync 异步记录操作日志
// uid: 操作管理员ID；module/action/method/uri: 操作描述；requestID: 请求追踪ID；reqData: 脱敏后的请求参数
func (l *MailLogic) logOperationAsync(uid, module, action, method, uri, requestID string, reqData any) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("module", module).
		Set("action", action).
		Set("method", method).
		Set("uri", uri).
		Set("requestId", requestID).
		Set("reqData", reqData)
	l.asyncSvc.SubmitFunc("log", "operationLog", func(c *async.Context) error {
		logRepo := repo.NewOperationLogRepo(mongoSvc.Client())
		return logRepo.InsertLog(context.Background(), &model.OperationLog{
			UID:      c.GetString("uid"),
			Username: c.GetString("uid"),
			Module:   c.GetString("module"),
			Action:   c.GetString("action"),
			Method:   c.GetString("method"),
			URI:      c.GetString("uri"),
			Request:  c.Get("reqData"),
		})
	}, ctx)
}
