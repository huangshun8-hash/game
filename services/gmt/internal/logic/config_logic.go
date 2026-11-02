package logic

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"
	"kemiluo/services/shared/configx"

	"github.com/bytedance/sonic"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// tableNamePattern 配置表名合法性正则：字母开头，后续为字母/数字/下划线，总长度 1-64。
// 禁止 .、$、/ 及空字符，防止 MongoDB 字段路径或集合名注入。
var tableNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// ConfigLogic 配置管理业务逻辑
//
// 完整发布流程：
//
//	CreateRelease    → 创建任务（draft）
//	UploadToRelease  → 按表上传到暂存区（uploading）
//	CloseRelease     → 关闭任务，删除暂存数据（closed，终态）
//	PublishRelease   → 将暂存数据推入生产集合（published，终态）
type ConfigLogic struct {
	mongoSvc *mongox.Service
	redisSvc *redisx.Service
}

// NewConfigLogic 创建配置管理业务逻辑实例
func NewConfigLogic(mongoSvc *mongox.Service, redisSvc *redisx.Service) *ConfigLogic {
	return &ConfigLogic{mongoSvc: mongoSvc, redisSvc: redisSvc}
}

// GetManifest 获取所有配置表当前线上版本清单，供 Uploader 工具增量对比
func (l *ConfigLogic) GetManifest(ctx context.Context) (*dto.GetManifestResp, error) {
	manifests, err := repo.NewConfigManifestRepo(l.mongoSvc.Client()).FindAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询配置清单失败: %w", err)
	}
	tables := make(map[string]dto.ManifestTableInfo, len(manifests))
	for _, m := range manifests {
		tables[m.ID] = dto.ManifestTableInfo{
			StagingVersion:    objectIDHexOrEmpty(m.StagingVersion),
			StagingSchemaHash: m.StagingSchemaHash,
			StagingDataHash:   m.StagingDataHash,
			PublishedVersion:  objectIDHexOrEmpty(m.PublishedVersion),
		}
	}
	return &dto.GetManifestResp{Tables: tables}, nil
}

// objectIDHexOrEmpty 将 ObjectID 转为十六进制字符串，零值返回空字符串。
// 零值 ObjectID 的 Hex() 会得到全 0 字符串，与"空字符串表示从未发布"的语义不符。
func objectIDHexOrEmpty(id bson.ObjectID) string {
	if id.IsZero() {
		return ""
	}
	return id.Hex()
}

