package logic

import (
	"context"
	"errors"

	"kemiluo/pkg/async"
	"kemiluo/pkg/mongox"
	"kemiluo/services/gmt/internal/dto"
	"kemiluo/services/gmt/internal/model"
	"kemiluo/services/gmt/internal/repo"
)

// ArticleLogic 文章增删改查
type ArticleLogic struct {
	mongoSvc *mongox.Service
	asyncSvc *async.Service
}

// NewArticleLogic 创建文章业务逻辑。mongoSvc 提供到 MongoDB 的连接，文章实际写入 test 库。
func NewArticleLogic(mongoSvc *mongox.Service, asyncSvc *async.Service) *ArticleLogic {
	return &ArticleLogic{mongoSvc: mongoSvc, asyncSvc: asyncSvc}
}

// Create 创建文章，返回带自增 ID 和创建时间的文章
func (l *ArticleLogic) Create(ctx context.Context, req *dto.CreateArticleReq, operator string) (*dto.ArticleResp, error) {
	article := &model.Article{
		Title:   req.Title,
		Content: req.Content,
		Status:  req.Status,
	}
	articleRepo := repo.NewArticleRepo(l.mongoSvc.Client())
	if err := articleRepo.InsertArticle(ctx, article); err != nil {
		return nil, err
	}
	l.logOperationAsync(operator, "Create", "POST", "/api/article/create", req.RequestID(), req)
	return toArticleResp(article), nil
}

// Update 更新文章标题、正文和状态
func (l *ArticleLogic) Update(ctx context.Context, req *dto.UpdateArticleReq, operator string) (*dto.ArticleResp, error) {
	articleRepo := repo.NewArticleRepo(l.mongoSvc.Client())
	current, err := articleRepo.FindByArticleID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return nil, model.ErrArticleNotFound
		}
		return nil, err
	}
	current.Title = req.Title
	current.Content = req.Content
	current.Status = req.Status
	if err := articleRepo.UpdateArticle(ctx, current); err != nil {
		return nil, err
	}
	l.logOperationAsync(operator, "Update", "POST", "/api/article/update", req.RequestID(), req)
	return toArticleResp(current), nil
}

// Delete 按 ID 删除文章
func (l *ArticleLogic) Delete(ctx context.Context, req *dto.DeleteArticleReq, operator string) error {
	articleRepo := repo.NewArticleRepo(l.mongoSvc.Client())
	if err := articleRepo.DeleteByArticleID(ctx, req.ID); err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return model.ErrArticleNotFound
		}
		return err
	}
	l.logOperationAsync(operator, "Delete", "POST", "/api/article/delete", req.RequestID(), req)
	return nil
}

// Get 按 ID 查询文章
func (l *ArticleLogic) Get(ctx context.Context, req *dto.GetArticleReq) (*dto.ArticleResp, error) {
	article, err := repo.NewArticleRepo(l.mongoSvc.Client()).FindByArticleID(ctx, req.ID)
	if err != nil {
		if errors.Is(err, mongox.ErrNotFound) {
			return nil, model.ErrArticleNotFound
		}
		return nil, err
	}
	return toArticleResp(article), nil
}

// List 分页查询文章，按创建时间倒序。req.Title 非空时按标题子串过滤。
func (l *ArticleLogic) List(ctx context.Context, req *dto.ListArticleReq) (*dto.ListArticleResp, error) {
	page, err := repo.NewArticleRepo(l.mongoSvc.Client()).ListArticles(ctx, req.Page, req.PageSize, req.Title)
	if err != nil {
		return nil, err
	}
	items := make([]dto.ArticleResp, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, *toArticleResp(&page.Items[i]))
	}
	return &dto.ListArticleResp{
		Items:    items,
		Total:    page.Total,
		Page:     page.Page,
		PageSize: page.PageSize,
	}, nil
}

func toArticleResp(article *model.Article) *dto.ArticleResp {
	return &dto.ArticleResp{
		ID:        article.ID,
		Title:     article.Title,
		Content:   article.Content,
		CreatedAt: article.CreatedAt,
		Status:    article.Status,
	}
}

// logOperationAsync 异步写入 GMT 操作日志，失败不影响文章主流程
func (l *ArticleLogic) logOperationAsync(uid, action, method, uri, requestID string, reqData any) {
	if l.asyncSvc == nil {
		return
	}
	mongoSvc := l.mongoSvc
	ctx := async.NewContext().
		Set("uid", uid).
		Set("module", "Article").
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
