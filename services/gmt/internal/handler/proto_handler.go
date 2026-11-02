package handler

import (
	"fmt"
	"io"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"
	"strings"

	"go.uber.org/zap"
)

// ProtoHandler Proto动态数据管理HTTP处理器
type ProtoHandler struct {
	protoLogic *logic.ProtoLogic
}

// maxProtoFileSize Proto文件上传最大字节数（1MB）
const maxProtoFileSize = 1 << 20

// maxSavePlayerDataItems 单次批量保存玩家分片数据的最大条目数，防止超大请求
const maxSavePlayerDataItems = 50

// NewProtoHandler 创建Proto处理器
func NewProtoHandler(protoLogic *logic.ProtoLogic) *ProtoHandler {
	return &ProtoHandler{protoLogic: protoLogic}
}

// Register 实现 httpx.Router 接口，注册Proto相关路由
func (h *ProtoHandler) Register(r httpx.RouterGroup) {
	g := r.Group("/api/proto")
	g.Post("/upload", h.Upload)
	g.Get("/exists", h.Exists)
	g.Get("/playerData/get", h.GetPlayerData)
	g.Post("/playerData/save", h.SavePlayerData)
}

// Exists 查询Proto模板是否存在
// GET /api/proto/exists
// 返回全局唯一Proto模板的基本信息，若不存在则返回 exists=false
func (h *ProtoHandler) Exists(c httpx.Context) error {
	result, err := h.protoLogic.Exists(c.Context())
	if err != nil {
		logx.Error("查询Proto模板是否存在失败",
			zap.String("requestId", c.RequestID()),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeProtoInternal, "failed to check proto existence")
	}
	return httpx.OK(c, result)
}

// Upload 上传Proto文件
// POST /api/proto/upload
// Content-Type: multipart/form-data
// 表单字段: protoName(模板名称), file(Proto文件)
func (h *ProtoHandler) Upload(c httpx.Context) error {
	protoName := c.FormValue("protoName")
	if protoName == "" {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "protoName is required")
	}

	// 读取上传的文件
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "file is required")
	}

	if fileHeader.Size > maxProtoFileSize {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "file size exceeds 1MB limit")
	}

	file, err := fileHeader.Open()
	if err != nil {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "failed to open uploaded file")
	}
	defer file.Close()

	// 读取文件内容：必须使用 io.ReadFull 确保读满，单次 Read 不保证读取完整内容（大文件落磁盘临时文件时可能只读到部分字节）
	buf := make([]byte, fileHeader.Size)
	if _, err := io.ReadFull(file, buf); err != nil {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "failed to read uploaded file")
	}
	content := string(buf)

	if strings.TrimSpace(content) == "" {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "file content is empty")
	}

	operator := h.operator(c)
	requestID := c.RequestID()

	result, err := h.protoLogic.Upload(c.Context(), protoName, content, requestID, operator)
	fmt.Println(1, result)
	if err != nil {
		return h.handleUploadError(err, requestID, operator, protoName)
	}
	return httpx.OK(c, result)
}

// handleUploadError 处理上传错误，映射为对应的业务错误码
func (h *ProtoHandler) handleUploadError(err error, requestID, operator, protoName string) error {
	msg := err.Error()
	switch {
	case msg == "IMPORT_FORBIDDEN":
		return httpx.NewAppError(model.ErrCodeProtoImportForbidden, "proto file must not contain import statements")
	case strings.HasPrefix(msg, "SYNTAX_INVALID:"):
		detail := strings.TrimPrefix(msg, "SYNTAX_INVALID:")
		return httpx.NewAppError(model.ErrCodeProtoSyntaxInvalid, "proto syntax error: "+detail)
	case msg == "NO_MESSAGE":
		return httpx.NewAppError(model.ErrCodeProtoNoMessage, "proto file must contain at least one message")
	case msg == "MD5_UNCHANGED":
		return httpx.NewAppError(model.ErrCodeProtoMD5Unchanged, "proto content unchanged, no update needed")
	case strings.HasPrefix(msg, "BREAKING_CHANGE:"):
		detail := strings.TrimPrefix(msg, "BREAKING_CHANGE:")
		return httpx.NewAppError(model.ErrCodeProtoBreakingChange, "breaking schema change detected: "+detail)
	case strings.HasPrefix(msg, "PROTO_NAME_CONFLICT:"):
		existingName := strings.TrimPrefix(msg, "PROTO_NAME_CONFLICT:")
		fmt.Println(existingName)
		return httpx.NewAppError(model.ErrCodeProtoNameConflict, "a global proto template already exists with name: "+existingName)
	default:
		logx.Error("上传Proto模板失败",
			zap.String("requestId", requestID),
			zap.String("operator", operator),
			zap.String("protoName", protoName),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeProtoInternal, "failed to upload proto")
	}
}

