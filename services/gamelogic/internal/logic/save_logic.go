package logic

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/pkg/savex"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/gmtdto"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// SaveLogic 存档业务逻辑
type SaveLogic struct {
	mongoSvc    *mongox.Service
	playerCache *cache.Manager
}

// NewSaveLogic 创建存档业务逻辑
// redisSvc: Redis 服务实例，用于构建玩家存档缓存；Redis 未就绪时缓存降级为不可用
func NewSaveLogic(mongoSvc *mongox.Service, redisSvc *redisx.Service) *SaveLogic {
	var client *redisx.Client
	if redisSvc != nil {
		client = redisSvc.Client()
	}
	return &SaveLogic{mongoSvc: mongoSvc, playerCache: cache.New(client)}
}

func (l *SaveLogic) repos() (*repo.SaveHeadRepo, *repo.SaveSnapshotRepo) {
	client := l.mongoSvc.Client()
	return repo.NewSaveHeadRepo(client), repo.NewSaveSnapshotRepo(client)
}

// syncSaveCache 在存档写入成功后，同步更新玩家存档缓存。
// 缓存写入失败会尝试失效旧缓存并记录告警，避免后续读到陈旧数据，且不影响存档写入结果。
func (l *SaveLogic) syncSaveCache(ctx context.Context, playerID int64, key string, version int64, sign string, content string, requestID string) {
	if err := l.playerCache.Set(ctx, playerID, key, version, sign, content); err != nil {
		logx.Warn("存档缓存更新失败，尝试失效缓存",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", key),
			zap.Int64("version", version),
			zap.Error(err),
		)
		if delErr := l.playerCache.Del(ctx, playerID, key); delErr != nil {
			logx.Warn("失效存档缓存失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("key", key),
				zap.Error(delErr),
			)
		}
	}
}

// Upload 处理存档上传请求。
// req: proto 请求消息（业务字段，含 key 标识存档分类）。
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入。
func (l *SaveLogic) Upload(ctx context.Context, req *cspb.SaveUploadReq, requestID string, playerID int64) (*cspb.SaveUploadResp, error) {
	headRepo, snapRepo := l.repos()

	head, err := headRepo.FindOrNil(ctx, playerID, req.Key)
	if err != nil {
		logx.Error("查询存档头失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Error(err),
		)
		return nil, fmt.Errorf("查询存档头失败: %w", err)
	}

	if head == nil {
		// 服务端尚无该 key 的存档，属于首次上传：此时 baseVersion 不代表服务端状态，
		// 客户端可能携带本地版本号而非协议约定的 0，因此不参与冲突校验，
		// 直接以 newVersion 建立首个版本，保证首次上传始终可写入。
		return l.firstUpload(ctx, req, requestID, playerID, headRepo, snapRepo)
	}

	// 已有存档：强制覆盖只要求版本严格递增（baseVersion 不参与校验），
	// 普通上传要求 newVersion 大于 baseVersion，并进一步由事务内 CAS 校验 baseVersion 是否为服务端当前版本。
	if req.ForceOverwrite {
		if req.NewVersion <= head.CurrentVersion {
			logx.Warn("强制覆盖版本非递增，拒绝并返回冲突",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("key", req.Key),
				zap.Int64("newVersion", req.NewVersion),
				zap.Int64("currentVersion", head.CurrentVersion),
			)
			return nil, &saveConflictError{currentVersion: head.CurrentVersion}
		}
	} else if req.NewVersion <= req.BaseVersion {
		logx.Warn("上传版本未递增，拒绝写入",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Int64("newVersion", req.NewVersion),
			zap.Int64("baseVersion", req.BaseVersion),
		)
		return nil, errVersionNotMonotonic
	}

	return l.updateUpload(ctx, req, requestID, playerID, headRepo, snapRepo)
}

