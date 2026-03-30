package background

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luvxinc/vpn/server/models"
	"github.com/luvxinc/vpn/server/store"
)

// ClashPoller polls the sing-box Clash API every 10 seconds and writes traffic
// data to the access_log table.
type ClashPoller struct {
	DB         *store.DB
	RDB        *store.Redis
	ClashURL   string
	httpClient *http.Client
	// prevStats tracks last-seen upload/download per connection ID for delta calc.
	prevStats map[string]connStats
}

type connStats struct {
	upload   int64
	download int64
}

func NewClashPoller(db *store.DB, rdb *store.Redis, clashURL string) *ClashPoller {
	return &ClashPoller{
		DB:         db,
		RDB:        rdb,
		ClashURL:   clashURL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		prevStats:  make(map[string]connStats),
	}
}

// Run polls forever, stopping when ctx is cancelled.
func (p *ClashPoller) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		if err := p.PollOnce(ctx); err != nil {
			slog.Debug("clash poller error (will retry)", "err", err)
		}
	}
}

// PollOnce fetches /connections from the Clash API and upserts access_log.
// Exported for testing.
func (p *ClashPoller) PollOnce(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ClashURL+"/connections", nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil // sing-box not running locally — skip silently
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var data struct {
		Connections []map[string]json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return err
	}

	activeIDs := make(map[string]struct{}, len(data.Connections))

	for _, conn := range data.Connections {
		connIDRaw, ok := conn["id"]
		if !ok {
			continue
		}
		var connID string
		json.Unmarshal(connIDRaw, &connID)
		activeIDs[connID] = struct{}{}

		vlessUUID := extractVlessUUID(conn)
		if vlessUUID == "" {
			continue
		}

		sessionRaw, err := p.RDB.GetVlessMap(ctx, vlessUUID)
		if err != nil || sessionRaw == "" {
			continue
		}
		var info models.SessionInfo
		if err := json.Unmarshal([]byte(sessionRaw), &info); err != nil {
			continue
		}

		host := extractHost(conn)
		if host == "" {
			continue
		}

		var uploadTotal, downloadTotal int64
		if raw, ok := conn["upload"]; ok {
			json.Unmarshal(raw, &uploadTotal)
		}
		if raw, ok := conn["download"]; ok {
			json.Unmarshal(raw, &downloadTotal)
		}

		prev := p.prevStats[connID]
		uploadDelta := max64(0, uploadTotal-prev.upload)
		downloadDelta := max64(0, downloadTotal-prev.download)
		p.prevStats[connID] = connStats{upload: uploadTotal, download: downloadTotal}

		if uploadDelta == 0 && downloadDelta == 0 {
			continue
		}

		// Truncate to hour
		accessHour := time.Now().UTC().Truncate(time.Hour)
		if startRaw, ok := conn["start"]; ok {
			var startStr string
			if json.Unmarshal(startRaw, &startStr) == nil && startStr != "" {
				startStr = strings.Replace(startStr, "Z", "+00:00", 1)
				if t, err := time.Parse(time.RFC3339, startStr); err == nil {
					accessHour = t.UTC().Truncate(time.Hour)
				}
			}
		}

		userID, _ := uuid.Parse(info.UserID)
		sessionID, _ := uuid.Parse(info.SessionID)

		p.DB.UpsertAccessLog(ctx, userID, sessionID, host, accessHour, uploadDelta, downloadDelta)
		p.DB.UpdateSessionTraffic(ctx, sessionID, uploadDelta, downloadDelta)
	}

	// Prune stale delta entries
	for id := range p.prevStats {
		if _, ok := activeIDs[id]; !ok {
			delete(p.prevStats, id)
		}
	}

	return nil
}

// ExtractVlessUUID extracts the VLESS user UUID from connection metadata or chains.
// Exported for testing.
func ExtractVlessUUID(conn map[string]json.RawMessage) string {
	return extractVlessUUID(conn)
}

func extractVlessUUID(conn map[string]json.RawMessage) string {
	// Try metadata.inboundUser.uuid (sing-box 1.13+)
	if metaRaw, ok := conn["metadata"]; ok {
		var meta map[string]json.RawMessage
		if json.Unmarshal(metaRaw, &meta) == nil {
			if iuRaw, ok := meta["inboundUser"]; ok {
				var iu map[string]string
				if json.Unmarshal(iuRaw, &iu) == nil {
					if u, ok := iu["uuid"]; ok && u != "" {
						return u
					}
				}
			}
		}
	}
	// Fallback: scan chains for 36-char UUID-shaped strings
	if chainsRaw, ok := conn["chains"]; ok {
		var chains []string
		if json.Unmarshal(chainsRaw, &chains) == nil {
			for _, item := range chains {
				if len(item) == 36 && strings.Count(item, "-") == 4 {
					return item
				}
			}
		}
	}
	return ""
}

// extractHost returns the normalized host from connection metadata.
func extractHost(conn map[string]json.RawMessage) string {
	if metaRaw, ok := conn["metadata"]; ok {
		var meta map[string]string
		if json.Unmarshal(metaRaw, &meta) == nil {
			host := meta["host"]
			if host == "" {
				host = meta["destinationIP"]
			}
			if host != "" {
				return normalizeHost(host)
			}
		}
	}
	return ""
}

// NormalizeHost strips port from "host:port" and truncates to 253 chars. Exported for testing.
func NormalizeHost(host string) string { return normalizeHost(host) }

func normalizeHost(host string) string {
	if strings.HasPrefix(host, "[") {
		// IPv6 literal
	} else if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	if len(host) > 253 {
		host = host[:253]
	}
	return host
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
