package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/platform/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// PayWhitelistRepo 充值白名单数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type PayWhitelistRepo struct {
	mongox.BaseRepo[model.PayWhitelist]
}

// NewPayWhitelistRepo 创建充值白名单仓库实例
func NewPayWhitelistRepo(client *mongox.Client) *PayWhitelistRepo {
	return &PayWhitelistRepo{BaseRepo: mongox.NewBaseRepo[model.PayWhitelist](client, collectionPayWhitelist)}
}

// FindByOpenID 根据 openId 查找白名单记录
// 空 openId 时直接返回 ErrNotFound，避免空值查询导致意外匹配
func (r *PayWhitelistRepo) FindByOpenID(ctx context.Context, openID string) (*model.PayWhitelist, error) {
	if openID == "" {
		return nil, mongox.ErrNotFound
	}
	return r.FindOne(ctx, bson.D{{Key: "openId", Value: openID}})
}

// Insert 新增白名单记录，自动填充创建时间
// openId 唯一键冲突时返回重复键错误，由调用方按"已存在"处理
func (r *PayWhitelistRepo) Insert(ctx context.Context, w *model.PayWhitelist) error {
	w.CreatedAt = time.Now()
	id, err := r.BaseRepo.Insert(ctx, w)
	if err != nil {
		return err
	}
	w.ID = id
	return nil
}

// IsWhitelisted 判断 openId 是否在白名单中
// 返回 true 表示该账号处于白名单，下单时应收款自动完成
func (r *PayWhitelistRepo) IsWhitelisted(ctx context.Context, openID string) (bool, error) {
	w, err := r.FindByOpenID(ctx, openID)
	if err != nil {
		if err == mongox.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return w != nil, nil
}

// DeleteByOpenID 物理删除 openId 对应的白名单记录（DeleteOne）
// 返回是否删除到记录；未删除到（不存在）也视为幂等成功。
func (r *PayWhitelistRepo) DeleteByOpenID(ctx context.Context, openID string) (bool, error) {
	if openID == "" {
		return false, nil
	}
	count, err := r.DeleteOne(ctx, bson.D{{Key: "openId", Value: openID}})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// PageQuery 按 openId 精确过滤（可选）并分页查询，createdAt 倒序
func (r *PayWhitelistRepo) PageQuery(ctx context.Context, openID string, page, pageSize int64) (*mongox.PageResult[model.PayWhitelist], error) {
	filter := bson.D{}
	if openID != "" {
		filter = append(filter, bson.E{Key: "openId", Value: openID})
	}
	return mongox.Paginate[model.PayWhitelist](ctx, r.Collection(), filter, mongox.PageQuery{
		Page:     page,
		PageSize: pageSize,
		Sort:     bson.D{{Key: "createdAt", Value: -1}},
	})
}
