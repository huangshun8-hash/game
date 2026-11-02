package logic

import (
	"context"
	"strconv"
	"time"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/gamelogic/internal/repo"
	"kemiluo/services/shared/shareddto"

	"go.uber.org/zap"
)

// RoleLogic 角色信息查询业务逻辑
// 供平台侧跨服查询玩家角色信息与区服列表（LunPlay 平台角色接口的数据来源）
type RoleLogic struct {
	mongoSvc   *mongox.Service
	saveReader SaveReader
}

// NewRoleLogic 创建角色信息查询业务逻辑
func NewRoleLogic(mongoSvc *mongox.Service, saveReader SaveReader) *RoleLogic {
	return &RoleLogic{mongoSvc: mongoSvc, saveReader: saveReader}
}

// playerRepo 返回基于当前连接的玩家仓库实例
func (l *RoleLogic) playerRepo() *repo.PlayerRepo {
	return repo.NewPlayerRepo(l.mongoSvc.Client())
}

// timeFormat 角色时间字段的格式，与 LunPlay 接口文档约定一致
const timeFormat = "20060102 15:04:05"

// QueryByPassport 按平台用户ID（openId）查询角色列表
// serverCode 为 0 表示不过滤区服，返回该用户在全部区服的角色
//
// 参数：
//   - req: 查询条件，包含平台用户ID（openId）与可选的区服代码
//   - requestID: 请求追踪ID
//
// 返回：角色信息列表，无匹配角色时返回空列表
func (l *RoleLogic) QueryByPassport(ctx context.Context, req *shareddto.RoleQueryByPassportReq, requestID string) (*shareddto.RoleQueryResp, error) {
	players, err := l.playerRepo().FindRolesByOpenID(ctx, req.OpenID, req.ServerCode)
	if err != nil {
		logx.Error("按平台用户ID查询角色失败",
			zap.String("requestId", requestID),
			zap.String("openId", req.OpenID),
			zap.Int64("serverCode", req.ServerCode),
			zap.Error(err),
		)
		return nil, err
	}

	roles := make([]*shareddto.RoleInfo, 0, len(players))
	for _, p := range players {
		roles = append(roles, l.toRoleInfo(ctx, requestID, p))
	}
	return &shareddto.RoleQueryResp{Roles: roles}, nil
}

// QueryByRoleID 按角色ID（playerId）查询角色信息
//
// 参数：
//   - req: 查询条件，包含角色ID
//   - requestID: 请求追踪ID
//
// 返回：角色信息列表（通常为 0 或 1 条），无匹配角色时返回空列表
func (l *RoleLogic) QueryByRoleID(ctx context.Context, req *shareddto.RoleQueryByRoleIDReq, requestID string) (*shareddto.RoleQueryResp, error) {
	player, err := l.playerRepo().FindByPlayerID(ctx, req.RoleID)
	if err != nil {
		if err == mongox.ErrNotFound {
			return &shareddto.RoleQueryResp{Roles: []*shareddto.RoleInfo{}}, nil
		}
		logx.Error("按角色ID查询角色失败",
			zap.String("requestId", requestID),
			zap.Int64("roleId", req.RoleID),
			zap.Error(err),
		)
		return nil, err
	}

	return &shareddto.RoleQueryResp{Roles: []*shareddto.RoleInfo{l.toRoleInfo(ctx, requestID, player)}}, nil
}

// ServerListByRoleID 按角色ID（playerId）查询该角色所在区服列表
//
// 参数：
//   - req: 查询条件，包含角色ID
//   - requestID: 请求追踪ID
//
// 返回：区服列表，角色不存在时返回空列表
func (l *RoleLogic) ServerListByRoleID(ctx context.Context, req *shareddto.RoleQueryByRoleIDReq, requestID string) (*shareddto.ServerListResp, error) {
	player, err := l.playerRepo().FindByPlayerID(ctx, req.RoleID)
	if err != nil {
		if err == mongox.ErrNotFound {
			return &shareddto.ServerListResp{Servers: []*shareddto.ServerInfo{}}, nil
		}
		logx.Error("按角色ID查询区服失败",
			zap.String("requestId", requestID),
			zap.Int64("roleId", req.RoleID),
			zap.Error(err),
		)
		return nil, err
	}

	servers := []*shareddto.ServerInfo{{
		ServerCode: player.ServerID,
		// 区服名暂以 s{id} 兜底，后续接入区服配置表后可替换
		ServerName: "s" + strconv.FormatInt(player.ServerID, 10),
	}}
	return &shareddto.ServerListResp{Servers: servers}, nil
}

// toRoleInfo 将玩家实体转换为平台可见的角色信息，等级与昵称从存档 Base 读取。
// 时间字段按接口文档约定的 yyyyMMdd HH:mm:ss 格式化（UTC）
func (l *RoleLogic) toRoleInfo(ctx context.Context, requestID string, p *model.Player) *shareddto.RoleInfo {
	base, err := loadBase(ctx, l.saveReader, p.PlayerID)
	if err != nil {
		logx.Warn("读取角色存档失败",
			zap.String("requestId", requestID),
			zap.Int64("userId", p.PlayerID),
			zap.Error(err),
		)
	}
	return &shareddto.RoleInfo{
		RoleID:      p.PlayerID,
		Passport:    p.OpenID,
		ServerCode:  p.ServerID,
		PlayerLevel: int(base.GetLv()),
		PlayerName:  playerNickname(p.PlayerID, base),
		CreateDate:  formatTime(p.CreatedAt),
		LoginDate:   formatTime(p.LastLoginAt),
	}
}

// formatTime 将时间格式化为 UTC 的 yyyyMMdd HH:mm:ss，零值时间返回空字符串。
// 统一以 UTC 输出，避免部署在不同地区的服务器因本地时区不同而返回不一致的角色时间。
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}
