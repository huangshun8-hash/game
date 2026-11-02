package logic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/async"
	"kemiluo/pkg/authx"
	"kemiluo/pkg/httpcli"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/packetx"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/signx"
	"kemiluo/pkg/wsx"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/authdto"
	"kemiluo/services/shared/shareddto"
	"kemiluo/services/shared/sharedmodel"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// platformAuthResp 平台服务返回的认证结果
type platformAuthResp = shareddto.PlatformAuthResp

// AuthLogic 认证业务逻辑
type AuthLogic struct {
	mongoSvc    *mongox.Service
	jwt         *authx.JWT
	tokenStore  *authx.TokenStore
	asyncSvc    *async.Service
	platformSvc *httpcli.Service
	hub         *wsx.Hub
	// redisSvc Redis 服务，用于顶号事件跨实例广播（可能为 nil，单实例部署时无需广播）
	redisSvc *redisx.Service
	// instanceID 当前进程实例标识，用于顶号广播时跳过自身（避免重复处理）
	instanceID string
	// internalSecret 内部服务间调用签名密钥（调用 platform 内部接口时使用）
	internalSecret string
}

// NewAuthLogic 创建认证业务逻辑
// tokenStore: 会话 Token Redis 存储，可能为 nil（Redis 未就绪时退化为纯 JWT 校验）
// platformSvc: 平台服务 HTTP 客户端
// hub: WebSocket 会话管理器，用于注销/顶号时强制断开玩家长连接，可为 nil（未启用长连接时跳过）
// internalSecret: 内部服务间调用签名密钥
// redisSvc: Redis 服务，用于顶号事件跨实例广播，可为 nil（单实例部署时仅处理本实例连接）
// instanceID: 当前进程实例标识，写入顶号广播消息供订阅端跳过自身
func NewAuthLogic(mongoSvc *mongox.Service, jwt *authx.JWT, tokenStore *authx.TokenStore, asyncSvc *async.Service, platformSvc *httpcli.Service, hub *wsx.Hub, internalSecret string, redisSvc *redisx.Service, instanceID string) *AuthLogic {
	return &AuthLogic{
		mongoSvc:       mongoSvc,
		jwt:            jwt,
		tokenStore:     tokenStore,
		asyncSvc:       asyncSvc,
		platformSvc:    platformSvc,
		hub:            hub,
		redisSvc:       redisSvc,
		instanceID:     instanceID,
		internalSecret: internalSecret,
	}
}

func (l *AuthLogic) playerRepo() *repo.PlayerRepo {
	return repo.NewPlayerRepo(l.mongoSvc.Client())
}

