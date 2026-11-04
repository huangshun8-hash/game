package model

// 文章状态取值范围，对应 MySQL unsigned tinyint。
const (
	// ArticleStatusMin 状态最小值
	ArticleStatusMin = 0
	// ArticleStatusMax 状态最大值
	ArticleStatusMax = 255
	// ArticleTitleMaxLen 标题最大字符数，对应 varchar(255)
	ArticleTitleMaxLen = 255
	// ArticleContentMaxLen 正文最大字符数，对应 text
	ArticleContentMaxLen = 65535
)

// Article 文章文档，存放在 test 库的 article 集合。
// ID 为业务自增主键；MongoDB 自身的 _id 不对外返回。
type Article struct {
	// ID 文章 ID，从 1 开始自增
	ID int64 `bson:"id" json:"id"`
	// Title 标题
	Title string `bson:"title" json:"title"`
	// Content 正文
	Content string `bson:"content" json:"content"`
	// CreatedAt 创建时间，Unix 毫秒
	CreatedAt int64 `bson:"createdAt" json:"createdAt"`
	// Status 状态，取值 0-255
	Status int `bson:"status" json:"status"`
}
