package handler

import (
	"errors"
	"strings"
	"unicode/utf8"

	"kemiluo/pkg/httpx"
	"kemiluo/pkg/logx"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/logic"
	"kemiluo/services/gmt/internal/model"

	"go.uber.org/zap"
)

// ArticleHandler 文章管理 HTTP 处理器
type ArticleHandler struct {
	articleLogic *logic.ArticleLogic
}

// NewArticleHandler 创建文章处理器
func NewArticleHandler(articleLogic *logic.ArticleLogic) *ArticleHandler {
	return &ArticleHandler{articleLogic: articleLogic}
}

// Register 注册文章增删改查路由。路由走全局 JWT 鉴权。
func (h *ArticleHandler) Register(r httpx.RouterGroup) {
	g := r.Group("/api/article")
	g.Post("/create", h.Create)
	g.Post("/update", h.Update)
	g.Post("/delete", h.Delete)
	g.Get("/get", h.Get)
	g.Get("/list", h.List)
}

// Create 创建文章
// POST /api/article/create
func (h *ArticleHandler) Create(c httpx.Context) error {
	req, err := httpx.Bind[dto.CreateArticleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "invalid request parameters")
	}
	req.Title = strings.TrimSpace(req.Title)
	if err := validateArticleFields(req.Title, req.Content, req.Status); err != nil {
		return err
	}

	operator := c.UserID()
	result, err := h.articleLogic.Create(c.Context(), req, operator)
	if err != nil {
		return h.handleError(err, req.RequestID(), operator)
	}
	return httpx.OK(c, result)
}

// Update 更新文章
// POST /api/article/update
func (h *ArticleHandler) Update(c httpx.Context) error {
	req, err := httpx.Bind[dto.UpdateArticleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "invalid request parameters")
	}
	if req.ID <= 0 {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "id is required")
	}
	req.Title = strings.TrimSpace(req.Title)
	if err := validateArticleFields(req.Title, req.Content, req.Status); err != nil {
		return err
	}

	operator := c.UserID()
	result, err := h.articleLogic.Update(c.Context(), req, operator)
	if err != nil {
		return h.handleError(err, req.RequestID(), operator)
	}
	return httpx.OK(c, result)
}

// Delete 删除文章
// POST /api/article/delete
func (h *ArticleHandler) Delete(c httpx.Context) error {
	req, err := httpx.Bind[dto.DeleteArticleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "invalid request parameters")
	}
	if req.ID <= 0 {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "id is required")
	}

	operator := c.UserID()
	if err := h.articleLogic.Delete(c.Context(), req, operator); err != nil {
		return h.handleError(err, req.RequestID(), operator)
	}
	return httpx.OK(c, nil)
}

// Get 按 ID 查询文章
// GET /api/article/get?id=
func (h *ArticleHandler) Get(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.GetArticleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "invalid request parameters")
	}
	if req.ID <= 0 {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "id is required")
	}

	result, err := h.articleLogic.Get(c.Context(), req)
	if err != nil {
		return h.handleError(err, req.RequestID(), c.UserID())
	}
	return httpx.OK(c, result)
}

// List 分页查询文章，可按标题关键字过滤
// GET /api/article/list?page=&pageSize=&title=
func (h *ArticleHandler) List(c httpx.Context) error {
	req, err := httpx.BindQuery[dto.ListArticleReq](c)
	if err != nil {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "invalid request parameters")
	}
	req.Title = strings.TrimSpace(req.Title)
	if utf8.RuneCountInString(req.Title) > model.ArticleTitleMaxLen {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "title must not exceed 255 characters")
	}

	result, err := h.articleLogic.List(c.Context(), req)
	if err != nil {
		return h.handleError(err, req.RequestID(), c.UserID())
	}
	return httpx.OK(c, result)
}

// validateArticleFields 校验标题、正文和状态。title 需已去掉首尾空白。
func validateArticleFields(title, content string, status int) error {
	if title == "" {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "title is required")
	}
	if utf8.RuneCountInString(title) > model.ArticleTitleMaxLen {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "title must not exceed 255 characters")
	}
	if utf8.RuneCountInString(content) > model.ArticleContentMaxLen {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "content must not exceed 65535 characters")
	}
	if status < model.ArticleStatusMin || status > model.ArticleStatusMax {
		return httpx.NewAppError(model.ErrCodeArticleBadRequest, "status must be between 0 and 255")
	}
	return nil
}

// handleError 将文章业务错误转换为统一响应
func (h *ArticleHandler) handleError(err error, requestID, operatorID string) error {
	if errors.Is(err, model.ErrArticleNotFound) {
		return httpx.NewAppError(model.ErrCodeArticleNotFound, "article not found")
	}
	logx.Error("文章操作失败",
		zap.String("requestId", requestID),
		zap.String("userId", operatorID),
		zap.Error(err),
	)
	return httpx.NewAppError(model.ErrCodeArticleInternal, "article service error")
}
