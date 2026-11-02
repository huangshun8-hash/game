package logic

import (
	"context"
	"errors"
	"fmt"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"
	"kemiluo/services/platform/internal/repo"
	"kemiluo/services/shared/gmtdto"
	"kemiluo/services/shared/sharedmodel"

	"go.uber.org/zap"
)

// whitelistPageSizeRange 白名单分页每页上限
const whitelistPageSizeRange = 100

// WhitelistLogic 充值白名单业务逻辑，负责白名单的添加、删除、查询与校验。
// 白名单记录账号 openId，供 Platform 下单时判断是否直接支付完成。
type WhitelistLogic struct {
	mongoSvc *mongox.Service
}

// NewWhitelistLogic 创建充值白名单逻辑实例
// mongoSvc: MongoDB 服务
func NewWhitelistLogic(mongoSvc *mongox.Service) *WhitelistLogic {
	return &WhitelistLogic{mongoSvc: mongoSvc}
}

// payWhitelistRepo 返回基于当前连接的白名单仓库实例
func (l *WhitelistLogic) payWhitelistRepo() *repo.PayWhitelistRepo {
	return repo.NewPayWhitelistRepo(l.mongoSvc.Client())
}

// accountRepo 返回基于当前连接的账号仓库实例
func (l *WhitelistLogic) accountRepo() *repo.AccountRepo {
	return repo.NewAccountRepo(l.mongoSvc.Client())
}

// resolveOpenID 将请求中的 account 或 playerId 解析为白名单账号 openId。
// 只校验现存账号：按 playerId 解析时仅当账号存在且未注销才返回其原始 openId；
// 账号不存在或已注销（openId 以 del_ 开头）视为无现存身份，返回 ok=false 且不返回错误。
//
// 返回：
//   - openID: 解析出的原始 openId
//   - ok:     是否解析到有效现存账号身份
//   - err:    参数缺失（ErrWhitelistInvalidIdentity）或数据库查询错误
func (l *WhitelistLogic) resolveOpenID(ctx context.Context, account string, playerID int64) (openID string, ok bool, err error) {
	if account != "" {
		return account, true, nil
	}
	if playerID <= 0 {
		return "", false, model.ErrWhitelistInvalidIdentity
	}

	acc, aErr := l.accountRepo().FindByPlayerID(ctx, playerID)
	if aErr != nil {
		if errors.Is(aErr, mongox.ErrNotFound) {
			// 账号不存在：无现存身份，返回 ok=false
			return "", false, nil
		}
		return "", false, fmt.Errorf("查询账号失败: %w", aErr)
	}
	// 已注销账号身份已作废，不做还原；只校验现存账号
	if sharedmodel.IsDeactivatedOpenID(acc.OpenID) {
		return "", false, nil
	}
	return acc.OpenID, true, nil
}

// Add 添加白名单账号。
// 参数：
//   - req: 添加请求（account 或 playerId 至少其一；playerId 时只解析现存账号）
//   - requestID: 请求追踪ID
//
// 返回：添加结果（含入库 openId 与是否已存在），或业务错误
func (l *WhitelistLogic) Add(ctx context.Context, req *gmtdto.GmtWhitelistAddReq, requestID string) (*gmtdto.GmtWhitelistAddResp, error) {
	openID, ok, err := l.resolveOpenID(ctx, req.Account, req.PlayerID)
	if err != nil {
		logx.Warn("白名单添加参数缺失",
			zap.String("requestId", requestID),
			zap.Error(err),
		)
		return nil, err
	}
	if !ok {
		logx.Warn("白名单添加：账号不存在或已注销，拒绝",
			zap.String("requestId", requestID),
			zap.Int64("playerId", req.PlayerID),
		)
		return nil, model.ErrWhitelistAccountNotFound
	}

	// 已存在则幂等返回
	existing, eErr := l.payWhitelistRepo().FindByOpenID(ctx, openID)
	if eErr != nil && !errors.Is(eErr, mongox.ErrNotFound) {
		logx.Error("白名单已存在查询失败",
			zap.String("requestId", requestID),
			zap.String("openId", openID),
			zap.Error(eErr),
		)
		return nil, fmt.Errorf("查询白名单失败: %w", eErr)
	}
	if existing != nil {
		logx.Info("白名单账号已存在，幂等返回",
			zap.String("requestId", requestID),
			zap.String("openId", openID),
		)
		return &gmtdto.GmtWhitelistAddResp{
			OpenID:        openID,
			AlreadyExists: true,
			CreatedAt:     existing.CreatedAt.UnixMilli(),
		}, nil
	}

	w := &model.PayWhitelist{OpenID: openID}
	if insErr := l.payWhitelistRepo().Insert(ctx, w); insErr != nil {
		// openId 唯一键冲突兜底：并发下已由其他请求插入，视为幂等成功
		if mongox.IsDuplicateKeyError(insErr) {
			return &gmtdto.GmtWhitelistAddResp{
				OpenID:        openID,
				AlreadyExists: true,
				CreatedAt:     w.CreatedAt.UnixMilli(),
			}, nil
		}
		logx.Error("白名单添加失败",
			zap.String("requestId", requestID),
			zap.String("openId", openID),
			zap.Error(insErr),
		)
		return nil, fmt.Errorf("添加白名单失败: %w", insErr)
	}

	logx.Info("白名单账号添加成功",
		zap.String("requestId", requestID),
		zap.String("openId", openID),
	)
	return &gmtdto.GmtWhitelistAddResp{
		OpenID:    openID,
		CreatedAt: w.CreatedAt.UnixMilli(),
	}, nil
}

