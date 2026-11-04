package repo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ArticleRepo 文章数据访问层，读写 test 库的 article 集合
type ArticleRepo struct {
	coll *mongo.Collection
}

// NewArticleRepo 创建文章仓库。client 与 GMT 主库共用连接，仅库名切换为 test。
func NewArticleRepo(client *mongox.Client) *ArticleRepo {
	return &ArticleRepo{coll: client.DB(databaseTest).Collection(collectionArticle)}
}

// InsertArticle 写入文章。ID 由 counters 集合原子自增，创建时间取当前毫秒时间戳。
func (r *ArticleRepo) InsertArticle(ctx context.Context, article *model.Article) error {
	id, err := r.nextID(ctx)
	if err != nil {
		return err
	}
	article.ID = id
	article.CreatedAt = time.Now().UnixMilli()
	if _, err = r.coll.InsertOne(ctx, article); err != nil {
		return fmt.Errorf("写入文章失败: %w", err)
	}
	return nil
}

// FindByArticleID 按业务 ID 查询文章
func (r *ArticleRepo) FindByArticleID(ctx context.Context, id int64) (*model.Article, error) {
	article, err := mongox.FindOne[model.Article](ctx, r.coll, bson.D{{Key: "id", Value: id}})
	if err != nil {
		return nil, err
	}
	return &article, nil
}

// UpdateArticle 更新标题、正文和状态，不改创建时间
func (r *ArticleRepo) UpdateArticle(ctx context.Context, article *model.Article) error {
	result, err := r.coll.UpdateOne(ctx, bson.D{{Key: "id", Value: article.ID}}, bson.D{{
		Key: "$set",
		Value: bson.D{
			{Key: "title", Value: article.Title},
			{Key: "content", Value: article.Content},
			{Key: "status", Value: article.Status},
		},
	}})
	if err != nil {
		return fmt.Errorf("更新文章失败: %w", err)
	}
	if result.MatchedCount == 0 {
		return model.ErrArticleNotFound
	}
	return nil
}

// DeleteByArticleID 按业务 ID 物理删除文章
func (r *ArticleRepo) DeleteByArticleID(ctx context.Context, id int64) error {
	deleted, err := mongox.DeleteOne(ctx, r.coll, bson.D{{Key: "id", Value: id}})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return model.ErrArticleNotFound
	}
	return nil
}

// ListArticles 按创建时间倒序分页查询。title 非空时对标题做不区分大小写的子串匹配。
func (r *ArticleRepo) ListArticles(ctx context.Context, page, pageSize int64, title string) (*mongox.PageResult[model.Article], error) {
	filter := bson.D{}
	if title != "" {
		// QuoteMeta 把用户输入当字面量，避免正则特殊字符改变匹配语义
		filter = bson.D{{Key: "title", Value: bson.D{
			{Key: "$regex", Value: regexp.QuoteMeta(title)},
			{Key: "$options", Value: "i"},
		}}}
	}
	return mongox.Paginate[model.Article](ctx, r.coll, filter, mongox.PageQuery{
		Page:     page,
		PageSize: pageSize,
		Sort: bson.D{
			{Key: "createdAt", Value: -1},
			{Key: "id", Value: -1},
		},
	})
}

// nextID 从 test.counters 原子领取下一个文章 ID。计数器不存在时从 1 开始。
func (r *ArticleRepo) nextID(ctx context.Context) (int64, error) {
	coll := r.coll.Database().Collection(collectionArticleCounters)
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var result struct {
		Seq int64 `bson:"seq"`
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		err = coll.FindOneAndUpdate(ctx,
			bson.D{{Key: "_id", Value: "article"}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: int64(1)}}}},
			opts,
		).Decode(&result)
		if err == nil || !mongox.IsDuplicateKeyError(err) {
			break
		}
	}
	if err != nil {
		return 0, fmt.Errorf("生成文章ID失败: %w", err)
	}
	if result.Seq <= 0 {
		return 0, errors.New("生成文章ID失败")
	}
	return result.Seq, nil
}
