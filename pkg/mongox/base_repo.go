package mongox

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// BaseRepo 泛型基础仓库，提供通用的 CRUD 操作
// 业务 Repo 通过嵌入此结构体获得基础能力，仅需实现业务特有的查询方法
//
// 用法示例：
//
//	type UserRepo struct {
//	    mongox.BaseRepo[model.User]
//	}
//
//	func NewUserRepo(client *Client) *UserRepo {
//	    return &UserRepo{BaseRepo: mongox.NewBaseRepo[model.User](client, "users")}
//	}
type BaseRepo[T any] struct {
	coll *mongo.Collection
}

// NewBaseRepo 创建泛型基础仓库实例
// client: mongox.Client 实例
// collection: 集合名称
func NewBaseRepo[T any](client *Client, collection string) BaseRepo[T] {
	return BaseRepo[T]{coll: client.Collection(collection)}
}

// Collection 返回底层的 mongo.Collection，供业务 Repo 使用自定义查询
func (r *BaseRepo[T]) Collection() *mongo.Collection {
	return r.coll
}

// Insert 插入单个文档，返回插入后的 ObjectID
func (r *BaseRepo[T]) Insert(ctx context.Context, doc *T) (bson.ObjectID, error) {
	return InsertOne(ctx, r.coll, doc)
}

// FindByID 根据 ObjectID 查找单个文档
func (r *BaseRepo[T]) FindByID(ctx context.Context, id bson.ObjectID) (*T, error) {
	result, err := FindByID[T](ctx, r.coll, id)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// FindOne 根据 filter 查找单个文档
func (r *BaseRepo[T]) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) (*T, error) {
	result, err := FindOne[T](ctx, r.coll, filter, opts...)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// FindMany 根据 filter 查找多个文档
func (r *BaseRepo[T]) FindMany(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	return FindMany[T](ctx, r.coll, filter, opts...)
}

// UpdateOne 更新单个文档
// filter: 匹配条件
// update: 更新操作（如 bson.D{{"$set", bson.D{...}}}）
func (r *BaseRepo[T]) UpdateOne(ctx context.Context, filter any, update any, opts ...options.Lister[options.UpdateOneOptions]) (int64, error) {
	return UpdateOne(ctx, r.coll, filter, update, opts...)
}

// UpdateByID 根据 ObjectID 更新单个文档
func (r *BaseRepo[T]) UpdateByID(ctx context.Context, id bson.ObjectID, update any) (int64, error) {
	return UpdateByID(ctx, r.coll, id, update)
}

// DeleteOne 删除单个文档
func (r *BaseRepo[T]) DeleteOne(ctx context.Context, filter any) (int64, error) {
	return DeleteOne(ctx, r.coll, filter)
}

// DeleteByID 根据 ObjectID 删除单个文档
func (r *BaseRepo[T]) DeleteByID(ctx context.Context, id bson.ObjectID) (int64, error) {
	return DeleteByID(ctx, r.coll, id)
}

// DeleteMany 删除匹配的多个文档
func (r *BaseRepo[T]) DeleteMany(ctx context.Context, filter any) (int64, error) {
	return DeleteMany(ctx, r.coll, filter)
}

// Count 统计匹配的文档数量
func (r *BaseRepo[T]) Count(ctx context.Context, filter any) (int64, error) {
	return Count(ctx, r.coll, filter)
}

// Exists 判断是否存在匹配的文档
func (r *BaseRepo[T]) Exists(ctx context.Context, filter any) (bool, error) {
	return Exists(ctx, r.coll, filter)
}

// Paginate 分页查询
func (r *BaseRepo[T]) Paginate(ctx context.Context, filter any, query PageQuery) (*PageResult[T], error) {
	return Paginate[T](ctx, r.coll, filter, query)
}
