package logic

import (
	"context"
	"fmt"
	"time"

	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/repo"
)

// OperationLogLogic 操作日志业务逻辑
type OperationLogLogic struct {
	mongoSvc *mongox.Service
}

// NewOperationLogLogic 创建操作日志业务逻辑实例
func NewOperationLogLogic(mongoSvc *mongox.Service) *OperationLogLogic {
	return &OperationLogLogic{mongoSvc: mongoSvc}
}

// ListLogs 分页查询操作日志
// req: 查询请求DTO，包含过滤条件和分页参数
func (l *OperationLogLogic) ListLogs(ctx context.Context, req *dto.ListOperationLogsReq) (*dto.ListOperationLogsResp, error) {
	// 构建过滤条件，将 Unix 毫秒时间戳转换为 time.Time
	filter := repo.OperationLogFilter{
		UID:    req.UID,
		Module: req.Module,
		Action: req.Action,
	}
	if req.StartTime > 0 {
		filter.StartTime = time.UnixMilli(req.StartTime)
	}
	if req.EndTime > 0 {
		filter.EndTime = time.UnixMilli(req.EndTime)
	}

	// 页码默认为1
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	logRepo := repo.NewOperationLogRepo(l.mongoSvc.Client())
	result, err := logRepo.PageLogs(ctx, filter, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("查询操作日志失败: %w", err)
	}

	items := make([]*dto.OperationLogItem, 0, len(result.Items))
	for i := range result.Items {
		doc := &result.Items[i]
		items = append(items, &dto.OperationLogItem{
			ID:        doc.ID.Hex(),
			UID:       doc.UID,
			Username:  doc.Username,
			Module:    doc.Module,
			Action:    doc.Action,
			Method:    doc.Method,
			URI:       doc.URI,
			IP:        doc.IP,
			Request:   doc.Request,
			CreatedAt: doc.CreatedAt.UnixMilli(),
		})
	}

	return &dto.ListOperationLogsResp{
		Items:      items,
		Total:      result.Total,
		Page:       result.Page,
		PageSize:   result.PageSize,
		TotalPages: result.TotalPages,
	}, nil
}