// CreateRelease 创建配置发布任务
// 生成全局唯一 releaseId，初始状态为 draft，记录创建事件。
// 无需预声明配置表，上传哪些表就发布哪些表。
func (l *ConfigLogic) CreateRelease(ctx context.Context, req *dto.CreateReleaseReq, creator string) (*dto.CreateReleaseResp, error) {
	if req.Description == "" {
		return nil, fmt.Errorf("description is required")
	}

	// 同一时刻只允许存在一个未完成的发布任务，防止并发任务互相干扰线上配置。
	// 通过 config_releases 集合上 activeFlag 的部分唯一索引保证原子性，
	// 并发创建时后写入者会触发唯一索引冲突，杜绝先查后插的 TOCTOU 竞态。
	releaseRepo := repo.NewConfigReleaseRepo(l.mongoSvc.Client())

	now := time.Now()
	releaseID := bson.NewObjectID()

	release := &model.ConfigRelease{
		ID:           releaseID,
		Description:  req.Description,
		Tables:       []string{},
		TableDetails: make(map[string]model.ReleaseTableDetail),
		Status:       model.ReleaseStatusDraft,
		ActiveFlag:   true,
		Events: []model.ReleaseEvent{
			{
				EventType:  model.ReleaseEventCreate,
				Operator:   creator,
				Message:    fmt.Sprintf("创建发布任务：%s", req.Description),
				OccurredAt: now,
			},
		},
		CreatedBy: creator,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := releaseRepo.Insert(ctx, release); err != nil {
		// 唯一索引冲突说明已存在进行中的任务，与原 ExistsActive 语义等价
		if mongox.IsDuplicateKeyError(err) {
			return nil, model.ErrConfigActiveReleaseExists
		}
		return nil, fmt.Errorf("failed to create release: %w", err)
	}

	return &dto.CreateReleaseResp{
		ID:          releaseID.Hex(),
		Description: req.Description,
		Status:      model.ReleaseStatusDraft,
		CreatedAt:   now.UnixMilli(),
	}, nil
}

// UploadToRelease 向指定发布任务上传单张配置表数据
//
// 校验：
//   - 任务必须存在且未终止
//   - 配置数据合法性校验
//
// 每次上传将数据全量写入该任务专属的暂存数据库（staging_<releaseId>），
// 并把 Schema 快照记录到任务文档，追加上传事件，任务状态转为 uploading。
// Schema 与生产 config_schema、config_manifest 均在发布时才落地，上传阶段不影响线上。
func (l *ConfigLogic) UploadToRelease(ctx context.Context, req *dto.UploadToReleaseReq, operator string) (*dto.UploadToReleaseResp, error) {
	if err := l.validateUpload(req); err != nil {
		return nil, err
	}

	releaseOID, err := bson.ObjectIDFromHex(req.ReleaseID)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid release id", model.ErrConfigReleaseNotFound)
	}

	client := l.mongoSvc.Client()
	releaseRepo := repo.NewConfigReleaseRepo(client)

	// 加载任务
	release, err := releaseRepo.FindByID(ctx, releaseOID)
	if err == mongox.ErrNotFound {
		return nil, model.ErrConfigReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询发布任务失败: %w", err)
	}
	if release.IsTerminated() {
		return nil, model.ErrConfigReleaseTerminated
	}

	// 判断是否 Schema 变更：优先与本任务已暂存版本对比，本任务未暂存过该表时再与线上（已发布）结构对比，
	// 用于提示是否需要重新生成代码并部署
	isSchemaChanged := true
	manifestRepo := repo.NewConfigManifestRepo(client)
	if prev, ok := release.TableDetails[req.Table]; ok && prev.IsUploaded {
		// 本任务已暂存过该表，以本任务暂存版本为基准
		isSchemaChanged = prev.SchemaHash != req.SchemaHash
	} else {
		// 本任务未暂存该表，回退到与线上已发布结构对比
		existing, mErr := manifestRepo.FindByID(ctx, req.Table)
		if mErr == nil {
			isSchemaChanged = existing.PublishedSchemaHash != req.SchemaHash
		} else if mErr != mongox.ErrNotFound {
			return nil, fmt.Errorf("查询配置清单失败: %w", mErr)
		}
		// mErr == ErrNotFound 表示全新表，isSchemaChanged 保持 true
	}

	// 写入该任务专属暂存数据库（全量替换），不同任务库级隔离，互不干扰
	// buildDocs 依据 Schema 归一化数组/映射类型字段，避免空单元格被写成空字符串导致读取端解码失败
	if err := repo.NewConfigStagingRepo(client, releaseOID, req.Table).ReplaceAll(ctx, buildDocs(req.Data, req.Schema)); err != nil {
		return nil, fmt.Errorf("写入暂存数据失败: %w", err)
	}

	// 立即更新 config_manifest 暂存区快照，让 GetManifest 接口在发布前也能返回最新暂存版本，
	// 供 Uploader 工具下次运行时做增量对比，避免重复上传未变更的配置表
	uploadTime := time.Now()
	if err := manifestRepo.UpsertStaging(ctx, req.Table, releaseOID, req.SchemaHash, req.DataHash, operator, uploadTime); err != nil {
		return nil, fmt.Errorf("更新配置清单暂存区失败: %w", err)
	}

	// Schema 有变更（或全新表）时立即更新 config_schema，供开发者在发布前运行 genconfig 生成代码。
	// genconfig 属于开发期工具，需要在代码编译阶段就能感知到最新结构，不应等到发布后才写入。
	if isSchemaChanged {
		if err := repo.NewConfigSchemaRepo(client).Upsert(ctx, &model.ConfigSchema{
			ID:         req.Table,
			Version:    releaseOID,
			SchemaHash: req.SchemaHash,
			Fields:     req.Schema,
		}); err != nil {
			return nil, fmt.Errorf("更新配置结构失败: %w", err)
		}
	}

	// 更新任务中该表的详情；$addToSet 会自动将表名追加到 tables 数组
	now := time.Now()
	uploadedAt := uploadTime
	detail := model.ReleaseTableDetail{
		Table:           req.Table,
		IsUploaded:      true,
		SchemaHash:      req.SchemaHash,
		DataHash:        req.DataHash,
		Schema:          req.Schema,
		IsSchemaChanged: isSchemaChanged,
		UploadedAt:      &uploadedAt,
		UploadedBy:      operator,
	}

	// 若任务此前已处于验收中或验收完成，则本次上传使验收失效，需重新验收
	message := fmt.Sprintf("上传配置表 %s（schemaChanged=%v）", req.Table, isSchemaChanged)
	event := model.ReleaseEvent{
		EventType:  model.ReleaseEventUpload,
		Operator:   operator,
		Message:    message,
		OccurredAt: now,
	}

	// 首次上传后从 draft 转为 uploading；再次上传统一保持 uploading
	newStatus := model.ReleaseStatusUploading

	if err := releaseRepo.UpdateTableDetail(ctx, releaseOID, detail, event, newStatus, false); err != nil {
		return nil, fmt.Errorf("更新发布任务状态失败: %w", err)
	}

	return &dto.UploadToReleaseResp{
		ReleaseID:       req.ReleaseID,
		Table:           req.Table,
		IsSchemaChanged: isSchemaChanged,
		ReleaseStatus:   newStatus,
	}, nil
}

