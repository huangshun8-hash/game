package repo

import (
	"context"
	"strings"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/shared/configx"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// --- ConfigReleaseRepo ---

// ConfigReleaseRepo 配置发布任务数据访问层
type ConfigReleaseRepo struct {
	coll *mongo.Collection
}

// NewConfigReleaseRepo 创建发布任务仓库实例
func NewConfigReleaseRepo(client *mongox.Client) *ConfigReleaseRepo {
	return &ConfigReleaseRepo{coll: client.Collection(collectionConfigReleases)}
}

// Insert 插入新的发布任务文档
func (r *ConfigReleaseRepo) Insert(ctx context.Context, release *model.ConfigRelease) error {
	_, err := r.coll.InsertOne(ctx, release)
	return err
}

// FindByID 按任务ID查找发布任务
func (r *ConfigReleaseRepo) FindByID(ctx context.Context, id bson.ObjectID) (*model.ConfigRelease, error) {
	var result model.ConfigRelease
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	return &result, err
}

// UpdateTableDetail 上传成功后更新指定表的详情，同时追加事件流水，并更新任务状态。
// 若该表是首次上传，通过 $addToSet 自动将表名追加到 tables 数组（去重）。
func (r *ConfigReleaseRepo) UpdateTableDetail(
	ctx context.Context,
	id bson.ObjectID,
	detail model.ReleaseTableDetail,
	event model.ReleaseEvent,
	newStatus string,
	_ bool, // 保留参数，兼容调用方签名，验收流程已移除
) error {
	now := time.Now()
	detailKey := "tableDetails." + detail.Table
	filter := bson.D{{Key: "_id", Value: id}}
	setFields := bson.D{
		{Key: detailKey, Value: detail},
		{Key: "status", Value: newStatus},
		{Key: "updatedAt", Value: now},
	}
	update := bson.D{
		{Key: "$set", Value: setFields},
		{Key: "$push", Value: bson.D{
			{Key: "events", Value: event},
		}},
		// 将表名追加到 tables 数组，$addToSet 自动去重
		{Key: "$addToSet", Value: bson.D{
			{Key: "tables", Value: detail.Table},
		}},
	}
	result, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// UpdateStatus 更新任务状态，同时追加事件流水
// 用于 publishing / published / failed 等状态转换
func (r *ConfigReleaseRepo) UpdateStatus(
	ctx context.Context,
	id bson.ObjectID,
	status string,
	event model.ReleaseEvent,
	extra ...bson.E, // 可追加额外的 $set 字段（如 publishedBy / publishedAt / failReason）
) error {
	now := time.Now()
	setFields := bson.D{
		{Key: "status", Value: status},
		{Key: "updatedAt", Value: now},
	}
	// 进入终态（published / closed）时清除活动标记，释放"仅一个进行中任务"的占用
	if status == model.ReleaseStatusPublished || status == model.ReleaseStatusClosed {
		setFields = append(setFields, bson.E{Key: "activeFlag", Value: false})
	}
	setFields = append(setFields, extra...)
	filter := bson.D{{Key: "_id", Value: id}}
	update := bson.D{
		{Key: "$set", Value: setFields},
		{Key: "$push", Value: bson.D{{Key: "events", Value: event}}},
	}
	result, err := r.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if result.MatchedCount == 0 {
		return mongox.ErrNotFound
	}
	return nil
}

// ListPage 分页查询发布任务列表，按创建时间倒序
func (r *ConfigReleaseRepo) ListPage(ctx context.Context, page, pageSize int64) ([]model.ConfigRelease, int64, error) {
	total, err := r.coll.CountDocuments(ctx, bson.D{})
	if err != nil {
		return nil, 0, err
	}
	skip := (page - 1) * pageSize
	opts := options.Find().
		SetSort(bson.D{{Key: "_id", Value: -1}}).
		SetSkip(skip).
		SetLimit(pageSize)
	cursor, err := r.coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var results []model.ConfigRelease
	return results, total, cursor.All(ctx, &results)
}

// --- ConfigSchemaRepo ---

// ConfigSchemaRepo 配置表结构数据访问层
type ConfigSchemaRepo struct {
	coll *mongo.Collection
}

// NewConfigSchemaRepo 创建配置结构仓库实例
func NewConfigSchemaRepo(client *mongox.Client) *ConfigSchemaRepo {
	return &ConfigSchemaRepo{coll: client.Collection(collectionConfigSchema)}
}

// Upsert 插入或更新配置表结构
func (r *ConfigSchemaRepo) Upsert(ctx context.Context, schema *model.ConfigSchema) error {
	schema.UpdatedAt = time.Now()
	filter := bson.D{{Key: "_id", Value: schema.ID}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "version", Value: schema.Version},
		{Key: "schemaHash", Value: schema.SchemaHash},
		{Key: "fields", Value: schema.Fields},
		{Key: "updatedAt", Value: schema.UpdatedAt},
	}}}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

