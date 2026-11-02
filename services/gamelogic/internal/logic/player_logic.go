package logic

import (
	"context"
	"errors"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/invitex"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"

	"go.uber.org/zap"
)

// PlayerLogic 玩家查询业务逻辑
type PlayerLogic struct {
	mongoSvc   *mongox.Service
	saveReader SaveReader
}

// NewPlayerLogic 创建玩家查询业务逻辑
func NewPlayerLogic(mongoSvc *mongox.Service, saveReader SaveReader) *PlayerLogic {
	return &PlayerLogic{mongoSvc: mongoSvc, saveReader: saveReader}
}

// playerRepo 返回基于当前连接的玩家仓库实例
func (l *PlayerLogic) playerRepo() *repo.PlayerRepo {
	return repo.NewPlayerRepo(l.mongoSvc.Client())
}

// Query 按玩家ID或邀请码查询玩家基本信息（玩家ID、昵称、等级）。
// playerId 与 inviteCode 至少提供一个，同时提供时优先使用 playerId；
// 等级与昵称从 Base 存档缓存读取（未命中回源 DB），未提供自定义昵称时回退默认昵称。
//
// 参数：
//   - req: 查询条件，包含玩家ID（playerId）或邀请码（inviteCode）
//   - requestID: 请求追踪ID
//   - playerID: 调用方玩家ID
//
// 返回：玩家基本信息；玩家不存在时返回 ErrPlayerNotFound
func (l *PlayerLogic) Query(ctx context.Context, req *cspb.PlayerQueryReq, requestID string, playerID int64) (*cspb.PlayerQueryResp, error) {
	// 1. 解析查询目标玩家ID：优先玩家ID，其次将邀请码反解为玩家ID
	targetID, err := resolveTargetPlayerID(req.GetPlayerId(), req.GetInviteCode())
	if err != nil {
		return nil, err
	}

	// 2. 校验目标玩家存在
	player, err := l.playerRepo().FindByPlayerID(ctx, targetID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return nil, model.ErrPlayerNotFound
		}
		logx.Error("查询玩家失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("targetPlayerId", targetID),
			zap.Error(err),
		)
		return nil, model.ErrPlayerQueryFailed
	}

	// 3. 读取 Base 存档获取昵称与等级（读取失败时仅告警，昵称回退默认，等级为 0）
	base, err := loadBase(ctx, l.saveReader, player.PlayerID)
	if err != nil {
		logx.Warn("读取玩家Base存档失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Int64("targetPlayerId", player.PlayerID),
			zap.Error(err),
		)
	}

	return &cspb.PlayerQueryResp{
		Code:    0,
		Message: "ok",
		Data: &cspb.PlayerQueryData{
			PlayerId: player.PlayerID,
			Nickname: playerNickname(player.PlayerID, base),
			Level:    base.GetLv(),
			Avatar:   base.GetAvatar(),
		},
	}, nil
}

// resolveTargetPlayerID 解析查询目标玩家ID：playerId 大于 0 时直接使用；
// 否则将 inviteCode 反解为玩家ID，缺失或不合法时返回对应业务错误。
// 参数：
//   - playerId: 查询使用的玩家ID
//   - inviteCode: 查询使用的邀请码
//
// 返回：目标玩家ID，或参数不合法时的业务错误
func resolveTargetPlayerID(playerId int64, inviteCode string) (int64, error) {
	if playerId > 0 {
		return playerId, nil
	}
	if inviteCode == "" {
		return 0, model.ErrPlayerQueryParamMissing
	}
	uid, err := invitex.InviteCode2UID(inviteCode)
	if err != nil {
		return 0, model.ErrPlayerQueryInvalidCode
	}
	return uid, nil
}
