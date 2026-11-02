package logic

import (
	"context"
	"errors"
	"fmt"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/dto"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/platform/internal/repo"

	"github.com/gofiber/fiber/v3"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// 三方数据签名校验失败的哨兵错误，供日志区分具体失败原因
var (
	// errThirdYreSecretMissing 未配置三方签名密钥
	errThirdYreSecretMissing = errors.New("third party secret not configured")
	// errThirdYreTimeWindow 时间戳超出容忍窗口
	errThirdYreTimeWindow = errors.New("third party timestamp out of window")
	// errThirdYreSignMismatch 签名不匹配
	errThirdYreSignMismatch = errors.New("third party signature mismatch")
)

// AuthLogic 认证业务逻辑，负责三方 token 验证和 Account 管理
type AuthLogic struct {
	mongoSvc         *mongox.Service
	thirdYreVerifier ThirdYreVerifier
}

// NewAuthLogic 创建认证业务逻辑
// mongoSvc: MongoDB 服务
// thirdPartyKey: 三方平台接口共用签名密钥，用于构造默认三方数据（thirdYre）签名校验器
func NewAuthLogic(mongoSvc *mongox.Service, thirdPartyKey string) *AuthLogic {
	return &AuthLogic{
		mongoSvc:         mongoSvc,
		thirdYreVerifier: NewThirdYreVerifier(thirdPartyKey),
	}
}

// NewAuthLogicWithVerifier 使用自定义三方数据校验器创建认证业务逻辑。
// 便于按需替换校验实现（如不同签名算法/时间窗）或在测试中注入桩实现。
func NewAuthLogicWithVerifier(mongoSvc *mongox.Service, verifier ThirdYreVerifier) *AuthLogic {
	return &AuthLogic{
		mongoSvc:         mongoSvc,
		thirdYreVerifier: verifier,
	}
}

// accountRepo 返回基于当前连接的账号仓库实例
func (a *AuthLogic) accountRepo() *repo.AccountRepo {
	return repo.NewAccountRepo(a.mongoSvc.Client())
}

// Auth 统一认证入口，根据 loginType 内部映射到对应的 token 验证逻辑
// loginType 是产品渠道标签，platform 维护渠道到验证方式的映射，调用方无需感知技术细节
func (a *AuthLogic) Auth(ctx context.Context, req *dto.AuthReq) (*dto.AuthResp, error) {
	// 三方数据签名校验：强制校验渠道（如 yre）必须携带 thirdYre 且校验通过；
	// 其余渠道携带 thirdYre 时做尽力校验，不携带则跳过
	if err := a.verifyThirdYre(req); err != nil {
		return nil, err
	}

	if req.LoginType == model.LoginTypeGuest {
		return a.guestAuth(ctx, req)
	}
	// 非游客渠道：仅白名单内的免验证渠道（SDK 已自行完成登录认证）可直接信任 account 登录；
	// 其余渠道（含已下线的 Apple / Google 登录方式）一律拒绝，避免"无验证放行"风险。
	if model.IsDirectAccountChannel(req.LoginType) {
		return a.directAccountAuth(ctx, req)
	}
	return nil, fiber.NewError(fiber.StatusBadRequest, "unsupported login type")
}

// directAccountAuth 免验证渠道认证：渠道 SDK 已完成登录认证，服务端不再校验 token，
// 直接信任客户端上报的第三方账号（account）作为身份标识（openID），查询或创建账号。
func (a *AuthLogic) directAccountAuth(ctx context.Context, req *dto.AuthReq) (*dto.AuthResp, error) {
	if req.Account == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "account is required for this login type")
	}
	return a.findOrCreateAccount(ctx, req, req.Account)
}