func (l *SaveLogic) firstUpload(
	ctx context.Context,
	req *cspb.SaveUploadReq,
	requestID string,
	playerID int64,
	headRepo *repo.SaveHeadRepo,
	snapRepo *repo.SaveSnapshotRepo,
) (*cspb.SaveUploadResp, error) {
	client := l.mongoSvc.Client()
	var savedVersion int64

	// 协议层 content 为原始二进制线格式字节，存储层统一存 base64 编码，上传/下载边界各自转码
	contentB64 := base64.StdEncoding.EncodeToString(req.Content)

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		head := &model.SaveHead{PlayerID: playerID, Key: req.Key, CurrentVersion: req.NewVersion}
		isCreated, err := headRepo.UpsertHead(txCtx, head)
		if err != nil {
			return fmt.Errorf("创建存档头失败: %w", err)
		}
		if !isCreated {
			return errFirstUploadRace
		}
		snap := &model.SaveSnapshot{
			PlayerID:    playerID,
			Key:         req.Key,
			Version:     req.NewVersion,
			PrevVersion: 0, // 首次上传无前一版本
			SnapshotSeq: 1,
			Sign:        req.Sign,
			Content:     contentB64,
		}
		if err := snapRepo.InsertSnapshot(txCtx, snap); err != nil {
			return fmt.Errorf("创建首次存档快照失败: %w", err)
		}
		if err := headRepo.FixSnapshotID(txCtx, playerID, req.Key, req.NewVersion, snap.ID, 1, 0); err != nil {
			return fmt.Errorf("回填快照ID失败: %w", err)
		}
		savedVersion = req.NewVersion
		return nil
	})

	if errors.Is(err, errFirstUploadRace) {
		logx.Warn("首次存档并发竞态，降级为更新流程",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
		)
		return l.updateUpload(ctx, req, requestID, playerID, headRepo, snapRepo)
	}
	if err != nil {
		logx.Error("首次存档写入失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Error(err),
		)
		return nil, err
	}

	logx.Info("首次存档写入成功",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("key", req.Key),
		zap.Int64("version", savedVersion),
	)
	l.syncSaveCache(ctx, playerID, req.Key, savedVersion, req.Sign, contentB64, requestID)
	return &cspb.SaveUploadResp{
		Data: &cspb.SaveUploadData{Version: savedVersion},
	}, nil
}

