package authx

import "github.com/golang-jwt/jwt/v5"

// Claims is the custom JWT claims structure used across services.
type Claims struct {
	jwt.RegisteredClaims

	// UserID is the authenticated user's unique identifier.
	UserID string `json:"uid"`

	// Role is an optional field for role-based access control.
	Role string `json:"role,omitempty"`

	// PacketKey is the player's packet (request/response) encryption key (base64 encoded),
	// embedded at login time so the transport layer can encrypt/decrypt protobuf packets
	// without an extra DB query. Empty when the token is not issued for a player (e.g. admin tokens).
	PacketKey string `json:"packetKey,omitempty"`
}
