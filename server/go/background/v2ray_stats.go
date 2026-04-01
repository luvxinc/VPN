package background

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luvxinc/vpn/server/config"
	"github.com/luvxinc/vpn/server/models"
	"github.com/luvxinc/vpn/server/singbox"
	statsCommand "github.com/v2fly/v2ray-core/v5/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/luvxinc/vpn/server/store"
)

// StatsPoller periodically queries sing-box v2ray_api gRPC endpoint for accurate traffic metrics.
type StatsPoller struct {
	DB     *store.DB
	RDB    *store.Redis
	Cfg    *config.Config
	Addr   string // e.g. "127.0.0.1:10086"
	ctx    context.Context
	cancel context.CancelFunc
}

func NewStatsPoller(db *store.DB, rdb *store.Redis, cfg *config.Config, addr string) *StatsPoller {
	return &StatsPoller{
		DB:   db,
		RDB:  rdb,
		Cfg:  cfg,
		Addr: addr,
	}
}

func (p *StatsPoller) Start() {
	p.ctx, p.cancel = context.WithCancel(context.Background())
	go p.loop()
}

func (p *StatsPoller) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *StatsPoller) loop() {
	slog.Info("V2Ray StatsPoller started", "addr", p.Addr)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if err := p.poll(); err != nil {
				slog.Error("StatsPoller poll error", "err", err)
			}
		}
	}
}

func (p *StatsPoller) poll() error {
	conn, err := grpc.NewClient(p.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("grpc dial error: %w", err)
	}
	defer conn.Close()

	client := statsCommand.NewStatsServiceClient(conn)
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()

	// QueryStats with Reset_=true so sing-box clears the counters.
	// This gives us the exact delta bytes since the last poll!
	resp, err := client.QueryStats(ctx, &statsCommand.QueryStatsRequest{
		Pattern: "",
		Reset_:  true,
	})
	if err != nil {
		return fmt.Errorf("QueryStats error: %w", err)
	}

	// We group deltas by UUID.
	type traffic struct {
		up   int64
		down int64
	}
	deltas := make(map[string]*traffic)

	for _, stat := range resp.Stat {
		// Stat names look like: user>>>UUID>>>traffic>>>uplink
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) == 4 && parts[0] == "user" && parts[2] == "traffic" {
			uid := parts[1]
			if deltas[uid] == nil {
				deltas[uid] = &traffic{}
			}
			if parts[3] == "uplink" {
				deltas[uid].up += stat.Value
			} else if parts[3] == "downlink" {
				deltas[uid].down += stat.Value
			}
		}
	}

	accessHour := time.Now().UTC().Truncate(time.Hour)

	// Since we got deltas by UUID, we look up the Session ID.
	// Users may have exactly one active session per VLESS UUID.
	for uid, t := range deltas {
		if t.up == 0 && t.down == 0 {
			continue
		}

		rawSession, err := p.RDB.GetVlessMap(ctx, uid)
		if err != nil || rawSession == "" {
			continue // Ghost traffic or unrecognized UUID
		}



		var info models.SessionInfo
		if err := json.Unmarshal([]byte(rawSession), &info); err != nil {
			continue
		}

		userID, _ := uuid.Parse(info.UserID)
		sessionID, _ := uuid.Parse(info.SessionID)

		// Record the traffic delta!
		p.DB.UpsertAccessLog(ctx, userID, sessionID, "", accessHour, t.up, t.down)
		p.DB.UpdateSessionTraffic(ctx, sessionID, t.up, t.down)

		// Check Quota and actively Kill Switch if exceeded
		limits, err := p.DB.GetUserLimits(ctx, userID)
		if err == nil && limits.QuotaBytes != nil && limits.QuotaPeriod != nil {
			used, _, err := p.DB.GetQuotaUsed(ctx, userID, *limits.QuotaPeriod)
			if err == nil && used >= *limits.QuotaBytes {
				slog.Warn("StatsPoller: User exceeded quota, applying kill switch", "user_id", userID)
				p.kickUser(ctx, userID)
			}
		}
	}

	return nil
}

func (p *StatsPoller) kickUser(ctx context.Context, userID uuid.UUID) {
	actives, err := p.DB.GetActiveSessionsByUser(ctx, userID)
	if err == nil {
		for _, s := range actives {
			fp, _ := p.DB.GetDeviceFingerprintByID(ctx, s.DeviceID)
			if fp != "" {
				p.RDB.DeleteKey(ctx, "active_session:"+fp)
			}
			p.RDB.DeleteKey(ctx, "vless_map:"+s.VlessUUID)
		}
	}
	p.DB.DeactivateUserSessions(ctx, userID)
	p.DB.DeactivateUserDevices(ctx, userID)

	uuids, err := p.DB.GetAllActiveDeviceUsers(ctx)
	if err == nil {
		deviceUsers := make([]singbox.DeviceUser, len(uuids))
		for i, u := range uuids {
			deviceUsers[i] = singbox.DeviceUser{UUID: u}
		}
		if err := singbox.SyncUsers(p.Cfg.SingBox.ConfigPath, deviceUsers); err != nil {
			slog.Error("StatsPoller: SyncUsers failed during quota kill", "err", err)
		}
	}
}
