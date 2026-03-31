package background

import (
	"context"
	"log/slog"
	"time"

	"github.com/luvxinc/vpn/server/store"
)

// SessionCleaner periodically marks stale sessions (no heartbeat for 5 min) as inactive.
type SessionCleaner struct {
	DB *store.DB
}

func NewSessionCleaner(db *store.DB) *SessionCleaner {
	return &SessionCleaner{DB: db}
}

func (s *SessionCleaner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
		if err := s.DB.DeactivateStaleSessions(ctx); err != nil {
			slog.Debug("session cleanup error", "err", err)
		}
	}
}