func (l *SaveLogic) updateUpload(
	ctx context.Context,
	req *cspb.SaveUploadReq,
	requestID string,
	playerID int64,
	headRepo *repo.SaveHeadRepo,
	snapRepo *repo.SaveSnapshotRepo,
) (*cspb.SaveUploadResp, error) {
	client := l.mongoSvc.Client()
	var savedVersion, committedSeq int64

	// 协议层 content 与首次上传一致为原始二进制，存储层转 base64 后落库与回填缓存
	contentB64 := base64.StdEncoding.EncodeToString(req.Content)

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		var newSeq int64
		var prevVersion int64
		if req.ForceOverwrite {
			seq, pv, err := headRepo.ForceUpdateSeqOnly(txCtx, playerID, req.Key)
			if err != nil {
				return fmt.Errorf("强制更新存档头序号失败: %w", err)
			}
			// 事务内依据返回的更新前版本再次校验单调性，防止事务外读取的 head 过期导致版本回退
			if req.NewVersion <= pv {
				return &saveConflictError{currentVersion: pv}
			}
			newSeq = seq
			prevVersion = pv
		} else {
			updated, seq, err := headRepo.CASUpdateSeqOnly(txCtx, playerID, req.Key, req.BaseVersion)
			if err != nil {
				return fmt.Errorf("CAS更新存档头序号失败: %w", err)
			}
			if updated == 0 {
				return errCASFailed
			}
			newSeq = seq
			// CAS 成功意味着 baseVersion 就是更新前的 currentVersion
			prevVersion = req.BaseVersion
		}
		snap := &model.SaveSnapshot{
			PlayerID:    playerID,
			Key:         req.Key,
			Version:     req.NewVersion,
			PrevVersion: prevVersion,
			SnapshotSeq: newSeq,
			Sign:        req.Sign,
			Content:     contentB64,
		}
		if err := snapRepo.InsertSnapshot(txCtx, snap); err != nil {
			return fmt.Errorf("写入存档快照失败: %w", err)
		}
		if err := headRepo.FixSnapshotID(txCtx, playerID, req.Key, req.NewVersion, snap.ID, newSeq, prevVersion); err != nil {
			return fmt.Errorf("回填快照ID失败: %w", err)
		}
		savedVersion = req.NewVersion
		committedSeq = newSeq
		return nil
	})

	if err == nil {
		if _, pruneErr := snapRepo.PruneOldSnapshots(ctx, playerID, req.Key, committedSeq); pruneErr != nil {
			logx.Warn("清理旧存档快照失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("key", req.Key),
				zap.Int64("snapshotSeq", committedSeq),
				zap.Error(pruneErr),
			)
		}
	}

	if errors.Is(err, errCASFailed) {
		latest, queryErr := headRepo.FindByKey(ctx, playerID, req.Key)
		if queryErr != nil {
			logx.Error("CAS失败后查询最新存档头失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("key", req.Key),
				zap.Error(queryErr),
			)
			return nil, fmt.Errorf("查询存档头失败: %w", queryErr)
		}
		logx.Warn("存档CAS冲突（另一设备已提交）",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Int64("baseVersion", req.BaseVersion),
			zap.Int64("currentVersion", latest.CurrentVersion),
		)
		return nil, &saveConflictError{currentVersion: latest.CurrentVersion}
	}
	if ce, ok := IsSaveConflict(err); ok {
		logx.Warn("存档版本冲突（强制覆盖版本未递增）",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Int64("currentVersion", ce.CurrentVersion()),
		)
		return nil, ce
	}
	if err != nil {
		logx.Error("存档更新写入失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.String("key", req.Key),
			zap.Error(err),
		)
		return nil, err
	}

	logx.Info("存档更新成功",
		zap.String("requestId", requestID),
		zap.Int64("userId", playerID),
		zap.String("key", req.Key),
		zap.Int64("version", savedVersion),
		zap.Bool("forceOverwrite", req.ForceOverwrite),
	)
	l.syncSaveCache(ctx, playerID, req.Key, savedVersion, req.Sign, contentB64, requestID)
	return &cspb.SaveUploadResp{
		Data: &cspb.SaveUploadData{Version: savedVersion},
	}, nil
}

