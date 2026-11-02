package logic

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/signx"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/shared/gmtdto"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// platformResp 解析 Platform 返回的统一响应结构
type platformResp[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    *T     `json:"data"`
}

// IAPLogic GMT IAP 业务逻辑层，负责将 Web 前端请求转发到后端服务
// platformSvc 用于渠道支付侧订单（Platform），gamelogicSvc 用于业务履约侧订单（GameLogic）
type IAPLogic struct {
	platformSvc  *httpcli.Service
	gamelogicSvc *httpcli.Service
}

// NewIAPLogic 创建 GMT IAP 逻辑实例
// platformSvc:  调用 Platform GMT 接口的客户端
// gamelogicSvc: 调用 GameLogic GMT 接口的客户端
func NewIAPLogic(platformSvc, gamelogicSvc *httpcli.Service) *IAPLogic {
	return &IAPLogic{platformSvc: platformSvc, gamelogicSvc: gamelogicSvc}
}

// QueryOrders 查询订单列表，转发到 Platform GET /gmt/iap/orders
//
// 参数：
//   - ctx:       请求上下文
//   - req:       来自前端的查询条件（已转换为 gmtdto 类型）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：订单分页结果，或业务错误
func (l *IAPLogic) QueryOrders(ctx context.Context, req *gmtdto.GmtQueryOrderReq, requestID string) (*gmtdto.GmtQueryOrderResp, error) {
	cli := l.platformSvc.Client()
	secret := []byte(l.platformSvc.Secret())

	// 分页参数归一化：页码最小 1，每页数量限制在 [1,100]，防止超大分页
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	} else if req.PageSize > 100 {
		req.PageSize = 100
	}

	// 构建查询参数（GET 请求 body 为空，bodyMD5 基于空串）
	params := map[string]string{
		"orderId":       req.OrderID,
		"paymentStatus": req.PaymentStatus,
		"channel":       req.Channel,
	}
	if req.PlayerID > 0 {
		params["playerId"] = strconv.FormatInt(req.PlayerID, 10)
	}
	if req.StartTime > 0 {
		params["startTime"] = strconv.FormatInt(req.StartTime, 10)
	}
	if req.EndTime > 0 {
		params["endTime"] = strconv.FormatInt(req.EndTime, 10)
	}
	if req.Page > 0 {
		params["page"] = strconv.FormatInt(req.Page, 10)
	}
	if req.PageSize > 0 {
		params["pageSize"] = strconv.FormatInt(req.PageSize, 10)
	}

	signHeaders := buildSignHeaders(secret, nil)

	// 打印出站请求日志，便于跨服务链路追踪
	logx.Info("转发订单查询请求至 Platform",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/iap/orders"),
		zap.Any("query", params),
	)

	resp, err := cli.Get(ctx, "/gmt/iap/orders",
		httpcli.WithParams(params),
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发订单查询请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/orders"),
			zap.Error(err),
		)
		return nil, model.ErrPlatformUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtQueryOrderResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 Platform 订单查询响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/orders"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrPlatformUnavailable
	}

	if result.Code != 0 {
		return nil, mapPlatformError(result.Code, result.Message)
	}
	return result.Data, nil
}

// QueryGameOrders 查询 GameLogic 业务订单列表，转发到 GameLogic GET /gmt/iap/orders
// 与 QueryOrders（Platform 渠道支付视角）互补，返回业务履约视角：
// 业务状态机、发奖情况、奖励内容等。
//
// 参数：
//   - ctx:       请求上下文
//   - req:       来自前端的查询条件（已转换为 gmtdto 类型）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：业务订单分页结果，或业务错误
func (l *IAPLogic) QueryGameOrders(ctx context.Context, req *gmtdto.GmtQueryGameOrderReq, requestID string) (*gmtdto.GmtQueryGameOrderResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	// 分页参数归一化：页码最小 1，每页数量限制在 [1,100]，防止超大分页
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	} else if req.PageSize > 100 {
		req.PageSize = 100
	}

	// 构建查询参数（GET 请求 body 为空，bodyMD5 基于空串）
	params := map[string]string{
		"orderId": req.OrderID,
		"status":  req.Status,
		"channel": req.Channel,
	}
	if req.PlayerID > 0 {
		params["playerId"] = strconv.FormatInt(req.PlayerID, 10)
	}
	if req.StartTime > 0 {
		params["startTime"] = strconv.FormatInt(req.StartTime, 10)
	}
	if req.EndTime > 0 {
		params["endTime"] = strconv.FormatInt(req.EndTime, 10)
	}
	if req.Page > 0 {
		params["page"] = strconv.FormatInt(req.Page, 10)
	}
	if req.PageSize > 0 {
		params["pageSize"] = strconv.FormatInt(req.PageSize, 10)
	}

	signHeaders := buildSignHeaders(secret, nil)

	// 打印出站请求日志，便于跨服务链路追踪
	logx.Info("转发业务订单查询请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/iap/orders"),
		zap.Any("query", params),
	)

	resp, err := cli.Get(ctx, "/gmt/iap/orders",
		httpcli.WithParams(params),
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发业务订单查询请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/orders"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtQueryGameOrderResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 业务订单查询响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/orders"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, mapGameLogicError(result.Code, result.Message)
	}
	return result.Data, nil
}

