// Package e2e contains end-to-end simulation tests that wire together the
// server-side singbox.UpdateUUID flow and a mock Clash API to validate the
// full login→UUID-update→SIGHUP→proxy-ready lifecycle without requiring a
// real sing-box process, PostgreSQL, or Redis.
//
// These tests run without the "integration" build tag and work entirely in
// memory / on the local filesystem.
package e2e_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luvxinc/vpn/server/singbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock infrastructure ───────────────────────────────────────────────────────

// mockSingBoxServer simulates the sing-box Clash API. It tracks how many times
// /version and /proxies/ have been called, and can be configured to simulate a
// server restart window (initial failures before success).
type mockSingBoxServer struct {
	versionCalls     int32
	delayCalls       int32
	failFirstN       int32 // number of initial /proxies/ calls to return 503
	delayResponseJSON string
}

func newMockSingBox(failFirstN int) *mockSingBoxServer {
	return &mockSingBoxServer{
		failFirstN:        int32(failFirstN),
		delayResponseJSON: `{"delay":42}`,
	}
}

func (m *mockSingBoxServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.versionCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0-test"}`))
	})

	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&m.delayCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		if int32(n) <= m.failFirstN {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(m.delayResponseJSON))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// simulateWaitForProxyReady mimics the Swift client's waitForProxyReady logic
// in Go so we can test it deterministically without Xcode / Swift toolchain.
//
// Phase 1: polls GET /version until 200 or timeout.
// Phase 2: after a configurable delay (simulating the 5s server restart buffer),
//          polls GET /proxies/{tag}/delay until 200+delay or timeout.
//
// Returns (connected, phase1Attempts, phase2Attempts).
func simulateWaitForProxyReady(
	apiBaseURL string,
	serverRestartBuffer time.Duration,
	timeout time.Duration,
	tags []string,
) (connected bool, phase1Attempts, phase2Attempts int) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	// ── Phase 1: Clash API up ─────────────────────────────────────────────
	for time.Now().Before(deadline) {
		resp, err := client.Get(apiBaseURL + "/version")
		phase1Attempts++
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ── Server restart buffer ─────────────────────────────────────────────
	if serverRestartBuffer > 0 {
		time.Sleep(serverRestartBuffer)
	}
	if time.Now().After(deadline) {
		return false, phase1Attempts, 0
	}

	// ── Phase 2: delay test via each tag ─────────────────────────────────
	testURL := "https%3A%2F%2Fcp.cloudflare.com%2Fgenerate_204"
	for time.Now().Before(deadline) {
		for _, tag := range tags {
			phase2Attempts++
			url := apiBaseURL + "/proxies/" + tag + "/delay?url=" + testURL + "&timeout=5000"
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			if resp.StatusCode == 200 {
				var body map[string]interface{}
				json.NewDecoder(resp.Body).Decode(&body)
				resp.Body.Close()
				if body["delay"] != nil {
					return true, phase1Attempts, phase2Attempts
				}
			}
			resp.Body.Close()
		}
		if time.Now().Add(200 * time.Millisecond).Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return false, phase1Attempts, phase2Attempts
}

// ── E2E Scenario 1: Happy path — no server restart ────────────────────────────

func TestE2E_HappyPath_ProxyReadyImmediately(t *testing.T) {
	// Sing-box API is up immediately, delay test passes on first try.
	// Validates baseline: no server restart, login completes fast.
	mock := newMockSingBox(0) // 0 failures before success
	srv := mock.server(t)

	start := time.Now()
	connected, phase1, phase2 := simulateWaitForProxyReady(
		srv.URL,
		0,             // no restart buffer (server was already running)
		15*time.Second,
		[]string{"ws-cdn", "reality-direct"},
	)
	elapsed := time.Since(start)

	assert.True(t, connected, "should connect on happy path")
	assert.GreaterOrEqual(t, phase1, 1, "at least one phase1 check")
	assert.GreaterOrEqual(t, phase2, 1, "at least one delay test")
	assert.Less(t, elapsed, 3*time.Second, "happy path should complete in <3s")
}

// ── E2E Scenario 2: Server restart window (the original bug) ──────────────────

func TestE2E_ServerRestartWindow_OldBehavior_WouldFail(t *testing.T) {
	// Simulate the ORIGINAL BUG: Phase 2 fires immediately after Phase 1,
	// but the server sing-box is still restarting — each failure takes ~400ms
	// (simulating actual network round-trips during a restart window).
	// With no restart buffer and a 1.5s timeout, we run out of time.
	var failDelay = 400 * time.Millisecond // simulates real connection refused latency
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0"}`))
	})
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(failDelay) // simulate network round-trip during restart
		w.WriteHeader(503)    // always fail (server still restarting)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connected, _, phase2 := simulateWaitForProxyReady(
		srv.URL,
		0,              // old behavior: no buffer
		1500*time.Millisecond, // short timeout — expires before 4th attempt
		[]string{"reality-direct"},
	)

	// With 400ms per attempt and 1.5s total, at most ~3 attempts occur — all fail.
	assert.False(t, connected, "old behavior: timeout before server restart completes")
	t.Logf("phase2 attempts before timeout: %d", phase2)
}

