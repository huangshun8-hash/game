package handler

import (
	"errors"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.uber.org/zap"
)

// ConfigHandler 游戏配置管理相关的HTTP处理器
type ConfigHandler struct {
	configLogic *logic.ConfigLogic
}

// NewConfigHandler 创建配置处理器
func NewConfigHandler(configLogic *logic.ConfigLogic) *ConfigHandler {
	return &ConfigHandler{configLogic: configLogic}
}

// Register 实现 httpx.Router 接口，注册配置相关路由
func (h *ConfigHandler) Register(r httpx.RouterGroup) {
	cfgGroup := r.Group("/api/config")

	// 供 ConfigUploader 工具调用：获取当前线上版本清单，用于增量对比
	cfgGroup.Get("/manifest", h.GetManifest)

	// 发布任务管理（供 GMT 前端调用）
	cfgGroup.Post("/releases", h.CreateRelease)
	cfgGroup.Get("/releases", h.ListReleases)
	cfgGroup.Get("/releases/:id", h.GetRelease)
	cfgGroup.Post("/releases/:id/upload", h.UploadToRelease)
	cfgGroup.Post("/releases/:id/close", h.CloseRelease)
	cfgGroup.Post("/releases/:id/publish", h.PublishRelease)
}

// GetManifest 获取配置清单（供 Uploader 工具）
// GET /api/config/manifest
func (h *ConfigHandler) GetManifest(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.GetManifestReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	result, err := h.configLogic.GetManifest(c.Context())
	if err != nil {
		logx.Error("获取配置清单失败",
			zap.String("requestId", req.RequestID()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "failed to get manifest")
	}
	return httpx.OK(c, result)
}

// CreateRelease 创建配置发布任务
// POST /api/config/releases
func (h *ConfigHandler) CreateRelease(c httpx.Context) error {
	req, err := httpx.Bind[dto.CreateReleaseReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}

	operator := h.operator(c)
	result, err := h.configLogic.CreateRelease(c.Context(), req, operator)
	if err != nil {
		logx.Warn("创建发布任务失败",
			zap.String("requestId", req.RequestID()),
			zap.String("operator", operator),
			zap.Error(err),
		)
		if errors.Is(err, model.ErrConfigActiveReleaseExists) {
			return httpx.NewAppError(model.ErrCodeConfigActiveReleaseExists, err.Error())
		}
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, err.Error())
	}
	return httpx.OK(c, result)
}