// Login 统一登录流程：平台验证 → 查找/创建玩家 → 签发令牌 → 顶掉旧端
// req: proto 请求消息（业务字段）
// requestID: 请求追踪ID，由 handler 从 Context 读取后显式传入
// clientIP: 登录请求的客户端 IP（用于顶号通知中展示新登录端信息）
func (l *AuthLogic) Login(ctx context.Context, req *cspb.LoginReq, requestID, clientIP string) (*cspb.LoginResp, error) {
	authResp, err := l.verifyWithPlatform(ctx, req, requestID)
	if err != nil {
		return nil, err
	}

	playerRepo := l.playerRepo()

	player, err := playerRepo.FindByOpenID(ctx, authResp.OpenID)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("查询玩家失败: %w", err)
	}

	isNew := false
	isBound := false

	if player == nil && authResp.LoginType != model.LoginTypeGuest && req.Uuid != "" {
		guestPlayer, guestErr := playerRepo.FindByOpenID(ctx, req.Uuid)
		if guestErr != nil && guestErr != mongox.ErrNotFound {
			return nil, fmt.Errorf("查询游客账号失败: %w", guestErr)
		}
		if guestPlayer != nil && guestPlayer.IsGuest {
			upgradeErr := playerRepo.UpgradeFromGuest(ctx, guestPlayer.PlayerID, authResp.OpenID, authResp.LoginType, req.Account)
			if upgradeErr != nil {
				logx.Error("游客账号升级写入失败，本次降级为游客身份继续，下次登录自动重试",
					zap.String("requestId", requestID),
					zap.Int64("playerId", guestPlayer.PlayerID),
					zap.String("openId", authResp.OpenID),
					zap.String("loginType", authResp.LoginType),
					zap.Error(upgradeErr),
				)
				player = guestPlayer
			} else {
				guestPlayer.OpenID = authResp.OpenID
				guestPlayer.LoginType = authResp.LoginType
				guestPlayer.Account = req.Account
				guestPlayer.IsGuest = false
				player = guestPlayer
				isBound = true
				logx.Info("游客账号绑定成功",
					zap.String("requestId", requestID),
					zap.Int64("playerId", player.PlayerID),
					zap.String("openId", authResp.OpenID),
					zap.String("loginType", authResp.LoginType),
				)
			}
		}
	}

	// 顶号判定基准：本次登录会用请求中的 clientInfo 覆盖库里的设备信息，必须先取出登录前的设备标识
	previousDeviceID := ""
	if player != nil {
		previousDeviceID = player.ClientInfo.DeviceID
	}

	if player != nil {
		if player.IsBanned() {
			return nil, model.ErrPlayerBanned
		}
		if req.ClientInfo != nil {
			ci := protoClientInfoToModel(req.ClientInfo)
			if updateErr := playerRepo.UpdateLastLogin(ctx, player.PlayerID, ci); updateErr != nil {
				logx.Warn("更新最后登录时间失败",
					zap.String("requestId", requestID),
					zap.Int64("playerId", player.PlayerID),
					zap.Error(updateErr),
				)
			}
		} else {
			if updateErr := playerRepo.UpdateLastLoginTime(ctx, player.PlayerID); updateErr != nil {
				logx.Warn("更新最后登录时间失败",
					zap.String("requestId", requestID),
					zap.Int64("playerId", player.PlayerID),
					zap.Error(updateErr),
				)
			}
		}
	} else {
		isNew = true
		player, err = l.createPlayer(ctx, playerRepo, authResp, req, requestID)
		if err != nil {
			return nil, err
		}
	}

	resp, err := l.issueToken(ctx, player, requestID)
	if err != nil {
		return nil, err
	}

	resp.Data.IsNew = isNew
	resp.Data.IsBound = isBound

	// 严格单端：设备标识变化时把旧端踢下线（先下发 WS 顶号通知，再断开连接）。
	// 时机必须在签发 Token 之后：TokenStore 已完成覆盖，旧端此时重连会被握手校验拒绝；
	// 也必须在返回响应之前：新端尚未拿到 Token、不可能建连，避免把新连接误判为旧端踢掉。
	if shouldKickReplacedSession(isNew, previousDeviceID, loginDeviceID(req)) {
		l.kickReplacedSession(player.PlayerID, req, resp.Data.Token, clientIP, requestID)
	}

	l.logLoginAsync(player.PlayerID, authResp.OpenID, requestID)

	return resp, nil
}

// loginDeviceID 返回本次登录请求上报的设备标识；未上报时返回空串
func loginDeviceID(req *cspb.LoginReq) string {
	if req == nil || req.ClientInfo == nil {
		return ""
	}
	return req.ClientInfo.DeviceId
}

// shouldKickReplacedSession 判定本次登录是否需要顶掉旧端。
// 新注册玩家必然没有旧连接，直接跳过（省一次无意义的广播）；其余情况按设备标识是否变化判定。
func shouldKickReplacedSession(isNew bool, previous, current string) bool {
	return !isNew && isDeviceChanged(previous, current)
}

// isDeviceChanged 判断本次登录是否来自新的设备端。
// 严格单端策略下，只有「两次都上报了设备标识且完全一致」才视为同一端：
// 缺省 deviceId 无法证明是同端，保守按「换端」处理，避免单端策略被空值绕过。
// 登录入口已强制要求 clientInfo.deviceId，故正常链路下 current 不会为空；
// 保留空值判定是防御性兜底（如内部调用方绕过 handler 校验）。
func isDeviceChanged(previous, current string) bool {
	if previous == "" || current == "" {
		return true
	}
	return previous != current
}

