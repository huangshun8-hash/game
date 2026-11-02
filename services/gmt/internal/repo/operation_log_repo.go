package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// OperationLogRepo 操作日志数据访问层
type OperationLogRepo struct {
	mongox.BaseRepo[model.OperationLog]
}

// NewOperationLogRepo 创建操作日志仓库实例
func NewOperationLogRepo(client *mongox.Client) *OperationLogRepo {
	return &OperationLogRepo{BaseRepo: mongox.NewBaseRepo[model.OperationLog](client, collectionOperationLogs)}
}

// InsertLog 插入一条操作日志，自动填充创建时间
func (r *OperationLogRepo) InsertLog(ctx context.Context, log *model.OperationLog) error {
	log.CreatedAt = time.Now()
	_, err := r.Insert(ctx, log)
	return err
}

// OperationLogFilter 操作日志查询过滤条件
// 所有字段均为可选，零值表示不过滤
type OperationLogFilter struct {
	// UID 按操作管理员ID过滤，空字符串表示不过滤
	UID string
	// Module 按操作模块过滤，空字符串表示不过滤
	Module string
	// Action 按操作动作过滤，空字符串表示不过滤
	Action string
	// StartTime 时间范围起点（含），零值表示不限制
	StartTime time.Time
	// EndTime 时间范围终点（含），零值表示不限制
	EndTime time.Time
}

// buildFilter 将过滤条件转换为 MongoDB bson.D 查询文档
func (f *OperationLogFilter) buildFilter() bson.D {
	filter := bson.D{}
	if f.UID != "" {
		filter = append(filter, bson.E{Key: "uid", Value: f.UID})
	}
	if f.Module != "" {
		filter = append(filter, bson.E{Key: "module", Value: f.Module})
	}
	if f.Action != "" {
		filter = append(filter, bson.E{Key: "action", Value: f.Action})
	}
	// 时间范围过滤：起点和终点均可独立生效
	timeFilter := bson.D{}
	if !f.StartTime.IsZero() {
		timeFilter = append(timeFilter, bson.E{Key: "$gte", Value: f.StartTime})
	}
	if !f.EndTime.IsZero() {
		timeFilter = append(timeFilter, bson.E{Key: "$lte", Value: f.EndTime})
	}
	if len(timeFilter) > 0 {
		filter = append(filter, bson.E{Key: "createdAt", Value: timeFilter})
	}
	return filter
}

// PageLogs 分页查询操作日志，按创建时间倒序排列
// filter: 过滤条件；page/pageSize: 分页参数
func (r *OperationLogRepo) PageLogs(ctx context.Context, f OperationLogFilter, page, pageSize int64) (*mongox.PageResult[model.OperationLog], error) {
	return r.Paginate(ctx, f.buildFilter(), mongox.PageQuery{
		Page:     page,
		PageSize: pageSize,
		Sort:     bson.D{{Key: "createdAt", Value: -1}},
	})
}
