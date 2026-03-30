//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"github.com/google/uuid"
	"github.com/luvxinc/vpn/server/auth"
	"github.com/luvxinc/vpn/server/background"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/handlers"
	"github.com/luvxinc/vpn/server/middleware"
	"github.com/luvxinc/vpn/server/singbox"
	"github.com/luvxinc/vpn/server/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ── Test constants ────────────────────────────────────────────────────────────

const (
	testDBURL    = "postgresql://localhost/weiai_vpn_test"
	testRedisURL = "redis://localhost:6379/1"
)

var testCfg = &config.Config{
	Database: config.DatabaseConfig{URL: testDBURL, PoolSize: 3},
	Redis:    config.RedisConfig{URL: testRedisURL},
	Server: config.ServerConfig{
		IP: "127.0.0.1", Port: 443,
		PublicKey: "testpublickey", PrivateKey: "testprivkey",
		ShortID: "a1b2c3d4", ServerName: "www.example.com",
	},
	Auth: config.AuthConfig{
		JWTSecret:          "test_secret_key_at_least_32_chars!",
		JWTExpiryMinutes:   15,
		RefreshExpiryHours: 24,
	},
	Admin: config.AdminConfig{
		AllowedLANPrefixes: []string{"127.", "192.168.", "10."},
		Username:           "admin",
	},
	SingBox: config.SingBoxConfig{
		ConfigPath:  "",
		ClashAPIURL: "http://127.0.0.1:9090",
	},
	Log: config.LogConfig{RetentionDays: 90, MaxDomainsPerUserPerDay: 500},
	Client: config.ClientConfig{
		MinVersion:  "1.0.0",
		DownloadURL: "https://example.com/download",
	},
}

// ── TestMain ──────────────────────────────────────────────────────────────────

var (
	testDB  *store.DB
	testRDB *store.Redis
	testApp *fiber.App
)

func TestMain(m *testing.M) {
	// Set admin password hash
	hash, _ := bcrypt.GenerateFromPassword([]byte("adminpass"), bcrypt.DefaultCost)
	testCfg.Admin.PasswordHash = string(hash)

	// Write a stub sing-box config
	sbFile, err := os.CreateTemp("", "singbox-test-*.json")
	if err != nil {
		panic(err)
	}
	sbFile.WriteString(`{"inbounds":[{"type":"vless","users":[{"uuid":"stub","flow":"xtls-rprx-vision"}]}]}`)
	sbFile.Close()
	testCfg.SingBox.ConfigPath = sbFile.Name()
	defer os.Remove(sbFile.Name())

	// No-op sing-box exec in tests
	singbox.ExecFunc = func(name string, args ...string) error { return nil }

	// No-op geoip (DB not available in CI)
	// geoip.LookupIP will return Unknown for public IPs (no DB initialized)

	ctx := context.Background()

	testDB, err = store.NewDB(ctx, testDBURL, 3)
	if err != nil {
		panic("test DB connection failed: " + err.Error())
	}

	testRDB, err = store.NewRedis(ctx, testRedisURL)
	if err != nil {
		panic("test Redis connection failed: " + err.Error())
	}

	testApp = buildTestApp(testDB, testRDB, testCfg)

	code := m.Run()

	testDB.Close()
	testRDB.Close()
	os.Exit(code)
}