// CloseRelease 关闭发布任务
//
// 将任务状态置为 closed（终态），并删除该任务的暂存数据库。
// 任何非终态的任务均可关闭；关闭后不可再进行任何操作。
func (l *ConfigLogic) CloseRelease(ctx context.Context, req *dto.CloseReleaseReq, operator string) (*dto.CloseReleaseResp, error) {
	releaseOID, err := bson.ObjectIDFromHex(req.ReleaseID)
	if err != nil {
		return nil, model.ErrConfigReleaseNotFound
	}

	client := l.mongoSvc.Client()
	releaseRepo := repo.NewConfigReleaseRepo(client)

	release, err := releaseRepo.FindByID(ctx, releaseOID)
	if err == mongox.ErrNotFound {
		return nil, model.ErrConfigReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询发布任务失败: %w", err)
	}
	if release.IsTerminated() {
		return nil, model.ErrConfigReleaseTerminated
	}

	now := time.Now()
	event := model.ReleaseEvent{
		EventType:  model.ReleaseEventClosed,
		Operator:   operator,
		Message:    "关闭发布任务",
		OccurredAt: now,
	}
	if err := releaseRepo.UpdateStatus(ctx, releaseOID, model.ReleaseStatusClosed, event); err != nil {
		return nil, fmt.Errorf("更新任务状态失败: %w", err)
	}

	// 删除本任务的暂存集合；失败仅告警，不影响关闭结果
	if err := repo.DropStagingByRelease(ctx, client, releaseOID); err != nil {
		logx.Warn("关闭任务时清理暂存数据失败",
			zap.String("requestId", req.RequestID()),
			zap.String("operator", operator),
			zap.String("releaseId", req.ReleaseID),
			zap.Error(err),
		)
	}

	// 清理 config_manifest 中属于本任务的暂存区快照，防止 Uploader 下次运行时误判 hash 相同而跳过上传
	// 暂存数据已删除，若 hash 残留会导致新任务中该表始终被跳过
	if err := repo.NewConfigManifestRepo(client).ClearStagingByRelease(ctx, releaseOID); err != nil {
		logx.Warn("关闭任务时清理 manifest 暂存快照失败",
			zap.String("requestId", req.RequestID()),
			zap.String("operator", operator),
			zap.String("releaseId", req.ReleaseID),
			zap.Error(err),
		)
	}

	return &dto.CloseReleaseResp{
		ReleaseID: req.ReleaseID,
		Status:    model.ReleaseStatusClosed,
	}, nil
}

