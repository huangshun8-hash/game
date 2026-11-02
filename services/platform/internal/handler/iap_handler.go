package handler

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"time"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/platform/internal/dto"
	"kemiluo/services/platform/internal/logic"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/shared/gmtdto"
	"kemiluo/services/shared/iapdto"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
)

// IAPHandler 内购相关 HTTP 处理器（Platform 侧）
type IAPHandler struct {
	iapLogic *logic.IAPLogic
	gmtLogic *logic.GmtLogic
	deps     *Deps
}

// NewIAPHandler 创建 Platform 内购处理器
func NewIAPHandler(iapLogic *logic.IAPLogic, gmtLogic *logic.GmtLogic, deps *Deps) *IAPHandler {
	return &IAPHandler{iapLogic: iapLogic, gmtLogic: gmtLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册内购相关路由
func (h *IAPHandler) Register(r httpx.RouterGroup) {
	// 三方充值回调接口（游戏方发起充值请求，MD5 验签，不挂 JWT/内部签名）
	r.Get("/pay/notify", h.Notify)
	r.Post("/pay/notify", h.Notify)

	// 内部接口（仅供 GameLogic 调用，通过 MD5 签名验证，不挂 JWT）
	internalGroup := r.Group("/internal/iap")
	internalGroup.Post("/paymentOrder", h.CreatePaymentOrder)

	// GMT 运营管理后台接口（仅供内部 GMT 系统调用，通过 MD5 签名验证）
	gmtGroup := r.Group("/gmt/iap")
	gmtGroup.Get("/orders", h.GmtQueryOrders)
	gmtGroup.Post("/replenish", h.GmtReplenishOrder)
}

// rechargeTimeTolerance 充值请求时间戳容忍窗口（毫秒级时间戳，防重放）；
// 放宽至 24 小时以兼容玩家支付流程长导致的渠道回调延迟，重放防护仍由验签与订单幂等兜底
const rechargeTimeTolerance = 24 * time.Hour

// Notify 三方充值回调接口（游戏方调用平台充值）
// GET/POST /pay/notify
// 请求方式 GET（query）或 POST（application/x-www-form-urlencoded）均可；
// 验签方式：md5(passport+order+key+server+time+agent+money+goodid)，key 为三方平台共用签名密钥。
// 响应体遵循接口文档约定的 {"code":...,"content":"..."} 结构（content 文案与文档一致）。
func (h *IAPHandler) Notify(c httpx.Context) error {
	requestID := c.RequestID()

	// 1. 解析原始参数（兼容 GET query 与 POST form-urlencoded）
	params, ok := h.parseRechargeParams(c)
	if !ok {
		logx.Warn("充值请求必填参数缺失",
			zap.String("requestId", requestID),
			zap.String("path", "/pay/notify"),
		)
		return h.rechargeResponse(c, model.RechargeCodeInvalidInfo)
	}

	// 2. 验签：使用三方平台共用签名密钥 key
	key2 := h.deps.Config.ThirdPartyKey
	if key2 == "" {
		logx.Warn("三方平台签名密钥未配置",
			zap.String("requestId", requestID),
			zap.String("agent", params["agent"]),
			zap.String("order", params["order"]),
		)
		return h.rechargeResponse(c, model.RechargeCodeUnknown)
	}
	rawSign := params["passport"] + params["order"] + key2 + params["server"] + params["time"] + params["agent"] + params["money"] + params["goodid"]
	if !verifyRechargeSign(rawSign, params["sign"]) {
		logx.Warn("充值签名验证失败",
			zap.String("requestId", requestID),
			zap.String("agent", params["agent"]),
			zap.String("order", params["order"]),
			zap.String("sign", params["sign"]),
		)
		return h.rechargeResponse(c, model.RechargeCodeUnknown)
	}

	// 3. 解析数值字段并校验
	// money 请求单位为元（可能带小数，如 1.99），统一四舍五入转为分（int64），
	// 与订单入库金额 round(product.Value*100) 保持相同的取整规则，避免精度误差导致金额校验失败。
	moneyYuan, err := strconv.ParseFloat(params["money"], 64)
	if err != nil || moneyYuan <= 0 {
		logx.Warn("充值金额非法",
			zap.String("requestId", requestID),
			zap.String("money", params["money"]),
		)
		return h.rechargeResponse(c, model.RechargeCodeInvalidInfo)
	}
	money := int64(math.Round(moneyYuan * 100))
	ts, err := strconv.ParseInt(params["time"], 10, 64)
	if err != nil || !withinRechargeTimeWindow(ts) {
		logx.Warn("充值时间戳非法或超窗",
			zap.String("requestId", requestID),
			zap.String("time", params["time"]),
		)
		return h.rechargeResponse(c, model.RechargeCodeInvalidInfo)
	}
	req := &dto.RechargeReq{
		Agent:    params["agent"],
		Server:   params["server"],
		Passport: params["passport"],
		Money:    money,
		Order:    params["order"],
		Time:     ts,
		Param:    params["param"],
		GoodID:   params["goodid"],
	}

	// 4. 执行业务处理
	if err := h.iapLogic.Notify(c.Context(), req, requestID); err != nil {
		code := h.mapRechargeError(err)
		logx.Error("充值处理失败",
			zap.String("requestId", requestID),
			zap.String("agent", req.Agent),
			zap.String("order", req.Order),
			zap.Int("code", code),
			zap.Error(err),
		)
		return h.rechargeResponse(c, code)
	}

	logx.Info("充值请求处理成功",
		zap.String("requestId", requestID),
		zap.String("agent", req.Agent),
		zap.String("order", req.Order),
	)
	return h.rechargeResponse(c, model.RechargeCodeSuccess)
}

// parseRechargeParams 解析充值请求参数，兼容 GET query 与 POST form-urlencoded。
// 表单值优先，其次查询参数；必填项（agent/server/passport/money/order/time/goodid/sign）缺失或为空时返回 false。
// 返回值为原始字符串映射，保证签名校验与金额解析均基于请求原文，避免格式归一化导致验签失败。
func (h *IAPHandler) parseRechargeParams(c httpx.Context) (map[string]string, bool) {
	get := func(key string) string {
		if v := c.FormValue(key); v != "" {
			return v
		}
		return c.Query(key)
	}
	params := map[string]string{
		"agent":    get("agent"),
		"server":   get("server"),
		"passport": get("passport"),
		"money":    get("money"),
		"order":    get("order"),
		"time":     get("time"),
		"param":    get("param"),
		"sign":     get("sign"),
		"goodid":   get("goodid"),
	}
	for _, key := range []string{"agent", "server", "passport", "money", "order", "time", "goodid", "sign"} {
		if params[key] == "" {
			return nil, false
		}
	}
	return params, true
}

// verifyRechargeSign 校验充值签名。
// 期望值：md5(passport+order+key2+server+time+agent+money+goodid) 的小写十六进制；
// 使用常量时间比较防时序侧信道。
func verifyRechargeSign(rawSign, providedSign string) bool {
	expected := md5Hex(rawSign)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(providedSign)) == 1
}

// withinRechargeTimeWindow 判断毫秒时间戳是否落在容忍窗口内（防重放）。
func withinRechargeTimeWindow(ts int64) bool {
	reqTime := time.UnixMilli(ts)
	diff := time.Since(reqTime)
	if diff < 0 {
		diff = -diff
	}
	return diff <= rechargeTimeTolerance
}

// md5Hex 计算字符串的 MD5 摘要，返回小写十六进制。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// rechargeContentMap 三方充值接口 code 对应的 content 文案（与接口文档一致）。
// 注意：该接口为第三方对接契约，响应结构与 content 文案按文档约定输出（含中文），
// 与常规客户端接口的英文 message 约定不同。
var rechargeContentMap = map[int]string{
	model.RechargeCodeSuccess:            "充值成功",
	model.RechargeCodeInvalidAccount:     "无效的帐号名",
	model.RechargeCodeInvalidRole:        "无效的角色名",
	model.RechargeCodeInvalidTarget:      "无效的充值目标标识",
	model.RechargeCodeDuplicateOrder:     "重复的订单号",
	model.RechargeCodeInvalidInfo:        "无效的充值信息",
	model.RechargeCodeInvalidExtra:       "无效的附加充值信息",
	model.RechargeCodeServerCommError:    "与游戏服通讯异常",
	model.RechargeCodeServerProcessError: "游戏服处理异常",
	model.RechargeCodeUnknown:            "未知错误",
}

// rechargeContent 返回充值接口 code 对应的文案，未知 code 兜底为"未知错误"。
func rechargeContent(code int) string {
	if content, ok := rechargeContentMap[code]; ok {
		return content
	}
	return rechargeContentMap[model.RechargeCodeUnknown]
}

// rechargeResponse 输出三方充值接口约定的响应格式 {"code":...,"content":"..."}（HTTP 状态恒为 200）。
func (h *IAPHandler) rechargeResponse(c httpx.Context, code int) error {
	return c.JSON(fiber.Map{"code": code, "content": rechargeContent(code)})
}

// mapRechargeError 将充值业务错误映射为接口文档约定的错误码。
func (h *IAPHandler) mapRechargeError(err error) int {
	switch {
	case errors.Is(err, model.ErrRechargeInvalidAccount):
		return model.RechargeCodeInvalidAccount
	case errors.Is(err, model.ErrRechargeDuplicateOrder):
		return model.RechargeCodeDuplicateOrder
	case errors.Is(err, model.ErrRechargeOrderNotFound), errors.Is(err, model.ErrRechargeInvalidInfo):
		return model.RechargeCodeInvalidInfo
	case errors.Is(err, model.ErrRechargeServerCommError):
		return model.RechargeCodeServerCommError
	case errors.Is(err, model.ErrRechargeServerProcessError):
		return model.RechargeCodeServerProcessError
	default:
		return model.RechargeCodeUnknown
	}
}

// CreatePaymentOrder 创建平台支付订单（内部接口，供 GameLogic 调用）
// POST /internal/iap/paymentOrder
func (h *IAPHandler) CreatePaymentOrder(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[iapdto.CreatePaymentOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if req.OrderID == "" || req.ProductID == "" || req.Channel == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId, productId and channel are required")
	}
	if req.PlayerID <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerId is required")
	}

	requestID := c.RequestID()
	result, err := h.iapLogic.CreatePaymentOrder(c.Context(), req, requestID)
	if err != nil {
		return h.handleError(err, requestID)
	}
	return httpx.OK(c, result)
}

