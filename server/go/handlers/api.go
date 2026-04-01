package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luvxinc/vpn/server/auth"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/geoip"
	"github.com/luvxinc/vpn/server/models"
	"github.com/luvxinc/vpn/server/singbox"
	"github.com/luvxinc/vpn/server/store"
)

type APIHandler struct {
	DB  *store.DB
	RDB *store.Redis
	Cfg *config.Config
}

// checkClientVersion writes a 426 response and returns true if the client is outdated.
// Callers must return immediately when this returns true.
func (h *APIHandler) checkClientVersion(c *fiber.Ctx) (outdated bool, err error) {
	raw := c.Get("X-Client-Version")
	if raw == "" {
		return false, nil // no header → old client, let through
	}
	verStr := raw
	if idx := strings.LastIndex(raw, "/"); idx >= 0 {
		verStr = raw[idx+1:]
	}
	clientVer := auth.ParseVersion(verStr)
	minVer := auth.ParseVersion(h.Cfg.Client.MinVersion)
	if auth.ClientVersionOutdated(clientVer, minVer) {
		err = c.Status(fiber.StatusUpgradeRequired).JSON(fiber.Map{
			"detail": fiber.Map{
				"error":           "client_version_outdated",
				"current_version": verStr,
				"min_version":     h.Cfg.Client.MinVersion,
				"download_url":    h.Cfg.Client.DownloadURL,
			},
		})
		return true, err
	}
	return false, nil
}

// createSession is shared by Connect and VerifyDevice.
// Returns (vlessUUID, accessToken, refreshToken) or an error.
//
// P1 optimization (v2ray-core Validator philosophy):
// If the device already has a stable vlessUUID, we reuse it — no sing-box
// config update is needed. The UUID is already in sing-box from the first
// time this device connected. This eliminates SIGHUP on every login,
// reducing reconnection latency from ~400ms to <5ms.
//
// A new UUID is only assigned on first registration (deviceVlessUUID == ""),
// or when the admin kicks the user (RotateDeviceUUID + SyncUsers).
func (h *APIHandler) createSession(c *fiber.Ctx, userIDStr, fingerprint string, deviceID uuid.UUID, deviceVlessUUID string) (string, string, string, error) {
	ctx := c.Context()

	// Deactivate existing active sessions for this device
	if err := h.DB.DeactivateDeviceSessions(ctx, deviceID); err != nil {
		return "", "", "", err
	}

	// Clean up old Redis session
	oldRaw, _ := h.RDB.GetActiveSession(ctx, fingerprint)
	if oldRaw != "" {
		var oldInfo models.SessionInfo
		if json.Unmarshal([]byte(oldRaw), &oldInfo) == nil {
			h.RDB.DeleteSession(ctx, fingerprint, oldInfo.VlessUUID)
			if oldInfo.RefreshToken != "" {
				h.RDB.DeleteKey(ctx, "refresh:"+oldInfo.RefreshToken)
			}
		}
	}

	// ── P1: Stable per-device UUID (v2ray-core Validator philosophy) ──────────
	// If this device already has a UUID, reuse it — zero sing-box overhead.
	// If this is a new device (first registration), assign a permanent UUID
	// and do a one-time sing-box config update.
	vlessUUID := deviceVlessUUID
	if vlessUUID == "" {
		// First-time registration: generate a stable UUID for this device.
		vlessUUID = uuid.New().String()
		if err := h.DB.AssignDeviceUUID(ctx, deviceID, vlessUUID); err != nil {
			return "", "", "", fmt.Errorf("assign device uuid: %w", err)
		}
		// Sync ALL device UUIDs to sing-box (one-time SIGHUP for this device).
		if err := h.syncSingBoxUsers(ctx); err != nil {
			return "", "", "", fmt.Errorf("sing-box user sync: %w", err)
		}

	}
	// If vlessUUID != "": device already has a UUID in sing-box → no SIGHUP needed.

	// GeoIP
	clientIP := c.IP()
	country, city := geoip.LookupIP(clientIP)

	// Parse userID
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return "", "", "", err
	}

	// Create session in DB
	sessionID, err := h.DB.CreateSession(ctx, userID, deviceID, vlessUUID, clientIP, country, city)
	if err != nil {
		return "", "", "", err
	}

	// JWT
	accessToken, err := auth.MakeUserJWT(userIDStr, sessionID.String(), h.Cfg.Auth.JWTSecret, h.Cfg.Auth.JWTExpiryMinutes)
	if err != nil {
		return "", "", "", err
	}

	// Refresh token: base64url(32 random bytes) = 43 chars
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", err
	}
	refreshToken := base64.RawURLEncoding.EncodeToString(buf)

	// Store in Redis
	info := models.SessionInfo{
		UserID:       userIDStr,
		SessionID:    sessionID.String(),
		VlessUUID:    vlessUUID,
		RefreshToken: refreshToken,
	}
	infoJSON, err := json.Marshal(info)
	if err != nil {
		return "", "", "", err
	}
	infoStr := string(infoJSON)

	if err := h.RDB.SetSession(ctx, fingerprint, vlessUUID, infoStr); err != nil {
		return "", "", "", err
	}

	ttl := time.Duration(h.Cfg.Auth.RefreshExpiryHours) * time.Hour
	if err := h.RDB.SetRefreshToken(ctx, refreshToken, infoStr, ttl); err != nil {
		return "", "", "", err
	}

	// Update device last seen
	h.DB.UpdateDeviceLastSeen(ctx, deviceID)

	return vlessUUID, accessToken, refreshToken, nil
}