// buildKickedOfflinePush 构建顶号通知负载，携带新登录端信息供旧端展示登录来源。
// 参数：
//   - req:      本次登录请求
//   - clientIP: 本次登录请求的客户端 IP
//   - loginAt:  本次登录时间（Unix 毫秒）
func buildKickedOfflinePush(req *cspb.LoginReq, clientIP string, loginAt int64) *cspb.KickedOfflinePush {
	payload := &cspb.KickedOfflinePush{
		LoginAt: loginAt,
		Ip:      clientIP,
	}
	if req == nil {
		return payload
	}
	payload.Platform = req.Platform
	payload.DeviceId = loginDeviceID(req)
	if req.ClientInfo != nil {
		payload.DeviceName = req.ClientInfo.DeviceName
	}
	return payload
}

// kickReplacedSession 设备标识变化时把旧端踢下线：先下发 WS 顶号通知，再断开长连接。
// 本地实例直接处理，同时通过 Redis Pub/Sub 广播到其它实例——每个实例的 Hub 只管理本进程连接，
// 登录请求与玩家旧连接可能落在不同实例，仅靠本地 kick 会漏踢远端连接。
// 通知与断开属收尾动作，失败仅记录日志，不影响登录结果：Token 已被新登录覆盖，
// 旧端后续 HTTP 请求/握手同样会因令牌失效被拒绝。
//
// 参数：
//   - playerID:  登录成功的玩家ID
//   - req:       本次登录请求（提供新端平台与设备信息）
//   - token:     本次新签发的 Token（其摘要用于广播时跳过新登录端连接）
//   - clientIP:  本次登录请求的客户端 IP
//   - requestID: 登录请求追踪ID
func (l *AuthLogic) kickReplacedSession(playerID int64, req *cspb.LoginReq, token, clientIP, requestID string) {
	loginAt := time.Now().UnixMilli()
	payload := buildKickedOfflinePush(req, clientIP, loginAt)
	tokenDigest := wsx.TokenDigest(token)

	if l.hub != nil {
		closed := l.hub.KickPlayerWithNoticeExcept(playerID, &wsx.PushPayload{
			// bizId 使用登录请求ID，保证一次顶号事件的推送唯一可追溯
			BizID: requestID,
			Data:  payload,
		}, tokenDigest)
		if closed > 0 {
			logx.Info("登录设备变更，已通知并断开旧端长连接",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("platform", payload.Platform),
				zap.String("deviceId", payload.DeviceId),
				zap.String("ip", clientIP),
				zap.Int("closedConnections", closed),
			)
		}
	}

	l.publishSessionKicked(authdto.SessionKickedMessage{
		PlayerID:       playerID,
		LoginAt:        loginAt,
		Platform:       payload.Platform,
		DeviceName:     payload.DeviceName,
		DeviceID:       payload.DeviceId,
		IP:             clientIP,
		TokenDigest:    tokenDigest,
		OriginInstance: l.instanceID,
		RequestID:      requestID,
	})
}

// publishSessionKicked 把顶号事件广播到其它 GameLogic 实例，由持有旧连接的实例执行通知与断开。
// redisSvc 为 nil（未启用 Redis）时跳过，退化为仅处理本实例连接。
// 消息携带发起实例标识与保留 Token 摘要：订阅端跳过自身、并跳过新登录端已建立的连接。
func (l *AuthLogic) publishSessionKicked(msg authdto.SessionKickedMessage) {
	if l.redisSvc == nil {
		return
	}
	client := l.redisSvc.Client()
	if client == nil {
		return
	}

	body, err := sonic.Marshal(&msg)
	if err != nil {
		logx.Warn("顶号广播消息序列化失败，仅本地实例完成断开",
			zap.String("requestId", msg.RequestID),
			zap.Int64("userId", msg.PlayerID),
			zap.Error(err),
		)
		return
	}

	// 独立超时上下文：登录响应不应因为跨实例广播（非关键路径）长时间阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := client.Raw().Publish(ctx, authdto.SessionKickedChannel, body).Result(); err != nil {
		logx.Warn("顶号广播发布失败，仅本地实例完成断开",
			zap.String("requestId", msg.RequestID),
			zap.Int64("userId", msg.PlayerID),
			zap.Error(err),
		)
	}
}

