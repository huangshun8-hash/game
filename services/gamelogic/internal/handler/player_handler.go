package handler

import (
	"errors"

	"kemiluo/pb/cspb"
	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gamelogic/internal/logic"
	"kemiluo/services/gamelogic/internal/model"

	"go.uber.org/zap"
)

// PlayerHandler 玩家查询处理器
type PlayerHandler struct {
	playerLogic *logic.PlayerLogic
}

// NewPlayerHandler 创建玩家查询处理器
func NewPlayerHandler(playerLogic *logic.PlayerLogic) *PlayerHandler {
	return &PlayerHandler{playerLogic: playerLogic}
}

// Register 注册玩家相关路由
func (h *PlayerHandler) Register(r httpx.RouterGroup) {
	playerGroup := r.Group("/api/player")
	playerGroup.Post("/query", h.Query)
}

// Query 按玩家ID或邀请码查询玩家基本信息。
// POST /api/player/query
//
// 参数：
//   - c: 请求上下文，包含请求ID与调用方玩家ID
//
// 返回：玩家基本信息（玩家ID、昵称、等级）
func (h *PlayerHandler) Query(c httpx.Context) error {
	req, err := httpx.Bind[cspb.PlayerQueryReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.playerLogic.Query(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handlePlayerError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// handlePlayerError 将玩家查询业务错误转换为客户端响应错误。
// 参数：
//   - err: 逻辑层返回的业务错误
//   - requestID: 请求追踪ID
//   - playerID: 调用方玩家ID
//
// 返回：客户端可读的业务错误
func (h *PlayerHandler) handlePlayerError(err error, requestID string, playerID int64) error {
	switch {
	case errors.Is(err, model.ErrPlayerQueryParamMissing):
		return httpx.NewAppError(model.ErrCodePlayerQueryInvalidParam, "playerId or inviteCode is required")
	case errors.Is(err, model.ErrPlayerQueryInvalidCode):
		return httpx.NewAppError(model.ErrCodePlayerQueryInvalidCode, "invalid inviteCode")
	case errors.Is(err, model.ErrPlayerNotFound):
		return httpx.NewAppError(model.ErrCodePlayerNotFound, "player not found")
	case errors.Is(err, model.ErrPlayerQueryFailed):
		return httpx.NewAppError(model.ErrCodePlayerQueryFailed, "player query failed")
	default:
		logx.Error("玩家查询未知错误",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
