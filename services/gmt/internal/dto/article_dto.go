package dto

import "kemiluo/pkg/httpx"

// CreateArticleReq 创建文章请求
type CreateArticleReq struct {
	httpx.BaseReq
	// Title 标题
	Title string `json:"title"`
	// Content 正文
	Content string `json:"content"`
	// Status 状态，取值 0-255
	Status int `json:"status"`
}

// UpdateArticleReq 更新文章请求
type UpdateArticleReq struct {
	httpx.BaseReq
	// ID 文章 ID
	ID int64 `json:"id"`
	// Title 标题
	Title string `json:"title"`
	// Content 正文
	Content string `json:"content"`
	// Status 状态，取值 0-255
	Status int `json:"status"`
}

// DeleteArticleReq 删除文章请求
type DeleteArticleReq struct {
	httpx.BaseReq
	// ID 文章 ID
	ID int64 `json:"id"`
}

// GetArticleReq 按 ID 查询文章请求
type GetArticleReq struct {
	httpx.BaseReq
	// ID 文章 ID
	ID int64 `query:"id"`
}

// ListArticleReq 分页查询文章请求
type ListArticleReq struct {
	httpx.BaseReq
	// Page 页码，从 1 开始
	Page int64 `query:"page"`
	// PageSize 每页条数
	PageSize int64 `query:"pageSize"`
	// Title 标题关键字，按子串匹配，不区分大小写；为空时不过滤
	Title string `query:"title"`
}

// ArticleResp 文章响应
type ArticleResp struct {
	// ID 文章 ID
	ID int64 `json:"id"`
	// Title 标题
	Title string `json:"title"`
	// Content 正文
	Content string `json:"content"`
	// CreatedAt 创建时间，Unix 毫秒
	CreatedAt int64 `json:"createdAt"`
	// Status 状态
	Status int `json:"status"`
}

// ListArticleResp 文章分页响应
type ListArticleResp struct {
	// Items 当前页文章
	Items []ArticleResp `json:"items"`
	// Total 总条数
	Total int64 `json:"total"`
	// Page 当前页码
	Page int64 `json:"page"`
	// PageSize 每页条数
	PageSize int64 `json:"pageSize"`
}
