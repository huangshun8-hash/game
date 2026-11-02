package logic

import (
	"context"
	"fmt"
	"strconv"

	"kemiluo/pkg/authx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/wsx"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// BanLogic 封禁/解封业务逻辑
type BanLogic struct {
	mongoSvc   *mongox.Service
	tokenStore *authx.TokenStore
	hub        *wsx.Hub
}

// NewBanLogic 创建封禁业务逻辑实例
//
// 参数：
//   - mongoSvc:   MongoDB 服务实例
//   - tokenStore: 会话 Token 存储，用于封禁后立即撤销令牌；可为 nil（Redis 未就绪时跳过撤销）
//   - hub:        WebSocket 会话管理器，用于封禁后强制断开玩家长连接；可为 nil（未启用长连接时跳过）
func NewBanLogic(mongoSvc *mongox.Service, tokenStore *authx.TokenStore, hub *wsx.Hub) *BanLogic {
	return &BanLogic{
		mongoSvc:   mongoSvc,
		tokenStore: tokenStore,
		hub:        hub,
	}
}

// BatchBan 批量封禁玩家
// 一次查询验证玩家存在性 → UpdateMany 批量更新状态 → InsertMany 批量写入封禁记录 → 批量撤销 Token
//
// 参数：
//   - ctx:       请求上下文
//   - req:       封禁请求（包含玩家ID列表、原因、操作人）
//   - requestID: 请求追踪ID
//
// 返回：批量操作结果（成功列表 + 失败详情）
func (l *BanLogic) BatchBan(ctx context.Context, req *gmtdto.GmtBatchBanReq, requestID string) (*gmtdto.GmtBatchBanResp, error) {
	playerRepo := repo.NewPlayerRepo(l.mongoSvc.Client())
	banRecordRepo := repo.NewBanRecordRepo(l.mongoSvc.Client())

	// 1. 一次查询批量获取玩家，找出不存在的玩家
	players, err := playerRepo.FindByPlayerIDs(ctx, req.PlayerIDs)
	if err != nil {
		return nil, fmt.Errorf("批量查询玩家失败: %w", err)
	}

	// 构建已存在的玩家ID集合
	existMap := make(map[int64]*model.Player, len(players))
	for _, p := range players {
		existMap[p.PlayerID] = p
	}

	resp := &gmtdto.GmtBatchBanResp{
		SuccessIDs:  make([]int64, 0, len(req.PlayerIDs)),
		FailedItems: make([]*gmtdto.BanFailedItem, 0),
	}

	// 2. 分类：不存在的记为失败，已存在的（无论是否已封禁）都视为成功
	toBanIDs := make([]int64, 0, len(req.PlayerIDs))
	for _, pid := range req.PlayerIDs {
		if _, exists := existMap[pid]; !exists {
			resp.FailedItems = append(resp.FailedItems, &gmtdto.BanFailedItem{
				PlayerID: pid,
				Message:  "player not found",
			})
		} else {
			toBanIDs = append(toBanIDs, pid)
		}
	}

	if len(toBanIDs) == 0 {
		return resp, nil
	}

	// 3. 状态更新与封禁记录写入放入同一事务，保证二者原子一致，避免"已封禁但无记录"或"有记录但未封禁"
	client := l.mongoSvc.Client()
	txErr := client.RunTransaction(ctx, func(txCtx context.Context) error {
		// 事务内查询已有封禁记录，仅插入缺失记录，避免唯一索引重复键导致事务中止
		existing, err := banRecordRepo.FindExistingPlayerIDs(txCtx, toBanIDs)
		if err != nil {
			return fmt.Errorf("查询已有封禁记录失败: %w", err)
		}
		records := make([]*model.BanRecord, 0, len(toBanIDs))
		for _, pid := range toBanIDs {
			if _, ok := existing[pid]; ok {
				continue
			}
			records = append(records, &model.BanRecord{
				PlayerID: pid,
				Reason:   req.Reason,
			})
		}
		if len(records) > 0 {
			if insertErr := banRecordRepo.BatchInsert(txCtx, records); insertErr != nil {
				return fmt.Errorf("批量写入封禁记录失败: %w", insertErr)
			}
		}
		if _, updateErr := playerRepo.BatchUpdateStatus(txCtx, toBanIDs, model.StatusNormal, model.StatusBanned); updateErr != nil {
			return fmt.Errorf("批量更新封禁状态失败: %w", updateErr)
		}
		return nil
	})
	if txErr != nil {
		// 并发下另一请求已为同一玩家写入封禁记录（playerId 唯一索引冲突），
		// 属幂等场景：对方事务已同时完成状态更新与记录写入，本请求降级为成功。
		if mongox.IsDuplicateKeyError(txErr) {
			logx.Warn("批量封禁并发幂等：封禁记录已由并发请求写入",
				zap.String("requestId", requestID),
				zap.Error(txErr),
			)
		} else {
			logx.Error("批量封禁事务失败",
				zap.String("requestId", requestID),
				zap.Error(txErr),
			)
			return nil, fmt.Errorf("批量封禁失败: %w", txErr)
		}
	}

	// 5. 批量撤销 Token 并强制断开长连接
	for _, pid := range toBanIDs {
		l.revokeToken(ctx, pid, requestID)
		l.kickConnections(pid, requestID)
	}

	resp.SuccessIDs = toBanIDs
	logx.Info("批量封禁完成",
		zap.String("requestId", requestID),
		zap.String("operatorId", req.OperatorID),
		zap.String("reason", req.Reason),
		zap.Int("successCount", len(toBanIDs)),
		zap.Int("failedCount", len(resp.FailedItems)),
	)

	return resp, nil
}

// BatchUnban 批量解封玩家
// 一次查询验证玩家存在性 → UpdateMany 批量更新状态 → DeleteMany 批量删除封禁记录
//
// 参数：
//   - ctx:       请求上下文
//   - req:       解封请求（包含玩家ID列表、原因、操作人）
//   - requestID: 请求追踪ID
//
// 返回：批量操作结果（成功列表 + 失败详情）
func (l *BanLogic) BatchUnban(ctx context.Context, req *gmtdto.GmtBatchUnbanReq, requestID string) (*gmtdto.GmtBatchBanResp, error) {
	playerRepo := repo.NewPlayerRepo(l.mongoSvc.Client())
	banRecordRepo := repo.NewBanRecordRepo(l.mongoSvc.Client())

	// 1. 一次查询批量获取玩家，找出不存在的玩家
	players, err := playerRepo.FindByPlayerIDs(ctx, req.PlayerIDs)
	if err != nil {
		return nil, fmt.Errorf("批量查询玩家失败: %w", err)
	}

	existMap := make(map[int64]*model.Player, len(players))
	for _, p := range players {
		existMap[p.PlayerID] = p
	}

	resp := &gmtdto.GmtBatchBanResp{
		SuccessIDs:  make([]int64, 0, len(req.PlayerIDs)),
		FailedItems: make([]*gmtdto.BanFailedItem, 0),
	}

	// 2. 分类：不存在的记为失败，已存在的（无论是否已封禁）都视为成功
	toUnbanIDs := make([]int64, 0, len(req.PlayerIDs))
	for _, pid := range req.PlayerIDs {
		if _, exists := existMap[pid]; !exists {
			resp.FailedItems = append(resp.FailedItems, &gmtdto.BanFailedItem{
				PlayerID: pid,
				Message:  "player not found",
			})
		} else {
			toUnbanIDs = append(toUnbanIDs, pid)
		}
	}

	if len(toUnbanIDs) == 0 {
		return resp, nil
	}

	// 3. 解封状态更新与封禁记录删除放入同一事务，保证二者原子一致
	client := l.mongoSvc.Client()
	txErr := client.RunTransaction(ctx, func(txCtx context.Context) error {
		if _, updateErr := playerRepo.BatchUpdateStatus(txCtx, toUnbanIDs, model.StatusBanned, model.StatusNormal); updateErr != nil {
			return fmt.Errorf("批量更新解封状态失败: %w", updateErr)
		}
		if delErr := banRecordRepo.DeleteByPlayerIDs(txCtx, toUnbanIDs); delErr != nil {
			return fmt.Errorf("批量删除封禁记录失败: %w", delErr)
		}
		return nil
	})
	if txErr != nil {
		logx.Error("批量解封事务失败",
			zap.String("requestId", requestID),
			zap.Error(txErr),
		)
		return nil, fmt.Errorf("批量解封失败: %w", txErr)
	}

	resp.SuccessIDs = toUnbanIDs
	logx.Info("批量解封完成",
		zap.String("requestId", requestID),
		zap.String("operatorId", req.OperatorID),
		zap.String("reason", req.Reason),
		zap.Int("successCount", len(toUnbanIDs)),
		zap.Int("failedCount", len(resp.FailedItems)),
	)

	return resp, nil
}

// QueryBanRecords 分页查询封禁记录
//
// 参数：
//   - ctx:       请求上下文
//   - req:       查询请求（支持按玩家ID过滤）
//   - requestID: 请求追踪ID
//
// 返回：封禁记录分页结果
func (l *BanLogic) QueryBanRecords(ctx context.Context, req *gmtdto.GmtQueryBanRecordReq, requestID string) (*gmtdto.GmtQueryBanRecordResp, error) {
	banRecordRepo := repo.NewBanRecordRepo(l.mongoSvc.Client())

	// 构建查询条件
	filter := bson.D{}
	if req.PlayerID > 0 {
		filter = append(filter, bson.E{Key: "playerId", Value: req.PlayerID})
	}

	result, err := banRecordRepo.QueryRecords(ctx, filter, req.Page, req.PageSize)
	if err != nil {
		return nil, fmt.Errorf("查询封禁记录失败: %w", err)
	}

	// 转换为 DTO
	items := make([]*gmtdto.GmtBanRecordItem, 0, len(result.Items))
	for i := range result.Items {
		record := &result.Items[i]
		items = append(items, &gmtdto.GmtBanRecordItem{
			ID:        record.ID.Hex(),
			PlayerID:  record.PlayerID,
			Reason:    record.Reason,
			CreatedAt: record.CreatedAt.UnixMilli(),
		})
	}

	return &gmtdto.GmtQueryBanRecordResp{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}

// kickConnections 断开玩家已建立的 WebSocket 长连接。
// WebSocket 握手鉴权只校验 JWT 签名、不查 Token 存储，因此撤销 Redis Token
// 只能拦截后续 HTTP 请求与新的握手，已在线的长连接必须在此显式关闭，
// 否则被封禁玩家仍能持续收发消息直至 JWT 自然过期。
// hub 为 nil（未启用长连接）时直接跳过。
//
// 参数：playerID 目标玩家ID；requestID 请求追踪ID
func (l *BanLogic) kickConnections(playerID int64, requestID string) {
	if l.hub == nil {
		return
	}
	if closed := l.hub.KickPlayer(playerID); closed > 0 {
		logx.Info("封禁后已断开玩家长连接",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int("closedConnections", closed),
		)
	}
}

// revokeToken 撤销玩家 Token，使其立即下线
// tokenStore 为 nil 或操作失败时仅告警，不影响封禁结果
func (l *BanLogic) revokeToken(ctx context.Context, playerID int64, requestID string) {
	if l.tokenStore == nil {
		return
	}
	playerIDStr := strconv.FormatInt(playerID, 10)
	if err := l.tokenStore.Revoke(ctx, playerIDStr); err != nil {
		logx.Warn("封禁后撤销 Token 失败，玩家 Token 将在过期后自动失效",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
	}
}