// FindByID 按表名查找配置结构
func (r *ConfigSchemaRepo) FindByID(ctx context.Context, tableName string) (*model.ConfigSchema, error) {
	var result model.ConfigSchema
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: tableName}}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	return &result, err
}

// FindAll 查询所有配置表结构定义
func (r *ConfigSchemaRepo) FindAll(ctx context.Context) ([]model.ConfigSchema, error) {
	cursor, err := r.coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var results []model.ConfigSchema
	return results, cursor.All(ctx, &results)
}

// --- ConfigManifestRepo ---

// ConfigManifestRepo 配置清单数据访问层
// 记录每张配置表当前线上（已发布）的版本状态，供 Uploader 工具做增量对比
type ConfigManifestRepo struct {
	coll *mongo.Collection
}

// NewConfigManifestRepo 创建配置清单仓库实例
func NewConfigManifestRepo(client *mongox.Client) *ConfigManifestRepo {
	return &ConfigManifestRepo{coll: client.Collection(collectionConfigManifest)}
}

// UpsertStaging 上传时更新该表的暂存区快照，供 Uploader 工具在下次运行时做增量对比。
// 仅更新暂存区字段，不影响已发布区快照；不存在时自动插入（upsert）。
func (r *ConfigManifestRepo) UpsertStaging(ctx context.Context, tableName string, releaseID bson.ObjectID, schemaHash, dataHash, author string, stagingAt time.Time) error {
	filter := bson.D{{Key: "_id", Value: tableName}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "stagingVersion", Value: releaseID},
		{Key: "stagingSchemaHash", Value: schemaHash},
		{Key: "stagingDataHash", Value: dataHash},
		{Key: "stagingAuthor", Value: author},
		{Key: "stagingAt", Value: stagingAt},
		{Key: "updatedAt", Value: stagingAt},
	}}}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

// UpsertPublished 发布时更新该表的发布区快照（同步更新所有字段）
// 由 logic.publishOneTable 调用，传入完整的 ConfigManifest 快照
func (r *ConfigManifestRepo) UpsertPublished(ctx context.Context, m *model.ConfigManifest) error {
	now := time.Now()
	if m.PublishedAt == nil {
		m.PublishedAt = &now
	}
	filter := bson.D{{Key: "_id", Value: m.ID}}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "stagingVersion", Value: m.StagingVersion},
		{Key: "stagingSchemaHash", Value: m.StagingSchemaHash},
		{Key: "stagingDataHash", Value: m.StagingDataHash},
		{Key: "stagingAuthor", Value: m.StagingAuthor},
		{Key: "stagingAt", Value: m.StagingAt},
		{Key: "publishedVersion", Value: m.PublishedVersion},
		{Key: "publishedSchemaHash", Value: m.PublishedSchemaHash},
		{Key: "publishedDataHash", Value: m.PublishedDataHash},
		{Key: "publishedAuthor", Value: m.PublishedAuthor},
		{Key: "publishedAt", Value: m.PublishedAt},
		{Key: "releaseId", Value: m.ReleaseID},
		{Key: "status", Value: model.ConfigStatusPublished},
		{Key: "updatedAt", Value: now},
	}}}
	_, err := r.coll.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

// ClearStagingByRelease 关闭任务时，清除属于指定 releaseID 的所有暂存区快照字段，
// 避免 Uploader 下次运行时误判 hash 匹配而跳过上传（暂存数据已被删除，但 hash 还残留）。
// 只清理 stagingVersion 与 releaseID 匹配的记录，已发布字段不受影响。
// 全部暂存字段统一用 $unset 清除，避免用零值 ObjectID 造成语义混淆。
func (r *ConfigManifestRepo) ClearStagingByRelease(ctx context.Context, releaseID bson.ObjectID) error {
	filter := bson.D{{Key: "stagingVersion", Value: releaseID}}
	update := bson.D{
		{Key: "$unset", Value: bson.D{
			{Key: "stagingVersion", Value: ""},
			{Key: "stagingSchemaHash", Value: ""},
			{Key: "stagingDataHash", Value: ""},
			{Key: "stagingAuthor", Value: ""},
			{Key: "stagingAt", Value: ""},
		}},
		{Key: "$set", Value: bson.D{
			{Key: "updatedAt", Value: time.Now()},
		}},
	}
	_, err := r.coll.UpdateMany(ctx, filter, update)
	return err
}

