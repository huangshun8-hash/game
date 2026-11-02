package logic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"

	"go.uber.org/zap"
)

// FriendLogic 好友业务逻辑
type FriendLogic struct {
	mongoSvc   *mongox.Service
	saveReader SaveReader
	// friendCache 好友关系与账号解析缓存（排行榜热路径使用）；Redis 未就绪时读写自动降级为回源 DB
	friendCache *cache.FriendCache
}

// NewFriendLogic 创建好友业务逻辑
func NewFriendLogic(mongoSvc *mongox.Service, saveReader SaveReader, friendCache *cache.FriendCache) *FriendLogic {
	return &FriendLogic{mongoSvc: mongoSvc, saveReader: saveReader, friendCache: friendCache}
}

func (l *FriendLogic) repos() (*repo.FriendRepo, *repo.FriendApplyRepo, *repo.PlayerRepo) {
	client := l.mongoSvc.Client()
	return repo.NewFriendRepo(client), repo.NewFriendApplyRepo(client), repo.NewPlayerRepo(client)
}

// playerBases 批量读取多个玩家的 Base 数据，供列表展示等级与昵称；读取失败时仅告警，缺失玩家按无 Base 处理。
func (l *FriendLogic) playerBases(ctx context.Context, requestID string, playerID int64, ids []int64) map[int64]*cspb.Base {
	if l.saveReader == nil || len(ids) == 0 {
		return nil
	}
	bases, err := loadBases(ctx, l.saveReader, ids)
	if err != nil {
		logx.Warn("批量读取玩家存档失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64s("playerIds", ids),
			zap.Error(err),
		)
	}
	return bases
}

// Apply 发送好友申请
// 好友关系检查 + 申请插入统一放入 MongoDB 事务，在快照一致性下尽量减少"已成为好友后仍产生申请"的窗口
// 申请记录存在即表示待处理，同方向（applyUid → targetUid）由 (applyUid, targetUid) 唯一索引保证最多一条，
// 并发插入的唯一冲突转换为 ErrFriendApplyAlreadyPending
// 好友数量上限：本人好友数已达 maxFriendCount（100）时拒绝发送申请（ErrFriendCountLimitExceeded），
// 同意申请时同样会校验双方数量
// 并发语义说明：MongoDB 多文档事务采用 snapshot read concern，事务内读到的好友关系为事务开始时的快照。
// 极端并发下（apply 与 accept 同时进行），可能出现"好友关系已建立但残留一条申请"，
// 该残留不产生数据损坏，被申请人重复处理（同意）时由幂等 UpsertRelation 收敛为一致状态并删除残留申请。
// requestID/playerID: 由 handler 从请求上下文显式读取传入
func (l *FriendLogic) Apply(ctx context.Context, req *cspb.FriendApplyReq, requestID string, playerID int64) error {
	friendRepo, applyRepo, playerRepo := l.repos()
	client := l.mongoSvc.Client()

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		// 1. 禁止添加自己为好友
		if req.GetTargetUid() == playerID {
			return model.ErrFriendSelfAdd
		}

		// 2. 校验目标玩家存在
		if _, err := playerRepo.FindByPlayerID(txCtx, req.GetTargetUid()); err != nil {
			if errors.Is(err, mongox.ErrNotFound) {
				return model.ErrFriendTargetNotFound
			}
			return fmt.Errorf("查询目标玩家失败: %w", err)
		}

		// 3. 已经是好友则直接返回，不创建申请
		isAlreadyFriend, err := friendRepo.IsFriend(txCtx, playerID, req.GetTargetUid())
		if err != nil {
			return fmt.Errorf("查询好友关系失败: %w", err)
		}
		if isAlreadyFriend {
			return model.ErrFriendAlreadyExists
		}

		// 4. 好友数量上限：本人已满员（>= maxFriendCount）不允许再发送申请，避免为满员玩家堆积无效申请
		friendCount, err := friendRepo.CountByUid(txCtx, playerID)
		if err != nil {
			return fmt.Errorf("统计好友数量失败: %w", err)
		}
		if friendCount >= maxFriendCount {
			return model.ErrFriendCountLimitExceeded
		}

		// 5. 对方已向你发送申请（反向记录存在）时不创建新申请，提示对方已有申请
		hasReversePending, err := applyRepo.ExistsPending(txCtx, req.GetTargetUid(), playerID)
		if err != nil {
			return fmt.Errorf("查询反向申请失败: %w", err)
		}
		if hasReversePending {
			return model.ErrFriendApplyReceived
		}

		// 6. 插入申请记录：同方向记录已存在时唯一索引冲突，转换为 ErrFriendApplyAlreadyPending
		nowMs := time.Now().UnixMilli()
		if err := applyRepo.InsertPending(txCtx, playerID, req.GetTargetUid(), nowMs); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if model.IsFriendBusinessError(err) {
			return err
		}
		logx.Error("发送好友申请事务失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("targetUid", req.GetTargetUid()),
			zap.Error(err),
		)
		return model.ErrFriendOperationFailed
	}

	// 事件扩展点：申请创建成功即投递事件（V1 记录日志，后续可接消息队列/WebSocket）
	logx.Info("好友申请创建事件",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int64("targetUid", req.GetTargetUid()),
	)
	return nil
}