// guestAuth 游客认证：直接使用客户端传入的 UUID 作为 openID 查询或创建 Account
func (a *AuthLogic) guestAuth(ctx context.Context, req *dto.AuthReq) (*dto.AuthResp, error) {
	if req.OpenID == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "openid is required for guest login")
	}

	accountRepo := a.accountRepo()

	account, err := accountRepo.FindByOpenID(ctx, req.OpenID)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("查询账号失败: %w", err)
	}

	if account != nil {
		// 已存在，检查封禁状态
		if account.IsBanned() {
			return nil, model.ErrAccountBanned
		}
		// 更新最后登录时间
		if updateErr := accountRepo.UpdateLastLogin(ctx, req.OpenID); updateErr != nil {
			logx.Warn("更新最后登录时间失败",
				zap.String("requestId", req.RequestID()),
				zap.String("openId", req.OpenID),
				zap.Error(updateErr),
			)
		}
	} else {
		// 创建新账号
		account, err = a.createAccount(ctx, accountRepo, req)
		if err != nil {
			return nil, err
		}
	}

	logx.Info("游客认证成功",
		zap.String("requestId", req.RequestID()),
		zap.String("openId", account.OpenID),
	)

	return &dto.AuthResp{
		PlayerID:  account.PlayerID,
		OpenID:    account.OpenID,
		LoginType: account.LoginType,
	}, nil
}

// findOrCreateAccount 根据正式 openID 查找或创建 Account
// 若客户端同时传入了游客 openID（UUID），优先尝试将游客账号升级
func (a *AuthLogic) findOrCreateAccount(ctx context.Context, req *dto.AuthReq, openID string) (*dto.AuthResp, error) {
	accountRepo := a.accountRepo()

	// 先按正式 openID 查找
	account, err := accountRepo.FindByOpenID(ctx, openID)
	if err != nil && err != mongox.ErrNotFound {
		return nil, fmt.Errorf("查询账号失败: %w", err)
	}

	if account != nil {
		if account.IsBanned() {
			return nil, model.ErrAccountBanned
		}
		// 已存在账号必须与本次登录渠道一致：openID 为跨渠道共享命名空间，
		// 渠道不匹配说明发生碰撞（如不同渠道相同 account、撞上游客账号），拒绝登录避免串号/接管
		if account.LoginType != req.LoginType {
			logx.Warn("openID 已存在但登录渠道不一致，拒绝登录",
				zap.String("requestId", req.RequestID()),
				zap.String("openId", openID),
				zap.String("existLoginType", account.LoginType),
				zap.String("reqLoginType", req.LoginType),
			)
			return nil, fiber.NewError(fiber.StatusBadRequest, "account login type mismatch")
		}
		if updateErr := accountRepo.UpdateLastLogin(ctx, openID); updateErr != nil {
			logx.Warn("更新最后登录时间失败",
				zap.String("requestId", req.RequestID()),
				zap.String("openId", openID),
				zap.Error(updateErr),
			)
		}
	} else {
		// 正式 openID 不存在，尝试通过游客 openID（UUID）找到游客账号并升级
		if req.OpenID != "" {
			guestAccount, guestErr := accountRepo.FindByOpenID(ctx, req.OpenID)
			if guestErr != nil && guestErr != mongox.ErrNotFound {
				return nil, fmt.Errorf("查询游客账号失败: %w", guestErr)
			}
			if guestAccount != nil && guestAccount.LoginType == model.LoginTypeGuest {
				// 游客账号存在，尝试升级为正式账号
				if upgradeErr := accountRepo.UpgradeFromGuest(ctx, guestAccount.PlayerID, openID, req.LoginType); upgradeErr != nil {
					// 降级：升级失败可能因并发下已被其他请求完成升级，按玩家ID重查最新状态，
					// 避免返回升级前的过期游客身份快照；重查失败时保守降级为升级前快照，
					// 保证玩家可正常进入游戏。三方凭证已验证通过，下次登录将自动重试升级。
					// 记为 Error 级别便于监控长期未完成升级的账号。
					logx.Error("游客账号升级失败，重查最新状态",
						zap.String("requestId", req.RequestID()),
						zap.Int64("playerId", guestAccount.PlayerID),
						zap.String("newOpenId", openID),
						zap.String("loginType", req.LoginType),
						zap.Error(upgradeErr),
					)
					latest, queryErr := accountRepo.FindByPlayerID(ctx, guestAccount.PlayerID)
					if queryErr != nil {
						logx.Warn("升级失败后重查游客账号失败，降级返回升级前快照",
							zap.String("requestId", req.RequestID()),
							zap.Int64("playerId", guestAccount.PlayerID),
							zap.Error(queryErr),
						)
						account = guestAccount
					} else {
						account = latest
						if latest.LoginType != model.LoginTypeGuest {
							logx.Info("游客账号已由并发请求完成升级，按最新正式账号状态返回",
								zap.String("requestId", req.RequestID()),
								zap.Int64("playerId", latest.PlayerID),
								zap.String("newOpenId", openID),
							)
						}
					}
				} else {
					guestAccount.OpenID = openID
					guestAccount.LoginType = req.LoginType
					account = guestAccount
					logx.Info("游客账号升级成功",
						zap.String("requestId", req.RequestID()),
						zap.Int64("playerId", account.PlayerID),
						zap.String("newOpenId", openID),
						zap.String("loginType", req.LoginType),
					)
				}
			}
		}

		// 仍未找到账号，创建新账号
		if account == nil {
			account, err = a.createAccountWithOpenID(ctx, accountRepo, openID, req.LoginType, req.Platform, req.ClientInfo, req.RequestID())
			if err != nil {
				return nil, err
			}
		}
	}

	return &dto.AuthResp{
		PlayerID:  account.PlayerID,
		OpenID:    account.OpenID,
		LoginType: account.LoginType,
	}, nil
}

