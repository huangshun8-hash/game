package confx

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

var weakSecretValues = map[string]struct{}{
	"admin123":                      {},
	"changeme":                      {},
	"change-me":                     {},
	"default":                       {},
	"default-secret":                {},
	"dev-only-unsafe-key-change-me": {},
	"internal-dev-secret":           {},
	"password":                      {},
	"secret":                        {},
}

// ValidateBase 校验所有服务共享的基础安全配置。
func ValidateBase(base Base) error {
	if base.Mongo == nil {
		return fmt.Errorf("mongo config is required")
	}
	if strings.TrimSpace(base.Mongo.URI) == "" {
		return fmt.Errorf("mongo.uri is required")
	}
	if strings.TrimSpace(base.Mongo.Database) == "" {
		return fmt.Errorf("mongo.database is required")
	}

	if base.Redis == nil {
		return fmt.Errorf("redis config is required")
	}
	if strings.TrimSpace(base.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr is required")
	}

	if err := ValidateSecret("jwt.signingKey", string(base.JWT.SigningKey), 32); err != nil {
		return err
	}
	return nil
}

// ValidateSecret 校验敏感共享密钥的字节长度和弱口令风险。
// 对于 HMAC-SHA256 等对称密钥，minLen 应 >= 32 bytes。
func ValidateSecret(name, value string, minLen int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}
	if minLen > 0 && len(trimmed) < minLen {
		return fmt.Errorf("%s must be at least %d bytes", name, minLen)
	}
	if _, ok := weakSecretValues[strings.ToLower(trimmed)]; ok {
		return fmt.Errorf("%s uses a blocked weak value", name)
	}
	return nil
}

// ValidateURL 校验服务地址是否合法，并在非本地环境阻止指向回环地址。
func ValidateURL(name, rawURL string, isLocal bool) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("%s is required", name)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", name)
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("%s host is required", name)
	}
	if !isLocal && isLoopbackHost(host) {
		return fmt.Errorf("%s must not point to a loopback address outside local env", name)
	}
	return nil
}

// MustGetEnvStrict 读取环境变量并 trim 空格，返回空字符串时表示未配置。
// 调用方应在 Validate 阶段统一校验其非空，缺失时返回明确错误并阻止启动。
func MustGetEnvStrict(key string) string {
	return strings.TrimSpace(GetEnv(key, ""))
}

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost":
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
