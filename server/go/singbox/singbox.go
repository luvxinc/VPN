package singbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ExecFunc is the function used to run external commands.
// Replaced in tests to skip pkill.
var ExecFunc = defaultExec

func defaultExec(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// UpdateUUID atomically rewrites the sing-box config with a new VLESS user UUID.
// It preserves all other fields via json.RawMessage round-trip, then renames the
// temp file and restarts sing-box.
func UpdateUUID(configPath, newUUID string) error {
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

	// Update every VLESS inbound. Reality inbounds need xtls-rprx-vision;
	// WebSocket inbounds must not set flow (incompatible).
	for i := range inbounds {
		var tag string
		_ = json.Unmarshal(inbounds[i]["type"], &tag)
		if tag != "vless" {
			continue
		}
		flow := ""
		if tlsRaw, ok := inbounds[i]["tls"]; ok {
			var tls map[string]json.RawMessage
			if json.Unmarshal(tlsRaw, &tls) == nil {
				if realityRaw, ok := tls["reality"]; ok {
					var reality struct {
						Enabled bool `json:"enabled"`
					}
					if json.Unmarshal(realityRaw, &reality) == nil && reality.Enabled {
						flow = "xtls-rprx-vision"
					}
				}
			}
		}
		type User struct {
			UUID string `json:"uuid"`
			Flow string `json:"flow,omitempty"`
		}
		users := []User{{UUID: newUUID, Flow: flow}}
		usersJSON, err := json.Marshal(users)
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

	// Restart sing-box
	_ = ExecFunc("pkill", "-f", "sing-box")
	time.Sleep(1500 * time.Millisecond)

	// Resolve binary path for re-launch (best effort; systemd/launchd will restart it)
	if dir := filepath.Dir(configPath); dir != "" {
		_ = dir // the service manager handles restart
	}

	return nil
}