// syncSingBoxUsers fetches all active device UUIDs from DB and syncs them to
// the sing-box config via a single SIGHUP. Called only when the device UUID
// pool changes (first registration or kick).
// Returns an error so callers can surface failures (e.g. disk full, config missing).
func (h *APIHandler) syncSingBoxUsers(ctx context.Context) error {
	uuids, err := h.DB.GetAllActiveDeviceUsers(ctx)
	if err != nil {
		slog.Error("syncSingBoxUsers: failed to fetch active device UUIDs", "err", err)
		return fmt.Errorf("fetch device UUIDs: %w", err)
	}
	if len(uuids) == 0 {
		// No active devices — nothing to sync (first device will be added via AssignDeviceUUID)
		return nil
	}
	deviceUsers := make([]singbox.DeviceUser, len(uuids))
	for i, u := range uuids {
		deviceUsers[i] = singbox.DeviceUser{UUID: u}
	}
	if err := singbox.SyncUsers(h.Cfg.SingBox.ConfigPath, deviceUsers); err != nil {
		slog.Error("syncSingBoxUsers: SyncUsers failed", "err", err, "device_count", len(deviceUsers))
		return fmt.Errorf("SyncUsers: %w", err)
	}
	return nil
}


// buildPolicy fetches user limits and calculates current quota usage.
func (h *APIHandler) buildPolicy(ctx context.Context, userID uuid.UUID) models.UserPolicy {
	limits, err := h.DB.GetUserLimits(ctx, userID)
	if err != nil {
		return models.UserPolicy{}
	}
	var used int64
	var resetsAt *time.Time
	if limits.QuotaPeriod != nil {
		u, r, err := h.DB.GetQuotaUsed(ctx, userID, *limits.QuotaPeriod)
		if err == nil {
			used = u
			resetsAt = &r
		}
	}
	exceeded := limits.QuotaBytes != nil && used >= *limits.QuotaBytes
	return models.UserPolicy{
		SpeedLimitUpKbps:   limits.SpeedLimitUpKbps,
		SpeedLimitDownKbps: limits.SpeedLimitDownKbps,
		QuotaBytes:         limits.QuotaBytes,
		QuotaPeriod:        limits.QuotaPeriod,
		QuotaUsedBytes:     used,
		QuotaResetsAt:      resetsAt,
		QuotaExceeded:      exceeded,
	}
}

// vpnResponse builds the ConnectResponse struct.
func (h *APIHandler) vpnResponse(vlessUUID, accessToken, refreshToken string, policy models.UserPolicy) models.ConnectResponse {
	srv := h.Cfg.Server
	vc := models.VlessConfig{
		UUID:       vlessUUID,
		Server:     srv.IP,
		Port:       srv.Port,
		PublicKey:  srv.PublicKey,
		ShortID:    srv.ShortID,
		ServerName: srv.ServerName,
	}
	if srv.WSFallbackDomain != "" {
		vc.WSFallbackDomain = srv.WSFallbackDomain
		vc.WSFallbackPort = srv.WSCDNPort // 443: Cloudflare external port (not sing-box's local 8888)
		vc.WSFallbackPath = "/ws"
	}
	return models.ConnectResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		VlessConfig:  vc,
		Policy:       policy,
	}
}