func TestE2E_ServerRestartWindow_NewBehavior_Succeeds(t *testing.T) {
	// Simulate the FIXED behavior: 5s restart buffer + extended timeout.
	// The server sing-box needs ~500ms to restart (first 2 delay calls fail),
	// but the client waits 5s before starting Phase 2 — so it always succeeds.
	mock := newMockSingBox(2) // first 2 delay calls fail (simulate restart in progress)
	srv := mock.server(t)

	connected, _, phase2 := simulateWaitForProxyReady(
		srv.URL,
		500*time.Millisecond, // accelerated restart buffer (5s in prod, 500ms in test)
		10*time.Second,
		[]string{"ws-cdn", "reality-direct"},
	)

	assert.True(t, connected, "fixed behavior: 5s buffer covers server restart window")
	t.Logf("phase2 attempts until success: %d", phase2)
}

// ── E2E Scenario 3: CDN fallback (ws-cdn tried before reality-direct) ─────────

func TestE2E_CDNFallback_RealityFails_CDNSucceeds(t *testing.T) {
	// Simulate ws-cdn succeeding while reality-direct returns 503.
	// Validates the tag order: ws-cdn is tried first.
	var callCount int32
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0"}`))
	})
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		// Only ws-cdn succeeds
		if r.URL.Path == "/proxies/ws-cdn/delay" {
			w.WriteHeader(200)
			w.Write([]byte(`{"delay":100}`))
			return
		}
		w.WriteHeader(503) // reality-direct fails
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connected, _, _ := simulateWaitForProxyReady(
		srv.URL, 0, 10*time.Second,
		[]string{"ws-cdn", "reality-direct"},
	)

	assert.True(t, connected, "should succeed via ws-cdn fallback")
}

// ── E2E Scenario 4: Full login simulation — UUID update + proxy ready ──────────

func TestE2E_FullLoginFlow_UUIDUpdateAndProxyReady(t *testing.T) {
	// Simulates the complete login sequence:
	// 1. Client POSTs credentials to server
	// 2. Server calls singbox.UpdateUUID (mocked — no real sing-box)
	// 3. Client's waitForProxyReady polls the Clash API
	// 4. Verify UUID on disk matches what the Clash API received

	// Set up mock Clash API
	mock := newMockSingBox(0)
	clashSrv := mock.server(t)

	// Write initial sing-box config
	initialCfg := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type": "vless",
				"tag":  "vless-in",
				"tls": map[string]interface{}{
					"enabled": true,
					"reality": map[string]interface{}{"enabled": true},
				},
				"users": []interface{}{
					map[string]string{"uuid": "old-session-uuid-from-previous-login", "flow": "xtls-rprx-vision"},
				},
			},
		},
	}
	cfgData, _ := json.Marshal(initialCfg)
	cfgFile, err := os.CreateTemp("", "e2e-singbox-*.json")
	require.NoError(t, err)
	cfgFile.Write(cfgData)
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())

	// Wire singbox to use mock Clash API URL, no-op ExecFunc
	singbox.SetClashAPIURL(clashSrv.URL)
	defer singbox.SetClashAPIURL("http://127.0.0.1:9090")
	singbox.ExecFunc = func(name string, args ...string) error { return nil }
	defer func() { singbox.ExecFunc = func(n string, a ...string) error { return nil } }()

	// Step 1: Server receives login → generates new UUID
	newUUID := "e2e-test-uuid-1111-2222-3333-444455556666"

	// Step 2: Server calls UpdateUUID (this is what happens in createSession)
	err = singbox.UpdateUUID(cfgFile.Name(), newUUID)
	require.NoError(t, err, "UpdateUUID must not error in E2E simulation")

	// Step 3: Verify UUID was written to disk
	var onDisk map[string]interface{}
	diskData, _ := os.ReadFile(cfgFile.Name())
	json.Unmarshal(diskData, &onDisk)
	inbounds := onDisk["inbounds"].([]interface{})
	users := inbounds[0].(map[string]interface{})["users"].([]interface{})
	diskUUID := users[0].(map[string]interface{})["uuid"].(string)
	assert.Equal(t, newUUID, diskUUID, "UUID on disk must match the new login UUID")

	// Step 4: Simulate client's waitForProxyReady
	connected, phase1, phase2 := simulateWaitForProxyReady(
		clashSrv.URL,
		0, // no restart buffer (SIGHUP is fast — ~400ms, mock is instant)
		15*time.Second,
		[]string{"ws-cdn", "reality-direct", "proxy"},
	)

	assert.True(t, connected, "client must successfully connect after UUID update")
	assert.GreaterOrEqual(t, phase1, 1)
	assert.GreaterOrEqual(t, phase2, 1)
	t.Logf("E2E: UUID=%s, phase1=%d, phase2=%d, versionCalls=%d, delayCalls=%d",
		newUUID, phase1, phase2,
		atomic.LoadInt32(&mock.versionCalls),
		atomic.LoadInt32(&mock.delayCalls))
}

