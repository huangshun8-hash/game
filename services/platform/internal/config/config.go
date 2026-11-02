package config

import (
	"fmt"
	"strings"

	"kemiluo/pkg/confx"
	"kemiluo/pkg/mongox"
)

// Config 平台服务配置，嵌入公共基础配置并叠加业务专属配置
type Config struct {
	// Base 公共基础配置（Server、Mongo、Redis、JWT、Log）
	confx.Base
	// ConfigMongo 游戏配置专用 MongoDB 连接配置（gconfig_db），与 GMT / gamelogic 共享同一实例，仅库名不同
	ConfigMongo *mongox.Config
	// InternalSecret 内部服务间验签密钥（与 gamelogic 等内部服务共享）
	InternalSecret string
	// GamelogicURL GameLogic 服务地址（用于支付通知回调）
	GamelogicURL string
	// ThirdPartyKey 三方平台接口共用签名密钥（key）
	// 由环境变量 THIRD_PARTY_KEY 配置；供充值回调、LunPlay 角色查询等三方接口验签
	// 未配置时对应接口对全部请求拒绝
	ThirdPartyKey string
}

// Load 从环境变量加载平台服务完整配置
func Load() *Config {
	base := confx.LoadBase("Platform", 8080, "platform_db")

	// ConfigMongo 复用主 Mongo 连接参数，仅将默认库名替换为 gconfig_db
	// 可通过环境变量 CONFIG_MONGO_DATABASE 单独覆盖库名
	configMongoCfg := *base.Mongo
	configMongoCfg.Database = confx.GetEnv("CONFIG_MONGO_DATABASE", "gconfig_db")

	return &Config{
		Base:           base,
		ConfigMongo:    &configMongoCfg,
		InternalSecret: confx.MustGetEnvStrict("INTERNAL_SECRET"),
		GamelogicURL:   confx.GetEnv("GAMELOGIC_URL", "http://localhost:8081"),
		ThirdPartyKey:  confx.GetEnv("THIRD_PARTY_KEY", ""),
	}
}

// Validate 校验平台服务配置中的敏感项与关键依赖。
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
	if err := confx.ValidateURL("gamelogicUrl", c.GamelogicURL, c.IsLocal()); err != nil {
		return err
	}
	return nil
}