func buildTestApp(db *store.DB, rdb *store.Redis, cfg *config.Config) *fiber.App {
	engine := html.New("../../templates", ".html")
	engine.AddFunc("toMB", func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/1048576) })
	engine.AddFunc("toGB", func(b int64) string { return fmt.Sprintf("%.2f", float64(b)/1073741824) })
	engine.AddFunc("toKB", func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/1024) })
	engine.AddFunc("contains", strings.Contains)
	engine.AddFunc("add", func(a, b int64) int64 { return a + b })
	engine.AddFunc("not", func(b bool) bool { return !b })

	app := fiber.New(fiber.Config{
		JSONEncoder:            sonic.Marshal,
		JSONDecoder:            sonic.Unmarshal,
		Views:                  engine,
		DisableStartupMessage:  true,
		ProxyHeader:            fiber.HeaderXForwardedFor,
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return c.Status(code).JSON(fiber.Map{"detail": err.Error()})
		},
	})

	apiH := &handlers.APIHandler{DB: db, RDB: rdb, Cfg: cfg}
	adminH := &handlers.AdminHandler{DB: db, RDB: rdb, Cfg: cfg}
	rateLimitMW := middleware.RateLimit(rdb)
	lanMW := middleware.RequireLAN(cfg)
	adminAuthMW := middleware.RequireAdminAuth(cfg)

	app.Get("/health", handlers.Health("1.0.0"))
	app.Post("/connect", rateLimitMW, apiH.Connect)
	app.Post("/verify-device", rateLimitMW, apiH.VerifyDevice)
	app.Post("/disconnect", apiH.Disconnect)
	app.Post("/refresh", apiH.Refresh)
	app.Get("/admin/login", lanMW, adminH.LoginPage)
	app.Post("/admin/login", lanMW, adminH.Login)
	app.Post("/admin/logout", adminH.Logout)
	app.Get("/admin/dashboard", adminAuthMW, adminH.Dashboard)
	app.Get("/admin/api/online-count", adminAuthMW, adminH.APIOnlineCount)
	app.Get("/admin/api/online", adminAuthMW, adminH.APIOnline)
	app.Get("/admin/users", adminAuthMW, adminH.UsersPage)
	app.Post("/admin/users", adminAuthMW, adminH.CreateUser)
	app.Post("/admin/users/:id/delete", adminAuthMW, adminH.DeleteUser)
	app.Post("/admin/users/:id/password", adminAuthMW, adminH.ChangePassword)
	app.Post("/admin/users/:id/toggle", adminAuthMW, adminH.ToggleUser)
	app.Post("/admin/users/:id/kick", adminAuthMW, adminH.KickUser)
	app.Get("/admin/users/:id/verif-code", adminAuthMW, adminH.GenerateVerifCode)
	app.Get("/admin/logs", adminAuthMW, adminH.LogsPage)
	app.Get("/admin/stats", adminAuthMW, adminH.StatsPage)

	return app
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func truncateAll(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	pool := testDB.Pool()
	_, err := pool.Exec(ctx,
		"TRUNCATE access_log, traffic_daily, sessions, devices, users RESTART IDENTITY CASCADE",
	)
	require.NoError(t, err)
	testRDB.FlushDB(ctx)
}

func createUser(t *testing.T, username, password string, active bool) string {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	var id uuid.UUID
	err := testDB.Pool().QueryRow(context.Background(),
		"INSERT INTO users (username, password_hash, is_active) VALUES ($1, $2, $3) RETURNING id",
		username, string(hash), active,
	).Scan(&id)
	require.NoError(t, err)
	return id.String()
}

func registerDevice(t *testing.T, userID, fingerprint, name string) string {
	t.Helper()
	uid, _ := uuid.Parse(userID)
	var id uuid.UUID
	err := testDB.Pool().QueryRow(context.Background(),
		"INSERT INTO devices (user_id, device_fingerprint, device_name) VALUES ($1, $2, $3) RETURNING id",
		uid, fingerprint, name,
	).Scan(&id)
	require.NoError(t, err)
	return id.String()
}

func doRequest(t *testing.T, method, path string, body interface{}, headers map[string]string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Fake LAN IP
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := testApp.Test(req, 10000)
	require.NoError(t, err)
	return resp
}

func parseJSON(t *testing.T, r *http.Response) map[string]interface{} {
	t.Helper()
	defer r.Body.Close()
	var m map[string]interface{}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&m))
	return m
}

func adminCookie(t *testing.T) string {
	t.Helper()
	token, err := auth.MakeAdminJWT(testCfg.Auth.JWTSecret)
	require.NoError(t, err)
	return "admin_token=" + token
}

// ── Tests 1-6: Connect ────────────────────────────────────────────────────────

func TestConnect_UnregisteredDevice(t *testing.T) {
	truncateAll(t)
	createUser(t, "alice", "password123", true)

	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "UNREGISTERED-DEVICE-ID",
	}, nil)

	assert.Equal(t, 403, resp.StatusCode)
	body := parseJSON(t, resp)
	detail := body["detail"].(map[string]interface{})
	assert.Equal(t, "device_not_registered", detail["error"])
}

func TestConnect_Success(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)

	assert.Equal(t, 200, resp.StatusCode)
	body := parseJSON(t, resp)
	assert.NotEmpty(t, body["access_token"])
	assert.NotEmpty(t, body["refresh_token"])
	vless := body["vless_config"].(map[string]interface{})
	assert.NotEmpty(t, vless["uuid"])
	assert.Equal(t, "127.0.0.1", vless["server"])
}

