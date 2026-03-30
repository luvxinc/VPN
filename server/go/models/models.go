package models

import (
	"time"

	"github.com/google/uuid"
)

// SessionInfo is stored in Redis (active_session, vless_map, refresh keys).
// Field names are snake_case JSON to match existing Redis data from Python server.
type SessionInfo struct {
	UserID       string `json:"user_id"`
	SessionID    string `json:"session_id"`
	VlessUUID    string `json:"vless_uuid"`
	RefreshToken string `json:"refresh_token"`
}

// VlessConfig is returned to the Swift client on successful connect.
type VlessConfig struct {
	UUID       string `json:"uuid"`
	Server     string `json:"server"`
	Port       int    `json:"port"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
	ServerName string `json:"server_name"`
}

// ConnectResponse is the full /connect and /verify-device response body.
type ConnectResponse struct {
	AccessToken  string      `json:"access_token"`
	RefreshToken string      `json:"refresh_token"`
	VlessConfig  VlessConfig `json:"vless_config"`
}

// OnlineSession holds data for the admin dashboard online-users table.
type OnlineSession struct {
	ID           uuid.UUID
	Username     string
	LoginIP      string
	LoginCountry string
	LoginCity    string
	ConnectedAt  time.Time
	UploadBytes  int64
	DownloadBytes int64
}

// UserRow holds data for the admin users page.
type UserRow struct {
	ID         uuid.UUID
	Username   string
	IsActive   bool
	CreatedAt  time.Time
	LastSeen   *time.Time
	DeviceName *string
	Online     int64
}

// AccessLogRow holds data for the admin logs page.
type AccessLogRow struct {
	Host          string
	AccessHour    time.Time
	RequestCount  int
	UploadBytes   int64
	DownloadBytes int64
}

// DailyTrafficRow holds data for the admin stats page.
type DailyTrafficRow struct {
	Date          time.Time
	UploadBytes   int64
	DownloadBytes int64
}

// TrafficSummary holds aggregated stats for the admin stats page.
type TrafficSummary struct {
	Upload   int64
	Download int64
	Sessions int64
}

// UserIDRow is a minimal user record (id + username) for select dropdowns.
type UserIDRow struct {
	ID       uuid.UUID
	Username string
}
