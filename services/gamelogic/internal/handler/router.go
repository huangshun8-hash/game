package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/cache"
	"kemiluo/services/gamelogic/internal/logic"
)

// NewRouters 返回 gamelogic 服务的所有路由处理器
// 通过 Deps 容器统一注入依赖，后续新增 handler 只需在此追加
func NewRouters(deps *Deps) []httpx.Router {
	authLogic := logic.NewAuthLogic(deps.MongoSvc, deps.JWT, deps.TokenStore, deps.AsyncSvc, deps.PlatformSvc, deps.Hub, deps.Config.InternalSecret, deps.RedisSvc, deps.InstanceID)
	saveLogic := logic.NewSaveLogic(deps.MongoSvc, deps.RedisSvc)
	saveCacheReader := logic.NewSaveCacheReader(deps.MongoSvc, deps.RedisSvc)
	mailLogic := logic.NewMailLogic(deps.MongoSvc)
	var redisCli *redisx.Client
	if deps.RedisSvc != nil {
		redisCli = deps.RedisSvc.Client()
	}
	payStatsCache := cache.NewPayStatsCache(redisCli)
	friendCache := cache.NewFriendCache(redisCli)
	iapLogic := logic.NewIAPLogic(deps.MongoSvc, deps.PlatformSvc, redisCli, payStatsCache)
	banLogic := logic.NewBanLogic(deps.MongoSvc, deps.TokenStore, deps.Hub)
	customConfigLogic := logic.NewCustomConfigLogic(deps.RedisSvc)
	userTagLogic := logic.NewUserTagLogic(deps.GdConfig, deps.MongoSvc, saveCacheReader, payStatsCache)
	roleLogic := logic.NewRoleLogic(deps.MongoSvc, saveCacheReader)
	friendLogic := logic.NewFriendLogic(deps.MongoSvc, saveCacheReader, friendCache)
	playerLogic := logic.NewPlayerLogic(deps.MongoSvc, saveCacheReader)

	return []httpx.Router{
		NewAuthHandler(authLogic),
		NewSaveHandler(saveLogic, deps),
		NewMailHandler(mailLogic, deps),
		NewIAPHandler(iapLogic, userTagLogic, deps),
		NewTimeHandler(),
		NewBanHandler(banLogic, deps),
		NewCustomConfigHandler(customConfigLogic),
		NewRoleHandler(roleLogic, deps),
		NewFriendHandler(friendLogic),
		NewPlayerHandler(playerLogic),
	}
}