func TestConnect_WrongPassword(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "wrongpassword",
		"device_id": "TESTDEVICE-001",
	}, nil)

	assert.Equal(t, 401, resp.StatusCode)
}

func TestConnect_UnknownUser(t *testing.T) {
	truncateAll(t)

	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "nonexistent",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)

	assert.Equal(t, 401, resp.StatusCode)
}

func TestConnect_InactiveUser(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", false)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)

	assert.Equal(t, 403, resp.StatusCode)
}

func TestConnect_RateLimit(t *testing.T) {
	truncateAll(t)
	// Make 6 requests from same IP — 6th should be 429
	for i := 0; i < 6; i++ {
		resp := doRequest(t, "POST", "/connect", map[string]interface{}{
			"username":  "nonexistent" + fmt.Sprintf("%d", i),
			"password":  "password",
			"device_id": "TESTDEVICE-001",
		}, nil)
		if i < 5 {
			assert.NotEqual(t, 429, resp.StatusCode, "request %d should not be rate limited", i+1)
		} else {
			assert.Equal(t, 429, resp.StatusCode, "6th request should be rate limited")
		}
	}
}

// ── Tests 7-9: Disconnect & Refresh ──────────────────────────────────────────

func TestDisconnect_ClearsSession(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	// Connect first
	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)
	require.Equal(t, 200, resp.StatusCode)
	body := parseJSON(t, resp)
	vlessUUID := body["vless_config"].(map[string]interface{})["uuid"].(string)

	// Disconnect
	resp2 := doRequest(t, "POST", "/disconnect", map[string]interface{}{
		"device_id": "TESTDEVICE-001",
	}, nil)
	assert.Equal(t, 200, resp2.StatusCode)

	// Verify Redis keys are gone
	ctx := context.Background()
	activeSession, _ := testRDB.GetActiveSession(ctx, "TESTDEVICE-001")
	assert.Empty(t, activeSession)
	vlessMap, _ := testRDB.GetVlessMap(ctx, vlessUUID)
	assert.Empty(t, vlessMap)

	// Verify session in DB is inactive
	var isActive bool
	testDB.Pool().QueryRow(ctx,
		"SELECT is_active FROM sessions WHERE vless_uuid=$1", vlessUUID,
	).Scan(&isActive)
	assert.False(t, isActive)
}

func TestRefresh_ValidToken(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	// Connect to get tokens
	resp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)
	require.Equal(t, 200, resp.StatusCode)
	body := parseJSON(t, resp)
	refreshToken := body["refresh_token"].(string)

	// Refresh
	resp2 := doRequest(t, "POST", "/refresh", map[string]interface{}{
		"refresh_token": refreshToken,
	}, nil)
	assert.Equal(t, 200, resp2.StatusCode)
	body2 := parseJSON(t, resp2)
	assert.NotEmpty(t, body2["access_token"])
}

func TestRefresh_InvalidToken(t *testing.T) {
	truncateAll(t)

	resp := doRequest(t, "POST", "/refresh", map[string]interface{}{
		"refresh_token": "invalid-token-that-doesnt-exist",
	}, nil)
	assert.Equal(t, 401, resp.StatusCode)
}

// ── Tests 10-12: VerifyDevice ─────────────────────────────────────────────────

func TestVerifyDevice_ValidCode(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)

	// Generate a verif code manually in Redis
	ctx := context.Background()
	testRDB.SetVerifCode(ctx, "TESTCODE", userID)

	resp := doRequest(t, "POST", "/verify-device", map[string]interface{}{
		"username":          "alice",
		"password":          "password123",
		"device_id":         "NEWDEVICE-001",
		"device_name":       "New Mac",
		"verification_code": "testcode", // lowercased — handler uppercases it
	}, nil)

	assert.Equal(t, 200, resp.StatusCode)
	body := parseJSON(t, resp)
	assert.NotEmpty(t, body["access_token"])

	// Code should be consumed
	stored, _ := testRDB.GetVerifCode(ctx, "TESTCODE")
	assert.Empty(t, stored)

	// Device should be in DB
	var count int
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*) FROM devices WHERE device_fingerprint=$1", "NEWDEVICE-001",
	).Scan(&count)
	assert.Equal(t, 1, count)
}

