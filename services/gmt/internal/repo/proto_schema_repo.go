package repo

import (
	"context"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// collectionProtoSchema Proto模板集合名称（gmt_db）
const collectionProtoSchema = "proto_schema"

// ProtoSchemaRepo Proto模板数据访问层
type ProtoSchemaRepo struct {
	coll *mongo.Collection
}

// NewProtoSchemaRepo 创建Proto模板仓库实例
func NewProtoSchemaRepo(client *mongox.Client) *ProtoSchemaRepo {
	return &ProtoSchemaRepo{coll: client.Collection(collectionProtoSchema)}
}

// Upsert 插入或覆盖更新Proto模板（按protoName唯一标识）
func (r *ProtoSchemaRepo) Upsert(ctx context.Context, doc *model.ProtoSchema) error {
	filter := bson.D{{Key: "_id", Value: doc.ProtoName}}
	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "content", Value: doc.Content},
			{Key: "messageList", Value: doc.MessageList},
			{Key: "md5", Value: doc.MD5},
			{Key: "updateTime", Value: doc.UpdateTime},
		}},
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "createTime", Value: doc.CreateTime},
		}},
	}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := r.coll.UpdateOne(ctx, filter, update, opts)
	return err
}

// FindByName 根据protoName查找Proto模板
func (r *ProtoSchemaRepo) FindByName(ctx context.Context, protoName string) (*model.ProtoSchema, error) {
	var result model.ProtoSchema
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: protoName}}).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// FindLatest 查询最新的一条Proto模板（全局唯一场景，按更新时间倒序取第一条）
func (r *ProtoSchemaRepo) FindLatest(ctx context.Context) (*model.ProtoSchema, error) {
	opts := options.FindOne().SetSort(bson.D{{Key: "updateTime", Value: -1}})
	var result model.ProtoSchema
	err := r.coll.FindOne(ctx, bson.D{}, opts).Decode(&result)
	if err == mongo.ErrNoDocuments {
		return nil, mongox.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}