// HandleApply 同意/拒绝好友申请
// 当前登录用户必须是申请记录中的 targetUid（被申请人），由 handler 侧从认证上下文解析的 playerID 保证
// accept=true 同意，false 拒绝
func (l *FriendLogic) HandleApply(ctx context.Context, req *cspb.FriendApplyHandleReq, requestID string, playerID int64) (*cspb.FriendApplyHandleResp, error) {
	if req.GetAccept() {
		return l.acceptApply(ctx, requestID, playerID, req.GetApplyUid())
	}
	return l.rejectApply(ctx, requestID, playerID, req.GetApplyUid())
}

// acceptApply 同意好友申请：事务内删除申请记录 + 写入双向好友关系
func (l *FriendLogic) acceptApply(ctx context.Context, requestID string, targetUID, applyUID int64) (*cspb.FriendApplyHandleResp, error) {
	friendRepo, applyRepo, playerRepo := l.repos()
	client := l.mongoSvc.Client()

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		// 0. 防御性校验：禁止与自己建立好友关系（Apply 已禁止自加，此处兜底防止伪造 handle 请求）
		if applyUID == targetUID {
			return model.ErrFriendSelfAdd
		}

		// 1. 校验申请人存在（仅对存在的玩家建立好友关系）
		if _, err := playerRepo.FindByPlayerID(txCtx, applyUID); err != nil {
			if errors.Is(err, mongox.ErrNotFound) {
				return model.ErrFriendTargetNotFound
			}
			return fmt.Errorf("查询申请人失败: %w", err)
		}

		// 2. 删除申请记录：记录不存在（已被处理或从未申请）则直接报错；
		//    删除与写入好友关系同处一个事务，并发 accept/reject/apply 只有一个能删除成功
		nowMs := time.Now().UnixMilli()
		deleted, err := applyRepo.DeleteByPair(txCtx, applyUID, targetUID)
		if err != nil {
			return fmt.Errorf("删除申请记录失败: %w", err)
		}
		if !deleted {
			return model.ErrFriendApplyNotFound
		}

		// 3. 好友数量上限：同意后双方各新增一名好友，任一方已满员（>= maxFriendCount）即拒绝；
		//    计数与写入同处事务快照，任一校验失败即回滚删除的申请记录；
		//    极端并发下（同一玩家同时被多个申请方同意）允许小幅软溢出，属软约束
		targetFriendCount, err := friendRepo.CountByUid(txCtx, targetUID)
		if err != nil {
			return fmt.Errorf("统计好友数量失败: %w", err)
		}
		if targetFriendCount >= maxFriendCount {
			return model.ErrFriendCountLimitExceeded
		}
		applyFriendCount, err := friendRepo.CountByUid(txCtx, applyUID)
		if err != nil {
			return fmt.Errorf("统计好友数量失败: %w", err)
		}
		if applyFriendCount >= maxFriendCount {
			return model.ErrFriendCountLimitExceeded
		}

		// 4. 写入双向好友关系（幂等 Upsert，自动补齐历史单向脏数据）
		if _, err := friendRepo.UpsertRelation(txCtx, applyUID, targetUID, nowMs); err != nil {
			return fmt.Errorf("写入好友关系失败: %w", err)
		}
		if _, err := friendRepo.UpsertRelation(txCtx, targetUID, applyUID, nowMs); err != nil {
			return fmt.Errorf("写入好友关系失败: %w", err)
		}
		return nil
	})
	if err != nil {
		if model.IsFriendBusinessError(err) {
			return nil, err
		}
		logx.Error("同意好友申请事务失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", targetUID),
			zap.Int64("applyUid", applyUID),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}

	// 好友关系已变更：删除双方好友ID缓存，使下一次排行立即读到新好友
	l.invalidateFriendIDs(ctx, requestID, targetUID, applyUID)

	// 事件扩展点：事务提交成功后投递同意事件
	logx.Info("好友申请同意事件",
		zap.String("requestId", requestID),
		zap.Int64("userId", targetUID),
		zap.Int64("applyUid", applyUID),
	)
	return &cspb.FriendApplyHandleResp{
		Code:    0,
		Message: "ok",
		Data:    &cspb.FriendApplyHandleData{Accept: true},
	}, nil
}