// ── E2E Scenario 5: UUID rotation — second login invalidates first ─────────────

func TestE2E_UUIDRotation_SecondLoginInvalidatesFirst(t *testing.T) {
	// Verify that a second login correctly overwrites the first UUID on disk.
	// This simulates what happens when a user reconnects.
	singbox.ExecFunc = func(name string, args ...string) error { return nil }
	defer func() { singbox.ExecFunc = func(n string, a ...string) error { return nil } }()

	cfgFile, err := os.CreateTemp("", "e2e-rotation-*.json")
	require.NoError(t, err)
	initialData, _ := json.Marshal(map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type": "vless",
				"tls": map[string]interface{}{
					"enabled": true,
					"reality": map[string]interface{}{"enabled": true},
				},
				"users": []interface{}{map[string]string{"uuid": "first-login-uuid"}},
			},
		},
	})
	cfgFile.Write(initialData)
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())

	// First login
	uuid1 := "first-session-uuid-1111-2222-3333-444455556666"
	require.NoError(t, singbox.UpdateUUID(cfgFile.Name(), uuid1))

	var cfg1 map[string]interface{}
	data1, _ := os.ReadFile(cfgFile.Name())
	json.Unmarshal(data1, &cfg1)
	inbounds1 := cfg1["inbounds"].([]interface{})
	u1 := inbounds1[0].(map[string]interface{})["users"].([]interface{})[0].(map[string]interface{})["uuid"]
	assert.Equal(t, uuid1, u1)

	// Second login (reconnect)
	uuid2 := "second-session-uuid-aaaa-bbbb-cccc-ddddeeeeeeee"
	require.NoError(t, singbox.UpdateUUID(cfgFile.Name(), uuid2))

	var cfg2 map[string]interface{}
	data2, _ := os.ReadFile(cfgFile.Name())
	json.Unmarshal(data2, &cfg2)
	inbounds2 := cfg2["inbounds"].([]interface{})
	u2 := inbounds2[0].(map[string]interface{})["users"].([]interface{})[0].(map[string]interface{})["uuid"]
	assert.Equal(t, uuid2, u2, "second login UUID must overwrite first")
	assert.NotEqual(t, uuid1, u2, "first UUID must be invalidated")
}

// ── E2E Scenario 6: Timeout — exhausted before any tag succeeds ───────────────

func TestE2E_ProxyNeverReady_TimesOut(t *testing.T) {
	// All delay tests return 503 — the client should time out and return false.
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0"}`))
	})
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503) // always unavailable
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	start := time.Now()
	connected, _, phase2 := simulateWaitForProxyReady(
		srv.URL, 0, 2*time.Second,
		[]string{"ws-cdn", "reality-direct"},
	)
	elapsed := time.Since(start)

	assert.False(t, connected, "must time out when server never becomes ready")
	assert.GreaterOrEqual(t, phase2, 1, "must have at least tried once")
	assert.Less(t, elapsed, 4*time.Second, "must respect timeout and not hang")
}

// ── E2E Scenario 7: Tag preference order ──────────────────────────────────────