// Download 处理存档批量下载请求
// req: proto 请求消息（含 keys 列表，每个 key 标识一种存档分类）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
// 先批量查出各 key 对应的存档头，再一次性拉取所有最新快照，合并后按请求 keys 顺序返回
func (l *SaveLogic) Download(ctx context.Context, req *cspb.SaveDownloadReq, requestID string, playerID int64) (*cspb.SaveDownloadResp, error) {
	headRepo, snapRepo := l.repos()

	// 批量查出所有存在的存档头，建立 key -> head 映射
	heads, err := headRepo.FindByKeys(ctx, playerID, req.Keys)
	if err != nil {
		logx.Error("批量查询存档头失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Any("keys", req.Keys),
			zap.Error(err),
		)
		return nil, fmt.Errorf("批量查询存档头失败: %w", err)
	}

	// 收集所有有效的 LatestSnapshotID，同时记录哪些 key 数据异常
	headByKey := make(map[string]*model.SaveHead, len(heads))
	snapshotIDs := make([]bson.ObjectID, 0, len(heads))
	for i := range heads {
		h := &heads[i]
		if h.LatestSnapshotID.IsZero() {
			logx.Error("存档头快照ID为零值，数据异常",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.String("key", h.Key),
			)
			// 数据异常的 key 不参与快照查询，结果中按无存档处理
			continue
		}
		headByKey[h.Key] = h
		snapshotIDs = append(snapshotIDs, h.LatestSnapshotID)
	}

	// 批量拉取快照，建立 snapshotID -> snapshot 映射
	snapByID := make(map[bson.ObjectID]*model.SaveSnapshot, len(snapshotIDs))
	if len(snapshotIDs) > 0 {
		snaps, err := snapRepo.FindBySnapshotIDs(ctx, snapshotIDs)
		if err != nil {
			logx.Error("批量查询存档快照失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Error(err),
			)
			return nil, fmt.Errorf("批量查询存档快照失败: %w", err)
		}
		for i := range snaps {
			s := &snaps[i]
			snapByID[s.ID] = s
		}
	}

	// 按请求 keys 顺序组装结果，缺失的 key 填入空条目（version=0）
	items := make([]*cspb.SaveDownloadItem, 0, len(req.Keys))
	for _, key := range req.Keys {
		item := &cspb.SaveDownloadItem{Key: key}
		if h, ok := headByKey[key]; ok {
			if snap, ok := snapByID[h.LatestSnapshotID]; ok {
				item.Version = snap.Version
				item.Sign = snap.Sign
				// 存储层为 base64 字符串，协议 bytes 字段下发原始线格式字节，需解码
				if content, decErr := base64.StdEncoding.DecodeString(snap.Content); decErr == nil {
					item.Content = content
				} else {
					logx.Error("存档内容不是合法的 base64，按无内容下发",
						zap.String("requestId", requestID),
						zap.Int64("userId", playerID),
						zap.String("key", key),
						zap.Int64("version", snap.Version),
						zap.Error(decErr),
					)
				}
				// 从数据库加载最新存档后，同步回填缓存。
				l.syncSaveCache(ctx, playerID, key, snap.Version, snap.Sign, snap.Content, requestID)
			} else {
				// 快照丢失：head 有效但快照不存在，属于数据异常，记录告警后按空处理
				logx.Error("存档快照丢失，数据异常",
					zap.String("requestId", requestID),
					zap.Int64("userId", playerID),
					zap.String("key", key),
					zap.Any("snapshotId", h.LatestSnapshotID),
				)
			}
		}
		items = append(items, item)
	}

	return &cspb.SaveDownloadResp{
		Data: &cspb.SaveDownloadData{Items: items},
	}, nil
}