// Delete 物理删除白名单账号（仅按账号 openId 删除）。
// 未提供 openId 返回 ErrWhitelistInvalidIdentity；白名单不存在时视为无可删记录，幂等成功。
//
// 参数：
//   - req: 删除请求（openId 必填）
//   - requestID: 请求追踪ID
func (l *WhitelistLogic) Delete(ctx context.Context, req *gmtdto.GmtWhitelistDeleteReq, requestID string) error {
	if req.OpenID == "" {
		logx.Warn("白名单删除参数缺失",
			zap.String("requestId", requestID),
			zap.Error(model.ErrWhitelistInvalidIdentity),
		)
		return model.ErrWhitelistInvalidIdentity
	}
	openID := req.OpenID

	deleted, delErr := l.payWhitelistRepo().DeleteByOpenID(ctx, openID)
	if delErr != nil {
		logx.Error("白名单删除失败",
			zap.String("requestId", requestID),
			zap.String("openId", openID),
			zap.Error(delErr),
		)
		return fmt.Errorf("删除白名单失败: %w", delErr)
	}

	logx.Info("白名单账号删除完成",
		zap.String("requestId", requestID),
		zap.String("openId", openID),
		zap.Bool("deleted", deleted),
	)
	return nil
}

// Query 分页查询白名单（无条件，仅分页返回全部记录）。
//
// 参数：
//   - req: 分页条件（page / pageSize）
//   - requestID: 请求追踪ID
//
// 返回：分页结果，或业务错误
func (l *WhitelistLogic) Query(ctx context.Context, req *gmtdto.GmtWhitelistQueryReq, requestID string) (*gmtdto.GmtWhitelistQueryResp, error) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > whitelistPageSizeRange {
		pageSize = whitelistPageSizeRange
	}

	result, qErr := l.payWhitelistRepo().PageQuery(ctx, "", page, pageSize)
	if qErr != nil {
		logx.Error("白名单分页查询失败",
			zap.String("requestId", requestID),
			zap.Int64("page", page),
			zap.Error(qErr),
		)
		return nil, fmt.Errorf("查询白名单失败: %w", qErr)
	}

	items := make([]*gmtdto.GmtWhitelistItem, len(result.Items))
	for i, w := range result.Items {
		items[i] = &gmtdto.GmtWhitelistItem{
			OpenID:    w.OpenID,
			CreatedAt: w.CreatedAt.UnixMilli(),
		}
	}

	logx.Info("白名单分页查询成功",
		zap.String("requestId", requestID),
		zap.Int64("total", result.Total),
		zap.Int64("page", result.Page),
	)
	return &gmtdto.GmtWhitelistQueryResp{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

// IsWhitelisted 判断 openId 是否处于充值白名单，供 IAP 下单链路使用。
// 判定失败返回 false 且不阻断下单（白名单查询故障不应误伤正常支付）。
//
// 参数：
//   - ctx:    请求上下文
//   - openID: 待判定的账号 openId
//
// 返回：是否在白名单，以及查询错误
func (l *WhitelistLogic) IsWhitelisted(ctx context.Context, openID string) (bool, error) {
	return l.payWhitelistRepo().IsWhitelisted(ctx, openID)
}
