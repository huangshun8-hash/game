package logic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"kemiluo/generated/gdconfig"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/signx"
	"kemiluo/services/platform/internal/dto"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/platform/internal/repo"
	"kemiluo/services/shared/iapdto"

	"github.com/bytedance/sonic"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// gamelogicNotifyPath 向 GameLogic 发送支付通知的内部接口路径
const gamelogicNotifyPath = "/internal/iap/notify"

// 模拟补单相关常量（仅 local 环境使用）
const (
	// mockPayTxPrefix 模拟补单渠道交易ID前缀
	mockPayTxPrefix = "mock_tx_"
	// mockMarkPaidBatchSize 模拟补单单次扫描批次大小
	mockMarkPaidBatchSize = 10
	// mockMarkPaidRequestID 模拟补单定时任务日志追踪ID（区别于真实请求）
	mockMarkPaidRequestID = "mock_mark_paid"
	// whitelistPayTxPrefix 白名单自动支付渠道交易ID前缀（whitelist_<orderId>，无需真实扣款）
	whitelistPayTxPrefix = "whitelist_"
)

// IAPLogic 内购业务逻辑（Platform 侧）
// 负责：支付订单管理、向 GameLogic 推送支付通知
type IAPLogic struct {
	mongoSvc       *mongox.Service
	gamelogicSvc   *httpcli.Service
	gdConfig       *gdconfig.ConfigManager
	internalSecret string
}

// NewIAPLogic 创建内购业务逻辑
// gamelogicSvc: 用于调用 GameLogic 内部接口的 HTTP 客户端
// gdConfig: 游戏数值配置管理器，用于查询商品配置
// internalSecret: 内部服务间 MD5 验签密钥
func NewIAPLogic(mongoSvc *mongox.Service, gamelogicSvc *httpcli.Service, gdConfig *gdconfig.ConfigManager, internalSecret string) *IAPLogic {
	return &IAPLogic{
		mongoSvc:       mongoSvc,
		gamelogicSvc:   gamelogicSvc,
		gdConfig:       gdConfig,
		internalSecret: internalSecret,
	}
}

// repos 返回基于当前连接的支付订单仓库实例
func (l *IAPLogic) repos() *repo.PaymentOrderRepo {
	return repo.NewPaymentOrderRepo(l.mongoSvc.Client())
}

