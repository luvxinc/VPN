package auth_test

import (
	"testing"

	"github.com/luvxinc/vpn/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := auth.HashPassword("mypassword123")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.True(t, auth.CheckPassword("mypassword123", hash))
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	hash, err := auth.HashPassword("correctpassword")
	require.NoError(t, err)
	assert.False(t, auth.CheckPassword("wrongpassword", hash))
}

func TestCheckPassword_EmptyPassword(t *testing.T) {
	hash, err := auth.HashPassword("nonempty")
	require.NoError(t, err)
	assert.False(t, auth.CheckPassword("", hash))
}