// PublishRelease 发布指定任务
//
// 只有 reviewed 状态（已完成验收）的任务才能发布。
// 所有已上传表的「暂存数据 → 生产集合全量替换」+「config_manifest 更新」
// + 「任务状态置为 published」在同一个 MongoDB 事务中执行，保证原子性：
// 要么全部生效，要么全部回滚，杜绝部分发布导致的生产数据不一致。
// config_schema 已在上传阶段（UploadToRelease）写入，发布流程不再重复写入。
//
// 注意：事务受 MongoDB 单事务体积上限约束（约 16MB oplog），
// 超大配置表需评估是否拆分或改用其他一致性方案。
func (l *ConfigLogic) PublishRelease(ctx context.Context, req *dto.PublishReleaseReq, operator string) (*dto.PublishReleaseResp, error) {
	releaseOID, err := bson.ObjectIDFromHex(req.ReleaseID)
	if err != nil {
		return nil, model.ErrConfigReleaseNotFound
	}

	client := l.mongoSvc.Client()
	releaseRepo := repo.NewConfigReleaseRepo(client)

	release, err := releaseRepo.FindByID(ctx, releaseOID)
	if err == mongox.ErrNotFound {
		return nil, model.ErrConfigReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询发布任务失败: %w", err)
	}
	if release.IsTerminated() {
		return nil, model.ErrConfigReleaseTerminated
	}
	if !release.CanPublish() {
		return nil, fmt.Errorf("%w: current status %s, at least one config table must be uploaded", model.ErrConfigCannotPublish, release.Status)
	}

	// 预读每张已上传表的暂存数据（事务外读取，缩短事务持有时间）
	type tablePayload struct {
		table  string
		docs   []any
		detail model.ReleaseTableDetail
	}
	payloads := make([]tablePayload, 0, len(release.Tables))
	for _, tableName := range release.Tables {
		detail, ok := release.TableDetails[tableName]
		if !ok || !detail.IsUploaded {
			continue // 跳过未上传的表（正常流程不会走到这）
		}
		stagingDocs, err := repo.NewConfigStagingRepo(client, release.ID, tableName).FindAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取暂存数据失败(%s): %w", tableName, err)
		}
		docs := make([]any, len(stagingDocs))
		for i, d := range stagingDocs {
			docs[i] = d
		}
		payloads = append(payloads, tablePayload{table: tableName, docs: docs, detail: detail})
	}
	if len(payloads) == 0 {
		return nil, model.ErrConfigNothingToPublish
	}

	manifestRepo := repo.NewConfigManifestRepo(client)
	publishedAt := time.Now()

	// 在单个事务内完成所有生产写入与状态更新，保证原子性
	// 注意：config_schema 已在上传阶段（UploadToRelease）写入，此处无需重复写入
	txErr := client.RunTransaction(ctx, func(sc context.Context) error {
		for _, p := range payloads {
			// 生产集合全量替换
			if err := repo.NewConfigDataRepo(client, p.table).ReplaceAll(sc, p.docs); err != nil {
				return fmt.Errorf("写入生产数据失败(%s): %w", p.table, err)
			}
			// 更新 config_manifest 发布区快照
			if err := manifestRepo.UpsertPublished(sc, &model.ConfigManifest{
				ID:                  p.table,
				StagingVersion:      release.ID,
				StagingSchemaHash:   p.detail.SchemaHash,
				StagingDataHash:     p.detail.DataHash,
				StagingAuthor:       p.detail.UploadedBy,
				StagingAt:           uploadedAtOrNow(p.detail, publishedAt),
				PublishedVersion:    release.ID,
				PublishedSchemaHash: p.detail.SchemaHash,
				PublishedDataHash:   p.detail.DataHash,
				PublishedAuthor:     operator,
				PublishedAt:         &publishedAt,
				ReleaseID:           release.ID,
				Status:              model.ConfigStatusPublished,
			}); err != nil {
				return fmt.Errorf("更新配置清单失败(%s): %w", p.table, err)
			}
		}

		// 任务状态置为 published（终态）
		return releaseRepo.UpdateStatus(sc, releaseOID, model.ReleaseStatusPublished,
			model.ReleaseEvent{
				EventType:  model.ReleaseEventPublished,
				Operator:   operator,
				Message:    fmt.Sprintf("发布成功，共 %d 张配置表", len(payloads)),
				OccurredAt: publishedAt,
			},
			bson.E{Key: "publishedBy", Value: operator},
			bson.E{Key: "publishedAt", Value: publishedAt},
		)
	})
	if txErr != nil {
		return nil, fmt.Errorf("发布事务执行失败: %w", txErr)
	}

	// 事务成功后向 config:publish 频道发布消息，通知各服务热更正式配置
	publishedTables := make([]string, 0, len(payloads))
	for _, p := range payloads {
		publishedTables = append(publishedTables, p.table)
	}
	pubMsg := configx.PublishMessage{
		ReleaseID:  req.ReleaseID,
		Tables:     publishedTables,
		Operator:   operator,
		OccurredAt: publishedAt.UnixMilli(),
	}
	pubPayload, err := sonic.Marshal(pubMsg)
	if err != nil {
		// 序列化失败不回滚，仅告警；服务重启时会从生产库全量加载最新配置
		logx.Warn("序列化配置发布消息失败，各服务需重启后才能感知最新配置",
			zap.String("requestId", req.RequestID()),
			zap.String("releaseId", req.ReleaseID),
			zap.Error(err),
		)
	} else if pubErr := l.redisSvc.Client().Raw().Publish(ctx, configx.PublishChannel, pubPayload).Err(); pubErr != nil {
		// Redis 发布失败不回滚，仅告警；服务重启时会从生产库全量加载最新配置
		logx.Warn("发布配置发布消息到 Redis 失败，各服务需重启后才能感知最新配置",
			zap.String("requestId", req.RequestID()),
			zap.String("releaseId", req.ReleaseID),
			zap.String("channel", configx.PublishChannel),
			zap.Error(pubErr),
		)
	}

	// 事务成功后异步清理该任务的暂存集合（失败仅告警，不影响发布结果）
	if err := repo.DropStagingByRelease(ctx, client, release.ID); err != nil {
		logx.Warn("清理发布任务暂存数据失败",
			zap.String("requestId", req.RequestID()),
			zap.String("releaseId", req.ReleaseID),
			zap.Error(err),
		)
	}

	results := make([]dto.PublishTableResult, 0, len(payloads))
	for _, p := range payloads {
		results = append(results, dto.PublishTableResult{
			Table:           p.table,
			IsSchemaChanged: p.detail.IsSchemaChanged,
		})
	}

	return &dto.PublishReleaseResp{
		ReleaseID:       req.ReleaseID,
		PublishedCount:  len(results),
		Results:         results,
		HasSchemaChange: release.HasSchemaChange(),
	}, nil
}

