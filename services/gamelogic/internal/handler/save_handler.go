package handler

import (
	"errors"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/redisx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"
	"kemiluo/services/shared/gmtdto"

	"go.uber.org/zap"
)

const (
	// maxSaveContentBytes 单次上传存档内容的最大字节数（1MB）
	maxSaveContentBytes = 1 << 20
	// maxSaveKeyLen 存档分类标识 key 的最大长度，限制为 protobuf 标识符长度。
	// Player 存档应使用其直接字段名作为 key，例如 base、item、board。
	maxSaveKeyLen = 64
	// maxDownloadKeys 单次下载请求允许的最大 key 数量，防止超大 $in 查询
	maxDownloadKeys = 50
	// maxGmtUploadItems 单次 GMT 批量保存允许的最大条目数
	maxGmtUploadItems = 50
)

// isValidSaveKey 校验存档 key 合法性：非空、长度受限、仅允许标识符字符（字母/数字/下划线）。
// 此处只校验标识符语法；Player 存档 key 的语义以 Player 直接字段名为准。
func isValidSaveKey(key string) bool {
	if key == "" || len(key) > maxSaveKeyLen {
		return false
	}
	for _, r := range key {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != '_' {
			return false
		}
	}
	return true
}

// dedupKeys 按首次出现顺序去重 key 列表，避免重复 key 导致的重复查询与重复响应项
func dedupKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	result := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		result = append(result, k)
	}
	return result
}

// SaveHandler 存档相关的HTTP处理器
type SaveHandler struct {
	saveLogic *logic.SaveLogic
	deps      *Deps
}

// NewSaveHandler 创建存档处理器
func NewSaveHandler(saveLogic *logic.SaveLogic, deps *Deps) *SaveHandler {
	return &SaveHandler{saveLogic: saveLogic, deps: deps}
}

// Register 实现 httpx.Router 接口，注册存档相关路由
func (h *SaveHandler) Register(r httpx.RouterGroup) {
	saveGroup := r.Group("/api/save")
	saveGroup.Post("/upload", h.Upload)
	saveGroup.Post("/download", h.Download)

	gmtGroup := r.Group("/gmt/save")
	gmtGroup.Post("/download", h.GmtDownload)
	gmtGroup.Post("/upload", h.GmtUpload)
}

// Upload 存档上传接口
// POST /api/save/upload
func (h *SaveHandler) Upload(c httpx.Context) error {
	req, err := httpx.Bind[cspb.SaveUploadReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.NewVersion <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "newVersion must be greater than 0")
	}
	if !isValidSaveKey(req.Key) {
		return httpx.NewAppError(model.ErrCodeBadRequest, "key is required and must be a valid identifier")
	}
	if req.Sign == "" {
		return httpx.NewAppError(model.ErrCodeBadRequest, "sign is required")
	}
	if len(req.Content) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "content is required")
	}
	if len(req.Content) > maxSaveContentBytes {
		return httpx.NewAppError(model.ErrCodeBadRequest, "content exceeds size limit")
	}
	if req.BaseVersion < 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "baseVersion must be non-negative")
	}
	// 此处不校验 newVersion > baseVersion：首次上传时服务端尚无该 key 存档，baseVersion 不代表服务端
	// 状态，客户端可能携带本地版本号；强制覆盖时 baseVersion 也不参与校验。版本单调性由 logic 层按存档
	// 是否已存在分别校验（普通更新要求 newVersion > baseVersion，强制覆盖要求 newVersion > 当前版本）。

	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.saveLogic.Upload(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleUploadError(c, err, requestID)
	}
	return httpx.OK(c, result)
}

// Download 存档下载接口
// POST /api/save/download
func (h *SaveHandler) Download(c httpx.Context) error {
	req, err := httpx.Bind[cspb.SaveDownloadReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if len(req.Keys) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "keys is required")
	}
	// 先去重，再校验数量与合法性
	req.Keys = dedupKeys(req.Keys)
	if len(req.Keys) > maxDownloadKeys {
		return httpx.NewAppError(model.ErrCodeBadRequest, "keys count exceeds limit")
	}
	for _, key := range req.Keys {
		if !isValidSaveKey(key) {
			return httpx.NewAppError(model.ErrCodeBadRequest, "keys contains invalid key")
		}
	}

	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.saveLogic.Download(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleDownloadError(err, requestID)
	}
	return httpx.OK(c, result)
}

// handleUploadError 处理上传接口的错误，冲突时返回含 currentVersion 的响应
func (h *SaveHandler) handleUploadError(c httpx.Context, err error, requestID string) error {
	if ce, ok := logic.IsSaveConflict(err); ok {
		return httpx.OK(c, &cspb.SaveUploadResp{
			Code:    model.ErrCodeSaveConflict,
			Message: "save conflict",
			Data:    &cspb.SaveUploadData{CurrentVersion: ce.CurrentVersion()},
		})
	}
	if logic.IsVersionNotMonotonic(err) {
		return httpx.NewAppError(model.ErrCodeBadRequest, "newVersion must be greater than baseVersion")
	}
	logx.Error("存档上传内部错误", zap.String("requestId", requestID), zap.Error(err))
	return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
}

// handleDownloadError 处理下载接口的错误
func (h *SaveHandler) handleDownloadError(err error, requestID string) error {
	switch {
	case errors.Is(err, model.ErrSaveNotFound):
		return httpx.NewAppError(model.ErrCodeSaveNotFound, "save not found")
	default:
		logx.Error("存档下载内部错误", zap.String("requestId", requestID), zap.Error(err))
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}

// GmtDownload GMT查询玩家存档数据
// POST /gmt/save/download
// 内部接口，需验证服务间签名
func (h *SaveHandler) GmtDownload(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtSaveDownloadReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.PlayerID == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerId is required")
	}
	// keys 可选，指定时去重并限制数量
	if len(req.Keys) > 0 {
		req.Keys = dedupKeys(req.Keys)
		if len(req.Keys) > maxDownloadKeys {
			return httpx.NewAppError(model.ErrCodeBadRequest, "keys count exceeds limit")
		}
	}

	requestID := c.RequestID()
	result, err := h.saveLogic.GmtDownload(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT查询玩家存档失败",
			zap.String("requestId", requestID),
			zap.Int64("playerId", req.PlayerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// GmtUpload GMT批量保存玩家存档数据
// POST /gmt/save/upload
// 内部接口，需验证服务间签名
func (h *SaveHandler) GmtUpload(c httpx.Context) error {
	if err := h.verifyInternalSignature(c); err != nil {
		return err
	}

	req, err := httpx.Bind[gmtdto.GmtSaveUploadReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	if req.PlayerID == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "playerId is required")
	}
	if len(req.Items) == 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "items is required")
	}
	if len(req.Items) > maxGmtUploadItems {
		return httpx.NewAppError(model.ErrCodeBadRequest, "items count exceeds limit")
	}

	requestID := c.RequestID()
	result, err := h.saveLogic.GmtUpload(c.Context(), req, requestID)
	if err != nil {
		logx.Error("GMT保存玩家存档失败",
			zap.String("requestId", requestID),
			zap.Int64("playerId", req.PlayerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
	return httpx.OK(c, result)
}

// verifyInternalSignature 校验来自 GMT 的内部服务签名
func (h *SaveHandler) verifyInternalSignature(c httpx.Context) error {
	var redisCli *redisx.Client
	if h.deps.RedisSvc != nil {
		redisCli = h.deps.RedisSvc.Client()
	}
	return httpx.VerifyInternalSignature(c, []byte(h.deps.Config.InternalSecret), redisCli)
}