// ListReleases 分页查询发布任务列表
// GET /api/config/releases?page=1&pageSize=20
func (h *ConfigHandler) ListReleases(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.ListReleasesReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	result, err := h.configLogic.ListReleases(c.Context(), req)
	if err != nil {
		logx.Error("查询发布任务列表失败",
			zap.String("requestId", req.RequestID()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "failed to list releases")
	}
	return httpx.OK(c, result)
}

// GetRelease 查询单个发布任务详情
// GET /api/config/releases/:id
func (h *ConfigHandler) GetRelease(c httpx.Context) error {
	id, err := parseReleaseID(c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid release id")
	}
	requestID := c.RequestID()
	result, err := h.configLogic.GetRelease(c.Context(), id)
	if err != nil {
		return h.handleReleaseError(err, requestID, id)
	}
	return httpx.OK(c, result)
}

// UploadToRelease 向指定发布任务上传单张配置表
// POST /api/config/releases/:id/upload
// 客户端可通过 Content-Encoding 头启用请求体压缩（Fiber v3 底层自动解压）
func (h *ConfigHandler) UploadToRelease(c httpx.Context) error {
	id, err := parseReleaseID(c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid release id")
	}

	req, err := httpx.Bind[dto.UploadToReleaseReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	req.ReleaseID = id.Hex()

	if req.Table == "" {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "table name is required")
	}

	operator := h.operator(c)
	result, err := h.configLogic.UploadToRelease(c.Context(), req, operator)
	if err != nil {
		return h.handleUploadError(err, req.RequestID(), req.Table, id)
	}
	return httpx.OK(c, result)
}

// CloseRelease 关闭发布任务
// POST /api/config/releases/:id/close
func (h *ConfigHandler) CloseRelease(c httpx.Context) error {
	id, err := parseReleaseID(c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid release id")
	}
	req, err := httpx.Bind[dto.CloseReleaseReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	req.ReleaseID = id.Hex()

	operator := h.operator(c)
	logx.Info("关闭发布任务",
		zap.String("requestId", req.RequestID()),
		zap.String("operator", operator),
		zap.String("releaseId", id.Hex()),
	)

	result, err := h.configLogic.CloseRelease(c.Context(), req, operator)
	if err != nil {
		return h.handleReleaseError(err, req.RequestID(), id)
	}
	return httpx.OK(c, result)
}

// PublishRelease 发布指定任务（将所有已上传的表推入生产）
// POST /api/config/releases/:id/publish
func (h *ConfigHandler) PublishRelease(c httpx.Context) error {
	id, err := parseReleaseID(c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid release id")
	}

	req, err := httpx.Bind[dto.PublishReleaseReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeConfigBadRequest, "invalid request parameters")
	}
	req.ReleaseID = id.Hex()

	operator := h.operator(c)
	logx.Info("触发配置发布",
		zap.String("requestId", req.RequestID()),
		zap.String("operator", operator),
		zap.String("releaseId", id.Hex()),
	)

	result, err := h.configLogic.PublishRelease(c.Context(), req, operator)
	if err != nil {
		return h.handlePublishError(err, req.RequestID(), operator, id)
	}
	return httpx.OK(c, result)
}

// operator 从 JWT Claims 中读取当前操作者账号名
func (h *ConfigHandler) operator(c httpx.Context) string {
	if claims := c.Claims(); claims != nil {
		return claims.UserID
	}
	return ""
}

// parseReleaseID 从路由参数 :id 解析 ObjectID 类型的发布任务ID
func parseReleaseID(c httpx.Context) (bson.ObjectID, error) {
	return bson.ObjectIDFromHex(c.Params("id"))
}

// handleReleaseError 将发布任务相关错误映射为 AppError
func (h *ConfigHandler) handleReleaseError(err error, requestID string, releaseID bson.ObjectID) error {
	switch {
	case errors.Is(err, model.ErrConfigReleaseNotFound):
		return httpx.NewAppError(model.ErrCodeConfigNotFound, "release not found")
	case errors.Is(err, model.ErrConfigReleaseTerminated):
		return httpx.NewAppError(model.ErrCodeConfigReleaseStatusConflict, "release is already terminated")
	case errors.Is(err, model.ErrConfigCannotPublish):
		return httpx.NewAppError(model.ErrCodeConfigReleaseStatusConflict, err.Error())
	case errors.Is(err, model.ErrConfigNothingToPublish):
		return httpx.NewAppError(model.ErrCodeConfigNothingToPublish, err.Error())
	default:
		logx.Error("发布任务操作失败",
			zap.String("requestId", requestID),
			zap.String("releaseId", releaseID.Hex()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "release operation failed")
	}
}

// handleUploadError 将上传业务错误映射为 AppError
func (h *ConfigHandler) handleUploadError(err error, requestID, table string, releaseID bson.ObjectID) error {
	switch {
	case errors.Is(err, model.ErrConfigReleaseNotFound):
		return httpx.NewAppError(model.ErrCodeConfigNotFound, "release not found")
	case errors.Is(err, model.ErrConfigReleaseTerminated):
		return httpx.NewAppError(model.ErrCodeConfigReleaseStatusConflict, "release is already terminated")
	case errors.Is(err, model.ErrConfigSchemaInvalid):
		return httpx.NewAppError(model.ErrCodeConfigSchemaInvalid, err.Error())
	case errors.Is(err, model.ErrConfigDataInvalid),
		errors.Is(err, model.ErrConfigIDDuplicate),
		errors.Is(err, model.ErrConfigIDMissing):
		return httpx.NewAppError(model.ErrCodeConfigDataInvalid, err.Error())
	default:
		logx.Error("配置上传失败",
			zap.String("requestId", requestID),
			zap.String("table", table),
			zap.String("releaseId", releaseID.Hex()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "upload failed")
	}
}

// handlePublishError 将发布错误映射为 AppError
func (h *ConfigHandler) handlePublishError(err error, requestID, operator string, releaseID bson.ObjectID) error {
	switch {
	case errors.Is(err, model.ErrConfigReleaseNotFound):
		return httpx.NewAppError(model.ErrCodeConfigNotFound, "release not found")
	case errors.Is(err, model.ErrConfigReleaseTerminated):
		return httpx.NewAppError(model.ErrCodeConfigReleaseStatusConflict, "release is already published")
	case errors.Is(err, model.ErrConfigCannotPublish):
		return httpx.NewAppError(model.ErrCodeConfigReleaseStatusConflict, err.Error())
	case errors.Is(err, model.ErrConfigNothingToPublish):
		return httpx.NewAppError(model.ErrCodeConfigNothingToPublish, err.Error())
	default:
		logx.Error("配置发布失败",
			zap.String("requestId", requestID),
			zap.String("operator", operator),
			zap.String("releaseId", releaseID.Hex()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeConfigInternal, "publish failed")
	}
}
