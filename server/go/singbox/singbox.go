package singbox

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExecFunc is the function used to run external commands.
// Replaced in tests to skip pkill / kill.
var ExecFunc = defaultExec

func defaultExec(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// clashAPIURL is the sing-box Clash-compatible control API.
// Used to read the running PID for SIGHUP hot-reload.
// Defaults to the standard sing-box Clash API address; overridable for tests.
var clashAPIURL = "http://127.0.0.1:9090"

// SetClashAPIURL overrides the sing-box Clash API URL.
// Call before UpdateUUID in tests or when using a non-default port.
func SetClashAPIURL(url string) {
	clashAPIURL = url
}

// DeviceUser represents a single VLESS user entry in the sing-box config.
// UUID is the stable per-device UUID (stored in devices.vless_uuid).
// Flow is set to "xtls-rprx-vision" for Reality inbounds; empty for WS.
type DeviceUser struct {
	UUID string
}

var syncMu sync.Mutex

// SyncUsers atomically rewrites the sing-box config with the full set of
// active device users, then hot-reloads the running sing-box via SIGHUP.
//
// This is the primary function called by the application. It replaces the
// old single-UUID UpdateUUID approach, mirroring v2ray-core's Validator
// model where the user pool is always the ground truth.
//
// The on-disk config reflects ALL active device UUIDs at all times.
// Normal logins that reuse an existing UUID do NOT need to call this.
// Only first-time device registration and kick operations call SyncUsers.
//
// Why SIGHUP instead of pkill+restart:
//   - sing-box's Clash API does NOT expose an endpoint for adding/removing
//     inbound users at runtime (unlike v2ray-core's gRPC Validator.Add/Del).
//   - SIGHUP triggers sing-box to re-read the config file in ~100-300 ms
//     without killing the process, so the service manager doesn't restart it.
//   - This removes the 3-5 s post-restart window that caused Phase 2 delay
//     tests to fail with "connection refused" on every login.
//
// Fallback: if the PID cannot be found via pgrep, falls back to the legacy
// pkill flow so nothing breaks in degraded environments.
func SyncUsers(configPath string, users []DeviceUser) error {
	syncMu.Lock()
	defer syncMu.Unlock()

	// ── Step 1: Rewrite on-disk config (atomically) ───────────────────────────
	if err := rewriteConfigMultiUser(configPath, users); err != nil {
		return err
	}

	// ── Step 2a: Hot-reload via SIGHUP (preferred) ────────────────────────────
	if pid, err := findSingBoxPID(); err == nil && pid > 0 {
		if err := ExecFunc("kill", "-HUP", strconv.Itoa(pid)); err == nil {
			// SIGHUP sent — give sing-box ~400 ms to finish reloading.
			time.Sleep(400 * time.Millisecond)
			return nil
		}
	}

	// ── Step 2b: Fallback — legacy pkill + sleep ──────────────────────────────
	_ = ExecFunc("pkill", "-f", "sing-box")
	time.Sleep(1500 * time.Millisecond)
	return nil
}

// UpdateUUID is the legacy single-UUID wrapper kept for backward compatibility
// and for test suites. New code should prefer SyncUsers.
//
// It is still used by KickUserSessions (kick uses a brand-new UUID per device
// to immediately invalidate the existing tunnel).
func UpdateUUID(configPath, newUUID string) error {
	return SyncUsers(configPath, []DeviceUser{{UUID: newUUID}})
}

// findSingBoxPID returns the PID of the running sing-box process.
// Tries three methods in order:
//  1. pgrep -x (exact process name match)
//  2. pgrep -f (full command line match, e.g. "sing-box run")
//  3. Clash API /version probe + pgrep retry
func findSingBoxPID() (int, error) {
	// Method 1: pgrep (available on both Linux and macOS)
	out, err := exec.Command("pgrep", "-x", "sing-box").Output()
	if err == nil {
		pidStr := strings.TrimSpace(string(out))
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			return pid, nil
		}
	}

	// Method 2: pgrep with -f (matches full command line, e.g. "sing-box run")
	out, err = exec.Command("pgrep", "-f", "sing-box run").Output()
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
				return pid, nil
			}
		}
	}

	// Method 3: Ask the Clash API for its "Hello" — the API itself is the
	// running process, so we can read its PID from /proc/net or via the API.
	// Actually just try GET /version to confirm it's alive, then use pgrep fallback.
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(clashAPIURL + "/version")
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		// API is up — try pgrep one more time (covers race between startup and query)
		out, err = exec.Command("pgrep", "-f", "sing-box").Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
					return pid, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("singbox: cannot find running sing-box PID")
}

// rewriteConfigMultiUser atomically rewrites the sing-box config file with
// the full set of active device users for every VLESS inbound.
//
// This mirrors v2ray-core's Validator: the config file IS the user pool.
// All active device UUIDs are written simultaneously, not just one at a time.
//
// For Reality inbounds (tls.reality.enabled = true), flow is set to
// "xtls-rprx-vision". For WS/CDN inbounds, flow must be empty (sing-box
// rejects flow on non-Reality transports).
func rewriteConfigMultiUser(configPath string, users []DeviceUser) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("singbox: read config: %w", err)
	}

	// Round-trip through map[string]json.RawMessage to preserve unknown fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("singbox: unmarshal config: %w", err)
	}

	inboundsRaw, ok := raw["inbounds"]
	if !ok {
		return fmt.Errorf("singbox: config has no 'inbounds' key")
	}

	var inbounds []map[string]json.RawMessage
	if err := json.Unmarshal(inboundsRaw, &inbounds); err != nil {
		return fmt.Errorf("singbox: unmarshal inbounds: %w", err)
	}
	if len(inbounds) == 0 {
		return fmt.Errorf("singbox: inbounds array is empty")
	}

	// Update every VLESS inbound with the full user list.
	// Reality inbounds need xtls-rprx-vision; WebSocket inbounds must not set flow.
	for i := range inbounds {
		var inboundType string
		_ = json.Unmarshal(inbounds[i]["type"], &inboundType)
		if inboundType != "vless" {
			continue
		}

		// Detect whether this is a Reality inbound
		isReality := false
		if tlsRaw, ok := inbounds[i]["tls"]; ok {
			var tls map[string]json.RawMessage
			if json.Unmarshal(tlsRaw, &tls) == nil {
				if realityRaw, ok := tls["reality"]; ok {
					var reality struct {
						Enabled bool `json:"enabled"`
					}
					if json.Unmarshal(realityRaw, &reality) == nil && reality.Enabled {
						isReality = true
					}
				}
			}
		}

		// Build the user list for this inbound
		type UserEntry struct {
			UUID string `json:"uuid"`
			Flow string `json:"flow,omitempty"`
		}
		entries := make([]UserEntry, 0, len(users))
		for _, u := range users {
			flow := ""
			if isReality {
				flow = "xtls-rprx-vision"
			}
			entries = append(entries, UserEntry{UUID: u.UUID, Flow: flow})
		}

		usersJSON, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("singbox: marshal users: %w", err)
		}
		inbounds[i]["users"] = json.RawMessage(usersJSON)
	}

	newInboundsJSON, err := json.Marshal(inbounds)
	if err != nil {
		return fmt.Errorf("singbox: marshal inbounds: %w", err)
	}
	raw["inbounds"] = json.RawMessage(newInboundsJSON)

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("singbox: marshal config: %w", err)
	}

	// Write to temp file in the SAME directory for atomic rename
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0644); err != nil {
		return fmt.Errorf("singbox: write tmp: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, configPath); err != nil {
		return fmt.Errorf("singbox: rename: %w", err)
	}

	return nil
}