// GmtDownload 处理 GMT 后台查询玩家存档请求
// keys 为空时返回该玩家所有存档，非空时返回指定 keys 的存档
func (l *SaveLogic) GmtDownload(ctx context.Context, req *gmtdto.GmtSaveDownloadReq, requestID string) (*gmtdto.GmtSaveDownloadResp, error) {
	headRepo, snapRepo := l.repos()

	// 根据是否指定 keys 选择查询方式
	var heads []model.SaveHead
	var err error
	if len(req.Keys) > 0 {
		heads, err = headRepo.FindByKeys(ctx, req.PlayerID, req.Keys)
	} else {
		heads, err = headRepo.FindAllByPlayer(ctx, req.PlayerID)
	}
	if err != nil {
		return nil, fmt.Errorf("查询存档头失败: %w", err)
	}

	// 收集有效的快照ID
	headByKey := make(map[string]*model.SaveHead, len(heads))
	snapshotIDs := make([]bson.ObjectID, 0, len(heads))
	for i := range heads {
		h := &heads[i]
		if h.LatestSnapshotID.IsZero() {
			logx.Warn("GMT存档查询：存档头快照ID为零值",
				zap.String("requestId", requestID),
				zap.Int64("playerId", req.PlayerID),
				zap.String("key", h.Key),
			)
			continue
		}
		headByKey[h.Key] = h
		snapshotIDs = append(snapshotIDs, h.LatestSnapshotID)
	}

	// 批量拉取快照
	snapByID := make(map[bson.ObjectID]*model.SaveSnapshot, len(snapshotIDs))
	if len(snapshotIDs) > 0 {
		snaps, err := snapRepo.FindBySnapshotIDs(ctx, snapshotIDs)
		if err != nil {
			return nil, fmt.Errorf("批量查询存档快照失败: %w", err)
		}
		for i := range snaps {
			s := &snaps[i]
			snapByID[s.ID] = s
		}
	}

	// 将单个 gzip 存档解压为明文 protobuf 原始字节，再以 base64 返回给 GMT。
	// 解码或解压失败时返回空内容，不阻断整体查询。
	toPlainBase64 := func(snap *model.SaveSnapshot) string {
		if snap == nil || snap.Content == "" {
			return ""
		}
		wire, err := base64.StdEncoding.DecodeString(snap.Content)
		if err != nil {
			logx.Warn("GMT存档查询：解码快照内容失败",
				zap.String("requestId", requestID),
				zap.Int64("playerId", req.PlayerID),
				zap.String("key", snap.Key),
				zap.Error(err),
			)
			return ""
		}
		plain, err := savex.DecompressWire(wire)
		if err != nil {
			logx.Warn("GMT存档查询：解压快照内容失败",
				zap.String("requestId", requestID),
				zap.Int64("playerId", req.PlayerID),
				zap.String("key", snap.Key),
				zap.Error(err),
			)
			return ""
		}
		return base64.StdEncoding.EncodeToString(plain)
	}

	// 组装结果
	var items []*gmtdto.GmtSaveDownloadItem
	if len(req.Keys) > 0 {
		// 按请求 keys 顺序返回
		items = make([]*gmtdto.GmtSaveDownloadItem, 0, len(req.Keys))
		for _, key := range req.Keys {
			item := &gmtdto.GmtSaveDownloadItem{Key: key}
			if h, ok := headByKey[key]; ok {
				if snap, ok := snapByID[h.LatestSnapshotID]; ok {
					item.Version = snap.Version
					item.Content = toPlainBase64(snap)
					item.UpdateTime = h.UpdatedAt.UnixMilli()
				}
			}
			items = append(items, item)
		}
	} else {
		// 返回所有存档
		items = make([]*gmtdto.GmtSaveDownloadItem, 0, len(headByKey))
		for key, h := range headByKey {
			item := &gmtdto.GmtSaveDownloadItem{Key: key}
			if snap, ok := snapByID[h.LatestSnapshotID]; ok {
				item.Version = snap.Version
				item.Content = toPlainBase64(snap)
				item.UpdateTime = h.UpdatedAt.UnixMilli()
			}
			items = append(items, item)
		}
	}

	return &gmtdto.GmtSaveDownloadResp{Items: items}, nil
}

// GmtUpload 处理 GMT 后台批量保存玩家存档请求
// 使用强制覆盖模式写入，版本号基于当前存档最新版本+1
// 仅新增一个快照版本，不执行旧快照清理（跳过最大存档数量限制）
// 所有分片在同一事务内写入，保证批量操作的原子性；版本号在事务内读取存档头后计算，避免并发竞态
func (l *SaveLogic) GmtUpload(ctx context.Context, req *gmtdto.GmtSaveUploadReq, requestID string) (*gmtdto.GmtSaveUploadResp, error) {
	headRepo, snapRepo := l.repos()
	client := l.mongoSvc.Client()

	err := client.RunTransaction(ctx, func(txCtx context.Context) error {
		for _, item := range req.Items {
			if err := l.gmtUploadItemTx(txCtx, req.PlayerID, item, headRepo, snapRepo); err != nil {
				return fmt.Errorf("save key %s failed: %w", item.Key, err)
			}
		}
		return nil
	})
	if err != nil {
		logx.Error("GMT批量保存玩家存档失败",
			zap.String("requestId", requestID),
			zap.Int64("playerId", req.PlayerID),
			zap.Error(err),
		)
		return nil, err
	}

	// 强制写入后失效对应存档缓存，避免读取到 DB 更新前的陈旧值
	for _, item := range req.Items {
		if delErr := l.playerCache.Del(ctx, req.PlayerID, item.Key); delErr != nil {
			logx.Warn("失效存档缓存失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", req.PlayerID),
				zap.String("key", item.Key),
				zap.Error(delErr),
			)
		}
	}

	return &gmtdto.GmtSaveUploadResp{
		UpdateTime: time.Now().UnixMilli(),
	}, nil
}