// uploadedAtOrNow 返回表详情的上传时间，缺失时回退到给定时间，避免空指针
func uploadedAtOrNow(detail model.ReleaseTableDetail, fallback time.Time) time.Time {
	if detail.UploadedAt != nil {
		return *detail.UploadedAt
	}
	return fallback
}

// GetRelease 获取单个发布任务详情
func (l *ConfigLogic) GetRelease(ctx context.Context, id bson.ObjectID) (*dto.ReleaseDetailResp, error) {
	release, err := repo.NewConfigReleaseRepo(l.mongoSvc.Client()).FindByID(ctx, id)
	if err == mongox.ErrNotFound {
		return nil, model.ErrConfigReleaseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询发布任务失败: %w", err)
	}
	return toReleaseDetail(release), nil
}

// ListReleases 分页查询发布任务列表
func (l *ConfigLogic) ListReleases(ctx context.Context, req *dto.ListReleasesReq) (*dto.ListReleasesResp, error) {
	page := max(req.Page, 1)
	pageSize := req.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	releases, total, err := repo.NewConfigReleaseRepo(l.mongoSvc.Client()).ListPage(ctx, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("查询发布任务列表失败: %w", err)
	}

	items := make([]dto.ReleaseListItem, 0, len(releases))
	for _, r := range releases {
		uploadedCount := 0
		for _, d := range r.TableDetails {
			if d.IsUploaded {
				uploadedCount++
			}
		}
		item := dto.ReleaseListItem{
			ID:              r.ID.Hex(),
			Description:     r.Description,
			Status:          r.Status,
			Tables:          r.Tables,
			UploadedCount:   uploadedCount,
			TotalCount:      len(r.Tables),
			CreatedBy:       r.CreatedBy,
			PublishedBy:     r.PublishedBy,
			HasSchemaChange: r.HasSchemaChange(),
			CreatedAt:       r.CreatedAt.UnixMilli(),
		}
		if r.PublishedAt != nil {
			item.PublishedAt = r.PublishedAt.UnixMilli()
		}
		items = append(items, item)
	}

	totalPages := (total + pageSize - 1) / pageSize
	return &dto.ListReleasesResp{
		Items:      items,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// toReleaseDetail 将 model.ConfigRelease 转换为详情响应 DTO
func toReleaseDetail(r *model.ConfigRelease) *dto.ReleaseDetailResp {
	tableDetails := make([]dto.ReleaseTableDetailItem, 0, len(r.Tables))
	for _, t := range r.Tables {
		d := r.TableDetails[t]
		item := dto.ReleaseTableDetailItem{
			Table:           d.Table,
			IsUploaded:      d.IsUploaded,
			IsSchemaChanged: d.IsSchemaChanged,
			SchemaHash:      d.SchemaHash,
			DataHash:        d.DataHash,
			UploadedBy:      d.UploadedBy,
		}
		if d.UploadedAt != nil {
			item.UploadedAt = d.UploadedAt.UnixMilli()
		}
		tableDetails = append(tableDetails, item)
	}

	events := make([]dto.ReleaseEventItem, 0, len(r.Events))
	for _, e := range r.Events {
		events = append(events, dto.ReleaseEventItem{
			EventType:  e.EventType,
			Operator:   e.Operator,
			Message:    e.Message,
			OccurredAt: e.OccurredAt.UnixMilli(),
		})
	}

	resp := &dto.ReleaseDetailResp{
		ID:              r.ID.Hex(),
		Description:     r.Description,
		Status:          r.Status,
		Tables:          r.Tables,
		TableDetails:    tableDetails,
		Events:          events,
		CreatedBy:       r.CreatedBy,
		PublishedBy:     r.PublishedBy,
		HasSchemaChange: r.HasSchemaChange(),
		CreatedAt:       r.CreatedAt.UnixMilli(),
		UpdatedAt:       r.UpdatedAt.UnixMilli(),
	}
	if r.PublishedAt != nil {
		resp.PublishedAt = r.PublishedAt.UnixMilli()
	}
	return resp
}

// validateUpload 校验上传请求合法性
func (l *ConfigLogic) validateUpload(req *dto.UploadToReleaseReq) error {
	if req.Table == "" {
		return fmt.Errorf("%w: table name is required", model.ErrConfigSchemaInvalid)
	}
	if !tableNamePattern.MatchString(req.Table) {
		return fmt.Errorf("%w: invalid table name %q", model.ErrConfigSchemaInvalid, req.Table)
	}
	if len(req.Schema) == 0 {
		return fmt.Errorf("%w: schema fields are empty", model.ErrConfigSchemaInvalid)
	}
	hasID := false
	for _, f := range req.Schema {
		if f.Name == "id" {
			hasID = true
			break
		}
	}
	if !hasID {
		return fmt.Errorf("%w: schema must contain 'id' field", model.ErrConfigSchemaInvalid)
	}
	if req.SchemaHash == "" {
		return fmt.Errorf("%w: schemaHash is required", model.ErrConfigSchemaInvalid)
	}
	if req.DataHash == "" {
		return fmt.Errorf("%w: dataHash is required", model.ErrConfigSchemaInvalid)
	}
	seen := make(map[any]struct{}, len(req.Data))
	for i, row := range req.Data {
		idVal, ok := row["id"]
		if !ok {
			return fmt.Errorf("%w: row[%d] missing 'id'", model.ErrConfigIDMissing, i)
		}
		if !isComparableID(idVal) {
			return fmt.Errorf("%w: row[%d] has non-comparable id type", model.ErrConfigDataInvalid, i)
		}
		if _, exists := seen[idVal]; exists {
			return fmt.Errorf("%w: duplicate id=%v in row[%d]", model.ErrConfigIDDuplicate, idVal, i)
		}
		seen[idVal] = struct{}{}
	}
	return nil
}

// isComparableID 判断 id 值是否为可作为 map key 的可比较类型且不会因浮点表示丢失精度。
// JSON 数字经 sonic 解析为 float64；float64 仅能精确表示绝对值小于 2^53 的整数，
// 超范围或非整数会丢精度，导致不同 id 被误判重复或入库错误，故必须校验为安全范围内的整数。
// map / slice 等不可比较类型返回 false，避免以其作为 map key 时触发 panic。
func isComparableID(v any) bool {
	switch t := v.(type) {
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32:
		return true
	case float64:
		return t == math.Trunc(t) && math.Abs(t) < (1<<53)
	default:
		return false
	}
}

// buildDocs 将 []map[string]any 中的 "id" key 转换为 MongoDB "_id"，
// 并依据 Schema 归一化数组/映射类型字段：空单元格（空字符串或 nil）会被上传端序列化为空字符串，
// 若直接写入库中，读取端将其解码进 []T / map[K]V 字段时会失败或产生脏数据，
// 故此处按声明类型将其归一化为空数组 / 空映射，保证写入 BSON 时类型正确。
func buildDocs(rows []map[string]any, schema []model.FieldDef) []any {
	// 记录数组字段的元素类型（[]int → "int"），映射字段仅记录字段名。
	// 数组归一化时需要按元素类型把标量转换成正确的数值类型，避免写入 BSON 后读取端解码失败。
	arrayElemTypes := make(map[string]string, len(schema))
	mapFields := make(map[string]struct{}, len(schema))
	for _, f := range schema {
		t := strings.TrimSpace(f.Type)
		switch {
		case strings.HasPrefix(t, "[]"):
			arrayElemTypes[f.Name] = strings.TrimSpace(t[2:])
		case strings.HasPrefix(t, "map["):
			mapFields[f.Name] = struct{}{}
		}
	}

	docs := make([]any, 0, len(rows))
	for _, row := range rows {
		doc := make(map[string]any, len(row))
		for k, v := range row {
			v = normalizeConfigValue(k, v, arrayElemTypes, mapFields)
			if k == "id" {
				doc["_id"] = v
			} else {
				doc[k] = v
			}
		}
		docs = append(docs, doc)
	}
	return docs
}

// normalizeConfigValue 依据字段声明类型归一化单个配置值，保证写入 BSON 时的类型与
// 读取端生成结构体字段类型（[]T / map[K]V）一致，避免 bson.Unmarshal 解码报错。
//
// 数组字段的归一化覆盖三种「非数组」来源（常见于字段类型由标量改为数组后遗留的旧数据）：
//   - 空单元格（nil / 空字符串）        → 空数组
//   - 逗号分隔字符串（如 "1,2,3"）      → 按逗号拆分并逐元素按元素类型转换
//   - 单个标量（如 数字 5 或字符串 "5"）→ 包成单元素数组，元素按元素类型转换
//
// 映射字段的归一化仅处理空单元格（nil / 空字符串）→ 空映射；非空值保持原样，
// 由上传端保证其为合法对象结构。
func normalizeConfigValue(key string, value any, arrayElemTypes map[string]string, mapFields map[string]struct{}) any {
	if elemType, isArray := arrayElemTypes[key]; isArray {
		return normalizeArrayValue(value, elemType)
	}
	if _, isMap := mapFields[key]; isMap {
		if isEmptyCell(value) {
			// 空映射字段统一写成空文档，读取端 bson.Unmarshal 进 map[K]V 时得到空 map
			return bson.D{}
		}
		return value
	}
	return value
}

// normalizeArrayValue 将任意来源的值归一化为 BSON 数组，并把每个元素转换为 elemType 对应的类型，
// 确保读取端 bson.Unmarshal 能正确解码进 []T 字段。
func normalizeArrayValue(value any, elemType string) any {
	switch v := value.(type) {
	case nil:
		// 空单元格：写成空数组
		return bson.A{}
	case bson.A:
		// 已是 BSON 数组，逐元素做类型转换后返回
		return convertArrayElems(v, elemType)
	case []any:
		// 已是切片，逐元素做类型转换后返回
		return convertArrayElems(bson.A(v), elemType)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			// 空字符串等同空单元格
			return bson.A{}
		}
		// 逗号分隔字符串按逗号拆分；无逗号时即单元素数组
		parts := strings.Split(s, ",")
		arr := make(bson.A, 0, len(parts))
		for _, p := range parts {
			arr = append(arr, convertElem(strings.TrimSpace(p), elemType))
		}
		return arr
	default:
		// 其余标量（数字、布尔等）包成单元素数组
		return bson.A{convertElem(value, elemType)}
	}
}

// convertArrayElems 对数组中的每个元素按 elemType 做类型转换。
func convertArrayElems(arr bson.A, elemType string) bson.A {
	out := make(bson.A, 0, len(arr))
	for _, e := range arr {
		out = append(out, convertElem(e, elemType))
	}
	return out
}

// convertElem 将单个数组元素转换为 elemType 对应的 Go 类型，供 BSON 以正确类型存储。
// 转换失败（无法解析为目标数值）时保持原值，交由后续校验/上传端保证数据合法性。
func convertElem(value any, elemType string) any {
	switch elemType {
	case "int":
		if n, ok := toInt64(value); ok {
			return int32(n)
		}
	case "long":
		if n, ok := toInt64(value); ok {
			return n
		}
	case "float", "double":
		if f, ok := toFloat64(value); ok {
			return f
		}
	case "string":
		if s, ok := value.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", value)
	}
	// 未识别元素类型或转换失败：保持原值
	return value
}

// toInt64 尽力将值转换为 int64，支持 JSON 解析出的 float64 及字符串数字。
func toInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		// JSON 数字经 sonic 解析为 float64，仅接受可无损转换为整数的值
		if n == math.Trunc(n) {
			return int64(n), true
		}
	case float32:
		f := float64(n)
		if f == math.Trunc(f) {
			return int64(f), true
		}
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// toFloat64 尽力将值转换为 float64，支持 JSON 数字及字符串数字。
func toFloat64(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// isEmptyCell 判断值是否为「空单元格」：nil 或空字符串。
// 上传端对未填写的单元格会序列化为空字符串，需要据此识别并归一化。
func isEmptyCell(value any) bool {
	if value == nil {
		return true
	}
	s, ok := value.(string)
	return ok && strings.TrimSpace(s) == ""
}