// Connect handles POST /connect.
func (h *APIHandler) Connect(c *fiber.Ctx) error {
	if outdated, err := h.checkClientVersion(c); outdated {
		return err
	}

	var body struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid JSON"})
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	if body.DeviceName == "" {
		body.DeviceName = "Unknown Device"
	}
	if len(body.DeviceName) > 128 {
		body.DeviceName = body.DeviceName[:128]
	}

	if body.Username == "" || body.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing username or password"})
	}
	if len(body.DeviceID) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing or invalid device_id"})
	}

	ctx := c.Context()

	// Verify credentials
	userID, hash, active, err := h.DB.GetUserByUsername(ctx, body.Username)
	if err == pgx.ErrNoRows {
		h.RDB.IncrRateLimit(ctx, c.IP())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}
	if err != nil {
		return err
	}
	if !active {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Account disabled"})
	}
	if !auth.CheckPassword(body.Password, hash) {
		h.RDB.IncrRateLimit(ctx, c.IP())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}

	// Check device registration
	// P1: GetDeviceByFingerprint now also returns the device's stable vlessUUID.
	// On subsequent logins the UUID is already in sing-box → no SIGHUP needed.
	deviceID, deviceActive, ownerID, deviceVlessUUID, err := h.DB.GetDeviceByFingerprint(ctx, body.DeviceID)
	if err == pgx.ErrNoRows {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"detail": fiber.Map{
				"error":   "device_not_registered",
				"message": "此设备未注册，请联系管理员获取验证码",
			},
		})
	}
	if err != nil {
		return err
	}
	if !deviceActive {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Device disabled"})
	}
	if ownerID != userID {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Device not associated with this account"})
	}

	// Check quota before creating session
	policy := h.buildPolicy(ctx, userID)
	if policy.QuotaExceeded {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"detail": fiber.Map{
				"error":           "quota_exceeded",
				"quota_resets_at": policy.QuotaResetsAt,
			},
		})
	}

	vlessUUID, accessToken, refreshToken, err := h.createSession(c, userID.String(), body.DeviceID, deviceID, deviceVlessUUID)
	if err != nil {
		return err
	}

	h.applyRateLimit(ctx, c.IP(), policy)
	return c.JSON(h.vpnResponse(vlessUUID, accessToken, refreshToken, policy))
}

