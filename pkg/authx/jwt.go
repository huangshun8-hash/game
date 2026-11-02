package authx

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrTokenExpired = errors.New("authx: token expired")
	ErrTokenInvalid = errors.New("authx: token invalid")
)

// JWTConfig holds JWT signing/verification configuration.
type JWTConfig struct {
	// SigningKey is the HMAC secret used to sign and verify tokens.
	SigningKey []byte

	// TokenTTL is the lifetime of a token.
	TokenTTL time.Duration

	// Issuer is written into the token's iss claim.
	Issuer string
}

// DefaultJWTConfig returns a config with sensible defaults.
func DefaultJWTConfig() JWTConfig {
	return JWTConfig{
		TokenTTL: 24 * time.Hour,
		Issuer:   "KeMiLuo",
	}
}

// JWT provides token generation and validation.
type JWT struct {
	cfg JWTConfig
}

// NewJWT creates a new JWT instance with the given config.
func NewJWT(cfg JWTConfig) *JWT {
	return &JWT{cfg: cfg}
}

// GenerateToken creates a signed token for the given user.
// packetKey is optional: when provided, it is embedded into the token claims so that
// the transport layer can encrypt/decrypt protobuf packets without an extra DB query.
func (j *JWT) GenerateToken(userID string, role string, packetKey ...string) (string, error) {
	now := time.Now()

	key := ""
	if len(packetKey) > 0 {
		key = packetKey[0]
	}

	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    j.cfg.Issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.cfg.TokenTTL)),
		},
		UserID:    userID,
		Role:      role,
		PacketKey: key,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.cfg.SigningKey)
}

// ParseToken parses and validates a token string, returning the claims.
func (j *JWT) ParseToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		// 确保签名方法为 HMAC
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrTokenInvalid
		}
		return j.cfg.SigningKey, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	return claims, nil
}

// TokenTTL returns the configured token TTL.
func (j *JWT) TokenTTL() time.Duration {
	return j.cfg.TokenTTL
}

// Validate is a convenience method compatible with httpx.AuthMiddleware's
// validate function signature: func(token string) (userID string, err error).
func (j *JWT) Validate(tokenStr string) (string, error) {
	claims, err := j.ParseToken(tokenStr)
	if err != nil {
		return "", err
	}
	return claims.UserID, nil
}