// QueryPayStats 查询玩家充值统计，转发到 GameLogic GET /gmt/iap/payStats
// 统计口径与用户标签匹配（MatchTags）使用的付费统计完全一致：
// 仅统计 PAID / DELIVERED 订单，单位分，包含最近1笔、最近4笔合计、最近30天合计与累积充值。
//
// 参数：
//   - ctx:       请求上下文
//   - req:       来自前端的查询条件（已转换为 gmtdto 类型）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：玩家充值统计，或业务错误
func (l *IAPLogic) QueryPayStats(ctx context.Context, req *gmtdto.GmtQueryPayStatsReq, requestID string) (*gmtdto.GmtPlayerPayStatsResp, error) {
	cli := l.gamelogicSvc.Client()
	secret := []byte(l.gamelogicSvc.Secret())

	// 构建查询参数（GET 请求 body 为空，bodyMD5 基于空串）
	params := map[string]string{
		"playerId": strconv.FormatInt(req.PlayerID, 10),
	}

	signHeaders := buildSignHeaders(secret, nil)

	// 打印出站请求日志，便于跨服务链路追踪
	logx.Info("转发玩家充值统计查询请求至 GameLogic",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/iap/payStats"),
		zap.Any("query", params),
	)

	resp, err := cli.Get(ctx, "/gmt/iap/payStats",
		httpcli.WithParams(params),
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发玩家充值统计查询请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/payStats"),
			zap.Error(err),
		)
		return nil, model.ErrGameLogicUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtPlayerPayStatsResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 GameLogic 玩家充值统计响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/payStats"),
			zap.Error(jsonErr),
		)
		return nil, model.ErrGameLogicUnavailable
	}

	if result.Code != 0 {
		return nil, mapGameLogicError(result.Code, result.Message)
	}
	return result.Data, nil
}

// ReplenishOrder 补单，转发到 Platform POST /gmt/iap/replenish
//
// 参数：
//   - ctx:       请求上下文
//   - req:       来自前端的补单请求（已转换为 gmtdto 类型）
//   - requestID: 请求追踪ID，用于日志关联
//
// 返回：补单结果，或业务错误
func (l *IAPLogic) ReplenishOrder(ctx context.Context, req *gmtdto.GmtReplenishOrderReq, requestID string) (*gmtdto.GmtReplenishOrderResp, error) {
	cli := l.platformSvc.Client()
	secret := []byte(l.platformSvc.Secret())

	bodyBytes, err := sonic.Marshal(req)
	if err != nil {
		logx.Error("序列化补单请求失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("marshal replenish request: %w", err)
	}

	signHeaders := buildSignHeaders(secret, bodyBytes)

	// 打印出站请求日志，便于跨服务链路追踪（body 中不含敏感信息，可安全记录）
	logx.Info("转发补单请求至 Platform",
		zap.String("requestId", requestID),
		zap.String("url", "/gmt/iap/replenish"),
		zap.Any("reqBody", req),
	)

	resp, err := cli.Post(ctx, "/gmt/iap/replenish", bodyBytes,
		httpcli.WithHeaders(signHeaders),
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
	)
	if err != nil {
		logx.Error("转发补单请求失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/replenish"),
			zap.String("orderId", req.OrderID),
			zap.Error(err),
		)
		return nil, model.ErrPlatformUnavailable
	}
	defer resp.Close()

	var result platformResp[gmtdto.GmtReplenishOrderResp]
	if jsonErr := resp.JSON(&result); jsonErr != nil {
		logx.Error("解析 Platform 补单响应失败",
			zap.String("requestId", requestID),
			zap.String("url", "/gmt/iap/replenish"),
			zap.String("orderId", req.OrderID),
			zap.Error(jsonErr),
		)
		return nil, model.ErrPlatformUnavailable
	}

	if result.Code != 0 {
		return nil, mapPlatformError(result.Code, result.Message)
	}
	return result.Data, nil
}

// buildSignHeaders 根据密钥和请求体构建内部签名请求头（HMAC-SHA256）
// body 为 nil 时表示 GET 请求，签名时使用空字节切片
func buildSignHeaders(secret, body []byte) map[string]string {
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

// mapPlatformError 将 Platform 返回的业务错误码映射为对应的 Go error
// 保留错误码语义，由 handler 层再映射为 HTTP 响应
func mapPlatformError(code int, message string) error {
	switch code {
	case httpx.ErrCodeUnauthorized, 1401:
		// 签名验证失败不应透传给前端，视为内部错误
		return model.ErrPlatformUnavailable
	case 1420:
		return model.ErrOrderNotFound
	case 1422:
		return model.ErrOrderStatusInvalid
	case 1426:
		return model.ErrVersionConflict
	case 1427:
		return model.ErrDuplicateTransaction
	default:
		return fmt.Errorf("platform error %d: %s", code, message)
	}
}

// mapGameLogicError 将 GameLogic 返回的业务错误码映射为对应的 Go error
// GameLogic 的 GMT 查询接口仅返回参数错误 / 签名失败 / 内部错误，均归为内部错误对前端屏蔽细节
func mapGameLogicError(code int, message string) error {
	switch code {
	case httpx.ErrCodeUnauthorized, 1401:
		// 签名验证失败不应透传给前端，视为内部错误
		return model.ErrGameLogicUnavailable
	default:
		return fmt.Errorf("gamelogic error %d: %s", code, message)
	}
}