// GetPlayerData 查询玩家分片数据
// GET /api/proto/playerData/get?playerId=xxx&messageName=xxx
// messageName 可选：传入时返回单个分片，为空时返回所有分片
func (h *ProtoHandler) GetPlayerData(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.GetPlayerDataReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "invalid request parameters")
	}
	if req.PlayerID == 0 {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "playerId is required")
	}

	result, err := h.protoLogic.GetPlayerData(c.Context(), req, c.RequestID())
	if err != nil {
		return h.handleDataError(c, err, req.MessageName)
	}
	return httpx.OK(c, result)
}

// SavePlayerData 批量保存玩家分片数据
// POST /api/proto/playerData/save
func (h *ProtoHandler) SavePlayerData(c httpx.Context) error {
	req, err := httpx.Bind[dto.SavePlayerDataReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "invalid request parameters")
	}
	if req.PlayerID == 0 {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "playerId is required")
	}
	if len(req.Items) == 0 {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, "items is required and must not be empty")
	}
	if len(req.Items) > maxSavePlayerDataItems {
		return httpx.NewAppError(model.ErrCodeProtoBadRequest, fmt.Sprintf("items count exceeds limit of %d", maxSavePlayerDataItems))
	}
	// 校验每个item及messageName唯一性
	seenNames := make(map[string]struct{}, len(req.Items))
	for i, item := range req.Items {
		if item.MessageName == "" {
			return httpx.NewAppError(model.ErrCodeProtoBadRequest, fmt.Sprintf("items[%d].messageName is required", i))
		}
		if item.Data == nil {
			return httpx.NewAppError(model.ErrCodeProtoBadRequest, fmt.Sprintf("items[%d].data is required", i))
		}
		if _, exists := seenNames[item.MessageName]; exists {
			return httpx.NewAppError(model.ErrCodeProtoBadRequest, fmt.Sprintf("items[%d].messageName '%s' is duplicated", i, item.MessageName))
		}
		seenNames[item.MessageName] = struct{}{}
	}

	operator := h.operator(c)
	requestID := c.RequestID()

	result, err := h.protoLogic.SavePlayerData(c.Context(), req, requestID, operator)
	if err != nil {
		return h.handleDataError(c, err, "")
	}
	return httpx.OK(c, result)
}

// handleDataError 处理数据操作错误，映射为对应的业务错误码
func (h *ProtoHandler) handleDataError(c httpx.Context, err error, messageName string) error {
	msg := err.Error()
	switch {
	case msg == "PROTO_NOT_FOUND":
		return httpx.NewAppError(model.ErrCodeProtoNotFound, "proto schema not found")
	case msg == "MESSAGE_NOT_FOUND":
		return httpx.NewAppError(model.ErrCodeProtoMessageNotFound, "message not found: "+messageName)
	case msg == "PLAYER_DATA_NOT_FOUND":
		return httpx.NewAppError(model.ErrCodeProtoPlayerDataNotFound, "player data not found")
	case strings.HasPrefix(msg, "DESERIALIZE_FAILED:"):
		detail := strings.TrimPrefix(msg, "DESERIALIZE_FAILED:")
		return httpx.NewAppError(model.ErrCodeProtoDeserializeFailed, "data deserialization failed: "+detail)
	case strings.HasPrefix(msg, "SERIALIZE_FAILED:"):
		detail := strings.TrimPrefix(msg, "SERIALIZE_FAILED:")
		return httpx.NewAppError(model.ErrCodeProtoSerializeFailed, "data serialization failed: "+detail)
	default:
		logx.Error("Proto数据操作失败",
			zap.String("requestId", c.RequestID()),
			zap.String("messageName", messageName),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeProtoInternal, "internal proto service error")
	}
}

// operator 从 JWT Claims 中读取当前操作者账号名
func (h *ProtoHandler) operator(c httpx.Context) string {
	if claims := c.Claims(); claims != nil {
		return claims.UserID
	}
	return ""
}