// VerifyDevice handles POST /verify-device.
func (h *APIHandler) VerifyDevice(c *fiber.Ctx) error {
	if outdated, err := h.checkClientVersion(c); outdated {
		return err
	}

	var body struct {
		Username         string `json:"username"`
		Password         string `json:"password"`
		DeviceID         string `json:"device_id"`
		DeviceName       string `json:"device_name"`
		VerificationCode string `json:"verification_code"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid JSON"})
	}
	body.Username = strings.TrimSpace(body.Username)
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	body.VerificationCode = strings.ToUpper(strings.TrimSpace(body.VerificationCode))
	if body.DeviceName == "" {
		body.DeviceName = "Unknown Device"
	}
	if len(body.DeviceName) > 128 {
		body.DeviceName = body.DeviceName[:128]
	}

	if body.Username == "" || body.Password == "" || body.DeviceID == "" || body.VerificationCode == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing required fields"})
	}
	if len(body.DeviceID) < 8 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid device_id"})
	}

	ctx := c.Context()

	// Verify credentials
	userID, hash, active, err := h.DB.GetUserByUsername(ctx, body.Username)
	if err == pgx.ErrNoRows {
		h.RDB.IncrRateLimit(ctx, c.IP())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}
	if err != nil {
		return err
	}
	if !active {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Account disabled"})
	}
	if !auth.CheckPassword(body.Password, hash) {
		h.RDB.IncrRateLimit(ctx, c.IP())
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid credentials"})
	}

	// Validate verification code
	storedUserID, err := h.RDB.GetVerifCode(ctx, body.VerificationCode)
	if err != nil {
		return err
	}
	if storedUserID == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Invalid or expired verification code"})
	}
	if storedUserID != userID.String() {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"detail": "Verification code not issued for this account"})
	}

	// Consume the code
	h.RDB.DeleteKey(ctx, "verif:"+body.VerificationCode)

	// Register / reactivate device. New devices have no vless_uuid yet.
	// createSession detects the empty UUID and assigns one (P1 first-registration path).
	deviceID, _, err := h.DB.UpsertDevice(ctx, userID, body.DeviceID, body.DeviceName)
	if err != nil {
		return err
	}

	// Check quota before creating session
	policy := h.buildPolicy(ctx, userID)
	if policy.QuotaExceeded {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"detail": fiber.Map{
				"error":           "quota_exceeded",
				"quota_resets_at": policy.QuotaResetsAt,
			},
		})
	}

	// New device has no UUID yet → empty string triggers AssignDeviceUUID path
	vlessUUID, accessToken, refreshToken, err := h.createSession(c, userID.String(), body.DeviceID, deviceID, "")
	if err != nil {
		return err
	}

	h.applyRateLimit(ctx, c.IP(), policy)
	return c.JSON(h.vpnResponse(vlessUUID, accessToken, refreshToken, policy))
}

// Disconnect handles POST /disconnect.
func (h *APIHandler) Disconnect(c *fiber.Ctx) error {
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid JSON"})
	}
	body.DeviceID = strings.TrimSpace(body.DeviceID)
	if body.DeviceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing device_id"})
	}

	ctx := c.Context()

	raw, err := h.RDB.GetActiveSession(ctx, body.DeviceID)
	if err != nil {
		return err
	}
	if raw != "" {
		var info models.SessionInfo
		if json.Unmarshal([]byte(raw), &info) == nil {
			sessionID, _ := uuid.Parse(info.SessionID)
			h.DB.DeactivateSession(ctx, sessionID)
			h.RDB.DeleteSession(ctx, body.DeviceID, info.VlessUUID)
			if info.RefreshToken != "" {
				h.RDB.DeleteKey(ctx, "refresh:"+info.RefreshToken)
			}
			// NOTE: We do NOT call UpdateUUID / SyncUsers here.
			// The device's UUID remains stable in sing-box (P1: per-device stable UUID).
			// Overwriting with a random UUID would evict ALL other active users from
			// the sing-box user pool, causing a network outage for everyone else.
			// UUID rotation only happens on admin kick (RotateDeviceUUID + SyncUsers).
		}
	}

	h.RDB.DeleteRateLimitByIP(ctx, c.IP())
	return c.JSON(fiber.Map{"status": "ok"})
}

// Refresh handles POST /refresh.
func (h *APIHandler) Refresh(c *fiber.Ctx) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Invalid JSON"})
	}
	body.RefreshToken = strings.TrimSpace(body.RefreshToken)
	if body.RefreshToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing refresh_token"})
	}

	ctx := c.Context()
	raw, err := h.RDB.GetRefreshToken(ctx, body.RefreshToken)
	if err != nil {
		return err
	}
	if raw == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid or expired refresh token"})
	}

	var info models.SessionInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid refresh token data"})
	}

	accessToken, err := auth.MakeUserJWT(info.UserID, info.SessionID, h.Cfg.Auth.JWTSecret, h.Cfg.Auth.JWTExpiryMinutes)
	if err != nil {
		return err
	}

	return c.JSON(fiber.Map{"access_token": accessToken})
}

// Status handles GET /status?device_id=...
// Returns current quota usage, speed limits, and whether policy was recently changed.
func (h *APIHandler) Status(c *fiber.Ctx) error {
	deviceID := strings.TrimSpace(c.Query("device_id"))
	if deviceID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"detail": "Missing device_id"})
	}

	ctx := c.Context()
	raw, err := h.RDB.GetActiveSession(ctx, deviceID)
	if err != nil {
		return err
	}
	if raw == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "No active session"})
	}

	var info models.SessionInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"detail": "Invalid session"})
	}

	userID, err := uuid.Parse(info.UserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"detail": "Invalid user ID in session"})
	}

	limits, err := h.DB.GetUserLimits(ctx, userID)
	if err != nil {
		return err
	}

	var used int64
	var resetsAt *time.Time
	if limits.QuotaPeriod != nil {
		u, r, err := h.DB.GetQuotaUsed(ctx, userID, *limits.QuotaPeriod)
		if err == nil {
			used = u
			resetsAt = &r
		}
	}

	exceeded := limits.QuotaBytes != nil && used >= *limits.QuotaBytes
	changed := h.RDB.GetAndDeletePolicyChanged(ctx, info.UserID)

	// Heartbeat: update last_heartbeat_at so the admin panel shows real-time online status.
	if sessionID, err2 := uuid.Parse(info.SessionID); err2 == nil {
		h.DB.UpdateSessionHeartbeat(ctx, sessionID)
	}

	return c.JSON(models.PolicyStatus{
		SpeedLimitUpKbps:   limits.SpeedLimitUpKbps,
		SpeedLimitDownKbps: limits.SpeedLimitDownKbps,
		QuotaBytes:         limits.QuotaBytes,
		QuotaPeriod:        limits.QuotaPeriod,
		QuotaUsedBytes:     used,
		QuotaResetsAt:      resetsAt,
		QuotaExceeded:      exceeded,
		PolicyChanged:      changed,
	})
}

// applyRateLimit writes per-IP rate limits to Redis so the proxy can enforce them.
func (h *APIHandler) applyRateLimit(_ interface{}, clientIP string, policy models.UserPolicy) {
	up := 0
	if policy.SpeedLimitUpKbps != nil {
		up = *policy.SpeedLimitUpKbps
	}
	down := 0
	if policy.SpeedLimitDownKbps != nil {
		down = *policy.SpeedLimitDownKbps
	}
	if up == 0 && down == 0 {
		return // unlimited, nothing to store
	}
	ttl := time.Duration(h.Cfg.Auth.RefreshExpiryHours) * time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	h.RDB.SetRateLimitByIP(ctx, clientIP, up, down, ttl)
}