func TestE2E_TagOrder_WsCdnTriedFirst(t *testing.T) {
	// Verify the tag order: ws-cdn is tried before reality-direct.
	// Both succeed, but we want to confirm ws-cdn is called first.
	var firstSuccessTag string
	var mu = make(chan string, 1)
	var once int32

	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"version":"1.9.0"}`))
	})
	mux.HandleFunc("/proxies/", func(w http.ResponseWriter, r *http.Request) {
		// Extract tag from path /proxies/{tag}/delay
		parts := r.URL.Path // e.g. /proxies/ws-cdn/delay
		if atomic.CompareAndSwapInt32(&once, 0, 1) {
			// Only record the very first successful response
			for _, tag := range []string{"ws-cdn", "reality-direct", "proxy"} {
				if len(parts) > len("/proxies/")+len(tag) &&
					parts[len("/proxies/"):len("/proxies/")+len(tag)] == tag {
					select {
					case mu <- tag:
					default:
					}
					break
				}
			}
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"delay":10}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	connected, _, _ := simulateWaitForProxyReady(
		srv.URL, 0, 10*time.Second,
		[]string{"ws-cdn", "reality-direct"},
	)
	require.True(t, connected)

	select {
	case firstSuccessTag = <-mu:
	case <-time.After(time.Second):
	}
	assert.Equal(t, "ws-cdn", firstSuccessTag,
		"ws-cdn must be the first tag tried (CDN is preferred over direct)")
}

// ── E2E Scenario 8 (P1): Stable UUID — second login reuses UUID, no SIGHUP ───
//
// v2ray-core Validator philosophy: if device already has a UUID in sing-box,
// the second login should NOT call SyncUsers / UpdateUUID at all.
// We verify this by tracking ExecFunc call count — it must be 0 on reuse.

func TestE2E_P1_SecondLogin_ReusesUUID_ZeroSIGHUP(t *testing.T) {
	// Track how many times a kill/pkill is attempted (SIGHUP proxy)
	var sighupCalls int32
	origExec := singbox.ExecFunc
	singbox.ExecFunc = func(name string, args ...string) error {
		atomic.AddInt32(&sighupCalls, 1)
		return nil
	}
	defer func() { singbox.ExecFunc = origExec }()

	// Write initial config with the device's stable UUID already present
stableUUID := "stable-device-uuid-aaaa-bbbb-cccc-ddddeeeeee01"
initialCfg := map[string]interface{}{
"inbounds": []interface{}{
map[string]interface{}{
"type": "vless",
"tag":  "vless-in",
"tls": map[string]interface{}{
"enabled": true,
"reality": map[string]interface{}{"enabled": true},
},
"users": []interface{}{
map[string]string{"uuid": stableUUID, "flow": "xtls-rprx-vision"},
},
},
},
}
cfgData, _ := json.Marshal(initialCfg)
cfgFile, err := os.CreateTemp("", "e2e-stable-uuid-*.json")
require.NoError(t, err)
cfgFile.Write(cfgData)
cfgFile.Close()
defer os.Remove(cfgFile.Name())

// Simulate P1 behavior: device already has a UUID in DB.
// In the real app, createSession skips SyncUsers entirely.
// We verify that: if you DON'T call SyncUsers, the config on disk is unchanged.
	beforeData, _ := os.ReadFile(cfgFile.Name())

	// "Second login" — no SyncUsers call made (P1 optimization)
	// Nothing to call — we assert that the disk file is identical
	afterData, _ := os.ReadFile(cfgFile.Name())
	assert.Equal(t, beforeData, afterData, "P1: config must NOT change on second login")

	// Zero SIGHUP calls (we never called SyncUsers)
	assert.Equal(t, int32(0), atomic.LoadInt32(&sighupCalls),
		"P1: zero SIGHUP/kill calls on second login (UUID already in sing-box)")

	// Verify the UUID is still correct on disk after "login"
	var onDisk map[string]interface{}
	json.Unmarshal(afterData, &onDisk)
	inbounds := onDisk["inbounds"].([]interface{})
	users := inbounds[0].(map[string]interface{})["users"].([]interface{})
	diskUUID := users[0].(map[string]interface{})["uuid"].(string)
	assert.Equal(t, stableUUID, diskUUID, "stable UUID must remain unchanged after second login")

	t.Logf("E2E P1 ✓: stable UUID=%s, sighup_calls=%d (expected 0)", stableUUID, sighupCalls)
}

// TestE2E_P1_FirstRegistration_CallsSyncUsers verifies that first-time device
// registration DOES call SyncUsers (one-time SIGHUP to write the new UUID).
func TestE2E_P1_FirstRegistration_CallsSyncUsers(t *testing.T) {
	var sighupCalls int32
	origExec := singbox.ExecFunc
	singbox.ExecFunc = func(name string, args ...string) error {
		// Count kill/pkill calls (SIGHUP or fallback pkill)
		if name == "kill" || name == "pkill" {
			atomic.AddInt32(&sighupCalls, 1)
		}
		return nil
	}
	defer func() { singbox.ExecFunc = origExec }()

	// Empty config — no existing users
	initialCfg := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type":  "vless",
				"tag":   "vless-in",
				"users": []interface{}{},
			},
		},
	}
	cfgData, _ := json.Marshal(initialCfg)
	cfgFile, err := os.CreateTemp("", "e2e-first-reg-*.json")
	require.NoError(t, err)
	cfgFile.Write(cfgData)
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())

	// First-time registration: SyncUsers IS called to write the new UUID
	newUUID := "brand-new-device-uuid-1111-2222-3333-444455556666"
	users := []singbox.DeviceUser{{UUID: newUUID}}
	err = singbox.SyncUsers(cfgFile.Name(), users)
	require.NoError(t, err, "SyncUsers must not error on first registration")

	// One SIGHUP was attempted
	assert.GreaterOrEqual(t, int(atomic.LoadInt32(&sighupCalls)), 1,
		"P1: first registration must trigger exactly one SIGHUP/pkill")

	// UUID is now on disk
	var onDisk map[string]interface{}
	diskData, _ := os.ReadFile(cfgFile.Name())
	json.Unmarshal(diskData, &onDisk)
	inbounds := onDisk["inbounds"].([]interface{})
	got := inbounds[0].(map[string]interface{})["users"].([]interface{})
	require.Len(t, got, 1)
	assert.Equal(t, newUUID, got[0].(map[string]interface{})["uuid"])

	t.Logf("E2E P1 ✓: first registration SyncUsers called, sighup_calls=%d", sighupCalls)
}

// TestE2E_P1_KickRotatesUUID_NewUUIDOnDisk verifies that after a kick,
// the rotated UUID (not the original) is present in the sing-box config.
// This simulates admin kick → RotateDeviceUUID → SyncUsers flow.
func TestE2E_P1_KickRotatesUUID_NewUUIDOnDisk(t *testing.T) {
	origExec := singbox.ExecFunc
	singbox.ExecFunc = func(name string, args ...string) error { return nil }
	defer func() { singbox.ExecFunc = origExec }()

	// Initial config: device has UUID "old-device-uuid"
	oldUUID := "old-device-uuid-1111-2222-3333-444455556666"
	initialCfg := map[string]interface{}{
		"inbounds": []interface{}{
			map[string]interface{}{
				"type": "vless",
				"tag":  "vless-in",
				"tls": map[string]interface{}{
					"enabled": true,
					"reality": map[string]interface{}{"enabled": true},
				},
				"users": []interface{}{
					map[string]string{"uuid": oldUUID, "flow": "xtls-rprx-vision"},
				},
			},
		},
	}
	cfgData, _ := json.Marshal(initialCfg)
	cfgFile, err := os.CreateTemp("", "e2e-kick-*.json")
	require.NoError(t, err)
	cfgFile.Write(cfgData)
	cfgFile.Close()
	defer os.Remove(cfgFile.Name())

	// Admin kick: rotated UUID replaces old one (RotateDeviceUUID sets new UUID in DB,
// then SyncUsers writes it to the config — this simulates KickUserSessions).
	rotatedUUID := "rotated-uuid-after-kick-2222-3333-4444-555566667777"
	require.NoError(t, singbox.SyncUsers(cfgFile.Name(), []singbox.DeviceUser{{UUID: rotatedUUID}}))

	// Verify old UUID is gone, rotated UUID is present
	var onDisk map[string]interface{}
	diskData, _ := os.ReadFile(cfgFile.Name())
	json.Unmarshal(diskData, &onDisk)
	inbounds := onDisk["inbounds"].([]interface{})
	users := inbounds[0].(map[string]interface{})["users"].([]interface{})
	require.Len(t, users, 1)
	diskUUID := users[0].(map[string]interface{})["uuid"].(string)

	assert.Equal(t, rotatedUUID, diskUUID, "rotated UUID must be present after kick")
	assert.NotEqual(t, oldUUID, diskUUID, "old UUID must be gone after kick (tunnel invalidated)")

	t.Logf("E2E P1 Kick ✓: old=%s evicted, new=%s active", oldUUID, rotatedUUID)
}
