package model

import (
	"kemiluo/services/shared/sharedmodel"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 登录渠道常量（产品维度，用于渠道归因，存入数据库）
// 与 gamelogic/internal/model/player.go 保持一致
const (
	// LoginTypeGuest 游客渠道
	LoginTypeGuest = "guest"
	// LoginTypeYre yre 渠道（SDK 自行完成登录认证的免验证渠道）
	LoginTypeYre = "yre"
)

// directAccountChannels 免验证渠道白名单：SDK 已自行完成登录认证，无需服务端验证 token，
// 服务端直接信任客户端上报的第三方账号（account）。接入此类渠道时在此登记 loginType；
// 未登记渠道一律拒绝，避免新渠道误配导致"无验证放行"。
var directAccountChannels = map[string]bool{
	LoginTypeYre: true,
}

// IsDirectAccountChannel 判断渠道是否为免验证渠道（白名单内）
func IsDirectAccountChannel(loginType string) bool {
	return directAccountChannels[loginType]
}

// thirdYreRequiredChannels 强制三方数据（thirdYre）签名校验的渠道白名单。
// 登记于此的渠道在认证时必须携带 thirdYre 且通过 md5(siteCode + t + key + uid) == ck 校验，
// 缺失或校验失败一律拒绝；未登记渠道即便携带 thirdYre 也仅做尽力校验（携带才校验）。
// 新增需强制三方验签的渠道时在此登记 loginType。
var thirdYreRequiredChannels = map[string]bool{
	LoginTypeYre: true,
}

// IsThirdYreRequired 判断渠道是否强制要求 thirdYre 签名校验
func IsThirdYreRequired(loginType string) bool {
	return thirdYreRequiredChannels[loginType]
}

// 账号状态常量
const (
	// StatusNormal 正常
	StatusNormal = 0
	// StatusBanned 封禁
	StatusBanned = 1
	// StatusDeactivated 已注销（登录身份已作废，账号永久保留但不可再登录）
	StatusDeactivated = 2
)

// ClientInfo 客户端设备信息（首次注册时由 gamelogic 透传）
// 复用 services/shared/sharedmodel 统一定义，避免与 gamelogic 侧重复声明导致字段漂移
type ClientInfo = sharedmodel.ClientInfo

// Account 账号身份记录（仅管理认证身份，不含游戏数据）
type Account struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// PlayerID 玩家唯一标识（自增，统一由平台服务生成）
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// OpenID 开放ID（全局唯一标识）
	OpenID string `bson:"openId" json:"openId"`
	// LoginType 业务渠道标签：guest / channel1 / channel2 ...
	LoginType string `bson:"loginType" json:"loginType"`
	// Platform 客户端平台：ios / android
	Platform string `bson:"platform" json:"platform"`
	// Status 账号状态：0=正常 1=封禁
	Status int `bson:"status" json:"status"`
	// ClientInfo 首次注册时的客户端设备信息
	ClientInfo ClientInfo `bson:"clientInfo" json:"clientInfo"`
	// LastLoginAt 最后登录时间
	LastLoginAt time.Time `bson:"lastLoginAt" json:"lastLoginAt"`
	// CreatedAt 创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// IsBanned 返回账号是否被封禁
func (a *Account) IsBanned() bool {
	return a.Status == StatusBanned
}
