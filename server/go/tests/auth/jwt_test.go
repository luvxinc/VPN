package auth_test

import (
	"testing"
	"time"

	"github.com/luvxinc/vpn/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test_secret_key_at_least_32chars!"

func TestMakeVerifyUserJWT(t *testing.T) {
	token, err := auth.MakeUserJWT("user-id-123", "session-id-456", testSecret, 15)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	claims, err := auth.VerifyUserJWT(token, testSecret)
	require.NoError(t, err)
	assert.Equal(t, "user-id-123", claims["sub"])
	assert.Equal(t, "session-id-456", claims["sid"])
	assert.Equal(t, "weiai-vpn", claims["iss"])
}

func TestVerifyUserJWT_WrongSecret(t *testing.T) {
	token, err := auth.MakeUserJWT("user-id-123", "session-456", testSecret, 15)
	require.NoError(t, err)

	_, err = auth.VerifyUserJWT(token, "wrong_secret_key_12345678901234!")
	assert.Error(t, err)
}

func TestVerifyUserJWT_Expired(t *testing.T) {
	// Create a token that expires immediately (0 minutes)
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix() - 100,
		"exp": now.Unix() - 50, // already expired
		"iss": "weiai-vpn",
		"sub": "user-id",
		"sid": "session-id",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)

	_, err = auth.VerifyUserJWT(signed, testSecret)
	assert.Error(t, err)
}

func TestMakeVerifyAdminJWT(t *testing.T) {
	token, err := auth.MakeAdminJWT(testSecret)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	assert.True(t, auth.VerifyAdminJWT(token, testSecret))
}

func TestVerifyAdminJWT_WrongSecret(t *testing.T) {
	token, err := auth.MakeAdminJWT(testSecret)
	require.NoError(t, err)

	// Wrong secret should fail
	assert.False(t, auth.VerifyAdminJWT(token, "different_secret_key_32chars!!!!"))
}

func TestVerifyAdminJWT_UserTokenRejected(t *testing.T) {
	// A user JWT should be rejected by VerifyAdminJWT (different issuer/secret)
	userToken, err := auth.MakeUserJWT("user-id", "session-id", testSecret, 15)
	require.NoError(t, err)

	assert.False(t, auth.VerifyAdminJWT(userToken, testSecret))
}