// verifyWithPlatform 调用平台服务验证身份
func (l *AuthLogic) verifyWithPlatform(ctx context.Context, req *cspb.LoginReq, requestID string) (*platformAuthResp, error) {
	reqPath := "/internal/account/auth"

	var clientInfo *sharedmodel.ClientInfo
	if req.ClientInfo != nil {
		ci := protoClientInfoToModel(req.ClientInfo)
		clientInfo = &ci
	}

	platformReq := shareddto.PlatformAuthReq{
		LoginType:  req.LoginType,
		Platform:   req.Platform,
		OpenID:     req.Uuid,
		AuthToken:  req.AuthToken,
		Account:    req.Account,
		ClientInfo: clientInfo,
	}

	// 透传三方数据（若客户端携带），供 platform 侧签名校验
	if req.ThirdYre != nil {
		platformReq.ThirdYre = &shareddto.ThirdYre{
			T:        req.ThirdYre.T,
			SiteCode: req.ThirdYre.SiteCode,
			Ck:       req.ThirdYre.Ck,
			Uid:      req.ThirdYre.Uid,
		}
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	bodyBytes, err := sonic.Marshal(platformReq)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	nonce := uuid.NewString()
	signature := signx.Sign([]byte(l.platformSvc.Secret()), timestamp, nonce, bodyBytes)

	client := l.platformSvc.Client()
	if client == nil {
		return nil, fmt.Errorf("平台服务客户端未就绪")
	}
	resp, err := client.Post(ctx, reqPath, bodyBytes,
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
		httpcli.WithHeader("X-Signature", signature),
		httpcli.WithHeader("X-Timestamp", timestamp),
		httpcli.WithHeader("X-Nonce", nonce),
	)
	if err != nil {
		logx.Error("调用平台认证接口失败",
			zap.String("requestId", requestID),
			zap.String("url", reqPath),
			zap.Any("reqBody", platformReq),
			zap.Error(err),
		)
		return nil, fiber.NewError(fiber.StatusBadGateway, "auth service unavailable")
	}
	defer resp.Close()

	var result httpx.Response
	if err := resp.JSON(&result); err != nil {
		return nil, fiber.NewError(fiber.StatusBadGateway, "invalid auth service response")
	}

	if result.Code != 0 {
		statusCode := resp.StatusCode()
		if statusCode >= 500 {
			logx.Error("平台认证服务返回错误",
				zap.String("requestId", requestID),
				zap.String("url", reqPath),
				zap.Int("status", statusCode),
				zap.String("message", result.Message),
			)
			return nil, fiber.NewError(fiber.StatusBadGateway, "auth service unavailable")
		}
		// 平台内部接口遵循"统一 HTTP 200 + code 字段"约定，业务性拒绝不应被误判为 500，
		// 统一透传为 400，由 handler 层识别为平台拒绝而非服务内部错误
		if statusCode < 400 {
			statusCode = fiber.StatusBadRequest
		}
		return nil, fiber.NewError(statusCode, result.Message)
	}

	authResp, err := l.parsePlatformAuthResp(result.Data)
	if err != nil {
		return nil, fmt.Errorf("解析平台认证结果失败: %w", err)
	}
	return authResp, nil
}

func (l *AuthLogic) parsePlatformAuthResp(data any) (*platformAuthResp, error) {
	bytes, err := sonic.Marshal(data)
	if err != nil {
		return nil, err
	}
	var resp platformAuthResp
	if err := sonic.Unmarshal(bytes, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// createPlayer 创建新玩家
func (l *AuthLogic) createPlayer(ctx context.Context, playerRepo *repo.PlayerRepo, authResp *platformAuthResp, req *cspb.LoginReq, requestID string) (*model.Player, error) {
	// 平台侧 PlayerID 由 counters 自增生成（起始 15000001），此处作为跨服务边界的防御性校验：
	// 若平台异常返回非正值，拒绝创建，避免生成 "Player0" 等无效默认昵称并落库
	if authResp.PlayerID <= 0 {
		logx.Error("平台返回的 playerID 无效，拒绝创建玩家",
			zap.String("requestId", requestID),
			zap.Int64("playerId", authResp.PlayerID),
			zap.String("openId", authResp.OpenID),
		)
		return nil, fmt.Errorf("平台返回的 playerID 无效: %d", authResp.PlayerID)
	}

	id := bson.NewObjectID()

	// 注册时为玩家生成专属存档加密秘钥，每位玩家各不相同，登录时下发给客户端。
	// GenerateKey 内部保证必定成功：熵源不可用时自动降级兜底，此处必然拿到有效秘钥
	saveKey := packetx.GenerateKey()

	player := &model.Player{
		ID:        id,
		PlayerID:  authResp.PlayerID,
		OpenID:    authResp.OpenID,
		LoginType: authResp.LoginType,
		Account:   req.Account,
		Platform:  req.Platform,
		ServerID:  model.DefaultServerID,
		IsGuest:   authResp.LoginType == model.LoginTypeGuest,
		SaveKey:   saveKey,
		Status:    model.StatusNormal,
	}
	if req.ClientInfo != nil {
		player.ClientInfo = protoClientInfoToModel(req.ClientInfo)
	}

	err := playerRepo.InsertPlayer(ctx, player)
	if err != nil {
		if mongox.IsDuplicateKeyError(err) {
			player, err = playerRepo.FindByOpenID(ctx, player.OpenID)
			if err != nil {
				return nil, fmt.Errorf("并发创建后查询玩家失败: %w", err)
			}
			return player, nil
		}
		return nil, fmt.Errorf("创建玩家失败: %w", err)
	}

	logx.Info("玩家创建成功",
		zap.String("requestId", requestID),
		zap.Int64("playerId", player.PlayerID),
		zap.String("openId", player.OpenID),
	)
	return player, nil
}

// issueToken 签发令牌并写入 Redis
// 玩家存档加密秘钥（base64）随 JWT Claims 一并签发，后续请求可从 Token 直接取用，
// 读取自身 Base 存档时免去每次查询 MongoDB 获取秘钥
func (l *AuthLogic) issueToken(ctx context.Context, player *model.Player, requestID string) (*cspb.LoginResp, error) {
	playerIDStr := strconv.FormatInt(player.PlayerID, 10)

	token, err := l.jwt.GenerateToken(playerIDStr, player.LoginType, player.SaveKey)
	if err != nil {
		return nil, fmt.Errorf("生成令牌失败: %w", err)
	}

	if l.tokenStore != nil {
		if err := l.tokenStore.Save(ctx, playerIDStr, token, l.jwt.TokenTTL()); err != nil {
			logx.Warn("写入 Token 至 Redis 失败，降级为纯 JWT 校验",
				zap.String("requestId", requestID),
				zap.Int64("playerId", player.PlayerID),
				zap.Error(err),
			)
		}
	}

	// 存储层 saveKey 为 base64 字符串，协议 bytes 字段下发原始 32 字节二进制，需解码后下发
	rawKey, keyErr := packetx.DecodeKey(player.SaveKey)
	if keyErr != nil && player.SaveKey != "" {
		logx.Warn("登录下发存档秘钥解码失败，按无秘钥下发",
			zap.String("requestId", requestID),
			zap.Int64("playerId", player.PlayerID),
			zap.Error(keyErr),
		)
	}

	return &cspb.LoginResp{
		Data: &cspb.LoginData{
			Token:    token,
			PlayerId: player.PlayerID,
			ServerId: player.ServerID,
			SaveKey:  rawKey,
		},
	}, nil
}

// logLoginAsync 异步记录登录日志
func (l *AuthLogic) logLoginAsync(playerID int64, openID, requestID string) {
	if l.asyncSvc == nil {
		return
	}
	ctx := async.NewContext().
		Set("playerId", playerID).
		Set("openId", openID).
		Set("requestId", requestID)
	l.asyncSvc.SubmitFunc("log", "loginLog", func(c *async.Context) error {
		logx.Info("玩家登录",
			zap.String("requestId", c.GetString("requestId")),
			zap.Int64("playerId", c.GetInt64("playerId")),
			zap.String("openId", c.GetString("openId")),
		)
		return nil
	}, ctx)
}

// Deactivate 账号注销：作废玩家的登录身份，原 Player 永久保留。
// 设计原则：注销不是删除 Player，而是永久作废 Account 的登录身份——
//  1. 调用 Platform 内部接口作废 Account.openId（两侧 openId 唯一索引须同步释放，否则下次登录会串号）；
//  2. 本地作废 Player.openId（追加 del_ 前缀释放原身份，Player 数据全部保留）；
//  3. 撤销 Redis Token 并强制断开长连接，使玩家立即下线。
//
// 注销后再次以原身份（游客 UUID / 三方 account）登录时，两侧均查不到旧记录，将创建全新的 Account + Player。
//
// 步骤顺序说明：先作废 Platform 侧 Account（登录链路的第一道身份查询在 Platform），
// 成功后再作废本地 Player。两侧均把「记录不存在 / 已注销」按幂等成功处理，
// 保证任一侧已释放时仍能继续释放另一侧；仅真实错误（写库失败等）才中止并返回错误。
//
// 一致性说明：两侧跨服务写入无分布式事务。若 Platform 已作废而本地 Player 作废失败，
// 会返回错误让客户端重试——重试时 Platform 侧按幂等成功放行、本地继续作废，最终收敛一致。
// Token 撤销/踢连接为收尾清理，失败仅告警不影响注销结果（Token 会在 TTL 到期后自然失效）。
//
// 参数：
//   - ctx:       请求上下文
//   - playerID:  待注销玩家的唯一标识（取自认证上下文，禁止客户端伪造）
//   - requestID: 请求追踪ID
//
// 返回：错误（nil 表示注销成功）
func (l *AuthLogic) Deactivate(ctx context.Context, playerID int64, requestID string) error {
	if playerID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid player id")
	}

	// 1. 先作废 Platform 侧 Account 登录身份（登录链路首查在 Platform，先释放此处防止串号）
	if err := l.deactivatePlatformAccount(ctx, playerID, requestID); err != nil {
		return err
	}

	// 2. 作废本地 Player 登录身份（Player 数据全部保留，仅释放 openId 并置为已注销状态）
	playerRepo := l.playerRepo()
	_, err := playerRepo.DeactivateByPlayerID(ctx, playerID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) || errors.Is(err, model.ErrNoRowsUpdated) {
			// Player 不存在或已注销：Platform 侧已作废，本地视为幂等成功，继续收尾清理
			logx.Warn("本地玩家不存在或已注销，按幂等处理",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
			)
		} else {
			// 此时 Platform 侧 Account 已作废、本地 Player 未作废，两侧短暂不一致：
			// 返回错误由客户端重试，重试时 Platform 按幂等成功放行、本地继续作废后最终一致
			logx.Error("作废本地玩家登录身份失败，平台侧已作废，需重试收敛",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Error(err),
			)
			return model.ErrAccountDeactivateFailed
		}
	}

	// 3. 收尾清理：撤销 Token + 断开长连接，使玩家立即下线（失败仅告警）
	l.revokeToken(ctx, playerID, requestID)
	l.kickConnections(playerID, requestID)

	logx.Info("账号注销成功，登录身份已作废",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
	)
	return nil
}

// deactivatePlatformAccount 携带内部签名调用 Platform /internal/account/deactivate 作废账号登录身份。
// 与 verifyWithPlatform 相同的签名与错误映射约定：平台业务性拒绝透传为 400，5xx/网络错误透传为网关错误。
func (l *AuthLogic) deactivatePlatformAccount(ctx context.Context, playerID int64, requestID string) error {
	reqPath := "/internal/account/deactivate"

	platformReq := shareddto.PlatformDeactivateReq{PlayerID: playerID}
	bodyBytes, err := sonic.Marshal(platformReq)
	if err != nil {
		return fmt.Errorf("序列化注销请求体失败: %w", err)
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	nonce := uuid.NewString()
	signature := signx.Sign([]byte(l.internalSecret), timestamp, nonce, bodyBytes)

	client := l.platformSvc.Client()
	if client == nil {
		return fmt.Errorf("平台服务客户端未就绪")
	}
	resp, err := client.Post(ctx, reqPath, bodyBytes,
		httpcli.WithHeader("Content-Type", "application/json"),
		httpcli.WithHeader("X-Request-Id", requestID),
		httpcli.WithHeader("X-Signature", signature),
		httpcli.WithHeader("X-Timestamp", timestamp),
		httpcli.WithHeader("X-Nonce", nonce),
	)
	if err != nil {
		logx.Error("调用平台注销接口失败",
			zap.String("requestId", requestID),
			zap.String("url", reqPath),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return fiber.NewError(fiber.StatusBadGateway, "auth service unavailable")
	}
	defer resp.Close()

	var result httpx.Response
	if err := resp.JSON(&result); err != nil {
		return fiber.NewError(fiber.StatusBadGateway, "invalid auth service response")
	}

	if result.Code != 0 {
		statusCode := resp.StatusCode()
		// 平台侧所有响应（含 /internal 接口）都由 httpx.DefaultErrorHandler 统一以 HTTP 200 返回，
		// 业务结果仅体现在响应体 code 字段，故平台内部错误必须以 code 判定；
		// statusCode 仅用于兜底识别网关/代理层产生的真实 5xx。
		isPlatformInternalErr := statusCode >= 500 || result.Code == shareddto.PlatformErrCodeInternal
		if isPlatformInternalErr {
			logx.Error("平台注销服务返回错误",
				zap.String("requestId", requestID),
				zap.String("url", reqPath),
				zap.Int64("userId", playerID),
				zap.Int("status", statusCode),
				zap.Int("code", result.Code),
				zap.String("message", result.Message),
			)
			return fiber.NewError(fiber.StatusBadGateway, "auth service unavailable")
		}
		logx.Warn("平台注销账号被拒绝",
			zap.String("requestId", requestID),
			zap.String("url", reqPath),
			zap.Int64("userId", playerID),
			zap.Int("code", result.Code),
			zap.String("message", result.Message),
		)
		return model.ErrAccountDeactivateFailed
	}
	return nil
}

// revokeToken 撤销玩家 Token，使其立即下线（tokenStore 为 nil 或失败时仅告警）
func (l *AuthLogic) revokeToken(ctx context.Context, playerID int64, requestID string) {
	if l.tokenStore == nil {
		return
	}
	playerIDStr := strconv.FormatInt(playerID, 10)
	if err := l.tokenStore.Revoke(ctx, playerIDStr); err != nil {
		logx.Warn("注销后撤销 Token 失败，玩家 Token 将在过期后自动失效",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
	}
}

// kickConnections 断开玩家已建立的 WebSocket 长连接（hub 为 nil 时跳过）
func (l *AuthLogic) kickConnections(playerID int64, requestID string) {
	if l.hub == nil {
		return
	}
	if closed := l.hub.KickPlayer(playerID); closed > 0 {
		logx.Info("注销后已断开玩家长连接",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int("closedConnections", closed),
		)
	}
}

// protoClientInfoToModel 将 proto ClientInfo 转换为 sharedmodel.ClientInfo
func protoClientInfoToModel(ci *cspb.ClientInfo) sharedmodel.ClientInfo {
	return sharedmodel.ClientInfo{
		DeviceID:      ci.DeviceId,
		DeviceName:    ci.DeviceName,
		Brand:         ci.Brand,
		Model:         ci.Model,
		OS:            ci.Os,
		OSVersion:     ci.OsVersion,
		Language:      ci.Language,
		Country:       ci.Country,
		Timezone:      ci.Timezone,
		Network:       ci.Network,
		ScreenWidth:   int(ci.ScreenWidth),
		ScreenHeight:  int(ci.ScreenHeight),
		ClientVersion: ci.ClientVersion,
		BundleVersion: ci.BundleVersion,
		Channel:       ci.Channel,
		SDKVersion:    ci.SdkVersion,
		PackageName:   ci.PackageName,
		InstallID:     ci.InstallId,
		IsEmulator:    ci.IsEmulator,
	}
}
