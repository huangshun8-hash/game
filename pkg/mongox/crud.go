package mongox

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrNotFound is returned when a document is not found.
var ErrNotFound = errors.New("mongox: document not found")

// FindOne finds a single document matching the filter and decodes it into T.
// Returns ErrNotFound if no document matches.
func FindOne[T any](ctx context.Context, coll *mongo.Collection, filter any, opts ...options.Lister[options.FindOneOptions]) (T, error) {
	var result T
	err := coll.FindOne(ctx, filter, opts...).Decode(&result)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return result, ErrNotFound
		}
		return result, fmt.Errorf("mongox: find one failed: %w", err)
	}
	return result, nil
}

// FindMany finds all documents matching the filter and returns them as a slice of T.
func FindMany[T any](ctx context.Context, coll *mongo.Collection, filter any, opts ...options.Lister[options.FindOptions]) ([]T, error) {
	cursor, err := coll.Find(ctx, filter, opts...)
	if err != nil {
		return nil, fmt.Errorf("mongox: find many failed: %w", err)
	}
	defer cursor.Close(ctx)

	var results []T
	if err = cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("mongox: decode many failed: %w", err)
	}
	return results, nil
}

// FindByID finds a single document by its ObjectID.
// Returns ErrNotFound if no document matches.
func FindByID[T any](ctx context.Context, coll *mongo.Collection, id bson.ObjectID) (T, error) {
	return FindOne[T](ctx, coll, bson.D{{Key: "_id", Value: id}})
}

// InsertOne inserts a single document and returns the inserted ID.
func InsertOne(ctx context.Context, coll *mongo.Collection, doc any) (bson.ObjectID, error) {
	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return bson.ObjectID{}, fmt.Errorf("mongox: insert one failed: %w", err)
	}
	oid, ok := result.InsertedID.(bson.ObjectID)
	if !ok {
		return bson.ObjectID{}, fmt.Errorf("mongox: unexpected inserted ID type")
	}
	return oid, nil
}

// InsertMany inserts multiple documents and returns the inserted IDs.
func InsertMany(ctx context.Context, coll *mongo.Collection, docs []any) ([]bson.ObjectID, error) {
	result, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("mongox: insert many failed: %w", err)
	}
	ids := make([]bson.ObjectID, 0, len(result.InsertedIDs))
	for _, id := range result.InsertedIDs {
		oid, ok := id.(bson.ObjectID)
		if !ok {
			continue
		}
		ids = append(ids, oid)
	}
	return ids, nil
}

// UpdateOne updates a single document matching the filter.
// update should be a valid MongoDB update document (e.g., bson.D{{"$set", bson.D{...}}}).
// Returns the number of documents modified.
func UpdateOne(ctx context.Context, coll *mongo.Collection, filter any, update any, opts ...options.Lister[options.UpdateOneOptions]) (int64, error) {
	result, err := coll.UpdateOne(ctx, filter, update, opts...)
	if err != nil {
		return 0, fmt.Errorf("mongox: update one failed: %w", err)
	}
	return result.ModifiedCount, nil
}

// UpdateMany updates all documents matching the filter.
// Returns the number of documents modified.
func UpdateMany(ctx context.Context, coll *mongo.Collection, filter any, update any, opts ...options.Lister[options.UpdateManyOptions]) (int64, error) {
	result, err := coll.UpdateMany(ctx, filter, update, opts...)
	if err != nil {
		return 0, fmt.Errorf("mongox: update many failed: %w", err)
	}
	return result.ModifiedCount, nil
}

// UpdateByID updates a single document by its ObjectID.
// Returns the number of documents modified.
func UpdateByID(ctx context.Context, coll *mongo.Collection, id bson.ObjectID, update any) (int64, error) {
	return UpdateOne(ctx, coll, bson.D{{Key: "_id", Value: id}}, update)
}

// UpsertOne performs an upsert operation: inserts the document if no match is found, otherwise updates it.
// Returns the upserted ID (if a new document was created) and the modified count.
func UpsertOne(ctx context.Context, coll *mongo.Collection, filter any, update any) (*mongo.UpdateResult, error) {
	opts := options.UpdateOne().SetUpsert(true)
	result, err := coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return nil, fmt.Errorf("mongox: upsert one failed: %w", err)
	}
	return result, nil
}

// DeleteOne deletes a single document matching the filter.
// Returns the number of documents deleted.
func DeleteOne(ctx context.Context, coll *mongo.Collection, filter any) (int64, error) {
	result, err := coll.DeleteOne(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongox: delete one failed: %w", err)
	}
	return result.DeletedCount, nil
}

// DeleteMany deletes all documents matching the filter.
// Returns the number of documents deleted.
func DeleteMany(ctx context.Context, coll *mongo.Collection, filter any) (int64, error) {
	result, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongox: delete many failed: %w", err)
	}
	return result.DeletedCount, nil
}

// DeleteByID deletes a single document by its ObjectID.
// Returns the number of documents deleted.
func DeleteByID(ctx context.Context, coll *mongo.Collection, id bson.ObjectID) (int64, error) {
	return DeleteOne(ctx, coll, bson.D{{Key: "_id", Value: id}})
}

// Count returns the number of documents matching the filter.
func Count(ctx context.Context, coll *mongo.Collection, filter any) (int64, error) {
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongox: count failed: %w", err)
	}
	return count, nil
}

// Exists checks if at least one document matching the filter exists.
func Exists(ctx context.Context, coll *mongo.Collection, filter any) (bool, error) {
	count, err := coll.CountDocuments(ctx, filter, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("mongox: exists check failed: %w", err)
	}
	return count > 0, nil
}

// IsDuplicateKeyError 判断错误是否由 MongoDB 重复键引起。
// 直接复用官方 driver 的 mongo.IsDuplicateKeyError：其内部通过 ServerError 接口
// 统一识别 WriteException、BulkWriteException（InsertMany）、CommandError（事务内）
// 等多种错误类型，并覆盖重复键相关错误码（11000/11001/12582/16460+E11000）。
// 注意：此函数只要错误链中存在任意重复键错误即返回 true，适用于单文档写入的幂等判断。
func IsDuplicateKeyError(err error) bool {
	return mongo.IsDuplicateKeyError(err)
}

// IsDuplicateKeyOnlyError 判断错误是否"仅"由重复键引起（不含其他类型错误）。
// 用于 ordered=false 批量写入的幂等场景：当批次中所有写错误均为重复键错误时，
// 可安全视为并发幂等而吞掉；若批次中混有非重复键错误（如校验失败、写关注错误），
// 或错误根本不是服务器错误（网络/编解码等），则必须上报，防止部分文档静默丢失。
func IsDuplicateKeyOnlyError(err error) bool {
	if err == nil {
		return false
	}
	var se mongo.ServerError
	if !errors.As(err, &se) {
		// 非服务器错误（网络/编解码等），无法确认是重复键，需上报
		return false
	}
	codes := se.ErrorCodes()
	if len(codes) == 0 {
		return false
	}
	for _, code := range codes {
		switch code {
		case 11000, 11001, 12582:
			// 重复键错误码
		case 16460:
			// mongos 代理层重复键错误码，需携带 "E11000" 消息才能确认
			if !se.HasErrorCodeWithMessage(16460, " E11000 ") {
				return false
			}
		default:
			return false
		}
	}
	return true
}