// createAccount 创建新的 Account，时间戳由 InsertAccount 统一填充
// 并发场景下同一 openID 可能触发唯一键冲突，冲突时回退查找已有记录
// openID 参数支持外部传入（三方登录场景，openID 由 token 验证结果决定，与 req.OpenID 不同）
func (a *AuthLogic) createAccount(ctx context.Context, accountRepo *repo.AccountRepo, req *dto.AuthReq) (*model.Account, error) {
	return a.createAccountWithOpenID(ctx, accountRepo, req.OpenID, req.LoginType, req.Platform, req.ClientInfo, req.RequestID())
}

// createAccountWithOpenID 使用指定 openID 创建账号（供三方登录场景调用）
func (a *AuthLogic) createAccountWithOpenID(ctx context.Context, accountRepo *repo.AccountRepo, openID, loginType, platform string, clientInfo *model.ClientInfo, requestID string) (*model.Account, error) {
	// 生成自增玩家 ID
	playerID, err := accountRepo.NextPlayerID(ctx)
	if err != nil {
		return nil, fmt.Errorf("生成玩家ID失败: %w", err)
	}

	account := &model.Account{
		ID:        bson.NewObjectID(),
		PlayerID:  playerID,
		OpenID:    openID,
		LoginType: loginType,
		Platform:  platform,
		Status:    model.StatusNormal,
	}

	// 保存首次注册时的客户端信息
	if clientInfo != nil {
		account.ClientInfo = *clientInfo
	}

	err = accountRepo.InsertAccount(ctx, account)
	if err != nil {
		// 并发创建同 openID 账号时可能触发唯一键冲突，回退查找
		if mongox.IsDuplicateKeyError(err) {
			account, err = accountRepo.FindByOpenID(ctx, openID)
			if err != nil {
				return nil, fmt.Errorf("并发创建后查询账号失败: %w", err)
			}
			// 回退取得的账号需重新校验封禁状态与登录渠道一致性，
			// 防止并发下他人已创建的同 openID 账号为封禁账号或渠道不一致时被误放行
			if account.IsBanned() {
				return nil, model.ErrAccountBanned
			}
			if account.LoginType != loginType {
				logx.Warn("并发创建后账号登录渠道不一致，拒绝登录",
					zap.String("requestId", requestID),
					zap.String("openId", openID),
					zap.String("existLoginType", account.LoginType),
					zap.String("reqLoginType", loginType),
				)
				return nil, fiber.NewError(fiber.StatusBadRequest, "account login type mismatch")
			}
			return account, nil
		}
		return nil, fmt.Errorf("创建账号失败: %w", err)
	}

	logx.Info("账号创建成功",
		zap.String("requestId", requestID),
		zap.Int64("playerId", account.PlayerID),
		zap.String("openId", account.OpenID),
		zap.String("loginType", account.LoginType),
	)
	return account, nil
}

