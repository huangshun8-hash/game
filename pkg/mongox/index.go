package mongox

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// IndexDef defines a single index to be ensured on a collection.
type IndexDef struct {
	// Collection is the name of the target collection.
	Collection string
	// Keys specifies the index fields and order (e.g., bson.D{{"userId", 1}, {"createdAt", -1}}).
	Keys any
	// Unique indicates whether this is a unique index.
	Unique bool
	// Name is an optional custom name for the index.
	Name string
	// Background indicates if the index should be built in the background (ignored in MongoDB 4.2+).
	Background bool
	// ExpireAfterSeconds 启用 TTL 索引。值为 0 表示按文档中字段的时间精确过期。
	// 使用 IsTTL=true 配合 ExpireAfterSeconds=0 可创建精确过期的 TTL 索引。
	ExpireAfterSeconds int32
	// IsTTL 标记此索引是否为 TTL 索引。解决 ExpireAfterSeconds=0 时的歧义问题。
	IsTTL bool
	// Sparse indicates whether the index should be sparse.
	Sparse bool
	// PartialFilterExpression 指定部分索引的过滤条件，仅对满足条件的文档建立索引。
	PartialFilterExpression any
}

// EnsureIndexes creates or ensures the provided indexes exist on the specified collections.
// It uses the default database from the client.
// Existing indexes with the same key specification are skipped by the server.
func (c *Client) EnsureIndexes(ctx context.Context, indexes []IndexDef) error {
	return EnsureIndexesOnDB(ctx, c.db, indexes)
}

// EnsureIndexesOnDB creates or ensures the provided indexes exist on the specified database.
// This allows callers to target a non-default database.
func EnsureIndexesOnDB(ctx context.Context, db *mongo.Database, indexes []IndexDef) error {
	// Group indexes by collection for batch creation
	grouped := make(map[string][]mongo.IndexModel)
	for _, idx := range indexes {
		model := mongo.IndexModel{
			Keys: idx.Keys,
		}

		opts := options.Index()
		if idx.Unique {
			opts.SetUnique(true)
		}
		if idx.Name != "" {
			opts.SetName(idx.Name)
		}
		if idx.Sparse {
			opts.SetSparse(true)
		}
		if idx.PartialFilterExpression != nil {
			opts.SetPartialFilterExpression(idx.PartialFilterExpression)
		}
		if idx.IsTTL || idx.ExpireAfterSeconds > 0 {
			opts.SetExpireAfterSeconds(idx.ExpireAfterSeconds)
		}
		model.Options = opts

		grouped[idx.Collection] = append(grouped[idx.Collection], model)
	}

	for collName, models := range grouped {
		coll := db.Collection(collName)
		_, err := coll.Indexes().CreateMany(ctx, models)
		if err != nil {
			return fmt.Errorf("mongox: create indexes on %q failed: %w", collName, err)
		}
	}

	return nil
}
