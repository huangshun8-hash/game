package model

import "errors"

// 文章管理业务错误码（3800-3899）
const (
	// ErrCodeArticleBadRequest 请求参数错误
	ErrCodeArticleBadRequest = 3800
	// ErrCodeArticleNotFound 文章不存在
	ErrCodeArticleNotFound = 3804
	// ErrCodeArticleInternal 文章服务内部错误
	ErrCodeArticleInternal = 3850
)

var (
	// ErrArticleNotFound 文章不存在
	ErrArticleNotFound = errors.New("article not found")
)
