package repo

import (
	"context"

	"kemiluo/pkg/mongox"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EnsureIndexes 确保 GMT 服务（gmt_db）所需的数据库索引存在
func EnsureIndexes(ctx context.Context, client *mongox.Client) error {
	return client.EnsureIndexes(ctx, []mongox.IndexDef{
		// admins集合：username唯一索引，防止账号重复创建
		{
			Collection: collectionAdmins,
			Keys:       bson.D{{Key: "username", Value: 1}},
			Unique:     true,
			Name:       "ukUsername",
		},
		// admins集合：status普通索引，用于查询在线/禁用账号
		{
			Collection: collectionAdmins,
			Keys:       bson.D{{Key: "status", Value: 1}},
			Name:       "idxStatus",
		},
		// operation_logs集合：uid普通索引，用于按管理员查询日志
		{
			Collection: collectionOperationLogs,
			Keys:       bson.D{{Key: "uid", Value: 1}},
			Name:       "idxUid",
		},
		// operation_logs集合：createdAt普通索引，用于时间范围查询和分页排序
		{
			Collection: collectionOperationLogs,
			Keys:       bson.D{{Key: "createdAt", Value: -1}},
			Name:       "idxCreatedAt",
		},
		// operation_logs集合：(module, action) 复合索引，用于按模块和动作过滤
		{
			Collection: collectionOperationLogs,
			Keys:       bson.D{{Key: "module", Value: 1}, {Key: "action", Value: 1}},
			Name:       "idxModuleAction",
		},
	})
}

// EnsureArticleIndexes 确保文章库（test）所需的数据库索引存在。
// 建索引时 MongoDB 会一并创建 test 库和 article 集合。
func EnsureArticleIndexes(ctx context.Context, client *mongox.Client) error {
	return mongox.EnsureIndexesOnDB(ctx, client.DB(databaseTest), []mongox.IndexDef{
		{
			Collection: collectionArticle,
			Keys:       bson.D{{Key: "id", Value: 1}},
			Unique:     true,
			Name:       "ukId",
		},
		{
			Collection: collectionArticle,
			Keys:       bson.D{{Key: "createdAt", Value: -1}},
			Name:       "idxCreatedAt",
		},
	})
}

// EnsureConfigIndexes 确保游戏配置库（gconfig_db）所需的数据库索引存在
func EnsureConfigIndexes(ctx context.Context, configClient *mongox.Client) error {
	return configClient.EnsureIndexes(ctx, []mongox.IndexDef{
		// config_releases集合：status索引，用于按状态查询发布任务
		{
			Collection: collectionConfigReleases,
			Keys:       bson.D{{Key: "status", Value: 1}},
			Name:       "idxStatus",
		},
		// config_releases集合：activeFlag部分唯一索引，保证同一时刻最多只有一个进行中的发布任务。
		// 仅对 activeFlag=true 的文档建索引，终态（published/closed）任务会清除 activeFlag 从而不受约束。
		{
			Collection:              collectionConfigReleases,
			Keys:                    bson.D{{Key: "activeFlag", Value: 1}},
			Unique:                  true,
			Name:                    "ukActiveFlag",
			PartialFilterExpression: bson.D{{Key: "activeFlag", Value: true}},
		},
		// config_releases集合：createdAt索引，用于列表分页排序
		// 注：_id上已存在默认索引，MongoDB不允许对_id字段重复创建自定义索引
		{
			Collection: collectionConfigReleases,
			Keys:       bson.D{{Key: "createdAt", Value: -1}},
			Name:       "idxCreatedAt",
		},
	})
}
