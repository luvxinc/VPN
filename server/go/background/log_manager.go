package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/luvxinc/vpn/server/store"
)

// LogManager runs nightly cleanup at 3am.
type LogManager struct {
	DB                      *store.DB
	RetentionDays           int
	MaxDomainsPerUserPerDay int
}

func NewLogManager(db *store.DB, retentionDays, maxDomains int) *LogManager {
	return &LogManager{
		DB:                      db,
		RetentionDays:           retentionDays,
		MaxDomainsPerUserPerDay: maxDomains,
	}
}

// Run sleeps until 3am then runs cleanup, forever.
func (m *LogManager) Run(ctx context.Context) {
	for {
		m.sleepUntil3AM(ctx)
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := m.RunCleanup(ctx); err != nil {
			slog.Error("log cleanup error", "err", err)
			// Retry in 1 hour on error
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Hour):
			}
		}
	}
}

// sleepUntil3AM sleeps until the next 3am, respecting context cancellation.
func (m *LogManager) sleepUntil3AM(ctx context.Context) {
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if !target.After(now) {
		target = target.AddDate(0, 0, 1)
	}
	wait := time.Until(target)
	slog.Debug("next log cleanup", "in_hours", wait.Hours())
	select {
	case <-ctx.Done():
	case <-time.After(wait):
	}
}

// RunCleanup runs the three-step cleanup. Exported for testing.
func (m *LogManager) RunCleanup(ctx context.Context) error {
	cutoff := time.Now().AddDate(0, 0, -m.RetentionDays)

	// 1. Delete old access_log rows
	deleted, err := m.DB.DeleteOldAccessLogs(ctx, cutoff)
	if err != nil {
		return err
	}
	slog.Info("purged old access_log rows", "count", deleted, "retention_days", m.RetentionDays)

	// 2. Aggregate yesterday into traffic_daily (use UTC midnight to match DB timestamps)
	yesterday := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -1)
	if err := m.DB.UpsertTrafficDaily(ctx, yesterday); err != nil {
		return err
	}
	slog.Info("aggregated traffic_daily", "date", yesterday.Format("2006-01-02"))

	// 3. Cap access_log per user per day
	if err := m.DB.CapAccessLog(ctx, cutoff, m.MaxDomainsPerUserPerDay); err != nil {
		return err
	}
	slog.Info("log cleanup complete")
	return nil
}
