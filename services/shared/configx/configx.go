// Package configx 提供游戏配置发布流程的跨服务共享常量与消息结构体。
// gmt 服务（发布方）与 gamelogic 服务（订阅方）通过此包共享频道名、
// 暂存库名及 Redis Pub/Sub 消息格式，确保双方保持严格一致。
package configx

// PublishChannel 配置正式发布 Redis Pub/Sub 频道名。
// gmt 在发布任务完成（PublishRelease 事务成功）后向此频道 Publish 消息；
// 各订阅方（gamelogic、platform 等）收到消息后从生产库全量热更配置内存。
const PublishChannel = "config:publish"

// StagingDB 暂存数据库名。
// 所有发布任务共用同一张暂存数据库，库内集合名以 <releaseIdHex>_<表名小写> 与发布任务绑定，
// 不同任务的暂存数据在集合层面彼此隔离，即便并发也不会互相覆盖。
const StagingDB = "gconfig_staging"

// PublishMessage 配置正式发布 Redis Pub/Sub 消息结构体（频道：config:publish）。
// gmt 在 PublishRelease 事务成功后发布，订阅方消费后全量热更配置内存。
type PublishMessage struct {
	// ReleaseID 发布任务ID（MongoDB ObjectID 十六进制字符串）
	ReleaseID string `json:"releaseId"`
	// Tables 本次发布的配置表名列表
	Tables []string `json:"tables"`
	// Operator 发起发布的管理员账号名
	Operator string `json:"operator"`
	// OccurredAt 消息发布时间（Unix 毫秒）
	OccurredAt int64 `json:"occurredAt"`
}