// Deactivate 注销账号：作废该账号的登录身份（openId 追加 del_ 前缀释放）。
// 设计原则：注销不是删除 Account，而是永久作废其登录身份——原 Account 永久保留（供审计/对账），
// openId 释放后原身份不再被登录链路命中，下次以原身份登录将创建全新 Account。
//
// 幂等语义：注销是「释放身份」的删除型操作，因此「账号不存在」与「账号已注销」两种情况
// 都表示平台侧已无该身份可释放，一律返回成功（DeactivatedOpenID 为空）。
// 这两种情况必须返回成功，否则 gamelogic 的编排会在第一步就中止，
// 导致本地 Player 的 openId 永远无法释放，玩家永久注销不掉。
//
// 参数：
//   - ctx: 请求上下文
//   - req: 注销请求（含玩家ID，由 gamelogic 从认证上下文透传）
//
// 返回：注销结果（含作废前的原始 openId）与错误
func (a *AuthLogic) Deactivate(ctx context.Context, req *dto.DeactivateReq) (*dto.DeactivateResp, error) {
	// 显式取内嵌 DTO 的 PlayerID 字段，避免与 BaseReq.PlayerID() 方法产生选择器歧义
	playerID := req.PlatformDeactivateReq.PlayerID
	if playerID <= 0 {
		return nil, fiber.NewError(fiber.StatusBadRequest, "playerId is required")
	}

	accountRepo := a.accountRepo()
	originalOpenID, err := accountRepo.DeactivateByPlayerID(ctx, playerID)
	if err != nil {
		// 账号不存在 / 已注销：平台侧已无身份可释放，按幂等成功返回，
		// 让 gamelogic 得以继续作废本地 Player，避免身份永久无法释放
		if errors.Is(err, mongox.ErrNotFound) || errors.Is(err, model.ErrNoRowsUpdated) {
			logx.Warn("账号不存在或已注销，按幂等成功返回",
				zap.String("requestId", req.RequestID()),
				zap.Int64("playerId", playerID),
				zap.Error(err),
			)
			return &dto.DeactivateResp{PlayerID: playerID}, nil
		}
		return nil, fmt.Errorf("注销账号失败: %w", err)
	}

	logx.Info("账号注销成功，登录身份已作废",
		zap.String("requestId", req.RequestID()),
		zap.Int64("playerId", playerID),
	)

	return &dto.DeactivateResp{
		PlayerID:          playerID,
		DeactivatedOpenID: originalOpenID,
	}, nil
}

// verifyThirdYre 校验三方数据签名。
// 仅对强制校验渠道（当前为 yre，见 model.IsThirdYreRequired）执行校验：
// 该类渠道必须携带 thirdYre，缺失即拒绝，并须通过签名校验；其余渠道一律跳过（不校验）。
//
// 具体签名算法与时间窗由 thirdYreVerifier 抽象封装（默认 md5(siteCode + t + key + uid)）。
// 返回 nil 表示校验通过；校验失败返回 400 语义错误。
func (a *AuthLogic) verifyThirdYre(req *dto.AuthReq) error {
	// 非强制校验渠道直接跳过，不校验 thirdYre
	if !model.IsThirdYreRequired(req.LoginType) {
		return nil
	}

	yre := req.ThirdYre
	if yre == nil {
		logx.Warn("三方数据签名校验失败：强制校验渠道未携带三方数据",
			zap.String("requestId", req.RequestID()),
			zap.String("loginType", req.LoginType),
		)
		return fiber.NewError(fiber.StatusBadRequest, "third party verification failed")
	}

	if err := a.thirdYreVerifier.Verify(yre); err != nil {
		// 未配置密钥属服务端配置问题，记 Error 便于告警；其余为请求侧签名/时间问题，记 Warn
		if errors.Is(err, errThirdYreSecretMissing) {
			logx.Error("三方数据签名校验失败：未配置三方签名密钥",
				zap.String("requestId", req.RequestID()),
				zap.String("loginType", req.LoginType),
				zap.String("siteCode", yre.SiteCode),
				zap.String("uid", yre.Uid),
			)
		} else {
			logx.Warn("三方数据签名校验失败",
				zap.String("requestId", req.RequestID()),
				zap.String("loginType", req.LoginType),
				zap.String("siteCode", yre.SiteCode),
				zap.String("uid", yre.Uid),
				zap.Int64("t", yre.T),
				zap.Error(err),
			)
		}
		return fiber.NewError(fiber.StatusBadRequest, "third party verification failed")
	}

	return nil
}
