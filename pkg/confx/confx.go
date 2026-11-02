package confx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"kemiluo/pkg/authx"
	"kemiluo/pkg/logx"
	"kemiluo/pkg/mongox"
	"kemiluo/pkg/redisx"
)

// ServerConfig HTTP服务器通用配置
type ServerConfig struct {
	// Name 服务名称
	Name string
	// Port 监听端口
	Port int
}

// envLocal 本地开发环境标识
const envLocal = "local"

// envDev 开发联调环境标识
const envDev = "dev"

// Base 所有服务共享的基础配置
// 各服务通过嵌入此结构体获取通用基础设施配置，再叠加自己的业务配置
type Base struct {
	// Env 运行环境标识（由 APP_ENV 指定，默认 "local"），用于区分本地/测试/生产
	Env string
	// Server HTTP服务器配置
	Server ServerConfig
	// Mongo MongoDB连接配置
	Mongo *mongox.Config
	// Redis Redis连接配置
	Redis *redisx.Config
	// JWT JWT签发配置
	JWT authx.JWTConfig
	// Log 日志配置
	Log *logx.Config
}

// setDefaults 设置所有配置项的默认值（最低优先级）
func setDefaults(v *viper.Viper, serviceName string, defaultPort int, defaultDB string) {
	// Server
	v.SetDefault("server.name", serviceName)
	v.SetDefault("server.port", defaultPort)

	// MongoDB
	v.SetDefault("mongo.uri", "mongodb://localhost:27017")
	v.SetDefault("mongo.database", defaultDB)
	v.SetDefault("mongo.connect_timeout", 10*time.Second)
	v.SetDefault("mongo.ping_timeout", 5*time.Second)
	v.SetDefault("mongo.max_pool_size", 100)
	v.SetDefault("mongo.min_pool_size", 10)
	v.SetDefault("mongo.max_idle_time", 60*time.Second)

	// Redis
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 10)
	v.SetDefault("redis.min_idle_conns", 5)
	v.SetDefault("redis.dial_timeout", 5*time.Second)
	v.SetDefault("redis.read_timeout", 3*time.Second)
	v.SetDefault("redis.write_timeout", 3*time.Second)
	v.SetDefault("redis.ping_timeout", 5*time.Second)
	v.SetDefault("redis.max_retries", 3)

	// JWT
	v.SetDefault("jwt.token_ttl", 24*time.Hour)
	v.SetDefault("jwt.issuer", "KeMiLuo")

	// Log
	v.SetDefault("log.dir", "./logs")
	v.SetDefault("log.filename", serviceName)
	v.SetDefault("log.level", "debug")
	v.SetDefault("log.console", true)
	v.SetDefault("log.max_size", 200)
	// 历史日志保留 7 天，超期删除；除「启动后首次写入」与「写满 max_size 轮转」外，
	// logx 另有每日兜底轮转，保证低流量服务当天的超期日志也会被清理。
	// max_backups 置 0 表示不按份数限制，保留只由 max_age 决定，避免份数上限提前删掉 7 天内的日志。
	v.SetDefault("log.max_backups", 0)
	v.SetDefault("log.max_age", 7)
	// lumberjack 原生语义：轮转时即压缩为 .gz，不是「延迟 N 天再压缩」。
	v.SetDefault("log.compress", true)
}

// IsLocal 判断当前是否为本地开发环境（APP_ENV 未设置或为 "local"）
func (b Base) IsLocal() bool {
	return b.Env == envLocal
}

// IsDev 判断当前是否为开发联调环境（APP_ENV 为 "dev"）
func (b Base) IsDev() bool {
	return b.Env == envDev
}

// IsLocalOrDev 判断当前是否为本地或开发联调环境，用于注册开发辅助类定时任务等场景
func (b Base) IsLocalOrDev() bool {
	return b.IsLocal() || b.IsDev()
}

// currentEnv 读取运行环境标识，APP_ENV 未设置时默认为 "local"
func currentEnv() string {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = envLocal
	}
	return env
}