// GmtQueryOrders GMT 订单查询接口（供运营管理后台调用）
// GET /gmt/iap/orders
// 支持按 orderId 精确查询（同时匹配业务订单号与三方订单号）或按 playerId / paymentStatus / channel / 时间范围分页查询。
// 注意：查询参数位于 URL query，必须使用 BindQuery；若误用 Bind（仅解析请求体），
// GET 无 body 时全部条件为空，将退化为无过滤查询返回全量订单。
func (h *IAPHandler) GmtQueryOrders(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.BindQuery[gmtdto.GmtQueryOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	requestID := c.RequestID()
	result, err := h.gmtLogic.QueryOrders(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT查询订单失败",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtReplenishOrder GMT 补单接口（模拟三方支付回调，供运营管理后台调用）
// POST /gmt/iap/replenish
// 仅允许对 WAIT 状态订单操作，运营需提供渠道后台查到的交易ID及补单原因
func (h *IAPHandler) GmtReplenishOrder(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtReplenishOrderReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}

	if req.OrderID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "orderId is required")
	}
	if req.ThirdTransactionID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "thirdTransactionId is required")
	}
	if req.Reason == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "reason is required")
	}
	if req.OperatorID == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "operatorId is required")
	}

	requestID := c.RequestID()
	result, err := h.gmtLogic.ReplenishOrder(c.Context(), req, requestID)
	if err != nil {
		return h.handleGmtError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleGmtError 将 GMT 业务错误映射为 HTTP 错误响应
func (h *IAPHandler) handleGmtError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrPaymentOrderNotFound):
		return httpx.NewAppError(model.ErrCodeOrderNotFound, "payment order not found")
	case errors.Is(err, model.ErrOrderStatusInvalid):
		return httpx.NewAppError(model.ErrCodeOrderStatusInvalid, "order status does not allow this operation")
	case errors.Is(err, model.ErrDuplicateTransaction):
		return httpx.NewAppError(model.ErrCodeDuplicateTransaction, "duplicate transaction id")
	case errors.Is(err, model.ErrVersionConflict):
		return httpx.NewAppError(model.ErrCodeVersionConflict, "version conflict, please retry")
	default:
		logx.Error("GMT补单内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}

// verifyInternalSignature 验证内部服务间签名（HMAC-SHA256 + nonce 防重放）
// 供内部接口共用，Redis 客户端用于 nonce 去重，未就绪时降级为不去重（失败放行）
func (h *IAPHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}

// handleError 将业务错误映射为 HTTP 错误响应
func (h *IAPHandler) handleError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrPaymentOrderNotFound):
		return httpx.NewAppError(model.ErrCodeOrderNotFound, "payment order not found")
	case errors.Is(err, model.ErrOrderDuplicate):
		return httpx.NewAppError(model.ErrCodeOrderDuplicate, "order already exists")
	case errors.Is(err, model.ErrProductNotFound):
		return httpx.NewAppError(model.ErrCodeProductNotFound, "product not found")
	case errors.Is(err, model.ErrChannelError):
		return httpx.NewAppError(model.ErrCodeChannelError, "channel api error")
	case errors.Is(err, model.ErrVersionConflict):
		return httpx.NewAppError(model.ErrCodeVersionConflict, "version conflict, please retry")
	case errors.Is(err, model.ErrDuplicateTransaction):
		return httpx.NewAppError(model.ErrCodeDuplicateTransaction, "duplicate transaction id")
	default:
		logx.Error("内购服务内部错误",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
