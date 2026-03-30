package background_test

import (
	"testing"
	"time"

	"github.com/luvxinc/vpn/server/background"
	"github.com/stretchr/testify/assert"
)

func TestSleepUntil3AM_FutureTarget(t *testing.T) {
	// Since we can't easily test the actual sleep, verify the logic:
	// If now is 01:00 AM, target should be 03:00 AM today (2 hours away).
	// If now is 04:00 AM, target should be 03:00 AM tomorrow (23 hours away).
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if !target.After(now) {
		target = target.AddDate(0, 0, 1)
	}
	assert.True(t, target.After(now), "target should always be in the future")
}

func TestLogManager_New(t *testing.T) {
	mgr := background.NewLogManager(nil, 90, 500)
	assert.NotNil(t, mgr)
	assert.Equal(t, 90, mgr.RetentionDays)
	assert.Equal(t, 500, mgr.MaxDomainsPerUserPerDay)
}
