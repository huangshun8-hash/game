package repo

import (
	"context"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gamelogic/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MailTemplateRepo 系统邮件模板数据访问层，嵌入 BaseRepo 获得通用 CRUD 能力
type MailTemplateRepo struct {
	mongox.BaseRepo[model.MailTemplate]
}

// NewMailTemplateRepo 创建系统邮件模板仓库实例
func NewMailTemplateRepo(client *mongox.Client) *MailTemplateRepo {
	return &MailTemplateRepo{BaseRepo: mongox.NewBaseRepo[model.MailTemplate](client, collectionMailTemplates)}
}

// InsertTemplate 插入新的邮件模板，自动填充创建时间
func (r *MailTemplateRepo) InsertTemplate(ctx context.Context, t *model.MailTemplate) error {
	t.CreateTime = time.Now().UnixMilli()
	id, err := r.Insert(ctx, t)
	if err != nil {
		return err
	}
	t.ID = id
	return nil
}

// FindByTemplateID 根据模板业务ID查找模板
func (r *MailTemplateRepo) FindByTemplateID(ctx context.Context, templateID int64) (*model.MailTemplate, error) {
	return r.FindOne(ctx, bson.D{{Key: "templateId", Value: templateID}})
}

// FindTemplatesForPlayer 查询适用于指定玩家的全部可下发模板（全服 + 指定该玩家的）
// 仅返回处于预发布窗口内（服务器时间在 [startTime, endTime] 区间）且未被撤回的模板：
// startTime=0 立即生效、endTime=0 不限制结束时间
// 不再基于 token 签发时间做增量过滤：登录前创建的邮件也必须能被补发，
// 重复下发由调用方的 FindExistingTemplateIDs 去重 + (playerId, templateId) 唯一索引共同保证
// nowMs: 当前时间戳（Unix毫秒），用于预发布窗口过滤
func (r *MailTemplateRepo) FindTemplatesForPlayer(ctx context.Context, playerID int64, nowMs int64) ([]model.MailTemplate, error) {
	// 用 $and 包裹多个条件，避免 bson.D 重复 key 导致后者覆盖前者
	filter := bson.D{
		{Key: "$and", Value: bson.A{
			// 适用条件：全服 OR 指定该玩家
			bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "targetType", Value: model.MailTargetAll}},
				bson.D{
					{Key: "targetType", Value: bson.D{{Key: "$in", Value: bson.A{model.MailTargetUID, model.MailTargetUIDs}}}},
					{Key: "targetUIDs", Value: playerID},
				},
			}}},
			// 预发布窗口：startTime=0 立即生效，否则必须已到生效开始时间（服务器时间在 [startTime, endTime) 区间内才下发）
			bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "startTime", Value: 0}},
				bson.D{{Key: "startTime", Value: bson.D{{Key: "$lte", Value: nowMs}}}},
			}}},
			// 预发布结束：endTime=0 不限制，否则必须未到预发布结束时间
			bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "endTime", Value: 0}},
				bson.D{{Key: "endTime", Value: bson.D{{Key: "$gt", Value: nowMs}}}},
			}}},
			// 撤回过滤：仅下发未撤回模板（$ne 匹配字段缺失的历史数据）
			bson.D{{Key: "isRecalled", Value: bson.D{{Key: "$ne", Value: true}}}},
		}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "createTime", Value: 1}})
	return r.FindMany(ctx, filter, opts)
}

// MailTemplateFilter 邮件模板查询过滤条件（GMT 管理后台列表查询）
// 所有字段均为可选，零值/哨兵值表示不过滤
type MailTemplateFilter struct {
	// TemplateID 按模板业务ID精确过滤，0 表示不过滤
	TemplateID int64
	// TargetType 按发送目标类型过滤（0=全服 1=指定UID 2=UID列表），nil 表示不过滤
	TargetType *int
	// Recalled 按撤回状态过滤，nil 表示不过滤；非nil 按指针值精确匹配（false=未撤回 true=已撤回）
	Recalled *bool
	// StartTime 创建时间范围起点（含），0 表示不限制
	StartTime int64
	// EndTime 创建时间范围终点（含），0 表示不限制
	EndTime int64
}

// buildFilter 将查询过滤条件转换为 MongoDB bson.D 查询文档
func (f *MailTemplateFilter) buildFilter() bson.D {
	filter := bson.D{}
	if f.TemplateID > 0 {
		filter = append(filter, bson.E{Key: "templateId", Value: f.TemplateID})
	}
	if f.TargetType != nil {
		filter = append(filter, bson.E{Key: "targetType", Value: *f.TargetType})
	}
	if f.Recalled != nil {
		if *f.Recalled {
			filter = append(filter, bson.E{Key: "isRecalled", Value: true})
		} else {
			// $ne 匹配字段缺失的历史数据（视为未撤回）
			filter = append(filter, bson.E{Key: "isRecalled", Value: bson.D{{Key: "$ne", Value: true}}})
		}
	}
	// 创建时间范围：合并为单个 createTime 条件，避免 bson.D 重复 key 导致后者覆盖前者
	timeCond := bson.D{}
	if f.StartTime > 0 {
		timeCond = append(timeCond, bson.E{Key: "$gte", Value: f.StartTime})
	}
	if f.EndTime > 0 {
		timeCond = append(timeCond, bson.E{Key: "$lte", Value: f.EndTime})
	}
	if len(timeCond) > 0 {
		filter = append(filter, bson.E{Key: "createTime", Value: timeCond})
	}
	return filter
}

// PageTemplates 分页查询邮件模板（GMT 管理后台列表查询），按创建时间倒序排列
// f: 查询过滤条件；page/pageSize: 分页参数，由 Paginate 内部归一化（默认 page=1、pageSize=20，最大 200）
func (r *MailTemplateRepo) PageTemplates(ctx context.Context, f MailTemplateFilter, page, pageSize int64) (*mongox.PageResult[model.MailTemplate], error) {
	return r.Paginate(ctx, f.buildFilter(), mongox.PageQuery{
		Page:     page,
		PageSize: pageSize,
		Sort:     bson.D{{Key: "createTime", Value: -1}},
	})
}

// RecallTemplate 撤回邮件模板（CAS：仅未撤回的模板可被置为已撤回）
// templateID: 模板业务ID；nowMs: 撤回时间戳（Unix毫秒）
// 返回是否本次调用实际执行了撤回（modified>0）；已撤回或模板不存在返回 false
func (r *MailTemplateRepo) RecallTemplate(ctx context.Context, templateID int64, nowMs int64) (bool, error) {
	filter := bson.D{
		{Key: "templateId", Value: templateID},
		{Key: "isRecalled", Value: bson.D{{Key: "$ne", Value: true}}},
	}
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "isRecalled", Value: true},
		{Key: "recallTime", Value: nowMs},
	}}}
	modified, err := r.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, err
	}
	return modified > 0, nil
}
