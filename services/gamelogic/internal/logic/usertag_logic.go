package logic

import (
	"context"
	"slices"

	"kemiluo/generated/gdconfig"
	"kemiluo/pb/cspb"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/repo"

	"go.uber.org/zap"
)

// UserTagLogic 用户标签匹配业务逻辑
type UserTagLogic struct {
	gdConfig      *gdconfig.ConfigManager
	mongoSvc      *mongox.Service
	saveReader    SaveReader
	payStatsCache *cache.PayStatsCache
}

// NewUserTagLogic 创建用户标签业务逻辑
// payStatsCache: 玩家付费统计缓存；为 nil 时付费统计查询降级为直查 DB
func NewUserTagLogic(gdConfig *gdconfig.ConfigManager, mongoSvc *mongox.Service, saveReader SaveReader, payStatsCache *cache.PayStatsCache) *UserTagLogic {
	return &UserTagLogic{gdConfig: gdConfig, mongoSvc: mongoSvc, saveReader: saveReader, payStatsCache: payStatsCache}
}

// playerPayStats 玩家付费统计（分），每次标签匹配请求聚合一次，供所有配置行复用
type playerPayStats struct {
	lastPay  int64 // 最近1笔付费
	last4Pay int64 // 最近4笔付费合计
	monthPay int64 // 最近30天付费合计
	totalPay int64 // 累积付费
}

// MatchTags 按 enum 与用户分组（AB分组）过滤后，匹配满足条件的 tag 列表
// ctx: 请求上下文；req: proto 请求消息（业务字段，含 enum 与 group）
// requestID: 请求追踪ID；playerID: 玩家ID，均由 handler 从 Context 读取后显式传入
// 返回 map[string]string，key 为配置中的 enum，value 为匹配到的 tag
func (l *UserTagLogic) MatchTags(ctx context.Context, req *cspb.UserTagsReq, requestID string, playerID int64) (*cspb.UserTagsResp, error) {
	table := l.gdConfig.Usertag.Load()
	if table == nil {
		logx.Error("用户标签配置表未加载",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
		)
		return &cspb.UserTagsResp{
			Data: &cspb.UserTagsData{Tags: map[string]string{}},
		}, nil
	}

	allCfg := table.GetAll()
	// 按 enum 过滤并按 Id 升序排列，使"每个 enum 取第一条满足条件的 tag"的结果确定（Id 越小优先级越高），
	// 避免 map 随机遍历导致相同条件下返回不稳定
	cfgs := make([]*gdconfig.UsertagConfig, 0, len(allCfg))
	for _, cfg := range allCfg {
		if req.Enum != "" && cfg.Enum != req.Enum {
			continue
		}
		cfgs = append(cfgs, cfg)
	}
	slices.SortFunc(cfgs, func(a, b *gdconfig.UsertagConfig) int {
		return int(a.Id - b.Id)
	})

	// 仅在存在付费条件配置时查询付费统计，避免无付费条件的标签请求触发数据库查询
	var stats *playerPayStats
	if slices.ContainsFunc(cfgs, hasPayCondition) {
		stats = l.loadPayStats(ctx, playerID, requestID)
	}

	// 仅在存在等级条件配置时读取玩家等级，避免无等级条件的标签请求触发缓存读取
	var level int32
	var hasLevel bool
	if slices.ContainsFunc(cfgs, hasLevelCondition) {
		if base, ok := l.loadPlayerBase(ctx, requestID, playerID); ok {
			level = base.GetLv()
			hasLevel = true
		}
	}

	// 每个 enum 分组只返回第一条满足条件的 tag
	matched := make(map[string]string) // enum -> tag
	for _, cfg := range cfgs {
		if _, ok := matched[cfg.Enum]; ok {
			continue
		}
		if l.matchConditionImpl(cfg, req.Group, stats, level, hasLevel) {
			matched[cfg.Enum] = cfg.Tag
		}
	}

	return &cspb.UserTagsResp{
		Data: &cspb.UserTagsData{Tags: matched},
	}, nil
}

