package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/platform/internal/logic"
)

// NewRouters 返回平台服务的所有路由处理器
// 通过 Deps 容器统一注入依赖，后续新增 handler 只需在此追加即可
func NewRouters(deps *Deps) []httpx.Router {
	authLogic := logic.NewAuthLogic(deps.MongoSvc, deps.Config.ThirdPartyKey)
	iapLogic := logic.NewIAPLogic(deps.MongoSvc, deps.GamelogicSvc, deps.GdConfig, deps.Config.InternalSecret)
	gmtLogic := logic.NewGmtLogic(iapLogic)
	lunPlayLogic := logic.NewLunPlayLogic(deps.GamelogicSvc, deps.Config.InternalSecret, deps.Config.ThirdPartyKey)
	whitelistLogic := logic.NewWhitelistLogic(deps.MongoSvc)

	return []httpx.Router{
		NewAuthHandler(authLogic, lunPlayLogic, deps),
		NewIAPHandler(iapLogic, gmtLogic, deps),
		NewWhitelistHandler(whitelistLogic, deps),
	}
}