func TestVerifyDevice_ExpiredCode(t *testing.T) {
	truncateAll(t)
	createUser(t, "alice", "password123", true)

	resp := doRequest(t, "POST", "/verify-device", map[string]interface{}{
		"username":          "alice",
		"password":          "password123",
		"device_id":         "NEWDEVICE-001",
		"device_name":       "New Mac",
		"verification_code": "EXPIRED",
	}, nil)

	assert.Equal(t, 403, resp.StatusCode)
}

func TestVerifyDevice_WrongUserCode(t *testing.T) {
	truncateAll(t)
	createUser(t, "alice", "password123", true)
	bobID := createUser(t, "bob", "password123", true)

	// Code is for bob, but alice is trying to use it
	ctx := context.Background()
	testRDB.SetVerifCode(ctx, "BOBCODE", bobID)

	resp := doRequest(t, "POST", "/verify-device", map[string]interface{}{
		"username":          "alice",
		"password":          "password123",
		"device_id":         "NEWDEVICE-001",
		"device_name":       "New Mac",
		"verification_code": "BOBCODE",
	}, nil)

	assert.Equal(t, 403, resp.StatusCode)
}

// ── Test 13: Second connect deactivates first ─────────────────────────────────

func TestSecondConnect_DeactivatesFirst(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	// First connect
	resp1 := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)
	require.Equal(t, 200, resp1.StatusCode)
	body1 := parseJSON(t, resp1)
	firstVless := body1["vless_config"].(map[string]interface{})["uuid"].(string)

	// Second connect (same device)
	resp2 := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)
	require.Equal(t, 200, resp2.StatusCode)

	// First session should be inactive
	ctx := context.Background()
	var isActive bool
	testDB.Pool().QueryRow(ctx,
		"SELECT is_active FROM sessions WHERE vless_uuid=$1", firstVless,
	).Scan(&isActive)
	assert.False(t, isActive, "first session should be deactivated")
}

// ── Tests 14-16: Admin auth ────────────────────────────────────────────────────

func TestAdminLogin_Valid(t *testing.T) {
	truncateAll(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/login",
		strings.NewReader("username=admin&password=adminpass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	resp, err := testApp.Test(req, 10000)
	require.NoError(t, err)
	assert.Equal(t, 303, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Location"), "/admin/dashboard")
}

func TestAdminLogin_InvalidPassword(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/admin/login",
		strings.NewReader("username=admin&password=wrongpass"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	resp, _ := testApp.Test(req, 10000)
	assert.Equal(t, 401, resp.StatusCode)
}

func TestAdminRequiresLAN(t *testing.T) {
	cookie := adminCookie(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	resp, _ := testApp.Test(req, 10000)
	// Template rendering may fail without DB data but should not be 403
	assert.NotEqual(t, 403, resp.StatusCode)

	// External IP should be rejected
	req2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	req2.Header.Set("Cookie", cookie)
	req2.Header.Set("X-Forwarded-For", "8.8.8.8")
	resp2, _ := testApp.Test(req2, 10000)
	assert.Equal(t, 403, resp2.StatusCode)
}

// ── Tests 17-21: Admin user CRUD ──────────────────────────────────────────────

func adminReq(t *testing.T, method, path, body, contentType string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("Cookie", adminCookie(t))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := testApp.Test(req, 10000)
	require.NoError(t, err)
	return resp
}

func TestAdminCreateUser(t *testing.T) {
	truncateAll(t)
	resp := adminReq(t, "POST", "/admin/users",
		"username=testuser&password=testpass123",
		"application/x-www-form-urlencoded")
	assert.Equal(t, 303, resp.StatusCode)

	var count int
	testDB.Pool().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM users WHERE username='testuser'",
	).Scan(&count)
	assert.Equal(t, 1, count)
}

func TestAdminCreateUser_Duplicate(t *testing.T) {
	truncateAll(t)
	createUser(t, "alice", "password123", true)

	resp := adminReq(t, "POST", "/admin/users",
		"username=alice&password=newpassword",
		"application/x-www-form-urlencoded")
	assert.Equal(t, 409, resp.StatusCode)
}

func TestAdminCreateUser_ShortPassword(t *testing.T) {
	truncateAll(t)
	resp := adminReq(t, "POST", "/admin/users",
		"username=newuser&password=short",
		"application/x-www-form-urlencoded")
	assert.Equal(t, 400, resp.StatusCode)
}

func TestAdminDeleteUser(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)

	resp := adminReq(t, "POST", "/admin/users/"+userID+"/delete", "", "")
	assert.Equal(t, 303, resp.StatusCode)

	var count int
	testDB.Pool().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM users WHERE id=$1", userID,
	).Scan(&count)
	assert.Equal(t, 0, count)
}

func TestAdminToggleUser_DisablesActive(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)

	resp := adminReq(t, "POST", "/admin/users/"+userID+"/toggle", "", "")
	assert.Equal(t, 303, resp.StatusCode)

	var isActive bool
	testDB.Pool().QueryRow(context.Background(),
		"SELECT is_active FROM users WHERE id=$1", userID,
	).Scan(&isActive)
	assert.False(t, isActive)
}

// ── Tests 22-23: Verif code ───────────────────────────────────────────────────

func TestAdminGenerateVerifCode_Format(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)

	resp := adminReq(t, "GET", "/admin/users/"+userID+"/verif-code", "", "")
	assert.Equal(t, 200, resp.StatusCode)

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	code := body["code"].(string)
	assert.Len(t, code, 8)
	// Must be uppercase alphanumeric
	for _, ch := range code {
		assert.True(t, (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9'),
			"code character %c is not uppercase alphanumeric", ch)
	}
}

func TestAdminGenerateVerifCode_RedisTTL(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)

	resp := adminReq(t, "GET", "/admin/users/"+userID+"/verif-code", "", "")
	require.Equal(t, 200, resp.StatusCode)

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)
	code := body["code"].(string)

	// Check Redis TTL is approximately 900 seconds
	ctx := context.Background()
	ttl, err := testRDB.TTL(ctx, "verif:"+code)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, ttl, int64(890))
	assert.LessOrEqual(t, ttl, int64(910))
}

