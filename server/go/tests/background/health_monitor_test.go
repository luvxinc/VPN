package background_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luvxinc/vpn/server/background"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── OutboundHealthMonitor Unit Tests (P3) ─────────────────────────────────────
// These run without any real sing-box or Clash API — a mock HTTP server
// simulates the /proxies/{tag}/delay endpoint.

// mockDelayServer creates a test server that responds to Clash API delay probes.
// perTag maps tag → HTTP status + delay ms (0 → no delay field, error).
func mockDelayServer(t *testing.T, perTag map[string]struct {
	Status int
	Delay  int
}) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	// Clash API /version (health ping)
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0-test"}`))
	})
	// Clash API /proxies/{tag}/delay
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		// extract tag from /proxies/{tag}/delay
		path := r.URL.Path // e.g. /proxies/ws-cdn/delay
		var matchedTag string
		for tag := range perTag {
			prefix := "/proxies/" + tag + "/delay"
			if path == prefix {
				matchedTag = tag
				break
			}
		}
		if matchedTag == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cfg := perTag[matchedTag]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cfg.Status)
		if cfg.Status == http.StatusOK && cfg.Delay > 0 {
			json.NewEncoder(w).Encode(map[string]int{"delay": cfg.Delay})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestHealthMonitor_AllTagsOK verifies that all outbounds are marked "ok"
// when the mock returns 200 + delay for each tag.
func TestHealthMonitor_AllTagsOK(t *testing.T) {
	tags := []string{"reality-direct", "ws-cdn"}
	srv := mockDelayServer(t, map[string]struct {
		Status int
		Delay  int
	}{
		"reality-direct": {200, 42},
		"ws-cdn":         {200, 88},
	})

	m := background.NewOutboundHealthMonitor(srv.URL, tags)
	m.CheckOnce(context.Background())

	results := m.GetAll()
	require.Len(t, results, 2)

	tagMap := map[string]background.OutboundHealth{}
	for _, r := range results {
		tagMap[r.Tag] = r
	}

	assert.Equal(t, "ok", tagMap["reality-direct"].Status)
	assert.Equal(t, int64(42), tagMap["reality-direct"].LatencyMs)
	assert.Equal(t, 0, tagMap["reality-direct"].Failures)

	assert.Equal(t, "ok", tagMap["ws-cdn"].Status)
	assert.Equal(t, int64(88), tagMap["ws-cdn"].LatencyMs)
}

// TestHealthMonitor_SingleTagDown verifies that a 503 response marks the tag degraded.
func TestHealthMonitor_SingleTagDown(t *testing.T) {
	tags := []string{"reality-direct"}
	srv := mockDelayServer(t, map[string]struct {
		Status int
		Delay  int
	}{
		"reality-direct": {503, 0},
	})

	m := background.NewOutboundHealthMonitor(srv.URL, tags)
	m.CheckOnce(context.Background())

	results := m.GetAll()
	require.Len(t, results, 1)
	assert.Equal(t, "degraded", results[0].Status)
	assert.Equal(t, int64(-1), results[0].LatencyMs)
	assert.Equal(t, 1, results[0].Failures)
}

// TestHealthMonitor_ConsecutiveFailures_StatusEscalation tests that
// failures accumulate and status escalates: degraded → down.
func TestHealthMonitor_ConsecutiveFailures_StatusEscalation(t *testing.T) {
	tags := []string{"reality-direct"}

	// Always returns 503
	srv := mockDelayServer(t, map[string]struct {
		Status int
		Delay  int
	}{
		"reality-direct": {503, 0},
	})

	m := background.NewOutboundHealthMonitor(srv.URL, tags)

	// Run 5 consecutive failing checks — should escalate to "down"
	for i := 0; i < 5; i++ {
		m.CheckOnce(context.Background())
	}

	results := m.GetAll()
	require.Len(t, results, 1)
	assert.Equal(t, "down", results[0].Status, "after 5 consecutive failures status must be 'down'")
	assert.Equal(t, 5, results[0].Failures)
}

// TestHealthMonitor_RecoveryResetsFailures verifies that a successful check
// resets the failure counter back to 0.
func TestHealthMonitor_RecoveryResetsFailures(t *testing.T) {
	tags := []string{"ws-cdn"}
	var callCount int32

	mux := http.NewServeMux()
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if n <= 3 {
			// First 3 calls fail
			w.WriteHeader(503)
			return
		}
		// Subsequent calls succeed
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]int{"delay": 50})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	m := background.NewOutboundHealthMonitor(srv.URL, tags)

	// 3 failing checks
	m.CheckOnce(context.Background())
	m.CheckOnce(context.Background())
	m.CheckOnce(context.Background())

	// 1 successful check
	m.CheckOnce(context.Background())

	results := m.GetAll()
	require.Len(t, results, 1)
	assert.Equal(t, "ok", results[0].Status, "recovery must reset status to 'ok'")
	assert.Equal(t, 0, results[0].Failures, "recovery must reset failure counter to 0")
}

// TestHealthMonitor_EmptyTags verifies that zero tags produces no results
// and does not panic.
func TestHealthMonitor_EmptyTags(t *testing.T) {
	m := background.NewOutboundHealthMonitor("http://127.0.0.1:9090", []string{})
	m.CheckOnce(context.Background())
	assert.Empty(t, m.GetAll(), "no tags = no results")
}

// TestHealthMonitor_RunAndStop verifies the background Run goroutine exits
// cleanly when context is cancelled, without recording spurious probe failures.
//
// The fix in probe(): when ctx is already cancelled before a probe starts,
// the probe returns the previous (last-known-good) state instead of failing.
// This prevents the test from seeing "degraded" status on shutdown.
func TestHealthMonitor_RunAndStop(t *testing.T) {
	tags := []string{"ws-cdn"}
	srv := mockDelayServer(t, map[string]struct {
		Status int
		Delay  int
	}{
		"ws-cdn": {200, 10},
	})

	m := background.NewOutboundHealthMonitor(srv.URL, tags)
	m.Interval = 50 * time.Millisecond // Fast polling for test

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	// Run() must exit cleanly within 2s of context cancellation
	select {
	case <-done:
		// ✓ Run exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not exit after context cancellation within 2s")
	}

	// There must be at least one successful result (from the initial synchronous check).
	// After the production fix, context cancellation no longer causes spurious "degraded".
	results := m.GetAll()
	require.NotEmpty(t, results, "at least one health check must have run")
	// The initial check ran before ctx was cancelled → must be "ok"
	assert.Equal(t, "ok", results[0].Status,
		"initial check (before ctx cancel) must succeed; status must not be degraded by shutdown")
}



// TestHealthMonitor_LastCheckTimestamp verifies that LastCheck is updated after each probe.
func TestHealthMonitor_LastCheckTimestamp(t *testing.T) {
	tags := []string{"reality-direct"}
	srv := mockDelayServer(t, map[string]struct {
		Status int
		Delay  int
	}{
		"reality-direct": {200, 5},
	})

	m := background.NewOutboundHealthMonitor(srv.URL, tags)

	before := time.Now().Add(-time.Millisecond)
	m.CheckOnce(context.Background())
	after := time.Now().Add(time.Millisecond)

	results := m.GetAll()
	require.Len(t, results, 1)
	assert.True(t, results[0].LastCheck.After(before), "LastCheck must be after before-timestamp")
	assert.True(t, results[0].LastCheck.Before(after), "LastCheck must be before after-timestamp")
}
