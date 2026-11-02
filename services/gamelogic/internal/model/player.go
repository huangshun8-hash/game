package model

import (
	"kemiluo/services/shared/sharedmodel"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 登录渠道常量（产品维度，用于渠道归因，存入数据库）
// 与 platform/internal/model/account.go 保持一致
const (
	// LoginTypeGuest 游客渠道
	LoginTypeGuest = "guest"
)

// 账号状态常量
const (
	// StatusNormal 正常
	StatusNormal = 0
	// StatusBanned 封禁
	StatusBanned = 1
	// StatusDeactivated 已注销（登录身份已作废，Player 永久保留但不可再登录）
	StatusDeactivated = 2
)

// DefaultServerID 新玩家注册时的默认服务器ID（默认1服）
const DefaultServerID = 1

// ClientInfo 客户端信息
// 复用 services/shared/sharedmodel 统一定义，避免与 platform 侧重复声明导致字段漂移
type ClientInfo = sharedmodel.ClientInfo

// Player 逻辑服玩家实体
type Player struct {
	// ID MongoDB文档主键
	ID bson.ObjectID `bson:"_id,omitempty" json:"id"`
	// PlayerID 玩家唯一标识（跨服务引用）
	PlayerID int64 `bson:"playerId" json:"playerId"`
	// OpenID 平台开放ID（全局唯一身份标识）
	// 游客：存储客户端上报的安装 UUID；三方登录：存储平台返回的三方 openID
	// 与 platform 侧 Account.OpenID 的存储口径保持完全一致，保证可按同一字段互相查询
	OpenID string `bson:"openId" json:"openId"`
	// LoginType 业务渠道标签：guest / channel1 / channel2 ...
	LoginType string `bson:"loginType" json:"loginType"`
	// Account 第三方账号（免验证渠道上传，用于渠道归因与身份留存）
	Account string `bson:"account" json:"account"`
	// Platform 客户端平台：ios / android
	Platform string `bson:"platform" json:"platform"`
	// ServerID 所在服务器ID，新玩家注册默认1服
	ServerID int64 `bson:"serverId" json:"serverId"`
	// IsGuest 是否游客
	IsGuest bool `bson:"isGuest" json:"isGuest"`
	// SaveKey 玩家专属存档加密秘钥（注册时由服务器生成，32 字节随机值 base64 编码，登录时下发给客户端）
	SaveKey string `bson:"saveKey" json:"saveKey"`
	// ClientInfo 客户端设备信息
	ClientInfo ClientInfo `bson:"clientInfo" json:"clientInfo"`
	// Status 账号状态：0=正常 1=封禁
	Status int `bson:"status" json:"status"`
	// LastLoginAt 最后登录时间
	LastLoginAt time.Time `bson:"lastLoginAt" json:"lastLoginAt"`
	// CreatedAt 创建时间
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	// UpdatedAt 更新时间
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// IsBanned 返回玩家是否被封禁
func (p *Player) IsBanned() bool {
	return p.Status == StatusBanned
}

// DefaultNickname 返回玩家默认昵称（Base 存档未提供自定义昵称时的兜底值）。
// 默认昵称为确定性生成（Player + playerID），各消费方无需存储即可一致推导。
func DefaultNickname(playerID int64) string {
	return "Player" + strconv.FormatInt(playerID, 10)
}
