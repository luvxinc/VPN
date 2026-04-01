package background

import (

	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// OutboundHealthMonitor periodically tests both VPN outbound paths and stores
// the results in the provided store (in-memory map + optional Redis).
//
// Inspired by v2ray-core's Observatory module, which continuously probes
// outbound node health and exposes results to the routing engine.
// Our version is simpler: it checks two fixed tags via the Clash API's
// /proxies/{tag}/delay endpoint and exposes results via a simple Get() method
// (consumed by the Admin API at GET /admin/api/health).
//
// Tags tested:
//   - "GLOBAL"   → catch-all (confirms Clash API is reachable)
//   - Per-tag:   configured via OutboundTag slice (e.g. "ws-cdn", "reality-direct")
type OutboundHealthMonitor struct {
	ClashURL string
	Tags     []string
	Interval time.Duration

	mu      sync.RWMutex // protects results (read by HTTP handler, written by background goroutine)
	client  *http.Client
	results map[string]OutboundHealth
}

// OutboundHealth is the health snapshot for one outbound path.
type OutboundHealth struct {
	Tag       string    `json:"tag"`
	LatencyMs int64     `json:"latency_ms"` // -1 = timeout/error
	Status    string    `json:"status"`     // "ok", "degraded", "down"
	LastCheck time.Time `json:"last_check"`
	Failures  int       `json:"consecutive_failures"`
}

// NewOutboundHealthMonitor creates a health monitor for the given Clash API URL and tags.
// If tags is empty, skips per-tag delay tests (a no-op monitor).
func NewOutboundHealthMonitor(clashURL string, tags []string) *OutboundHealthMonitor {
	return &OutboundHealthMonitor{
		ClashURL: clashURL,
		Tags:     tags,
		Interval: 30 * time.Second,
		client:   &http.Client{Timeout: 8 * time.Second},
		results:  make(map[string]OutboundHealth),
	}
}

// Run polls until ctx is cancelled.
func (m *OutboundHealthMonitor) Run(ctx context.Context) {
	// Initial check immediately on startup
	m.checkAll(ctx)

	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

// GetAll returns a snapshot of all health results (safe for concurrent reads
// as we only update inside checkAll which is single-threaded per instance).
func (m *OutboundHealthMonitor) GetAll() []OutboundHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]OutboundHealth, 0, len(m.results))
	for _, h := range m.results {
		out = append(out, h)
	}
	return out
}

// CheckOnce runs a single health check cycle. Exported for tests.
func (m *OutboundHealthMonitor) CheckOnce(ctx context.Context) {
	m.checkAll(ctx)
}

func (m *OutboundHealthMonitor) checkAll(ctx context.Context) {
	for _, tag := range m.Tags {
		h := m.probe(ctx, tag)
		
		m.mu.Lock()
		m.results[tag] = h
		m.mu.Unlock()
		
		if h.Status != "ok" {
			slog.Warn("outbound health degraded", "tag", tag, "status", h.Status,
				"latency_ms", h.LatencyMs, "failures", h.Failures)
		} else {
			slog.Debug("outbound health ok", "tag", tag, "latency_ms", h.LatencyMs)
		}
	}
}

// probe tests one outbound tag via the Clash API delay probe.
// Uses the standard Cloudflare generate_204 URL (same as the client uses).
//
// IMPORTANT: We use a fresh probeCtx with a fixed timeout per probe,
// NOT the Run loop's ctx. This decouples HTTP request lifecycle from
// loop cancellation — when Run() is stopped the in-flight probe is
// allowed to finish or timeout on its own, preventing spurious "degraded"
// status caused by context-cancelled HTTP errors.
func (m *OutboundHealthMonitor) probe(ctx context.Context, tag string) OutboundHealth {
	m.mu.RLock()
	prev := m.results[tag]
	m.mu.RUnlock()
	
	result := OutboundHealth{
		Tag:       tag,
		LastCheck: time.Now(),
		Failures:  prev.Failures,
	}

	url := fmt.Sprintf(
		"%s/proxies/%s/delay?url=https%%3A%%2F%%2Fcp.cloudflare.com%%2Fgenerate_204&timeout=5000",
		m.ClashURL, tag,
	)

	// Use a per-probe context with a fixed timeout, NOT the loop ctx.
	// The loop ctx controls when we STOP scheduling new probes;
	// the probeCtx controls how long a single HTTP request may take.
	probeCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// If the loop has already been cancelled, skip this probe gracefully.
	select {
	case <-ctx.Done():
		// Loop is shutting down — preserve last known state rather than
		// recording a spurious failure caused by context cancellation.
		return prev
	default:
	}

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		result.LatencyMs = -1
		result.Failures = prev.Failures + 1
		result.Status = statusFromFailures(result.Failures)
		return result
	}

	start := time.Now()
	resp, err := m.client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil || resp.StatusCode != http.StatusOK {
		result.LatencyMs = -1
		result.Failures = prev.Failures + 1
		result.Status = statusFromFailures(result.Failures)
		if resp != nil {
			resp.Body.Close()
		}
		return result
	}
	defer resp.Body.Close()

	var body struct {
		Delay int64 `json:"delay"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Delay > 0 {
		result.LatencyMs = body.Delay
	} else {
		result.LatencyMs = elapsed
	}
	result.Failures = 0
	result.Status = "ok"
	return result
}


// statusFromFailures maps consecutive failure count to a health status string.
//
// Thresholds:
//   - 0            → "ok"     (success, reset by any passing probe)
//   - 1–4          → "degraded" (transient / intermittent failures)
//   - 5+           → "down"   (consistently unreachable)
func statusFromFailures(failures int) string {
	switch {
	case failures == 0:
		return "ok"
	case failures >= 5:
		return "down"
	default: // 1–4
		return "degraded"
	}
}
