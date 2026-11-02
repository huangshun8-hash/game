package config

import (
	"fmt"
	"strings"

	"kemiluo/pkg/confx"
	"kemiluo/pkg/mongox"
)

// Config GMT管理后台配置，嵌入公共基础配置
type Config struct {
	confx.Base
	// ConfigMongo 游戏配置专用 MongoDB 连接配置，默认库名 gconfig_db，URI 与主 Mongo 共享
	ConfigMongo *mongox.Config
	// InternalSecret 内部服务间验签密钥（与 platform / gamelogic 共享，用于 GMT 调用内部接口签名）
	InternalSecret string
	// PlatformURL Platform 服务地址（GMT 转发渠道支付侧 IAP 接口到此服务）
	PlatformURL string
	// GameLogicURL GameLogic 服务地址（GMT 转发业务订单查询接口到此服务）
	GameLogicURL string
	// BootstrapAdminUsername 显式启用初始化超管时使用的账号名
	BootstrapAdminUsername string
	// BootstrapAdminPassword 显式启用初始化超管时使用的密码
	BootstrapAdminPassword string
	// BootstrapAdminNickname 显式启用初始化超管时使用的昵称
	BootstrapAdminNickname string
	// CORSAllowedOrigins 允许跨域访问的前端来源白名单（逗号分隔），为空表示不开放跨域
	CORSAllowedOrigins string
	// WebDist Vue 前端打包后的静态文件目录。为空表示不启用静态托管。
	// 启用后 GMT 同时托管后台页面，支持 SPA history 回退。
	WebDist string
	// WebProxyTarget 可选：后台 API 反向代理目标地址（如 http://localhost:8082）。
	// 为空时 /api 请求由同端口 GMT 路由处理（同源集成）；非空时将 /api 请求反向代理到该目标。
	WebProxyTarget string
}

// Load 从环境变量加载GMT服务完整配置
func Load() *Config {
	base := confx.LoadBase("GMT", 8082, "gmt_db")

	// ConfigMongo 复用主 Mongo 连接参数，仅将默认库名替换为 gconfig_db
	// 可通过环境变量 CONFIG_MONGO_DATABASE 单独覆盖库名
	configMongoCfg := *base.Mongo
	configMongoCfg.Database = confx.GetEnv("CONFIG_MONGO_DATABASE", "gconfig_db")

	return &Config{
		Base:                   base,
		ConfigMongo:            &configMongoCfg,
		InternalSecret:         confx.MustGetEnvStrict("INTERNAL_SECRET"),
		PlatformURL:            confx.GetEnv("PLATFORM_URL", "http://localhost:8080"),
		GameLogicURL:           confx.GetEnv("GAMELOGIC_URL", "http://localhost:8081"),
		BootstrapAdminUsername: confx.GetEnv("GMT_BOOTSTRAP_ADMIN_USERNAME", ""),
		BootstrapAdminPassword: confx.GetEnv("GMT_BOOTSTRAP_ADMIN_PASSWORD", ""),
		BootstrapAdminNickname: confx.GetEnv("GMT_BOOTSTRAP_ADMIN_NICKNAME", "超级管理员"),
		CORSAllowedOrigins:     confx.GetEnv("GMT_CORS_ALLOWED_ORIGINS", ""),
		WebDist:                confx.GetEnv("GMT_WEB_DIST", ""),
		WebProxyTarget:         confx.GetEnv("GMT_WEB_PROXY_TARGET", ""),
	}
}

// Validate 校验 GMT 服务配置中的敏感项与关键依赖。
func (c *Config) Validate() error {
	if err := confx.ValidateBase(c.Base); err != nil {
		return err
	}
	if c.ConfigMongo == nil || strings.TrimSpace(c.ConfigMongo.Database) == "" {
		return fmt.Errorf("configMongo.database is required")
	}
	if err := confx.ValidateSecret("internalSecret", c.InternalSecret, 32); err != nil {
		return err
	}
	if err := confx.ValidateURL("platformUrl", c.PlatformURL, c.IsLocal()); err != nil {
		return err
	}
	if err := confx.ValidateURL("gameLogicUrl", c.GameLogicURL, c.IsLocal()); err != nil {
		return err
	}
	// 超管初始化凭据必须成对提供，可以都不填（不自动创建超管）
	if (c.BootstrapAdminUsername == "") != (c.BootstrapAdminPassword == "") {
		return fmt.Errorf("bootstrap admin username and password must be provided together")
	}
	return nil
}
