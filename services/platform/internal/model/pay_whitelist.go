package model

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// PayWhitelist 充值白名单记录（按账号 openId 维度存取）。
// 被列入白名单的账号在 Platform 下单时会直接支付完成（无需真实扣款），便于测试/联调。
// 只记录 openId 与创建时间，不记录多余字段。
// 说明：注销重登后重新注册的账号会复用相同的原始 openId，故白名单按 openId 存储即可在注销重登后依然生效。
type PayWhitelist struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// OpenID 白名单账号（全局唯一标识，唯一键）
	OpenID string `bson:"openId" json:"openId"`
	// CreatedAt 创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
}
