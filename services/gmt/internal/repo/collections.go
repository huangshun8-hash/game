package repo

// GMT服务MongoDB集合名称常量
const (
	// collectionAdmins 管理员账号集合（gmt_db）
	collectionAdmins = "admins"
	// collectionOperationLogs 操作日志集合（gmt_db）
	collectionOperationLogs = "operation_logs"

	// 以下集合属于 gmt_db，但同时被 gamelogic 读取（通过 Redis 缓存优先）

	// collectionCustomConfigs 自定义配置集合，GMT后台配置的JSON数据原样存储（gmt_db）
	collectionCustomConfigs = "custom_configs"

	// 以下集合属于游戏配置专用库（gconfig_db），通过 ConfigMongoSvc 客户端访问

	// collectionConfigSchema 配置表结构定义集合（gconfig_db）
	collectionConfigSchema = "config_schema"
	// collectionConfigManifest 配置清单集合，记录每张表当前线上状态（gconfig_db）
	collectionConfigManifest = "config_manifest"
	// collectionConfigReleases 配置发布任务集合（gconfig_db）
	collectionConfigReleases = "config_releases"

	// databaseTest 文章业务库。与 gmt_db 共用同一 MongoDB 实例，仅库名不同。
	databaseTest = "test"
	// collectionArticle 文章集合（test）
	collectionArticle = "article"
	// collectionArticleCounters 文章自增 ID 计数器集合（test）
	collectionArticleCounters = "counters"
)
