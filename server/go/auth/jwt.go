package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// MakeUserJWT creates a 15-minute HS256 access token for a VPN user.
func MakeUserJWT(userID, sessionID, secret string, expiryMinutes int) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(time.Duration(expiryMinutes) * time.Minute).Unix(),
		"iss": "weiai-vpn",
		"sub": userID,
		"sid": sessionID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// VerifyUserJWT validates a user access token and returns the claims.
func VerifyUserJWT(tokenStr, secret string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	}, jwt.WithIssuer("weiai-vpn"), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

// MakeAdminJWT creates an 8-hour HS256 token for admin panel access.
func MakeAdminJWT(secret string) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": "admin",
		"iss": "weiai-admin",
		"iat": now.Unix(),
		"exp": now.Add(8 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(adminSecret(secret)))
}

// VerifyAdminJWT validates an admin token. Returns true if valid.
func VerifyAdminJWT(tokenStr, secret string) bool {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(adminSecret(secret)), nil
	}, jwt.WithIssuer("weiai-admin"), jwt.WithValidMethods([]string{"HS256"}))
	return err == nil && token.Valid
}

func adminSecret(base string) string {
	return base + ":admin"
}