// ── Test 24: Admin kick user ──────────────────────────────────────────────────

func TestAdminKickUser(t *testing.T) {
	truncateAll(t)
	userID := createUser(t, "alice", "password123", true)
	registerDevice(t, userID, "TESTDEVICE-001", "TestMac")

	// Connect to create an active session
	connectResp := doRequest(t, "POST", "/connect", map[string]interface{}{
		"username":  "alice",
		"password":  "password123",
		"device_id": "TESTDEVICE-001",
	}, nil)
	require.Equal(t, 200, connectResp.StatusCode)

	// Kick
	resp := adminReq(t, "POST", "/admin/users/"+userID+"/kick", "", "")
	assert.Equal(t, 303, resp.StatusCode)

	// Session should be inactive
	ctx := context.Background()
	var count int
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*) FROM sessions WHERE user_id=$1 AND is_active=true", userID,
	).Scan(&count)
	assert.Equal(t, 0, count)

	// Redis active_session key should be gone
	activeSession, _ := testRDB.GetActiveSession(ctx, "TESTDEVICE-001")
	assert.Empty(t, activeSession)
}

// ── Tests 25-27: DB aggregation ───────────────────────────────────────────────

func TestAccessLog_Aggregates(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	userID, _ := uuid.Parse(createUser(t, "alice", "password123", true))
	registerDevice(t, userID.String(), "TESTDEVICE-001", "TestMac")

	// Create a session manually
	sessionID := uuid.New()
	testDB.Pool().Exec(ctx,
		`INSERT INTO sessions (id, user_id, device_id, vless_uuid, login_ip, connected_at)
		 SELECT $1, $2, d.id, 'test-uuid', '127.0.0.1', NOW()
		 FROM devices d WHERE d.device_fingerprint='TESTDEVICE-001'`,
		sessionID, userID,
	)

	accessHour := time.Now().UTC().Truncate(time.Hour)
	// First upsert
	err := testDB.UpsertAccessLog(ctx, userID, sessionID, "example.com", accessHour, 100, 200)
	require.NoError(t, err)

	// Same host+hour — should aggregate (not insert new row)
	err = testDB.UpsertAccessLog(ctx, userID, sessionID, "example.com", accessHour, 50, 100)
	require.NoError(t, err)

	var count int
	var upload, download int64
	var reqCount int
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*), SUM(upload_bytes), SUM(download_bytes), SUM(request_count) FROM access_log WHERE session_id=$1",
		sessionID,
	).Scan(&count, &upload, &download, &reqCount)

	assert.Equal(t, 1, count, "should have 1 row (aggregated)")
	assert.Equal(t, int64(150), upload)
	assert.Equal(t, int64(300), download)
	assert.Equal(t, 2, reqCount)
}

