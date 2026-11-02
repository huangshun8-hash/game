package mongox

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// PageResult holds paginated query results.
// T is the document type.
type PageResult[T any] struct {
	// Items is the list of documents for the current page.
	Items []T
	// Total is the total count of documents matching the filter.
	Total int64
	// Page is the current page number (1-based).
	Page int64
	// PageSize is the number of documents per page.
	PageSize int64
	// TotalPages is the total number of pages.
	TotalPages int64
}

// PageQuery holds the pagination parameters for a query.
type PageQuery struct {
	// Page is the current page number (1-based). Default: 1.
	Page int64
	// PageSize is the number of documents per page. Default: 20, Max: 200.
	PageSize int64
	// Sort specifies the sort order (e.g., bson.D{{Key: "createdAt", Value: -1}}).
	Sort any
}

// normalize validates and adjusts page parameters to safe defaults.
func (p *PageQuery) normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 200 {
		p.PageSize = 200
	}
}

// Paginate performs a paginated query on the given collection.
// filter is the MongoDB query filter (use bson.D{} or bson.M{} for all documents).
// query holds pagination parameters.
// Returns a PageResult containing the documents and pagination metadata.
func Paginate[T any](ctx context.Context, coll *mongo.Collection, filter any, query PageQuery) (*PageResult[T], error) {
	query.normalize()

	// Count total documents matching the filter
	total, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("mongox: count documents failed: %w", err)
	}

	// Calculate total pages
	totalPages := total / query.PageSize
	if total%query.PageSize > 0 {
		totalPages++
	}

	// Calculate skip
	skip := (query.Page - 1) * query.PageSize

	// Build find options
	findOpts := options.Find().
		SetSkip(skip).
		SetLimit(query.PageSize)

	if query.Sort != nil {
		findOpts.SetSort(query.Sort)
	}

	cursor, err := coll.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongox: find failed: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]T, 0, query.PageSize)
	if err = cursor.All(ctx, &items); err != nil {
		return nil, fmt.Errorf("mongox: decode results failed: %w", err)
	}

	return &PageResult[T]{
		Items:      items,
		Total:      total,
		Page:       query.Page,
		PageSize:   query.PageSize,
		TotalPages: totalPages,
	}, nil
}

// CursorPageQuery holds cursor-based pagination parameters for large datasets.
type CursorPageQuery struct {
	// Cursor is the last seen document ID (empty string for the first page).
	Cursor string
	// Limit is the number of documents per page. Default: 20, Max: 200.
	Limit int64
	// Sort specifies the sort order. Default: {"_id": 1} (ascending).
	Sort any
}

// CursorPageResult holds cursor-based pagination results.
type CursorPageResult[T any] struct {
	// Items is the list of documents for the current page.
	Items []T
	// NextCursor is the cursor to use for the next page (empty if no more results).
	NextCursor string
	// HasMore indicates if there are more documents after this page.
	HasMore bool
}

// PaginateCursor performs cursor-based pagination on the given collection.
// This is more efficient than skip-based pagination for large datasets.
// filter is the base query filter; the cursor condition is appended automatically.
func PaginateCursor[T any](ctx context.Context, coll *mongo.Collection, filter bson.D, query CursorPageQuery) (*CursorPageResult[T], error) {
	if query.Limit < 1 {
		query.Limit = 20
	}
	if query.Limit > 200 {
		query.Limit = 200
	}

	// Append cursor condition to the filter
	if query.Cursor != "" {
		oid, err := bson.ObjectIDFromHex(query.Cursor)
		if err != nil {
			return nil, fmt.Errorf("mongox: invalid cursor: %w", err)
		}
		filter = append(filter, bson.E{Key: "_id", Value: bson.D{{Key: "$gt", Value: oid}}})
	}

	sort := query.Sort
	if sort == nil {
		sort = bson.D{{Key: "_id", Value: 1}}
	} else {
		// 游标过滤条件固定为 _id > cursor，因此仅支持按 _id 排序；
		// 若按其他字段（如 createdAt）排序，过滤条件与排序字段不一致会导致分页错乱、重复或漏数据
		raw, marshalErr := bson.Marshal(sort)
		if marshalErr != nil {
			return nil, fmt.Errorf("mongox: invalid sort: %w", marshalErr)
		}
		var sortDoc bson.D
		if unmarshalErr := bson.Unmarshal(raw, &sortDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("mongox: invalid sort: %w", unmarshalErr)
		}
		if len(sortDoc) != 1 || sortDoc[0].Key != "_id" {
			return nil, fmt.Errorf("mongox: cursor pagination only supports _id sort")
		}
		// 归一化后整数可能为 int32/int64（JSON 反序列化来源可能为 float64），统一判断是否为升序 1；
		// 降序（-1）与游标过滤 _id > cursor 的方向相反，会导致分页错乱、重复或漏数据
		var ascending bool
		switch v := sortDoc[0].Value.(type) {
		case int32:
			ascending = v == 1
		case int64:
			ascending = v == 1
		case float64:
			ascending = v == 1
		}
		if !ascending {
			return nil, fmt.Errorf("mongox: cursor pagination only supports ascending _id sort")
		}
	}

	// Fetch one extra to detect if there are more results
	findOpts := options.Find().
		SetLimit(query.Limit + 1).
		SetSort(sort)

	cursor, err := coll.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("mongox: cursor find failed: %w", err)
	}
	defer cursor.Close(ctx)

	items := make([]T, 0, query.Limit+1)
	if err = cursor.All(ctx, &items); err != nil {
		return nil, fmt.Errorf("mongox: decode cursor results failed: %w", err)
	}

	hasMore := int64(len(items)) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}

	var nextCursor string
	if hasMore && len(items) > 0 {
		// Extract _id from the last item for the next cursor
		lastDoc, marshalErr := bson.Marshal(items[len(items)-1])
		if marshalErr == nil {
			var raw bson.D
			if unmarshalErr := bson.Unmarshal(lastDoc, &raw); unmarshalErr == nil {
				for _, elem := range raw {
					if elem.Key == "_id" {
						if oid, ok := elem.Value.(bson.ObjectID); ok {
							nextCursor = oid.Hex()
						}
						break
					}
				}
			}
		}
	}

	return &CursorPageResult[T]{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}
