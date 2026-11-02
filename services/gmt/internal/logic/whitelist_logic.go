package logic

import (
	"context"
	"fmt"
	"strconv"

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

// WhitelistLogic 充值白名单业务逻辑层（GMT 侧），负责将 Web 前端请求转发到 Platform。
type WhitelistLogic struct {
	platformSvc *httpcli.Service
	mongoSvc    *mongox.Service
	asyncSvc    *async.Service
}

// NewWhitelistLogic 创建 GMT 白名单逻辑实例
// platformSvc: 调用 Platform GMT 面向接口的 HTTP 客户端
// mongoSvc: MongoDB 服务（用于写入操作日志）
// asyncSvc: 异步任务服务（用于异步写入操作日志）
func NewWhitelistLogic(platformSvc *httpcli.Service, mongoSvc *mongox.Service, asyncSvc *async.Service) *WhitelistLogic {
	return &WhitelistLogic{
		platformSvc: platformSvc,
		mongoSvc:    mongoSvc,
		asyncSvc:    asyncSvc,
	}
}

// Add 添加白名单账号，转发到 Platform POST /gmt/whitelist/add
//
// 参数：
//   - ctx:       请求上下文
//   - req:       白名单添加请求（account 或 playerId 至少其一）
//   - operatorID: 操作管理员 ID（取自登录态，用于操作日志）
//   - requestID: 请求追踪ID
//
// 返回：添加结果，或业务错误
func (l *WhitelistLogic) Add(ctx context.Context, req *gmtdto.GmtWhitelistAddReq, operatorID, requestID string) (*gmtdto.GmtWhitelistAddResp, error) {
	cli := l.platformSvc.Client()
	secret := []byte(l.platformSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化白名单添加请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal whitelist add request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	logx.Info("转发白名单添加请求至 Platform",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/whitelist/add"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/whitelist/add", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发白名单添加请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/add"),
			zap.Error(err),
		)
		return nil, model.ErrPlatformUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtWhitelistAddResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 Platform 白名单添加响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/add"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrPlatformUnavailable
	}

	if result.Code != 0 {
		return nil, mapWhitelistPlatformError(result.Code, result.Message)
	}

	// 异步记录操作日志（仅成功时记录）
	l.logOperationAsync(operatorID, "Whitelist", "Add", "POST",
		"/api/gmt/whitelist/add", requestID, map[string]any{
			"account":  req.Account,
			"playerId": req.PlayerID,
		})

	return result.Data, nil
}

// Delete 删除白名单账号（物理删除），转发到 Platform POST /gmt/whitelist/delete
//
// 参数：
//   - ctx:       请求上下文
//   - req:       白名单删除请求（openId 或 playerId 至少其一）
//   - operatorID: 操作管理员 ID（取自登录态，用于操作日志）
//   - requestID: 请求追踪ID
//
// 返回：业务错误（nil 表示删除成功/幂等成功）
func (l *WhitelistLogic) Delete(ctx context.Context, req *gmtdto.GmtWhitelistDeleteReq, operatorID, requestID string) error {
	cli := l.platformSvc.Client()
	secret := []byte(l.platformSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化白名单删除请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return fmt.Errorf("marshal whitelist delete request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	logx.Info("转发白名单删除请求至 Platform",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/whitelist/delete"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/whitelist/delete", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发白名单删除请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/delete"),
			zap.Error(err),
		)
		return model.ErrPlatformUnavailable
	}
	defer resp.Close()

	var result platformResp[any]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 Platform 白名单删除响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/delete"),
			zap.Error(jsonErr),
		)
		return model.ErrPlatformUnavailable
	}

	if result.Code != 0 {
		return mapWhitelistPlatformError(result.Code, result.Message)
	}

	// 异步记录操作日志（仅成功时记录）
	l.logOperationAsync(operatorID, "Whitelist", "Delete", "POST",
		"/api/gmt/whitelist/delete", requestID, map[string]any{
			"openId": req.OpenID,
		})

	return nil
}

// Query 分页查询白名单，转发到 Platform GET /gmt/whitelist/list
//
// 参数：
//   - ctx:       请求上下文
//   - req:       白名单查询条件
//   - requestID: 请求追踪ID
//
// 返回：分页结果，或业务错误
func (l *WhitelistLogic) Query(ctx context.Context, req *gmtdto.GmtWhitelistQueryReq, requestID string) (*gmtdto.GmtWhitelistQueryResp, error) {
	cli := l.platformSvc.Client()
	secret := []byte(l.platformSvc.Secret())

	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	} else if pageSize > 100 {
		pageSize = 100
	}

	params := map[string]string{
		"page":     strconv.FormatInt(page, 10),
		"pageSize": strconv.FormatInt(pageSize, 10),
	}

	signHeaders := buildSignHeaders(secret, nil)

	logx.Info("转发白名单查询请求至 Platform",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/whitelist/list"),
		zap.Any("query", params),
	)

	resp, err := cli.Get(ctx, "/gmt/whitelist/list",
		httpcli.WithParams(params),
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发白名单查询请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/list"),
			zap.Error(err),
		)
		return nil, model.ErrPlatformUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtWhitelistQueryResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 Platform 白名单查询响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/whitelist/list"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrPlatformUnavailable
	}

	if result.Code != 0 {
		return nil, mapWhitelistPlatformError(result.Code, result.Message)
	}
	return result.Data, nil
}

// mapWhitelistPlatformError 将 Platform 返回的白名单业务错误码映射为对应的 Go error
func mapWhitelistPlatformError(code int, message string) error {
	switch code {
	case whitelistPlatformErrCodeAccountNotFound:
		// 按 playerId 解析不到现存账号（不存在或已注销）
		return model.ErrWhitelistAccountNotFound
	case whitelistPlatformErrCodeBadRequest:
		// 未提供 playerId / account
		return model.ErrWhitelistInvalidIdentity
	case httpxErrCodeUnauthorized, model.ErrCodeInternal:
		// 签名失败 / 平台内部错误，对前端屏蔽细节，归为服务不可用
		return model.ErrPlatformUnavailable
	default:
		return fmt.Errorf("platform error %d: %s", code, message)
	}
}

// whitelist 白名单接口在 Platform 侧使用的业务错误码（与 gmtdto 约定一致）
const (
	// whitelistPlatformErrCodeBadRequest 参数缺失（对应 httpx.ErrCodeBadRequest=1400）
	whitelistPlatformErrCodeBadRequest = 1400
	// whitelistPlatformErrCodeAccountNotFound 账号不存在（对应 httpx.ErrCodeAccountNotFound=1404）
	whitelistPlatformErrCodeAccountNotFound = 1404
	// httpxErrCodeUnauthorized 签名验证失败（对应 httpx.ErrCodeUnauthorized=1401）
	httpxErrCodeUnauthorized = 1401
)

// logOperationAsync 异步记录操作日志
// uid: 操作管理员ID；module/action/method/uri: 操作描述；requestID: 请求追踪ID；reqData: 脱敏后的请求参数
func (l *WhitelistLogic) logOperationAsync(uid, module, action, method, uri, requestID string, reqData any) {
	if l.asyncSvc == nil || l.mongoSvc == nil {
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