// gmtUploadItemTx 在给定事务内写入单条 GMT 存档（强制覆盖，不清理旧快照）。
// 版本号基于事务内读取的存档头当前版本加一，保证同一事务内的一致性。
// 入参 item.Content 为明文 protobuf 线格式字节的 base64 编码，落库前仅执行 gzip 压缩。
func (l *SaveLogic) gmtUploadItemTx(txCtx context.Context, playerID int64, item *gmtdto.GmtSaveUploadItem, headRepo *repo.SaveHeadRepo, snapRepo *repo.SaveSnapshotRepo) error {
	head, err := headRepo.FindOrNil(txCtx, playerID, item.Key)
	if err != nil {
		return fmt.Errorf("查询存档头失败: %w", err)
	}

	if head == nil {
		// 存档不存在时不允许GMT创建，直接返回错误
		return fmt.Errorf("存档不存在, playerID=%d, key=%s", playerID, item.Key)
	}

	// 已有存档：强制递增版本号与序号
	newVersion := head.CurrentVersion + 1
	newSeq, _, err := headRepo.ForceUpdateSeqOnly(txCtx, playerID, item.Key)
	if err != nil {
		return fmt.Errorf("强制更新存档头序号失败: %w", err)
	}

	// 解码明文 protobuf 线格式字节
	plainBytes, err := base64.StdEncoding.DecodeString(item.Content)
	if err != nil {
		return fmt.Errorf("存档内容不是合法的 base64: %w", err)
	}

	// 将明文 protobuf 线格式字节压缩为存档线格式。
	wireBytes, err := savex.CompressWire(plainBytes)
	if err != nil {
		return fmt.Errorf("压缩存档内容失败: %w", err)
	}
	sign := savex.ComputeSign(wireBytes)
	snap := &model.SaveSnapshot{
		PlayerID:    playerID,
		Key:         item.Key,
		Version:     newVersion,
		PrevVersion: head.CurrentVersion,
		SnapshotSeq: newSeq,
		Sign:        sign,
		Content:     base64.StdEncoding.EncodeToString(wireBytes),
	}
	if err := snapRepo.InsertSnapshot(txCtx, snap); err != nil {
		return fmt.Errorf("写入存档快照失败: %w", err)
	}
	if err := headRepo.FixSnapshotID(txCtx, playerID, item.Key, newVersion, snap.ID, newSeq, head.CurrentVersion); err != nil {
		return fmt.Errorf("回填快照ID失败: %w", err)
	}
	return nil
}

// ---- 内部错误类型 ----

type saveConflictError struct{ currentVersion int64 }

func (e *saveConflictError) Error() string {
	return fmt.Sprintf("save conflict: currentVersion=%d", e.currentVersion)
}

// CurrentVersion 返回服务器当前版本号，供 handler 层构造冲突响应
func (e *saveConflictError) CurrentVersion() int64 { return e.currentVersion }

// IsSaveConflict 判断是否为存档冲突错误
func IsSaveConflict(err error) (*saveConflictError, bool) {
	var ce *saveConflictError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// IsVersionNotMonotonic 判断是否为版本未递增错误（已有存档更新时 newVersion 未大于 baseVersion）。
func IsVersionNotMonotonic(err error) bool {
	return errors.Is(err, errVersionNotMonotonic)
}

var errCASFailed = fmt.Errorf("cas failed")
var errFirstUploadRace = fmt.Errorf("first upload race")
var errVersionNotMonotonic = fmt.Errorf("newVersion must be greater than baseVersion")