// Source 配置源的只读视图，供各服务读取自身专属配置段（如 gamelogic 的 websocket 段），
// 避免 viper 依赖扩散到业务代码。所有取值方法在「键未配置」时返回调用方给出的默认值，
// 因此可以安全地用于「以代码默认值为基线、按需覆盖」的场景。
// 接收者为 nil 时所有方法均返回默认值，便于不关心专属配置的服务直接使用 LoadBase。
type Source struct {
	v *viper.Viper
}

// isSet 判断配置键是否被显式提供（配置文件或环境变量）
func (s *Source) isSet(key string) bool {
	if s == nil || s.v == nil {
		return false
	}
	return s.v.IsSet(key)
}

// Int 读取整型配置项，未配置时返回 defaultVal
func (s *Source) Int(key string, defaultVal int) int {
	if !s.isSet(key) {
		return defaultVal
	}
	return s.v.GetInt(key)
}

// Int64 读取 64 位整型配置项，未配置时返回 defaultVal
func (s *Source) Int64(key string, defaultVal int64) int64 {
	if !s.isSet(key) {
		return defaultVal
	}
	return s.v.GetInt64(key)
}

// Bool 读取布尔配置项，未配置时返回 defaultVal
func (s *Source) Bool(key string, defaultVal bool) bool {
	if !s.isSet(key) {
		return defaultVal
	}
	return s.v.GetBool(key)
}

// String 读取字符串配置项，未配置时返回 defaultVal
func (s *Source) String(key, defaultVal string) string {
	if !s.isSet(key) {
		return defaultVal
	}
	return s.v.GetString(key)
}

// Duration 读取时间段配置项（如 "30s"、"2h"），未配置时返回 defaultVal
func (s *Source) Duration(key string, defaultVal time.Duration) time.Duration {
	if !s.isSet(key) {
		return defaultVal
	}
	return s.v.GetDuration(key)
}

