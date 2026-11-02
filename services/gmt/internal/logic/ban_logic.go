package logic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/async"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/signx"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// BanLogic GMT 封禁/解封业务逻辑层，负责将 Web 前端请求转发到 GameLogic 服务
type BanLogic struct {
	gamelogicSvc *httpcli.Service
	mongoSvc     *mongox.Service
	asyncSvc     *async.Service
}

// NewBanLogic 创建 GMT 封禁逻辑实例
// gamelogicSvc: 调用 GameLogic GMT 内部接口的客户端
// mongoSvc: MongoDB服务（用于操作日志写入）
// asyncSvc: 异步任务服务（用于异步写入操作日志）
func NewBanLogic(gamelogicSvc *httpcli.Service, mongoSvc *mongox.Service, asyncSvc *async.Service) *BanLogic {
	return &BanLogic{
		gamelogicSvc: gamelogicSvc,
		mongoSvc:     mongoSvc,
		asyncSvc:     asyncSvc,
	}
}

// BatchBan 批量封禁玩家，转发到 GameLogic POST /gmt/ban/batchBan
//
// 参数：
//   - ctx:       请求上下文
//   - req:       封禁请求（玩家ID列表、原因、操作人）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：封禁结果，或业务错误
func (l *BanLogic) BatchBan(ctx context.Context, req *gmtdto.GmtBatchBanReq, requestID string) (*gmtdto.GmtBatchBanResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化封禁请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal ban request: %w", err)
	}

	signHeaders := l.buildSignHeaders(secret, bodyBytes)

	logx.Info("转发批量封禁请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/ban/batchBan"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/ban/batchBan", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发批量封禁请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/batchBan"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtBatchBanResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 封禁响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/batchBan"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic ban error %d: %s", result.Code, result.Message)
	}

	// 异步记录操作日志
	l.logOperationAsync(req.OperatorID, "Ban", "BatchBan", "POST",
		"/api/gmt/ban/batchBan", requestID, map[string]any{
			"playerIds": req.PlayerIDs,
			"reason":    req.Reason,
		})

	return result.Data, nil
}

// BatchUnban 批量解封玩家，转发到 GameLogic POST /gmt/ban/batchUnban
//
// 参数：
//   - ctx:       请求上下文
//   - req:       解封请求（玩家ID列表、原因、操作人）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：解封结果，或业务错误
func (l *BanLogic) BatchUnban(ctx context.Context, req *gmtdto.GmtBatchUnbanReq, requestID string) (*gmtdto.GmtBatchBanResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化解封请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal unban request: %w", err)
	}

	signHeaders := l.buildSignHeaders(secret, bodyBytes)

	logx.Info("转发批量解封请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/ban/batchUnban"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/ban/batchUnban", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发批量解封请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/batchUnban"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtBatchBanResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 解封响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/batchUnban"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic unban error %d: %s", result.Code, result.Message)
	}

	// 异步记录操作日志
	l.logOperationAsync(req.OperatorID, "Ban", "BatchUnban", "POST",
		"/api/gmt/ban/batchUnban", requestID, map[string]any{
			"playerIds": req.PlayerIDs,
			"reason":    req.Reason,
		})

	return result.Data, nil
}

// QueryBanRecords 查询封禁记录，转发到 GameLogic GET /gmt/ban/records
//
// 参数：
//   - ctx:       请求上下文
//   - req:       查询请求（支持按玩家ID过滤）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：封禁记录分页结果，或业务错误
func (l *BanLogic) QueryBanRecords(ctx context.Context, req *gmtdto.GmtQueryBanRecordReq, requestID string) (*gmtdto.GmtQueryBanRecordResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	// 构建查询参数
	params := map[string]string{}
	if req.PlayerID > 0 {
		params["playerId"] = strconv.FormatInt(req.PlayerID, 10)
	}
	if req.Page > 0 {
		params["page"] = strconv.FormatInt(req.Page, 10)
	}
	if req.PageSize > 0 {
		params["pageSize"] = strconv.FormatInt(req.PageSize, 10)
	}

	signHeaders := l.buildSignHeaders(secret, nil)

	logx.Info("转发封禁记录查询请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/ban/records"),
		zap.Any("query", params),
	)

	resp, err := cli.Get(ctx, "/gmt/ban/records",
		httpcli.WithParams(params),
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发封禁记录查询请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/records"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result gamelogicResp[gmtdto.GmtQueryBanRecordResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 封禁记录查询响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/ban/records"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, fmt.Errorf("gamelogic ban records error %d: %s", result.Code, result.Message)
	}
	return result.Data, nil
}

// gamelogicResp 解析 GameLogic 返回的统一响应结构
type gamelogicResp[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *T     `json:"data"`
}

// buildSignHeaders 根据密钥和请求体构建内部签名请求头（HMAC-SHA256）
func (l *BanLogic) buildSignHeaders(secret, body []byte) map[string]string {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.New().String()
	if body == nil {
		body = []byte{}
	}
	sig := signx.Sign(secret, timestamp, nonce, body)
	return map[string]string{
		"X-Timestamp": timestamp,
		"X-Nonce":     nonce,
		"X-Signature": sig,
	}
}

// logOperationAsync 异步记录操作日志
// uid: 操作管理员ID；module/action/method/uri: 操作描述；requestID: 请求追踪ID；reqData: 脱敏后的请求参数
func (l *BanLogic) logOperationAsync(uid, module, action, method, uri, requestID string, reqData any) {
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