// loadPayStats 查询并聚合玩家付费统计。
// 优先读取缓存：付费统计更新频率极低，缓存命中率高，可避免每次标签匹配请求触发 DB 聚合；
// 缓存未命中时回源 MongoDB 聚合查询，查询成功回填缓存，查询失败不写缓存。
// 数据源未注入或查询失败时降级返回空统计（付费条件按不满足处理），不阻塞标签匹配流程
func (l *UserTagLogic) loadPayStats(ctx context.Context, playerID int64, requestID string) *playerPayStats {
	if l.mongoSvc == nil {
		logx.Warn("付费统计数据源未注入，付费条件按不满足处理",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
		)
		return &playerPayStats{}
	}
	if l.payStatsCache != nil {
		if item, ok := l.payStatsCache.Get(ctx, playerID); ok {
			return &playerPayStats{
				lastPay:  item.LastPay,
				last4Pay: item.Last4Pay,
				monthPay: item.MonthPay,
				totalPay: item.TotalPay,
			}
		}
	}
	stats, err := repo.NewPayOrderRepo(l.mongoSvc.Client()).FindPaidStatsByPlayerID(ctx, playerID)
	if err != nil {
		logx.Error("聚合玩家付费统计失败，付费条件按不满足处理",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return &playerPayStats{}
	}
	item := cache.PayStatsItem{
		LastPay:  stats.LastPay,
		Last4Pay: stats.Last4Pay,
		MonthPay: stats.MonthPay,
		TotalPay: stats.TotalPay,
	}
	// 仅在查询成功后回填缓存；查询失败不写缓存，避免把"查询异常"固化为"无付费统计"
	if l.payStatsCache != nil {
		if err := l.payStatsCache.Set(ctx, playerID, item); err != nil {
			logx.Warn("回填付费统计缓存失败",
				zap.String("requestId", requestID),
				zap.Int64("userId", playerID),
				zap.Error(err),
			)
		}
	}
	return &playerPayStats{
		lastPay:  item.LastPay,
		last4Pay: item.Last4Pay,
		monthPay: item.MonthPay,
		totalPay: item.TotalPay,
	}
}

// loadPlayerBase 从存档 Base 读取玩家基础数据（本玩家自身）。
// 数据源未注入或读取失败时返回 ok=false（等级条件按不满足处理），不阻塞标签匹配流程
func (l *UserTagLogic) loadPlayerBase(ctx context.Context, requestID string, playerID int64) (*cspb.Base, bool) {
	if l.saveReader == nil {
		logx.Warn("玩家存档数据源未注入，等级条件按不满足处理",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
		)
		return nil, false
	}
	base, err := loadBase(ctx, l.saveReader, playerID)
	if err != nil {
		logx.Warn("读取玩家存档失败，等级条件按不满足处理",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return nil, false
	}
	if base == nil {
		return nil, false
	}
	return base, true
}

// hasPayCondition 判断配置行是否含付费相关条件（任一付费区间边界非 0 即视为含）
func hasPayCondition(cfg *gdconfig.UsertagConfig) bool {
	return cfg.SLastPay > 0 || cfg.ELastPay > 0 ||
		cfg.SLast4Pay > 0 || cfg.ELast4Pay > 0 ||
		cfg.SMonthPay > 0 || cfg.EMonthPay > 0 ||
		cfg.SPay > 0 || cfg.EPay > 0
}

// hasLevelCondition 判断配置行是否含等级条件（任一等级区间边界非 0 即视为含）
func hasLevelCondition(cfg *gdconfig.UsertagConfig) bool {
	return cfg.SLv > 0 || cfg.ELv > 0
}

// matchConditionImpl 按用户分组、付费与等级区间逐项校验配置条件
// cfg: 配置行；group: 玩家AB分组（来自请求，0 表示未分组）；stats: 付费统计；
// 仅当配置含付费条件时 stats 非 nil，此时做区间校验
// level/hasLevel: 玩家等级；仅当配置含等级条件时 hasLevel 为 true，此时做区间校验
// 配置约定：区间下限/上限为 0 表示该侧不限制；AbGroup 为空切片表示仅命中未分组玩家（group == 0）
func (l *UserTagLogic) matchConditionImpl(cfg *gdconfig.UsertagConfig, group int32, stats *playerPayStats, level int32, hasLevel bool) bool {
	// 用户分组匹配：空 AbGroup 表示仅命中未分组玩家（group == 0），非空则玩家分组须命中其一
	if len(cfg.AbGroup) == 0 {
		if group != 0 {
			return false
		}
	} else if !slices.Contains(cfg.AbGroup, group) {
		return false
	}

	if stats != nil {
		// 1. 最近1笔付费区间
		if !inPayRange(cfg.SLastPay, cfg.ELastPay, stats.lastPay) {
			return false
		}
		// 2. 最近4笔付费合计区间
		if !inPayRange(cfg.SLast4Pay, cfg.ELast4Pay, stats.last4Pay) {
			return false
		}
		// 3. 最近30天付费合计区间
		if !inPayRange(cfg.SMonthPay, cfg.EMonthPay, stats.monthPay) {
			return false
		}
		// 4. 累积付费区间
		if !inPayRange(cfg.SPay, cfg.EPay, stats.totalPay) {
			return false
		}
	}
	// 玩家等级区间校验
	if !inLevelRange(cfg.SLv, cfg.ELv, level, hasLevel) {
		return false
	}
	return true
}

// inPayRange 判断 value 是否落在 [lower, upper] 区间内；0 表示对应侧不限制。
// 当存在上限限制（upper > 0）且 value 为 0（玩家从未有该维度付费行为）时，
// 视为不满足条件：配置了付费区间意味着需要有付费行为，未付费玩家不应匹配。
func inPayRange(lower, upper int32, value int64) bool {
	// 双侧均为 0 表示未配置付费条件，不做校验
	if lower <= 0 && upper <= 0 {
		return true
	}
	if lower > 0 && value < int64(lower) {
		return false
	}
	// 配置了上限时，超出上限视为不满足
	if upper > 0 && value > int64(upper) {
		return false
	}
	return true
}

// inLevelRange 判断 level 是否落在 [lower, upper] 区间内；0 表示对应侧不限制。
// hasLevel 为 false 表示玩家等级数据不可用（数据源未注入或读取失败），
// 此时若配置了等级条件则视为不满足。
func inLevelRange(lower, upper int32, level int32, hasLevel bool) bool {
	// 双侧均为 0 表示未配置等级条件，不做校验
	if lower <= 0 && upper <= 0 {
		return true
	}
	if !hasLevel {
		level = 1
	}
	if lower > 0 && level < lower {
		return false
	}
	if upper > 0 && level > upper {
		return false
	}
	return true
}