// StringSlice 读取字符串列表配置项，未配置时返回 defaultVal。
// 同时兼容两种书写方式：配置文件中的 YAML 列表，以及环境变量中的逗号分隔串
// （环境变量无法表达列表结构，与 TRUSTED_PROXIES 的约定保持一致）。
// 空元素会被剔除，全部为空时返回 defaultVal，避免误将白名单清空成"全部拒绝"。
func (s *Source) StringSlice(key string, defaultVal []string) []string {
	if !s.isSet(key) {
		return defaultVal
	}
	var items []string
	switch raw := s.v.Get(key).(type) {
	case string:
		for item := range strings.SplitSeq(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	default:
		for _, item := range s.v.GetStringSlice(key) {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	}
	if len(items) == 0 {
		return defaultVal
	}
	return items
}

// LoadBase 加载基础配置，优先级：环境变量 > 配置文件 > 默认值
// serviceName: 服务名称，用于日志文件名和默认服务名
// defaultPort: 默认监听端口
// defaultDB: 默认数据库名
func LoadBase(serviceName string, defaultPort int, defaultDB string) Base {
	base, _ := LoadBaseWithSource(serviceName, defaultPort, defaultDB)
	return base
}

// LoadBaseWithSource 与 LoadBase 等价，并额外返回配置源视图，
// 供各服务在同一份配置文件/环境变量中读取自己的专属配置段。
//
// 参数：serviceName 服务名称；defaultPort 默认监听端口；defaultDB 默认数据库名
// 返回：base 基础配置；src 配置源只读视图（可传 nil 安全调用）
func LoadBaseWithSource(serviceName string, defaultPort int, defaultDB string) (base Base, src *Source) {
	v := viper.New()
	v.SetConfigType("yaml")

	// 环境变量自动绑定：将配置键中的 "." 替换为 "_" 并转为大写匹配环境变量
	// 例如 mongo.uri → MONGO_URI
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// 设置默认值（最低优先级）
	setDefaults(v, serviceName, defaultPort, defaultDB)

	// 尝试加载配置文件（中等优先级）
	readConfigFile(v, serviceName)

	base = Base{
		Env: currentEnv(),
		Server: ServerConfig{
			Name: v.GetString("server.name"),
			Port: v.GetInt("server.port"),
		},
		Mongo: &mongox.Config{
			URI:            v.GetString("mongo.uri"),
			Database:       v.GetString("mongo.database"),
			ConnectTimeout: v.GetDuration("mongo.connect_timeout"),
			PingTimeout:    v.GetDuration("mongo.ping_timeout"),
			MaxPoolSize:    uint64(v.GetInt("mongo.max_pool_size")),
			MinPoolSize:    uint64(v.GetInt("mongo.min_pool_size")),
			MaxIdleTime:    v.GetDuration("mongo.max_idle_time"),
		},
		Redis: &redisx.Config{
			Addr:         v.GetString("redis.addr"),
			Password:     v.GetString("redis.password"),
			DB:           v.GetInt("redis.db"),
			PoolSize:     v.GetInt("redis.pool_size"),
			MinIdleConns: v.GetInt("redis.min_idle_conns"),
			DialTimeout:  v.GetDuration("redis.dial_timeout"),
			ReadTimeout:  v.GetDuration("redis.read_timeout"),
			WriteTimeout: v.GetDuration("redis.write_timeout"),
			PingTimeout:  v.GetDuration("redis.ping_timeout"),
			MaxRetries:   v.GetInt("redis.max_retries"),
		},
		JWT: authx.JWTConfig{
			SigningKey: LoadSigningKey(),
			TokenTTL:   v.GetDuration("jwt.token_ttl"),
			Issuer:     v.GetString("jwt.issuer"),
		},
		Log: &logx.Config{
			Dir:        v.GetString("log.dir"),
			Filename:   v.GetString("log.filename"),
			Level:      v.GetString("log.level"),
			Console:    v.GetBool("log.console"),
			MaxSize:    v.GetInt("log.max_size"),
			MaxBackups: v.GetInt("log.max_backups"),
			MaxAge:     v.GetInt("log.max_age"),
			Compress:   v.GetBool("log.compress"),
		},
	}
	return base, &Source{v: v}
}

// readConfigFile 加载YAML配置文件
// 文件命名格式：{service}_{env}.yaml，如 platform_local.yaml
// 路径由 CONFIG_DIR 环境变量指定，默认为 "./configs"
// 环境由 APP_ENV 环境变量指定，默认为 "local"
func readConfigFile(v *viper.Viper, serviceName string) {
	env := currentEnv()

	configDir := os.Getenv("CONFIG_DIR")
	if configDir == "" {
		configDir = "configs"
	}

	// 服务名转小写：Platform → platform
	service := cases.Lower(language.English).String(serviceName)
	filename := fmt.Sprintf("%s_%s.yaml", service, env)
	path := filepath.Join(configDir, filename)

	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		if !os.IsNotExist(err) {
			fmt.Printf("[WARN] 加载配置文件失败: %s, %v\n", path, err)
		}
		// 配置文件不存在时静默跳过，回退到环境变量和默认值
		return
	}

	fmt.Printf("[INFO] 已加载配置文件: %s\n", path)
}

// LoadSigningKey 加载JWT签名密钥。
// 为避免弱默认值误入真实环境，此处不再提供任何兜底密钥，由上层配置校验决定是否允许启动。
// 返回值可能为空 []byte，调用方必须通过 ValidateBase 校验后使用。
func LoadSigningKey() []byte {
	key := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY"))
	if key == "" {
		fmt.Println("[WARN] JWT_SIGNING_KEY 未设置。生产环境将因配置校验失败而拒绝启动。")
	}
	return []byte(key)
}

// GetEnv 获取环境变量，若为空则返回默认值
func GetEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// GetEnvInt 获取整型环境变量，若为空或无效则返回默认值
func GetEnvInt(key string, defaultVal int) int {
	v := viper.New()
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetDefault(key, defaultVal)
	return v.GetInt(key)
}

// GetDurationEnv 获取时间段环境变量，格式如 "2h"、"30m"
func GetDurationEnv(key string, defaultVal time.Duration) time.Duration {
	v := viper.New()
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetDefault(key, defaultVal)
	return v.GetDuration(key)
}
