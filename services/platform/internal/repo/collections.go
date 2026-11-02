package repo

// 平台服务集合名称常量
const (
	// collectionAccounts 账号集合
	collectionAccounts = "accounts"
	// collectionCounters 自增序列计数器集合
	collectionCounters = "counters"
	// collectionPaymentOrders 平台支付主表
	collectionPaymentOrders = "payment_orders"
	// collectionPayWhitelist 充值白名单集合（按账号 openId 维度存取，仅记录 openId 与创建时间）
	collectionPayWhitelist = "pay_whitelist"
)
