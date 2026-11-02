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

// FriendHandler 好友系统相关的HTTP处理器
// 所有接口均需 JWT 鉴权，操作者身份一律取自认证上下文，禁止客户端伪造
type FriendHandler struct {
	friendLogic *logic.FriendLogic
}

// NewFriendHandler 创建好友系统处理器
func NewFriendHandler(friendLogic *logic.FriendLogic) *FriendHandler {
	return &FriendHandler{friendLogic: friendLogic}
}

// Register 实现 httpx.Router 接口，注册好友系统路由
func (h *FriendHandler) Register(r httpx.RouterGroup) {
	friendGroup := r.Group("/api/friend")
	friendGroup.Post("/apply", h.Apply)
	friendGroup.Post("/apply/handle", h.HandleApply)
	friendGroup.Post("/apply/cancel", h.CancelApply)
	friendGroup.Get("/apply/in", h.ApplyIn)
	friendGroup.Get("/apply/out", h.ApplyOut)
	friendGroup.Get("/list", h.FriendList)
	friendGroup.Post("/rank", h.FriendRank)
	friendGroup.Post("/delete", h.DeleteFriend)
}

// Apply 发送好友申请
// POST /api/friend/apply
func (h *FriendHandler) Apply(c httpx.Context) error {
	req, err := httpx.Bind[cspb.FriendApplyReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.GetTargetUid() <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "targetUid is required")
	}

	if err := h.friendLogic.Apply(c.Context(), req, requestID, playerID); err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, &cspb.FriendApplyResp{Code: 0, Message: "ok"})
}

// HandleApply 同意/拒绝好友申请
// POST /api/friend/apply/handle
func (h *FriendHandler) HandleApply(c httpx.Context) error {
	req, err := httpx.Bind[cspb.FriendApplyHandleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.GetApplyUid() <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "applyUid is required")
	}

	result, err := h.friendLogic.HandleApply(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// CancelApply 撤销我发出的申请
// POST /api/friend/apply/cancel
func (h *FriendHandler) CancelApply(c httpx.Context) error {
	req, err := httpx.Bind[cspb.FriendApplyCancelReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.GetTargetUid() <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "targetUid is required")
	}

	result, err := h.friendLogic.CancelApply(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// ApplyIn 获取收到的申请列表
// GET /api/friend/apply/in?start=1&end=20
func (h *FriendHandler) ApplyIn(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.FriendApplyListReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.friendLogic.ApplyIn(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// ApplyOut 获取发出的申请列表
// GET /api/friend/apply/out?start=1&end=20
func (h *FriendHandler) ApplyOut(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.FriendApplyListReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.friendLogic.ApplyOut(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// FriendList 获取好友列表
// GET /api/friend/list?start=1&end=20
func (h *FriendHandler) FriendList(c httpx.Context) error {
	req, err := httpx.BindQuery[cspb.FriendListReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}

	result, err := h.friendLogic.FriendList(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// FriendRank 好友排行榜（所有排行均在好友范围内，按分数倒序，同级按玩家ID升序，名次按位置连续编号，包含本人）
// POST /api/friend/rank
// 请求体 type 指定排行维度（当前支持等级排行，后续可扩展其他维度）；
// friends 为额外好友账号列表（account，可选），传入时由 logic 层解析出对应玩家并入排行榜候选（超过 100 个只取前 100 个）；
// 具体排行数据按类型在 logic 层分派获取，此处仅校验 type 必须为已定义类型（非 Unknown）
func (h *FriendHandler) FriendRank(c httpx.Context) error {
	req, err := httpx.Bind[cspb.FriendRankReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	// 未指定或未知的排行维度直接拒绝，避免把无意义的类型透传到 logic 层
	if req.GetType() == cspb.RankType_RankTypeUnknown {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid rank type")
	}
	result, err := h.friendLogic.FriendRank(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// DeleteFriend 删除好友
// POST /api/friend/delete
func (h *FriendHandler) DeleteFriend(c httpx.Context) error {
	req, err := httpx.Bind[cspb.FriendDeleteReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid request parameters")
	}
	requestID := c.RequestID()
	playerID := c.PlayerID()
	if playerID == 0 {
		return httpx.NewAppError(model.ErrCodeUnauthorized, "missing authorization token")
	}
	if req.GetFriendUid() <= 0 {
		return httpx.NewAppError(model.ErrCodeBadRequest, "friendUid is required")
	}

	result, err := h.friendLogic.DeleteFriend(c.Context(), req, requestID, playerID)
	if err != nil {
		return h.handleFriendError(err, requestID, playerID)
	}
	return httpx.OK(c, result)
}

// handleFriendError 将好友业务错误映射为对应的 AppError 响应
func (h *FriendHandler) handleFriendError(err error, requestID string, playerID int64) error {
	switch {
	case errors.Is(err, model.ErrFriendSelfAdd):
		return httpx.NewAppError(model.ErrCodeFriendSelfAdd, "cannot add yourself as friend")
	case errors.Is(err, model.ErrFriendAlreadyExists):
		return httpx.NewAppError(model.ErrCodeFriendAlreadyExists, "already friends")
	case errors.Is(err, model.ErrFriendTargetNotFound):
		return httpx.NewAppError(model.ErrCodeFriendTargetNotFound, "target player not found")
	case errors.Is(err, model.ErrFriendApplyAlreadyPending):
		return httpx.NewAppError(model.ErrCodeFriendApplyAlreadyPending, "apply already pending")
	case errors.Is(err, model.ErrFriendApplyNotFound):
		return httpx.NewAppError(model.ErrCodeFriendApplyNotFound, "apply not found")
	case errors.Is(err, model.ErrFriendApplyReceived):
		return httpx.NewAppError(model.ErrCodeFriendApplyReceived, "you already have an apply from this player")
	case errors.Is(err, model.ErrRankTypeUnsupported):
		return httpx.NewAppError(model.ErrCodeBadRequest, "invalid rank type")
	case errors.Is(err, model.ErrFriendCountLimitExceeded):
		return httpx.NewAppError(model.ErrCodeFriendCountLimitExceeded, "friend count limit exceeded")
	case errors.Is(err, model.ErrFriendOperationFailed):
		return httpx.NewAppError(model.ErrCodeFriendOperationFailed, "friend operation failed")
	default:
		logx.Error("好友操作未知错误",
			zap.String("requestId", requestID),
			zap.Int64("userId", playerID),
			zap.Error(err),
		)
		return httpx.NewAppError(model.ErrCodeInternal, "internal server error")
	}
}