// CreatePaymentOrder 创建平台支付订单（由 GameLogic 内部调用）
// Platform 自主查询商品配置，不信任上游透传的价格/SKU
func (l *IAPLogic) CreatePaymentOrder(ctx context.Context, req *iapdto.CreatePaymentOrderReq, requestID string) (*iapdto.CreatePaymentOrderResp, error) {
	paymentOrderRepo := l.repos()

	// 幂等检查：同一 orderId 已存在时，必须验证 playerID 一致才能复用
	// 防止极低概率的 orderId 碰撞被误用为其他玩家的支付订单
	existing, err := paymentOrderRepo.FindByOrderID(ctx, req.OrderID)
	if err != nil && !errors.Is(err, model.ErrPaymentOrderNotFound) {
		logx.Error("查询支付订单失败",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询支付订单失败: %w", err)
	}
	if existing != nil {
		// 严格校验：playerID 不一致说明 orderId 碰撞，拒绝复用
		if existing.PlayerID != req.PlayerID {
			logx.Error("Platform侧订单号碰撞：已有订单属于其他玩家",
				zap.String("requestId", requestID),
				zap.String("orderId", req.OrderID),
				zap.Int64("reqPlayerId", req.PlayerID),
				zap.Int64("existingPlayerId", existing.PlayerID),
			)
			return nil, fmt.Errorf("order id collision detected")
		}
		// 幂等返回：同玩家重复创建直接返回已有订单
		return &iapdto.CreatePaymentOrderResp{
			OrderID:   existing.OrderID,
			ProductID: existing.ProductID,
			SKU:       req.ProductID,
			Amount:    existing.Amount,
			Currency:  existing.Currency,
		}, nil
	}

	// 从 IAPProductGlobal 配置表查询商品
	product := l.findProductByPayID(req.ProductID)
	if product == nil {
		logx.Warn("商品配置不存在",
			zap.String("requestId", requestID),
			zap.String("productId", req.ProductID),
		)
		return nil, model.ErrProductNotFound
	}

	// 按渠道取该商品的渠道内购ID（goodId）：配置缺失视为商品不可用，直接下单失败，禁止放行无 goodId 的订单
	goodID := channelGoodIDByConfig(product, req.Channel)
	if goodID == "" {
		logx.Error("商品渠道内购ID未配置，拒绝下单",
			zap.String("requestId", requestID),
			zap.String("productId", req.ProductID),
			zap.String("channel", req.Channel),
		)
		return nil, model.ErrProductNotFound
	}

	// 将展示价格转为分（int64）：浮点乘100后四舍五入，避免截断导致金额少1分
	// （如 2.01 元直接截断会得到 200 分，与渠道/回调金额 201 分不匹配）
	amountCents := int64(math.Round(product.Value * 100))

	// 创建支付订单
	now := time.Now()
	order := &model.PaymentOrder{
		OrderID:       req.OrderID,
		PlayerID:      req.PlayerID,
		ProductID:     req.ProductID,
		GoodID:        goodID,
		Channel:       req.Channel,
		PaymentStatus: model.PaymentStatusWait,
		NotifyStatus:  model.NotifyStatusWait,
		Amount:        amountCents,
		Currency:      product.Symbol,
		CreateTime:    now.UnixMilli(),
	}
	if err = paymentOrderRepo.InsertOrder(ctx, order); err != nil {
		if mongox.IsDuplicateKeyError(err) {
			// 并发重复创建，回退查找并严格校验 playerID
			existing, findErr := paymentOrderRepo.FindByOrderID(ctx, req.OrderID)
			if findErr != nil {
				return nil, fmt.Errorf("并发创建后查询支付订单失败: %w", findErr)
			}
			if existing.PlayerID != req.PlayerID {
				logx.Error("Platform侧并发创建后发现订单归属冲突",
					zap.String("requestId", requestID),
					zap.String("orderId", req.OrderID),
					zap.Int64("reqPlayerId", req.PlayerID),
					zap.Int64("existingPlayerId", existing.PlayerID),
				)
				return nil, fmt.Errorf("order id collision detected")
			}
			return &iapdto.CreatePaymentOrderResp{
				OrderID:   existing.OrderID,
				ProductID: existing.ProductID,
				SKU:       req.ProductID,
				Amount:    existing.Amount,
				Currency:  existing.Currency,
			}, nil
		}
		logx.Error("创建支付订单失败",
			zap.String("requestId", requestID),
			zap.String("orderId", req.OrderID),
			zap.Int64("playerId", req.PlayerID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("创建支付订单失败: %w", err)
	}

	logx.Info("支付订单创建成功",
		zap.String("requestId", requestID),
		zap.String("orderId", req.OrderID),
		zap.Int64("playerId", req.PlayerID),
		zap.String("productId", req.ProductID),
		zap.String("channel", req.Channel),
	)

	// 白名单自动支付：若该订单所属账号处于充值白名单，则下单即直接支付完成（无需真实扣款）。
	// 复用 CASMarkPaySuccess + notifyGamelogic 走完整发奖链路，与真实支付 / GMT 补单共用同一收单路径。
	if l.isWhitelistedPayment(ctx, order.PlayerID, requestID) {
		l.markPaidForWhitelist(ctx, order, requestID)
	}

	return &iapdto.CreatePaymentOrderResp{
		OrderID:   req.OrderID,
		ProductID: req.ProductID,
		SKU:       req.ProductID,
		Amount:    amountCents,
		Currency:  product.Symbol,
	}, nil
}

// Notify 处理游戏方发起的三方充值回调。
// 流程：解析平台支付订单ID（由透传参数 param 携带）→ 严格校验订单（必须已存在、金额完全一致）→ 订单已处理完成时直接返回成功 → CAS 标记支付成功 → 复用 notifyGamelogic 通知 GameLogic 发放。
// 说明：本回调仅用于完成已存在的支付订单（由客户端内购下单流程预先创建），不做玩家/角色信息查询校验，
// 订单不存在时直接拒绝、不自动创建，防止游戏方伪造订单号凭空充值；金额必须与订单金额（分）严格一致，不匹配直接拒绝；
// 重复回调（订单已 SUCCESS）视为幂等成功直接返回，不再重复通知 GameLogic。
// 返回值：nil 表示充值成功；否则返回可被 handler 映射为接口错误码的哨兵错误。
func (l *IAPLogic) Notify(ctx context.Context, req *dto.RechargeReq, requestID string) error {
	// 1. 解析平台支付订单ID，无法解析视为无效充值信息
	orderID := req.Param
	if orderID == "" {
		logx.Warn("充值回调无法解析订单ID",
			zap.String("requestId", requestID),
			zap.String("order", req.Order),
		)
		return model.ErrRechargeInvalidInfo
	}

	// 2. 查询回调对应的支付订单
	paymentOrderRepo := l.repos()
	order, err := paymentOrderRepo.FindByOrderID(ctx, orderID)
	if err != nil {
		if errors.Is(err, model.ErrPaymentOrderNotFound) {
			logx.Warn("充值回调订单不存在",
				zap.String("requestId", requestID),
				zap.String("orderId", orderID),
			)
			return model.ErrRechargeOrderNotFound
		}
		logx.Error("查询充值订单失败",
			zap.String("requestId", requestID),
			zap.String("orderId", orderID),
			zap.Error(err),
		)
		return fmt.Errorf("查询充值订单失败: %w", err)
	}

	// 3. 严格校验金额（分），不匹配直接拒绝
	amountCents := req.Money
	if order.Amount != amountCents {
		logx.Warn("充值回调订单金额不匹配，拒绝回调",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Int64("reqAmount", amountCents),
			zap.Int64("existingAmount", order.Amount),
		)
		return model.ErrRechargeInvalidInfo
	}

	// 3.4 商品ID（goodId）严格校验：回调 goodid 必须与下单时固化的渠道内购ID 完全一致，
	// 防止金额相同但商品不同的回调顶替本单（如同一价格档位的其他 SKU）。
	// 回调缺失 goodid 或订单无固化值均视为无效充值信息直接拒绝，不做跳过或降级处理。
	if order.GoodID == "" {
		logx.Warn("充值回调订单缺少渠道商品ID，拒绝回调",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.String("productId", order.ProductID),
			zap.String("channel", order.Channel),
		)
		return model.ErrRechargeInvalidInfo
	}
	if req.GoodID != order.GoodID {
		logx.Warn("充值回调商品ID不匹配，拒绝回调",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.String("channel", order.Channel),
			zap.String("reqGoodId", req.GoodID),
			zap.String("expectedGoodId", order.GoodID),
		)
		return model.ErrRechargeInvalidInfo
	}

	// 3.5 玩家账号归属校验：按订单 playerId 实时查询账号表，校验回调 passport 与玩家 OpenID 一致
	// （platform 账号表以 OpenID 为唯一身份标识：三方渠道为三方 openID，免验证渠道为第三方账号，游客为安装 UUID）
	// 账号不存在或查询失败均无法证明充值目标归属，直接拒绝，防充值错账/被篡改订单ID指向他人订单
	account, accErr := repo.NewAccountRepo(l.mongoSvc.Client()).FindByPlayerID(ctx, order.PlayerID)
	if accErr != nil {
		if errors.Is(accErr, mongox.ErrNotFound) {
			logx.Warn("充值回调查询玩家账号不存在，拒绝回调",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
				zap.Int64("playerId", order.PlayerID),
			)
			return model.ErrRechargeInvalidAccount
		}
		logx.Error("充值回调查询玩家账号失败",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Int64("playerId", order.PlayerID),
			zap.Error(accErr),
		)
		return fmt.Errorf("查询玩家账号失败: %w", accErr)
	}
	if account == nil || account.OpenID != req.Passport {
		existingOpenID := ""
		if account != nil {
			existingOpenID = account.OpenID
		}
		logx.Warn("充值回调玩家账号不匹配，拒绝回调",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Int64("playerId", order.PlayerID),
			zap.String("reqPassport", req.Passport),
			zap.String("existingOpenId", existingOpenID),
		)
		return model.ErrRechargeInvalidAccount
	}

	// 4. 订单已处理完成（重复回调）：直接返回成功，不再重复通知 GameLogic
	if order.PaymentStatus == model.PaymentStatusSuccess {
		logx.Info("充值回调订单已处理完成，幂等返回成功",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
		)
		return nil
	}

	// 5. CAS 标记支付成功（WAIT → SUCCESS，同一次原子更新落归因信息并返回最新订单）
	updated, casErr := paymentOrderRepo.CASMarkPaySuccess(
		ctx, order.OrderID, order.Version,
		req.Order, amountCents, order.Currency,
		req.Agent, req.Server, req.Param,
	)
	if casErr != nil {
		// 唯一索引兜底：thirdTransactionId 在并发下被其他订单占用时视为重复订单号
		if mongox.IsDuplicateKeyError(casErr) {
			logx.Warn("充值订单标记支付成功：渠道交易ID重复",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
				zap.Error(casErr),
			)
			return model.ErrRechargeDuplicateOrder
		}
		logx.Error("充值订单标记支付成功失败",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Error(casErr),
		)
		return fmt.Errorf("充值订单标记支付成功失败: %w", casErr)
	}
	if updated == nil {
		// CAS 未命中：并发下订单已被其他请求处理，重查最新状态以区分返回语义
		latest, queryErr := paymentOrderRepo.FindByOrderID(ctx, order.OrderID)
		if queryErr != nil {
			logx.Error("充值订单CAS未命中后重查失败",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
				zap.Error(queryErr),
			)
			return fmt.Errorf("重查充值订单失败: %w", queryErr)
		}
		switch latest.PaymentStatus {
		case model.PaymentStatusSuccess:
			logx.Info("充值订单并发处理已完成，幂等返回成功",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
			)
			return nil
		case model.PaymentStatusFailed:
			logx.Warn("充值订单状态为失败，拒绝回调",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
				zap.String("paymentStatus", latest.PaymentStatus),
			)
			return model.ErrRechargeInvalidInfo
		default:
			logx.Warn("充值订单CAS标记失败，疑似重复处理",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
			)
			return model.ErrRechargeDuplicateOrder
		}
	}
	order = updated

	// 6. 通知 GameLogic 发放（复用现有支付通知机制；失败由补通知任务兜底重试）
	// 注意：CAS 已标记支付成功，即使通知失败也返回成功给三方平台，避免三方平台无意义重试。
	// 补通知定时任务会持续重试直至 GameLogic ACK。
	if err = l.notifyGamelogic(ctx, order, iapdto.NotifySourceChannel, requestID); err != nil {
		logx.Error("充值通知GameLogic失败，补通知任务将兜底重试",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Int64("userId", order.PlayerID),
			zap.Error(err),
		)
		// 不返回错误，三方平台收到成功即可，GameLogic 侧由补通知兜底
	}

	logx.Info("三方充值成功",
		zap.String("requestId", requestID),
		zap.String("orderId", order.OrderID),
		zap.Int64("userId", order.PlayerID),
		zap.String("agent", req.Agent),
		zap.String("server", req.Server),
		zap.Int64("amount", amountCents),
	)
	return nil
}

// RetryPendingNotifications 补通知定时任务：扫描支付成功但通知未成功的订单重新通知
// 仅处理未超出重试上限（次数 < NotifyMaxRetryCount 且支付时间在 NotifyMaxRetryDuration 内）的订单；
// 超限订单由 alertExhaustedOrders 单独扫描告警，需人工介入处理。
// 由定时任务周期性调用。
func (l *IAPLogic) RetryPendingNotifications(ctx context.Context) error {
	paymentOrderRepo := l.repos()

	orders, err := paymentOrderRepo.FindPendingNotifyOrders(ctx, 50)
	if err != nil {
		logx.Error("扫描待通知订单失败", zap.Error(err))
		return err
	}

	for _, order := range orders {
		if err = l.notifyGamelogic(ctx, order, order.NotifySource, "retry"); err != nil {
			logx.Warn("补通知失败",
				zap.String("orderId", order.OrderID),
				zap.Int("notifyCount", order.NotifyCount),
				zap.Error(err),
			)
		}
	}

	// 顺带扫描已超限订单并告警，便于运维发现需要人工处理的异常单
	l.alertExhaustedOrders(ctx, paymentOrderRepo)
	return nil
}

// SimulateMarkPaid 模拟补单定时任务（仅 local/dev 环境注册）。
// 功能：扫描待支付订单，模拟渠道支付成功并回调 GameLogic，用于本地/开发环境联调补单链路。
// 说明：与真实支付流程共用 CASMarkPaySuccess（乐观锁）与 notifyGamelogic（补通知复用 notifyId）；
// 模拟标记后订单落入正常支付成功路径，即使回调失败也会被现有 retryPendingNotifications 任务接手补偿。
func (l *IAPLogic) SimulateMarkPaid(ctx context.Context) error {
	paymentOrderRepo := l.repos()

	orders, err := paymentOrderRepo.FindWaitOrders(ctx, mockMarkPaidBatchSize)
	if err != nil {
		logx.Error("模拟补单扫描待支付订单失败", zap.Error(err))
		return err
	}

	for _, order := range orders {
		// 构造模拟渠道交易ID，仅用于本地联调
		thirdTransactionID := mockPayTxPrefix + order.OrderID

		// 乐观锁标记支付成功并返回最新订单，避免并发重复处理
		updated, casErr := paymentOrderRepo.CASMarkPaySuccess(ctx, order.OrderID, order.Version, thirdTransactionID, order.Amount, order.Currency, "", "", "")
		if casErr != nil {
			logx.Warn("模拟补单标记支付成功失败",
				zap.String("orderId", order.OrderID),
				zap.Error(casErr),
			)
			continue
		}
		if updated == nil {
			// 已被其他请求抢先标记，跳过
			continue
		}
		order = updated

		if notifyErr := l.notifyGamelogic(ctx, order, iapdto.NotifySourceChannel, mockMarkPaidRequestID); notifyErr != nil {
			logx.Warn("模拟补单回调GameLogic失败",
				zap.String("orderId", order.OrderID),
				zap.Int64("playerId", order.PlayerID),
				zap.Error(notifyErr),
			)
		}

		logx.Info("模拟补单完成",
			zap.String("orderId", order.OrderID),
			zap.Int64("playerId", order.PlayerID),
			zap.String("thirdTransactionId", thirdTransactionID),
		)
	}
	return nil
}

// isWhitelistedPayment 判断玩家账号是否处于充值白名单（下单时用于决定是否自动支付完成）。
// 按订单 playerId 查询账号取 openId，再查白名单；账号不存在/查询失败一律返回 false 并告警，
// 白名单不阻断非白名单下单，也不因白名单/账号查询故障误伤正常支付。
//
// 参数：
//   - ctx:      请求上下文
//   - playerID: 订单所属玩家ID
//   - requestID: 请求追踪ID
//
// 返回：true 表示该账号在白名单，下单应收款自动完成
func (l *IAPLogic) isWhitelistedPayment(ctx context.Context, playerID int64, requestID string) bool {
	account, err := repo.NewAccountRepo(l.mongoSvc.Client()).FindByPlayerID(ctx, playerID)
	if err != nil {
		logx.Warn("白名单支付判定：查询账号失败，按非白名单处理",
			zap.String("requestId", requestID),
			zap.Int64("playerId", playerID),
			zap.Error(err),
		)
		return false
	}
	if account == nil {
		return false
	}
	ok, wErr := repo.NewPayWhitelistRepo(l.mongoSvc.Client()).IsWhitelisted(ctx, account.OpenID)
	if wErr != nil {
		logx.Warn("白名单支付判定：查询白名单失败，按非白名单处理",
			zap.String("requestId", requestID),
			zap.String("openId", account.OpenID),
			zap.Error(wErr),
		)
		return false
	}
	if ok {
		logx.Info("账号命中充值白名单，下单将自动支付完成",
			zap.String("requestId", requestID),
			zap.Int64("playerId", playerID),
			zap.String("openId", account.OpenID),
		)
	}
	return ok
}

// markPaidForWhitelist 将订单按白名单自动标记为支付成功并通知 GameLogic 发放。
// 复用 CASMarkPaySuccess（WAIT→SUCCESS，乐观锁）与 notifyGamelogic（复用接口幂等与补通知兜底），
// 与真实支付、GMT 补单共用同一收单路径；CAS 未命中（并发已支付）/失败仅告警，不影响下单返回。
//
// 参数：
//   - ctx:      请求上下文
//   - order:    已插入的 WAIT 订单
//   - requestID: 请求追踪ID
func (l *IAPLogic) markPaidForWhitelist(ctx context.Context, order *model.PaymentOrder, requestID string) {
	paymentOrderRepo := l.repos()

	// 白名单自动支付交易号：whitelist_<orderId>（不占用真实渠道交易号命名空间）
	thirdTransactionID := whitelistPayTxPrefix + order.OrderID

	updated, casErr := paymentOrderRepo.CASMarkPaySuccess(ctx, order.OrderID, order.Version, thirdTransactionID, order.Amount, order.Currency, "", "", "")
	if casErr != nil {
		logx.Error("白名单自动支付：标记订单失败",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Error(casErr),
		)
		return
	}
	if updated == nil {
		// 乐观锁未命中：并发下已被其他请求标记支付成功，跳过通知（既有状态机已保证最终一致）
		logx.Warn("白名单自动支付：订单已被并发处理，跳过",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
		)
		return
	}
	order = updated

	// 通知 GameLogic 发放（失败由补通知定时任务兜底重试，与渠道回调语义一致）
	if notifyErr := l.notifyGamelogic(ctx, order, iapdto.NotifySourceChannel, requestID); notifyErr != nil {
		logx.Error("白名单自动支付：通知GameLogic失败，补通知任务将兜底重试",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Int64("userId", order.PlayerID),
			zap.Error(notifyErr),
		)
	}

	logx.Info("白名单下单自动支付完成",
		zap.String("requestId", requestID),
		zap.String("orderId", order.OrderID),
		zap.Int64("userId", order.PlayerID),
		zap.String("thirdTransactionId", thirdTransactionID),
	)
}

// alertExhaustedOrders 扫描已超出补通知限制的订单并打告警日志
// 超限条件：notifyCount >= NotifyMaxRetryCount 或 payTime 超过 NotifyMaxRetryDuration
// 这类订单已被 FindPendingNotifyOrders 排除在自动重试之外，需人工介入核查
func (l *IAPLogic) alertExhaustedOrders(ctx context.Context, paymentOrderRepo *repo.PaymentOrderRepo) {
	orders, err := paymentOrderRepo.FindExhaustedNotifyOrders(ctx, 20)
	if err != nil {
		logx.Warn("扫描超限通知订单失败", zap.Error(err))
		return
	}
	for _, order := range orders {
		logx.Error("支付通知已超出重试限制，需人工介入",
			zap.String("orderId", order.OrderID),
			zap.Int64("playerId", order.PlayerID),
			zap.Int("notifyCount", order.NotifyCount),
			zap.Int64("payTime", order.PayTime),
			zap.String("notifyStatus", order.NotifyStatus),
		)
	}
}

// notifyGamelogic 向 GameLogic 发送支付成功通知
// source: 本次通知来源（iapdto.NotifySourceChannel / NotifySourceGMT），
// 首次通知时连同 notifyID 一并固化，补通知沿用已固化来源；GMT 补单传入 gmt 可覆盖渠道来源，
// 使 GameLogic 侧对已关闭订单的来源判定保持一致。
// 同步调用，失败由调用方决定是否重试
func (l *IAPLogic) notifyGamelogic(ctx context.Context, order *model.PaymentOrder, source, requestID string) error {
	paymentOrderRepo := l.repos()

	// notifyID 固化复用：首次通知时生成，后续补通知沿用订单上已持久化的同一值，
	// 保证 Platform 日志、GameLogic 落库的 notifyId 一致，便于跨服务追溯。
	notifyID := order.NotifyID
	if notifyID == "" {
		notifyID = uuid.NewString()
		order.NotifyID = notifyID
	}
	// 通知来源：有明确来源时以本次调用为准（GMT 补单可覆盖历史渠道来源），否则沿用已固化来源
	if source != "" {
		order.NotifySource = source
	}
	notifyReq := iapdto.PayNotifyReq{
		NotifyID:           notifyID,
		Source:             order.NotifySource,
		OrderID:            order.OrderID,
		PlayerID:           order.PlayerID,
		ProductID:          order.ProductID,
		Channel:            order.Channel,
		ThirdTransactionID: order.ThirdTransactionID,
		Amount:             order.Amount,
		Currency:           order.Currency,
		Agent:              order.Agent,
		Server:             order.Server,
		Param:              order.Param,
	}

	// 序列化请求体
	bodyBytes, err := sonic.Marshal(notifyReq)
	if err != nil {
		return fmt.Errorf("序列化通知请求失败: %w", err)
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	signature := signx.Sign([]byte(l.internalSecret), timestamp, nonce, bodyBytes)

	// 发送 HTTP 通知
	client := l.gamelogicSvc.Client()
	if client == nil {
		return fmt.Errorf("GameLogic 服务客户端未就绪")
	}

	// 构建并发送 HTTP 通知
	resp, err := client.Post(ctx, gamelogicNotifyPath, bodyBytes,
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Signature", signature),
		httpcli.WithHeader("X-Timestamp", timestamp),
		httpcli.WithHeader("X-Nonce", nonce),
	)

	isSuccess := false
	if err != nil {
		logx.Error("通知 GameLogic 失败",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.String("notifyId", notifyID),
			zap.Error(err),
		)
	} else {
		defer resp.Close()
		if resp.StatusCode() == 200 {
			// 解析统一响应信封，确认业务 code == 0 才视为真正成功
			var envelope struct {
				Code int `json:"code"`
			}
			if jsonErr := sonic.Unmarshal(resp.Body(), &envelope); jsonErr != nil {
				logx.Error("通知 GameLogic 响应体解析失败",
					zap.String("requestId", requestID),
					zap.String("orderId", order.OrderID),
					zap.String("notifyId", notifyID),
					zap.String("respBody", resp.String()),
					zap.Error(jsonErr),
				)
			} else if envelope.Code == 0 {
				isSuccess = true
			} else {
				logx.Error("通知 GameLogic 返回业务错误",
					zap.String("requestId", requestID),
					zap.String("orderId", order.OrderID),
					zap.String("notifyId", notifyID),
					zap.Int("bizCode", envelope.Code),
					zap.String("respBody", resp.String()),
				)
			}
		} else {
			logx.Error("通知 GameLogic 返回非200",
				zap.String("requestId", requestID),
				zap.String("orderId", order.OrderID),
				zap.String("notifyId", notifyID),
				zap.Int("statusCode", resp.StatusCode()),
				zap.String("respBody", resp.String()),
			)
		}
	}

	// 更新通知状态，原子递增通知次数（$inc，避免并发覆盖）
	notifyStatus := model.NotifyStatusFailed
	if isSuccess {
		notifyStatus = model.NotifyStatusSuccess
	}
	if updateErr := paymentOrderRepo.UpdateNotifyStatus(ctx, order.OrderID, notifyID, order.NotifySource, notifyStatus); updateErr != nil {
		logx.Warn("更新通知状态失败",
			zap.String("requestId", requestID),
			zap.String("orderId", order.OrderID),
			zap.Error(updateErr),
		)
	}

	if !isSuccess {
		return fmt.Errorf("通知 GameLogic 未成功: orderId=%s", order.OrderID)
	}

	logx.Info("通知 GameLogic 成功",
		zap.String("requestId", requestID),
		zap.String("orderId", order.OrderID),
		zap.String("notifyId", notifyID),
	)
	return nil
}

// findProductByPayID 从 IAPProductGlobal 配置表中按 PayID 查找商品
func (l *IAPLogic) findProductByPayID(payID string) *gdconfig.IAPProductGlobalConfig {
	table := l.gdConfig.IAPProductGlobal.Load()
	if table == nil {
		return nil
	}
	for _, cfg := range table.GetAll() {
		if cfg.PayID == payID {
			return cfg
		}
	}
	return nil
}

// channelGoodIDByConfig 按支付渠道取商品配置中的渠道内购ID。
// apple 取 corre1（ios内购ID），google 取 corre2（google内购ID），其他渠道返回空串。
// 返回空串表示该渠道未配置对应内购ID，调用方（下单）应视为商品不可用而拒绝。
func channelGoodIDByConfig(cfg *gdconfig.IAPProductGlobalConfig, channel string) string {
	if cfg == nil {
		return ""
	}
	switch channel {
	case model.ChannelApple:
		return cfg.Corre1
	case model.ChannelGoogle:
		return cfg.Corre2
	default:
		return ""
	}
}
