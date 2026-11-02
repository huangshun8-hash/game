// Package sharedmodel 定义跨服务共享的业务领域模型。
// 这些类型属于业务层（非框架基础设施），被 platform、gamelogic 等多个服务共同依赖，
// 统一在此声明，避免在各服务的 internal/model 中重复定义导致字段漂移。
package sharedmodel

// ClientInfo 客户端设备信息
// 由客户端在登录时上报，gamelogic 透传给 platform 用于账号维度的注册信息留存，
// 同时 gamelogic 自身也会持久化到玩家维度。两侧序列化字段必须保持完全一致。
type ClientInfo struct {
	// DeviceID 设备唯一标识
	DeviceID string `bson:"deviceId" json:"deviceId"`
	// DeviceName 设备名称
	DeviceName string `bson:"deviceName" json:"deviceName"`
	// Brand 设备品牌
	Brand string `bson:"brand" json:"brand"`
	// Model 设备型号
	Model string `bson:"model" json:"model"`
	// OS 操作系统
	OS string `bson:"os" json:"os"`
	// OSVersion 系统版本
	OSVersion string `bson:"osVersion" json:"osVersion"`
	// Language 语言
	Language string `bson:"language" json:"language"`
	// Country 国家/地区
	Country string `bson:"country" json:"country"`
	// Timezone 时区
	Timezone string `bson:"timezone" json:"timezone"`
	// Network 网络类型
	Network string `bson:"network" json:"network"`
	// ScreenWidth 屏幕宽度
	ScreenWidth int `bson:"screenWidth" json:"screenWidth"`
	// ScreenHeight 屏幕高度
	ScreenHeight int `bson:"screenHeight" json:"screenHeight"`
	// ClientVersion 客户端版本号
	ClientVersion string `bson:"clientVersion" json:"clientVersion"`
	// BundleVersion 构建版本号
	BundleVersion string `bson:"bundleVersion" json:"bundleVersion"`
	// Channel 渠道
	Channel string `bson:"channel" json:"channel"`
	// SDKVersion SDK版本
	SDKVersion string `bson:"sdkVersion" json:"sdkVersion"`
	// PackageName 包名
	PackageName string `bson:"packageName" json:"packageName"`
	// InstallID 安装标识
	InstallID string `bson:"installId" json:"installId"`
	// IsEmulator 是否模拟器
	IsEmulator bool `bson:"isEmulator" json:"isEmulator"`
}
