package auth_test

import (
	"testing"

	"github.com/luvxinc/vpn/server/auth"
	"github.com/stretchr/testify/assert"
)

func TestParseVersion(t *testing.T) {
	assert.Equal(t, [3]int{1, 2, 3}, auth.ParseVersion("1.2.3"))
	assert.Equal(t, [3]int{1, 0, 0}, auth.ParseVersion("1.0.0"))
	assert.Equal(t, [3]int{0, 0, 0}, auth.ParseVersion("invalid"))
	assert.Equal(t, [3]int{0, 0, 0}, auth.ParseVersion(""))
	assert.Equal(t, [3]int{10, 20, 30}, auth.ParseVersion("10.20.30"))
}

func TestClientVersionOutdated(t *testing.T) {
	min := auth.ParseVersion("1.2.0")

	// Below minimum → outdated
	assert.True(t, auth.ClientVersionOutdated(auth.ParseVersion("1.1.9"), min))
	assert.True(t, auth.ClientVersionOutdated(auth.ParseVersion("0.9.9"), min))

	// Equal → not outdated
	assert.False(t, auth.ClientVersionOutdated(auth.ParseVersion("1.2.0"), min))

	// Above minimum → not outdated
	assert.False(t, auth.ClientVersionOutdated(auth.ParseVersion("1.2.1"), min))
	assert.False(t, auth.ClientVersionOutdated(auth.ParseVersion("2.0.0"), min))
}

func TestVersionString(t *testing.T) {
	assert.Equal(t, "1.2.3", auth.VersionString([3]int{1, 2, 3}))
	assert.Equal(t, "0.0.0", auth.VersionString([3]int{0, 0, 0}))
}
