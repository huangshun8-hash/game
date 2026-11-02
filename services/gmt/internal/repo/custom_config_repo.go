package repo

import (
	"context"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CustomConfigRepo 自定义配置数据访问层
type CustomConfigRepo struct {
	coll *mongo.Collection
}

// NewCustomConfigRepo 创建自定义配置仓库实例
func NewCustomConfigRepo(client *mongox.Client) *CustomConfigRepo {
	return &CustomConfigRepo{coll: client.Collection(collectionCustomConfigs)}
}

// Upsert 插入或更新自定义配置（全局仅一条，按固定 _id 覆盖写入）
func (r *CustomConfigRepo) Upsert(ctx context.Context, doc *model.CustomConfig) error {
	filter := bson.D{{Key: "_id", Value: model.CustomConfigID}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "data", Value: doc.Data},
			{Key: "operator", Value: doc.Operator},
			{Key: "updatedAt", Value: doc.UpdatedAt},
		}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "createdAt", Value: doc.CreatedAt},
		}},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := r.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// Find 查找全局唯一的自定义配置文档
func (r *CustomConfigRepo) Find(ctx context.Context) (*model.CustomConfig, error) {
	var result model.CustomConfig
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: model.CustomConfigID}}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	return &result, err
}
