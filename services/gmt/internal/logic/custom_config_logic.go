package logic

import (
	"context"
	"fmt"

	"kemiluo/pkg/async"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

// customConfigRedisKey Redis 中自定义配置的固定 key（全局仅一条，与 gamelogic 侧一致）
const customConfigRedisKey = "customConfig"

// CustomConfigLogic 自定义配置业务逻辑
type CustomConfigLogic struct {
	mongoSvc *mongox.Service
	redisSvc *redisx.Service
	asyncSvc *async.Service
}

// NewCustomConfigLogic 创建自定义配置业务逻辑实例
func NewCustomConfigLogic(mongoSvc *mongox.Service, redisSvc *redisx.Service, asyncSvc *async.Service) *CustomConfigLogic {
	return &CustomConfigLogic{mongoSvc: mongoSvc, redisSvc: redisSvc, asyncSvc: asyncSvc}
}

// Save 保存自定义配置，写入 MongoDB 并同步缓存到 Redis
func (l *CustomConfigLogic) Save(ctx context.Context, req *dto.SaveCustomConfigReq, operator string) (*dto.SaveCustomConfigResp, error) {
	configRepo := repo.NewCustomConfigRepo(l.mongoSvc.Client())

	doc := model.NewCustomConfig(req.Data, operator)

	// 写入 MongoDB（upsert）
	if err := configRepo.Upsert(ctx, doc); err != nil {
		return nil, fmt.Errorf("保存自定义配置失败: %w", err)
	}

	// 同步写入 Redis 缓存
	if err := l.cacheToRedis(ctx, doc); err != nil {
		// Redis 写入失败仅告警，不阻断主流程
		logx.Warn("自定义配置写入Redis缓存失败", zap.Error(err))
	}

	// 异步记录操作日志
	l.logOperationAsync(operator, "CustomConfig", "Save", "POST", "/api/customConfig/save", req.RequestID(), req.Data)

	return &dto.SaveCustomConfigResp{
		UpdatedAt: doc.UpdatedAt,
	}, nil
}

// Get 查询自定义配置
func (l *CustomConfigLogic) Get(ctx context.Context) (*dto.GetCustomConfigResp, error) {
	configRepo := repo.NewCustomConfigRepo(l.mongoSvc.Client())

	doc, err := configRepo.Find(ctx)
	if err != nil {
		return nil, err
	}

	return &dto.GetCustomConfigResp{
		Data:      doc.Data,
		UpdatedAt: doc.UpdatedAt,
	}, nil
}

// cacheToRedis 将自定义配置文档序列化后写入 Redis（无过期时间，永久缓存）
func (l *CustomConfigLogic) cacheToRedis(ctx context.Context, doc *model.CustomConfig) error {
	client := l.redisSvc.Client()
	if client == nil {
		return fmt.Errorf("redis client not ready")
	}

	data, err := sonic.MarshalString(doc)
	if err != nil {
		return fmt.Errorf("序列化自定义配置失败: %w", err)
	}

	return client.Set(ctx, customConfigRedisKey, data, 0)
}

// logOperationAsync 异步记录操作日志
func (l *CustomConfigLogic) logOperationAsync(uid, module, action, method, uri, requestID string, reqData any) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("module", module).
		Set("action", action).
		Set("method", method).
		Set("uri", uri).
		Set("requestId", requestID).
		Set("reqData", reqData)
	l.asyncSvc.SubmitFunc("log", "operationLog", func(c *async.Context) error {
		logRepo := repo.NewOperationLogRepo(mongoSvc.Client())
		return logRepo.InsertLog(context.Background(), &model.OperationLog{
			UID:      c.GetString("uid"),
			Username: c.GetString("uid"),
			Module:   c.GetString("module"),
			Action:   c.GetString("action"),
			Method:   c.GetString("method"),
			URI:      c.GetString("uri"),
			Request:  c.Get("reqData"),
		})
	}, ctx)
}