func TestCleanup_DeletesOldRows(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	userID, _ := uuid.Parse(createUser(t, "alice", "password123", true))
	registerDevice(t, userID.String(), "TESTDEVICE-001", "TestMac")

	var sessionID uuid.UUID
	testDB.Pool().QueryRow(ctx,
		`INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip)
		 SELECT $1, d.id, 'test-uuid', '127.0.0.1'
		 FROM devices d WHERE d.device_fingerprint='TESTDEVICE-001'
		 RETURNING id`,
		userID,
	).Scan(&sessionID)

	// Old row (91 days ago — should be deleted)
	oldHour := time.Now().UTC().Add(-91 * 24 * time.Hour).Truncate(time.Hour)
	testDB.UpsertAccessLog(ctx, userID, sessionID, "old.com", oldHour, 100, 200)

	// Recent row (1 day ago — should survive)
	recentHour := time.Now().UTC().Add(-1 * 24 * time.Hour).Truncate(time.Hour)
	testDB.UpsertAccessLog(ctx, userID, sessionID, "recent.com", recentHour, 50, 100)

	// Run cleanup
	mgr := background.NewLogManager(testDB, 90, 500)
	err := mgr.RunCleanup(ctx)
	require.NoError(t, err)

	// Old row should be gone
	var count int
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*) FROM access_log WHERE host='old.com'",
	).Scan(&count)
	assert.Equal(t, 0, count, "old row should be deleted")

	// Recent row should survive
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*) FROM access_log WHERE host='recent.com'",
	).Scan(&count)
	assert.Equal(t, 1, count, "recent row should survive")
}

func TestTrafficDaily_Upsert(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	userID, _ := uuid.Parse(createUser(t, "alice", "password123", true))
	registerDevice(t, userID.String(), "TESTDEVICE-001", "TestMac")

	// Create a session for yesterday (UTC midnight to match RunCleanup's date computation)
	yesterday := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -1)
	var sessionID uuid.UUID
	testDB.Pool().QueryRow(ctx,
		`INSERT INTO sessions (user_id, device_id, vless_uuid, login_ip, connected_at, upload_bytes, download_bytes)
		 SELECT $1, d.id, 'test-uuid2', '127.0.0.1', $2, 1000000, 5000000
		 FROM devices d WHERE d.device_fingerprint='TESTDEVICE-001'
		 RETURNING id`,
		userID, yesterday,
	).Scan(&sessionID)

	// Run cleanup which triggers traffic_daily aggregation
	mgr := background.NewLogManager(testDB, 90, 500)
	err := mgr.RunCleanup(ctx)
	require.NoError(t, err)

	// Check traffic_daily has a row for yesterday (UTC date)
	var upload, download int64
	err = testDB.Pool().QueryRow(ctx,
		"SELECT upload_bytes, download_bytes FROM traffic_daily WHERE user_id=$1 AND date=$2::date",
		userID, yesterday.Format("2006-01-02"),
	).Scan(&upload, &download)
	require.NoError(t, err)
	assert.Equal(t, int64(1000000), upload)
	assert.Equal(t, int64(5000000), download)

	// Run again — should UPDATE (ON CONFLICT), not insert duplicate
	err = mgr.RunCleanup(ctx)
	require.NoError(t, err)

	var count int
	testDB.Pool().QueryRow(ctx,
		"SELECT COUNT(*) FROM traffic_daily WHERE user_id=$1",
		userID,
	).Scan(&count)
	assert.Equal(t, 1, count, "should have exactly 1 row (upsert, not insert)")
}

// ── Verify DB/Redis store helper (bonus test for store layer) ─────────────────

func TestStore_SetAndGetActiveSession(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()

	data := `{"user_id":"u1","session_id":"s1","vless_uuid":"v1","refresh_token":"r1"}`
	err := testRDB.SetSession(ctx, "DEVICE-001", "v1", data)
	require.NoError(t, err)

	got, err := testRDB.GetActiveSession(ctx, "DEVICE-001")
	require.NoError(t, err)
	assert.Equal(t, data, got)

	gotVless, err := testRDB.GetVlessMap(ctx, "v1")
	require.NoError(t, err)
	assert.Equal(t, data, gotVless)

	err = testRDB.DeleteSession(ctx, "DEVICE-001", "v1")
	require.NoError(t, err)

	gone, _ := testRDB.GetActiveSession(ctx, "DEVICE-001")
	assert.Empty(t, gone)
}
