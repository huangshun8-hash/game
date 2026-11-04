package handler

import (
	"kemiluo/pkg/httpx"
	"kemiluo/services/gmt/internal/logic"
)

// NewRouters 返回 GMT 服务的所有路由处理器
// 通过 Deps 容器统一注入依赖，后续新增 handler 只需在此追加
func NewRouters(deps *Deps) []httpx.Router {
	authLogic := logic.NewAuthLogic(deps.MongoSvc, deps.JWT, deps.AsyncSvc, deps.TokenStore, deps.RedisSvc)
	profileLogic := logic.NewProfileLogic(deps.MongoSvc, deps.AsyncSvc, deps.TokenStore)
	adminLogic := logic.NewAdminLogic(deps.MongoSvc, deps.AsyncSvc, deps.TokenStore)
	operationLogLogic := logic.NewOperationLogLogic(deps.MongoSvc)
	iapLogic := logic.NewIAPLogic(deps.PlatformSvc, deps.GameLogicSvc)
	configLogic := logic.NewConfigLogic(deps.ConfigMongoSvc, deps.RedisSvc)
	customConfigLogic := logic.NewCustomConfigLogic(deps.MongoSvc, deps.RedisSvc, deps.AsyncSvc)
	banLogic := logic.NewBanLogic(deps.GameLogicSvc, deps.MongoSvc, deps.AsyncSvc)
	protoLogic := logic.NewProtoLogic(deps.MongoSvc, deps.AsyncSvc, deps.GameLogicSvc)
	mailLogic := logic.NewMailLogic(deps.GameLogicSvc, deps.MongoSvc, deps.AsyncSvc)
	whitelistLogic := logic.NewWhitelistLogic(deps.PlatformSvc, deps.MongoSvc, deps.AsyncSvc)
	articleLogic := logic.NewArticleLogic(deps.MongoSvc, deps.AsyncSvc)

	return []httpx.Router{
		NewAuthHandler(authLogic),
		NewProfileHandler(profileLogic),
		NewAdminHandler(adminLogic),
		NewOperationLogHandler(operationLogLogic),
		NewIAPHandler(iapLogic),
		NewConfigHandler(configLogic),
		NewCustomConfigHandler(customConfigLogic),
		NewBanHandler(banLogic),
		NewProtoHandler(protoLogic),
		NewMailHandler(mailLogic),
		NewWhitelistHandler(whitelistLogic),
		NewArticleHandler(articleLogic),
	}
}