// rejectApply 拒绝好友申请：删除申请记录即可，不需要创建好友关系，无需事务
func (l *FriendLogic) rejectApply(ctx context.Context, requestID string, targetUID, applyUID int64) (*cspb.FriendApplyHandleResp, error) {
	_, applyRepo, _ := l.repos()

	deleted, err := applyRepo.DeleteByPair(ctx, applyUID, targetUID)
	if err != nil {
		logx.Error("拒绝好友申请失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", targetUID),
			zap.Int64("applyUid", applyUID),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}
	if !deleted {
		return nil, model.ErrFriendApplyNotFound
	}

	// 事件扩展点：拒绝事件（可选）
	logx.Info("好友申请拒绝事件",
		zap.String("requestId", requestID),
		zap.Int64("userId", targetUID),
		zap.Int64("applyUid", applyUID),
	)
	return &cspb.FriendApplyHandleResp{
		Code:    0,
		Message: "ok",
		Data:    &cspb.FriendApplyHandleData{Accept: false},
	}, nil
}

// CancelApply 撤销我发出的申请（applyUid = 当前登录用户）
// 撤销即删除申请记录，记录不存在则报错；无需事务
func (l *FriendLogic) CancelApply(ctx context.Context, req *cspb.FriendApplyCancelReq, requestID string, playerID int64) (*cspb.FriendApplyCancelResp, error) {
	_, applyRepo, _ := l.repos()

	deleted, err := applyRepo.DeleteByPair(ctx, playerID, req.GetTargetUid())
	if err != nil {
		logx.Error("撤销好友申请失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("targetUid", req.GetTargetUid()),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}
	if !deleted {
		return nil, model.ErrFriendApplyNotFound
	}
	return &cspb.FriendApplyCancelResp{
		Code:    0,
		Message: "ok",
		Data:    &cspb.FriendApplyCancelData{TargetUid: req.GetTargetUid()},
	}, nil
}

// DeleteFriend 删除好友：事务内删除双向好友关系，保证不出现"只删除一侧"的脏数据
// 不修改 friend_apply 申请记录；删除后双方可重新申请
func (l *FriendLogic) DeleteFriend(ctx context.Context, req *cspb.FriendDeleteReq, requestID string, playerID int64) (*cspb.FriendDeleteResp, error) {
	friendRepo, _, _ := l.repos()
	client := l.mongoSvc.Client()

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		if _, err := friendRepo.DeleteRelation(txCtx, playerID, req.GetFriendUid()); err != nil {
			return fmt.Errorf("删除好友关系失败: %w", err)
		}
		if _, err := friendRepo.DeleteRelation(txCtx, req.GetFriendUid(), playerID); err != nil {
			return fmt.Errorf("删除好友关系失败: %w", err)
		}
		return nil
	})
	if err != nil {
		logx.Error("删除好友事务失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("friendUid", req.GetFriendUid()),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}

	// 好友关系已变更：删除双方好友ID缓存，使下一次排行立即读到最新好友
	l.invalidateFriendIDs(ctx, requestID, playerID, req.GetFriendUid())

	// 事件扩展点：删除好友事件（可选）
	logx.Info("好友删除事件",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.Int64("friendUid", req.GetFriendUid()),
	)
	return &cspb.FriendDeleteResp{
		Code:    0,
		Message: "ok",
		Data:    &cspb.FriendDeleteData{FriendUid: req.GetFriendUid()},
	}, nil
}

// FriendList 获取好友列表（位置分页，过滤不存在的幽灵用户）
func (l *FriendLogic) FriendList(ctx context.Context, req *cspb.FriendListReq, requestID string, playerID int64) (*cspb.FriendListResp, error) {
	friendRepo, _, playerRepo := l.repos()

	skip, limit := pageToSkipLimit(req.GetStart(), req.GetEnd())
	relations, hasMore, err := friendRepo.ListPage(ctx, playerID, skip, limit)
	if err != nil {
		logx.Error("查询好友列表失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}

	items := make([]*cspb.FriendItem, 0, len(relations))
	if len(relations) > 0 {
		// 批量查询好友玩家信息，过滤不存在的玩家
		friendIDs := make([]int64, 0, len(relations))
		for _, rel := range relations {
			friendIDs = append(friendIDs, rel.FriendUID)
		}
		players, err := playerRepo.FindByPlayerIDs(ctx, friendIDs)
		if err != nil {
			logx.Error("批量查询好友玩家信息失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Error(err),
			)
			return nil, model.ErrFriendOperationFailed
		}
		playerMap := make(map[int64]model.Player, len(players))
		for _, p := range players {
			if p != nil {
				playerMap[p.PlayerID] = *p
			}
		}
		// 批量读取好友 Base（一次 Redis MGET + 未命中批量回源 DB），避免逐好友 N+1 请求
		bases := l.playerBases(ctx, requestID, playerID, friendIDs)
		for _, rel := range relations {
			if _, ok := playerMap[rel.FriendUID]; !ok {
				// 不存在的玩家不展示（幽灵过滤）
				continue
			}
			base := bases[rel.FriendUID]
			items = append(items, &cspb.FriendItem{
				FriendUid: rel.FriendUID,
				Nickname:  playerNickname(rel.FriendUID, base),
				Level:     base.GetLv(),
				CreatedAt: rel.CreatedAt,
			})
		}
	}

	// hasMore 以数据库分页结果为准（即使末条被幽灵过滤，仍保证下一页不重复、不漏数据）
	return &cspb.FriendListResp{
		Code:    0,
		Message: "ok",
		Data: &cspb.FriendListData{
			Items:   items,
			HasMore: hasMore,
		},
	}, nil
}

// ApplyIn 获取收到的申请列表（位置分页，过滤不存在的玩家）
func (l *FriendLogic) ApplyIn(ctx context.Context, req *cspb.FriendApplyListReq, requestID string, playerID int64) (*cspb.FriendApplyListResp, error) {
	return l.applyList(ctx, requestID, playerID, req.GetStart(), req.GetEnd(), true)
}

// ApplyOut 获取发出的申请列表（位置分页，过滤不存在的玩家）
func (l *FriendLogic) ApplyOut(ctx context.Context, req *cspb.FriendApplyListReq, requestID string, playerID int64) (*cspb.FriendApplyListResp, error) {
	return l.applyList(ctx, requestID, playerID, req.GetStart(), req.GetEnd(), false)
}

// applyList 通用申请列表查询实现
// isInbound=true 查询收到的申请（targetUid=当前用户），false 查询发出的申请（applyUid=当前用户）
func (l *FriendLogic) applyList(ctx context.Context, requestID string, playerID int64, start, end int32, isInbound bool) (*cspb.FriendApplyListResp, error) {
	_, applyRepo, playerRepo := l.repos()

	skip, limit := pageToSkipLimit(start, end)
	var applies []model.FriendApply
	var hasMore bool
	var err error
	if isInbound {
		applies, hasMore, err = applyRepo.ListByTargetUid(ctx, playerID, skip, limit)
	} else {
		applies, hasMore, err = applyRepo.ListByApplyUid(ctx, playerID, skip, limit)
	}
	if err != nil {
		logx.Error("查询好友申请列表失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Bool("isInbound", isInbound),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}

	items := make([]*cspb.FriendApplyItem, 0, len(applies))
	if len(applies) > 0 {
		// 收集对方玩家ID（收到的申请为申请人，发出的申请为被申请人）
		counterpartIDs := make([]int64, 0, len(applies))
		for _, a := range applies {
			var counterID int64
			if isInbound {
				counterID = a.ApplyUID
			} else {
				counterID = a.TargetUID
			}
			counterpartIDs = append(counterpartIDs, counterID)
		}
		players, err := playerRepo.FindByPlayerIDs(ctx, counterpartIDs)
		if err != nil {
			logx.Error("批量查询申请对端玩家信息失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Bool("isInbound", isInbound),
				zap.Error(err),
			)
			return nil, model.ErrFriendOperationFailed
		}
		playerMap := make(map[int64]model.Player, len(players))
		for _, p := range players {
			if p != nil {
				playerMap[p.PlayerID] = *p
			}
		}
		// 批量读取对端玩家 Base，避免逐条申请 N+1 请求
		bases := l.playerBases(ctx, requestID, playerID, counterpartIDs)
		for _, a := range applies {
			var counterID int64
			if isInbound {
				counterID = a.ApplyUID
			} else {
				counterID = a.TargetUID
			}
			if _, ok := playerMap[counterID]; !ok {
				// 对方玩家不存在，不展示该申请（幽灵过滤）
				continue
			}
			base := bases[counterID]
			items = append(items, &cspb.FriendApplyItem{
				ApplyUid:  a.ApplyUID,
				TargetUid: a.TargetUID,
				Nickname:  playerNickname(counterID, base),
				Level:     base.GetLv(),
				CreatedAt: a.CreatedAt,
			})
		}
	}

	return &cspb.FriendApplyListResp{
		Code:    0,
		Message: "ok",
		Data: &cspb.FriendApplyListData{
			Items:   items,
			HasMore: hasMore,
		},
	}, nil
}

// friendPageSize 好友/申请列表默认每页条数
const friendPageSize = 20

// friendPageMaxLimit 好友/申请列表单页数量上限
const friendPageMaxLimit = 50

// maxFriendListOffset 好友/申请列表位置分页 skip 上限，防止超大偏移导致深分页拖垮数据库
const maxFriendListOffset = 100

// maxFriendCount 好友数量上限（达到上限后无法再发送申请或同意申请建立好友关系）
// 好友为双向冗余存储，每名好友两条记录，统计时按 uid 计数
const maxFriendCount = 100

// MaxFriendRankExtraFriends 好友排行榜额外好友（friends 参数，按 account 传入）数量上限
// 与好友数量上限保持一致；超出部分直接截断取前 N 个（而非报错），
// 防止客户端提交超大账号列表导致一次 $in 查询与后续批量读档过重
const MaxFriendRankExtraFriends = 100

// FriendRank 好友排行榜（所有排行均在好友范围内，排行维度由 req.type 指定，接口支持多种类型查询）
// 按类型分派到对应的排行实现：当前支持等级排行（RankTypeLevel）；
// 新增排行维度时在 def.proto 扩展 RankType 枚举并在此新增分支即可，handler 无需改动。
// requestID/playerID: 由 handler 从请求上下文显式读取传入
func (l *FriendLogic) FriendRank(ctx context.Context, req *cspb.FriendRankReq, requestID string, playerID int64) (*cspb.FriendRankResp, error) {
	switch req.GetType() {
	case cspb.RankType_RankTypeLevel:
		return l.friendRankByLevel(ctx, requestID, playerID, req.GetFriends())
	default:
		// 枚举已定义但暂未实现/未知的排行类型，客户端需使用受支持的 type
		logx.Warn("未支持的排行榜类型",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int32("rankType", int32(req.GetType())),
		)
		return nil, model.ErrRankTypeUnsupported
	}
}

// friendIDs 获取玩家全部好友ID。
// 优先读好友ID缓存，未命中时 ListFriendIDs 回源 DB（仅投影 friendUid，不取关系文档其余字段）
// 并回填缓存（好友数量少，一次性读取，不做分页）。
// 缓存不可用或回填失败不影响主流程，仅降级为每次都回源 DB。
func (l *FriendLogic) friendIDs(ctx context.Context, requestID string, playerID int64) ([]int64, error) {
	if ids, ok := l.friendCache.GetFriendIDs(ctx, playerID); ok {
		return ids, nil
	}

	friendRepo, _, _ := l.repos()
	ids, err := friendRepo.ListFriendIDs(ctx, playerID)
	if err != nil {
		logx.Error("查询好友关系失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, model.ErrFriendOperationFailed
	}
	if l.friendCache.IsAvailable() {
		if err := l.friendCache.SetFriendIDs(ctx, playerID, ids); err != nil {
			logx.Warn("写入好友ID缓存失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Error(err),
			)
		}
	}
	return ids, nil
}

// resolveExtraAccountPlayerIDs 将额外好友账号（account）解析为玩家ID列表。
// 优先批量读账号解析缓存（含"确认无对应玩家"的负缓存），仅对未命中的账号回源 DB 一次，
// 再逐个回填缓存；空账号与已注销玩家由 repo 层过滤。返回按输入顺序去重后的玩家ID列表。
// 缓存不可用时不回填、每次回源 DB，行为与未接入缓存一致。
//
// 返回值：
//   - ids: 按输入顺序去重后的玩家ID列表
//   - dbResolvedIDs: 本次请求由 DB（FindByAccounts）解析出的玩家ID集合。
//     该批玩家已在 DB 侧按 status != StatusDeactivated 过滤，本次请求内可直接视为"存在且可见"，
//     调用方无需再做存在性校验；缓存命中的结果不具备该保证（TTL 内玩家可能已注销），必须照常校验。
//
// 参数：
//   - accounts:   客户端传入的额外好友第三方账号列表（已由调用方完成数量上限截断）
//   - playerRepo: 玩家仓库（由调用方复用，避免重复构造）
func (l *FriendLogic) resolveExtraAccountPlayerIDs(ctx context.Context, requestID string, playerID int64, playerRepo *repo.PlayerRepo, accounts []string) ([]int64, map[int64]struct{}, error) {
	if len(accounts) == 0 {
		return nil, nil, nil
	}

	hits, missing := l.friendCache.GetAccountPlayerIDs(ctx, accounts)
	dbResolvedIDs := make(map[int64]struct{})
	if len(missing) > 0 {
		players, err := playerRepo.FindByAccounts(ctx, missing)
		if err != nil {
			logx.Error("批量查询额外好友账号失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int("friendAccountCount", len(missing)),
				zap.Error(err),
			)
			return nil, nil, model.ErrFriendOperationFailed
		}

		byAccount := make(map[string][]int64, len(players))
		for _, p := range players {
			if p != nil {
				byAccount[p.Account] = append(byAccount[p.Account], p.PlayerID)
			}
		}
		// 未命中的账号逐个回填（无结果按负缓存写入），避免下次继续回源
		for _, account := range missing {
			ids := byAccount[account]
			hits[account] = ids
			for _, id := range ids {
				dbResolvedIDs[id] = struct{}{}
			}
			if !l.friendCache.IsAvailable() {
				continue
			}
			if err := l.friendCache.SetAccountPlayerIDs(ctx, account, ids); err != nil {
				logx.Warn("写入额外好友账号缓存失败",
					zap.String("requestId", requestID),
					zap.Int64("userId", playerID),
					zap.String("account", account),
					zap.Error(err),
				)
			}
		}
	}

	// 按输入顺序合并去重（同一账号的多角色、跨账号重复的玩家均只保留一次）
	result := make([]int64, 0, len(accounts))
	seen := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		for _, id := range hits[account] {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return result, dbResolvedIDs, nil
}

// invalidateFriendIDs 好友关系变更后删除双方的好友ID缓存，使下一次排行读取立即回源。
// 删除失败仅告警：缓存 TTL 到期后自动收敛，不影响本次写操作结果。
func (l *FriendLogic) invalidateFriendIDs(ctx context.Context, requestID string, playerIDs ...int64) {
	if !l.friendCache.IsAvailable() {
		return
	}
	if err := l.friendCache.DelFriendIDs(ctx, playerIDs...); err != nil {
		logx.Warn("删除好友ID缓存失败",
			zap.String("requestId", requestID),
			zap.Int64s("userIds", playerIDs),
			zap.Error(err),
		)
	}
}

// friendRankByLevel 等级排行（RankTypeLevel）：按好友的玩家等级排序
// 排行按分数倒序、同级按玩家ID升序，名次按位置连续编号，列表包含本人。
// 好友为私榜且读多写少：好友ID列表与额外好友账号解析结果走 Redis 缓存，热路径不再打 DB；
// 命中不到时回源 DB 并按 Base 存档缓存批量读取分数（等级排行分数=玩家等级）后在内存排序，
// 无需引入 Redis/ZSET 等专用排行存储。
// extraAccounts 为客户端传入的额外好友账号（account），按账号批量解析为玩家后并入候选（与既有好友取并集并去重）；
// 数量超过 MaxFriendRankExtraFriends（100）时只取前 100 个。
// 本人的排名经响应 self 字段固定返回，不依赖 items 是否包含本人。
//
// 查询次数（候选 = 好友 + 额外好友 + 本人，上限 201）：
//   - 好友ID列表：Redis 命中则 0 次 DB，未命中 1 次关系查询（仅投影 friendUid）
//   - 额外好友账号：Redis 命中则 0 次 DB，未命中 1 次 account 查询
//   - 存在性校验：1 次 playerId 批量查询（仅投影 playerId），
//     已在 DB 侧按账号解析出的玩家与本人不参与该查询，不做重复校验
//   - 分数（等级）：1 次 Redis MGET，未命中才批量回源存档（save_heads + save_snapshots）
//     其中存在性校验只覆盖"仍需校验"的候选，Base 只读"校验通过"的玩家，避免为幽灵好友白读存档。
//
// requestID/playerID: 由 handler 从请求上下文显式读取传入
func (l *FriendLogic) friendRankByLevel(ctx context.Context, requestID string, playerID int64, extraAccounts []string) (*cspb.FriendRankResp, error) {
	_, _, playerRepo := l.repos()

	// 额外好友数量上限保护：超出部分直接截断取前 N 个，避免超大列表拖垮查询与批量读档
	if len(extraAccounts) > MaxFriendRankExtraFriends {
		extraAccounts = extraAccounts[:MaxFriendRankExtraFriends]
	}

	// 获取全部好友ID（优先读缓存，未命中回源 DB 并回填）
	friendIDs, err := l.friendIDs(ctx, requestID, playerID)
	if err != nil {
		return nil, err
	}

	// 额外好友：按 account 批量解析玩家（优先读缓存；空账号与已注销玩家在 repo 层被过滤）
	extraPlayerIDs, dbResolvedIDs, err := l.resolveExtraAccountPlayerIDs(ctx, requestID, playerID, playerRepo, extraAccounts)
	if err != nil {
		return nil, err
	}

	// 候选玩家 = 全部好友 + 额外好友（按 account 解析） + 本人；
	// 额外好友可能与既有好友或本人重叠，按 playerId 去重避免同一玩家在榜上重复出现
	candidateIDs := make([]int64, 0, len(friendIDs)+len(extraPlayerIDs)+1)
	seen := make(map[int64]struct{}, len(friendIDs)+len(extraPlayerIDs)+1)
	appendCandidate := func(id int64) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		candidateIDs = append(candidateIDs, id)
	}
	for _, id := range friendIDs {
		appendCandidate(id)
	}
	for _, id := range extraPlayerIDs {
		appendCandidate(id)
	}
	appendCandidate(playerID)

	// 已确认可见的候选：本人恒在榜、不做校验；由 DB 账号查询解析出的玩家已在 DB 侧
	// 过滤过已注销状态，本次请求内无需二次校验（缓存解析结果不具备该保证，仍需校验）。
	visible := make(map[int64]struct{}, len(candidateIDs))
	visible[playerID] = struct{}{}
	for id := range dbResolvedIDs {
		visible[id] = struct{}{}
	}

	// 仅对剩余候选做一次存在性校验：只投影 playerId，不拉取 openId/saveKey 等无关大字段；
	// 已不存在与已注销的玩家（幽灵好友）在此处一次性剔除
	pendingCheckIDs := make([]int64, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		if _, ok := visible[id]; ok {
			continue
		}
		pendingCheckIDs = append(pendingCheckIDs, id)
	}
	if len(pendingCheckIDs) > 0 {
		existingIDs, err := playerRepo.FindExistingPlayerIDs(ctx, pendingCheckIDs)
		if err != nil {
			logx.Error("批量校验候选玩家失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Int("candidateCount", len(pendingCheckIDs)),
				zap.Error(err),
			)
			return nil, model.ErrFriendOperationFailed
		}
		for _, id := range existingIDs {
			visible[id] = struct{}{}
		}
	}

	// 可见候选（已在榜玩家）：只对该列表读 Base 存档，避免为幽灵好友白读一次存档
	visibleIDs := make([]int64, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		if _, ok := visible[id]; !ok {
			continue
		}
		visibleIDs = append(visibleIDs, id)
	}

	// 批量读取候选玩家 Base（一次 Redis MGET + 未命中批量回源 DB），避免逐玩家 N+1 请求；
	// 等级排行分数即玩家等级（将来新增排行维度时在此处按类型取分）
	bases := l.playerBases(ctx, requestID, playerID, visibleIDs)

	items := make([]*cspb.RankItem, 0, len(visibleIDs))
	var selfItem *cspb.RankItem
	for _, id := range visibleIDs {
		base := bases[id]
		item := &cspb.RankItem{
			PlayerUid: id,
			Nickname:  playerNickname(id, base),
			Score:     base.GetLv(),
			Avatar:    base.GetAvatar(),
		}
		if id == playerID {
			selfItem = item
		}
		items = append(items, item)
	}

	// 排序：分数倒序，同级按玩家ID升序
	slices.SortFunc(items, func(a, b *cspb.RankItem) int {
		if a.GetScore() != b.GetScore() {
			if a.GetScore() > b.GetScore() {
				return -1
			}
			return 1
		}
		if a.GetPlayerUid() != b.GetPlayerUid() {
			if a.GetPlayerUid() < b.GetPlayerUid() {
				return -1
			}
			return 1
		}
		return 0
	})

	// 名次按位置连续编号（同级不共享名次）
	for i, item := range items {
		item.Rank = int32(i + 1)
	}

	// 本人的名次固定返回：按“严格排在本人之前的玩家数 + 1”计算，
	// 不依赖 items 是否包含本人（若后续 items 只含好友，本计算依然成立）
	selfRank := int32(1)
	for _, item := range items {
		if item.GetScore() > selfItem.GetScore() ||
			(item.GetScore() == selfItem.GetScore() && item.GetPlayerUid() < playerID) {
			selfRank++
		}
	}
	selfItem.Rank = selfRank

	return &cspb.FriendRankResp{
		Code:    0,
		Message: "ok",
		Data: &cspb.FriendRankData{
			Items: items,
			Self:  selfItem,
		},
	}, nil
}

// pageToSkipLimit 将位置分页参数 (start, end) 转换为数据库分页 (skip, limit)
// start/end 为从 1 开始的闭区间，(1,20) 表示查询前 20 条；
// start 缺省为 1，end 缺省为 start+friendPageSize-1；
// 单页数量上限 friendPageMaxLimit，skip 上限 maxFriendListOffset
func pageToSkipLimit(start, end int32) (int64, int64) {
	if start < 1 {
		start = 1
	}
	// 深分页保护：起始位置不能超过 maxFriendListOffset+1，否则 skip 会超出上限。
	// 此处截断 start 而非 skip，保证 limit 与 skip 基于同一窗口计算，
	// 避免"请求第 N 页却静默返回第 M 页"的错误数据
	if start > maxFriendListOffset+1 {
		start = maxFriendListOffset + 1
	}
	if end < start {
		end = start + friendPageSize - 1
	}
	limit := int64(end - start + 1)
	if limit > friendPageMaxLimit {
		limit = friendPageMaxLimit
	}
	skip := int64(start - 1)
	return skip, limit
}