// FindByID 按表名查找配置清单，不存在时返回 mongox.ErrNotFound
func (r *ConfigManifestRepo) FindByID(ctx context.Context, tableName string) (*model.ConfigManifest, error) {
	var result model.ConfigManifest
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: tableName}}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	return &result, err
}

// FindAll 查询所有配置清单（供 Uploader 获取 manifest）
func (r *ConfigManifestRepo) FindAll(ctx context.Context) ([]model.ConfigManifest, error) {
	cursor, err := r.coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var results []model.ConfigManifest
	return results, cursor.All(ctx, &results)
}

// --- ConfigStagingRepo ---

// stagingDBName 返回暂存数据库名
func stagingDBName() string {
	return configx.StagingDB
}

// stagingCollName 返回暂存集合名，格式：<releaseIdHex>_<表名小写>。
// 集合名与发布任务 ID 绑定，使不同发布任务在同一个共享暂存库内彼此隔离，
// 即便存在并发任务也不会互相覆盖暂存数据。
func stagingCollName(releaseID bson.ObjectID, tableName string) string {
	return releaseID.Hex() + "_" + strings.ToLower(tableName)
}

// ConfigStagingRepo 暂存配置数据访问层
// 所有发布任务共用同一个暂存数据库（gconfig_staging），
// 库内集合名以 <releaseIdHex>_<表名> 与发布任务绑定，实现任务间的数据隔离。
type ConfigStagingRepo struct {
	coll *mongo.Collection
}

// NewConfigStagingRepo 创建指定发布任务、指定表名的暂存数据仓库实例
func NewConfigStagingRepo(client *mongox.Client, releaseID bson.ObjectID, tableName string) *ConfigStagingRepo {
	coll := client.DB(stagingDBName()).Collection(stagingCollName(releaseID, tableName))
	return &ConfigStagingRepo{coll: coll}
}

// DropStagingByRelease 删除指定发布任务在共享暂存库内的所有集合（集合名前缀为 <releaseIdHex>_）。
// 仅清理本任务的暂存数据，不影响其他并发任务，故无需 Drop 整个数据库。
// 遇到单个集合 Drop 失败时继续清理其余集合，返回第一个遇到的错误。
func DropStagingByRelease(ctx context.Context, client *mongox.Client, releaseID bson.ObjectID) error {
	db := client.DB(stagingDBName())
	prefix := releaseID.Hex() + "_"
	names, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return err
	}
	var firstErr error
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			if err := db.Collection(name).Drop(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// ReplaceAll 全量替换暂存数据
func (r *ConfigStagingRepo) ReplaceAll(ctx context.Context, docs []any) error {
	if _, err := r.coll.DeleteMany(ctx, bson.D{}); err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	_, err := r.coll.InsertMany(ctx, docs)
	return err
}

// FindAll 读取所有暂存数据（发布时用）
func (r *ConfigStagingRepo) FindAll(ctx context.Context) ([]map[string]any, error) {
	cursor, err := r.coll.Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var results []map[string]any
	return results, cursor.All(ctx, &results)
}

// --- ConfigDataRepo（生产集合）---

// ConfigDataRepo 生产配置数据访问层
// 只有发布操作才会写入，集合名 = 表名小写
type ConfigDataRepo struct {
	coll *mongo.Collection
}

// NewConfigDataRepo 创建指定表名的生产数据仓库实例
func NewConfigDataRepo(client *mongox.Client, tableName string) *ConfigDataRepo {
	return &ConfigDataRepo{coll: client.Collection(strings.ToLower(tableName))}
}

// ReplaceAll 全量替换生产数据（仅发布时调用）
func (r *ConfigDataRepo) ReplaceAll(ctx context.Context, docs []any) error {
	if _, err := r.coll.DeleteMany(ctx, bson.D{}); err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}
	_, err := r.coll.InsertMany(ctx, docs)
	return err
}
